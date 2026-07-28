package controllers

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/canonicaldigest"
	"fluxagent/internal/domain"
	"fluxagent/internal/investigation"
	"fluxagent/internal/statusbudget"
	"fluxagent/internal/verifier"
	"fluxagent/internal/version"
)

const (
	rcaSchemaVersion                = "fluxagent-rca-result-v1"
	rcaCanonicalizationVersion      = canonicaldigest.RCAJSONV1
	reasoningPolicyVersion          = "rca-v2-compat"
	executionStateProviderCompleted = "ProviderCompleted"
	executionStateFinalized         = "Finalized"
	executionAttemptCompleted       = "Completed"
	defaultExecutionAttemptID       = "attempt-001"
	defaultMaxInvestigationDepth    = int32(1)
)

type InvestigationRequestReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Service *investigation.Service
	Now     func() time.Time
}

func (r *InvestigationRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var investigation v1alpha1.InvestigationRequest
	if err := r.Get(ctx, req.NamespacedName, &investigation); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	ttlResult, finished, err := r.handleTTL(ctx, &investigation, now())
	if err != nil || finished {
		return ttlResult, err
	}
	if shouldSkipInvestigationExecution(&investigation) {
		return ttlResult, nil
	}

	original := investigation.DeepCopy()
	restarting := original.Status.ObservedGeneration != investigation.Generation || isTerminalInvestigationPhase(original.Status.Phase)
	message := "investigation execution started"
	setInvestigationRequestStatus(&investigation.Status, v1alpha1.PhaseCollecting, message, investigation.Generation, now())
	if investigation.Status.StartedAt == nil || restarting {
		startedAt := metav1.NewTime(now())
		investigation.Status.StartedAt = &startedAt
	}
	investigation.Status.CompletedAt = nil
	investigation.Status.Outcome = ""
	investigation.Status.Failure = nil
	investigation.Status.Summary = ""
	investigation.Status.Hypothesis = ""
	investigation.Status.Confidence = 0
	investigation.Status.Provider = ""
	investigation.Status.Verdict = nil
	investigation.Status.Claims = nil
	investigation.Status.AlternativeHypotheses = nil
	investigation.Status.MissingEvidence = nil
	investigation.Status.Degradation = nil
	investigation.Status.Execution = nil
	investigation.Status.EvidenceRefs = nil
	investigation.Status.Lineage = lineageForReconcile(original.Status.Lineage, investigation.Annotations)
	investigation.Status.LinkedRiskSignalRef = nil

	if failure := investigationLoopFailure(&investigation); failure != nil {
		applyLoopPreventedInvestigationStatus(&investigation, failure, now())
	} else if invalidMessage := validateInvestigationRequestSpec(investigation.Spec); invalidMessage != "" {
		applyInvalidInvestigationStatus(&investigation, invalidMessage, now())
	} else {
		preflight, preflightErr := r.preflight(ctx, &investigation)
		if preflightErr != nil {
			return ctrl.Result{}, preflightErr
		}
		evidence, evidenceErr := r.collectEvidence(ctx, &investigation, preflight, now())
		if evidenceErr != nil {
			return ctrl.Result{}, evidenceErr
		}
		executionID := investigationExecutionID(&investigation, preflight, evidence)
		rca := emptyRCAResult()
		if providerResult := reusableProviderResult(original.Status.Execution, executionID); providerResult != nil {
			rca.Reasoning = reasoningFromProviderResult(providerResult)
		} else {
			generatedRCA, rcaErr := r.generateRCA(ctx, &investigation, preflight, evidence, now())
			if rcaErr != nil {
				return ctrl.Result{}, rcaErr
			}
			rca = generatedRCA
			if rca.Reasoning != nil {
				persistErr := r.persistProviderCompletedCheckpoint(ctx, &investigation, preflight, evidence, rca, executionID, now())
				if persistErr != nil && !apierrors.IsConflict(persistErr) {
					return ctrl.Result{}, persistErr
				}
			}
		}
		applyInvestigationExecutionStatus(&investigation, preflight, evidence, rca, message, now())
		if rca.Reasoning != nil && investigation.Spec.CreateRiskSignal {
			link, promoteErr := r.promoteToRiskSignal(ctx, &investigation, preflight, evidence, rca, now())
			if promoteErr != nil {
				return ctrl.Result{}, promoteErr
			}
			investigation.Status.LinkedRiskSignalRef = link
		}
	}

	applyInvestigationStatusBudget(&investigation, now())
	if !reflect.DeepEqual(original.Status, investigation.Status) {
		if err := r.Status().Update(ctx, &investigation); err != nil && !apierrors.IsConflict(err) {
			return ctrl.Result{}, err
		}
	}
	return ttlResult, nil
}

func applyInvestigationStatusBudget(request *v1alpha1.InvestigationRequest, now time.Time) {
	if !statusbudget.EnforceInvestigationStatus(&request.Status) {
		return
	}
	reason := v1alpha1.RCADegradationReason{
		Code:    "StatusBudgetExceeded",
		Stage:   v1alpha1.InvestigationStagePersistence,
		Message: "investigation status exceeded compact metadata budget and was truncated",
	}
	if request.Status.Degradation == nil {
		request.Status.Degradation = &v1alpha1.RCADegradation{Partial: true}
	}
	request.Status.Degradation.Partial = true
	request.Status.Degradation.Reasons = appendDegradationReason(request.Status.Degradation.Reasons, reason)
	setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionTrue, reason.Code, reason.Message, request.Generation, now)
}

func applyLoopPreventedInvestigationStatus(request *v1alpha1.InvestigationRequest, failure *v1alpha1.InvestigationFailure, now time.Time) {
	request.Status.Phase = v1alpha1.PhaseFailed
	request.Status.Message = failure.Message
	request.Status.Outcome = v1alpha1.InvestigationOutcomeUnknown
	request.Status.Failure = failure
	completedAt := metav1.NewTime(now)
	request.Status.CompletedAt = &completedAt
	setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionFalse, failure.Code, failure.Message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, failure.Code, failure.Message, request.Generation, now)
}

func investigationLoopFailure(request *v1alpha1.InvestigationRequest) *v1alpha1.InvestigationFailure {
	lineage := request.Status.Lineage
	if lineage == nil {
		return nil
	}
	maxDepth := request.Spec.LoopPolicy.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxInvestigationDepth
	}
	if lineage.InvestigationDepth >= maxDepth {
		return &v1alpha1.InvestigationFailure{
			Code:      "InvestigationDepthLimitExceeded",
			Message:   fmt.Sprintf("investigation lineage depth %d reached maxDepth %d", lineage.InvestigationDepth, maxDepth),
			Stage:     v1alpha1.InvestigationStageValidation,
			Retryable: false,
		}
	}
	if strings.EqualFold(lineage.Source.Kind, "RiskSignal") && !request.Spec.LoopPolicy.AllowRiskSignalSource {
		return &v1alpha1.InvestigationFailure{
			Code:      "RiskSignalSourceBlocked",
			Message:   "default loop policy blocks investigations sourced from RiskSignal",
			Stage:     v1alpha1.InvestigationStageValidation,
			Retryable: false,
		}
	}
	if strings.EqualFold(lineage.Source.Kind, "RiskSignal") && strings.TrimSpace(lineage.Source.UID) == "" {
		return &v1alpha1.InvestigationFailure{
			Code:      "RiskSignalSourceUIDRequired",
			Message:   "RiskSignal-sourced investigations require lineage source UID for loop prevention",
			Stage:     v1alpha1.InvestigationStageValidation,
			Retryable: false,
		}
	}
	return nil
}

func (r *InvestigationRequestReconciler) persistProviderCompletedCheckpoint(ctx context.Context, request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, evidence investigation.EvidenceCollectionResult, rca investigation.RCAResult, executionID string, now time.Time) error {
	checkpoint := request.DeepCopy()
	checkpoint.Status.Provider = providerNameForStatus(checkpoint, preflight, rca)
	checkpoint.Status.EvidenceRefs = evidence.EvidenceRefs
	checkpoint.Status.Execution = buildRCAExecution(checkpoint, preflight, evidence, rca, executionID, executionStateProviderCompleted, now)
	setInvestigationRequestStatus(&checkpoint.Status, v1alpha1.PhaseReasoning, "provider result persisted for RCA verification", checkpoint.Generation, now)
	applyInvestigationStatusBudget(checkpoint, now)
	if err := r.Status().Update(ctx, checkpoint); err != nil {
		return err
	}
	request.ResourceVersion = checkpoint.ResourceVersion
	return nil
}

func appendDegradationReason(reasons []v1alpha1.RCADegradationReason, reason v1alpha1.RCADegradationReason) []v1alpha1.RCADegradationReason {
	for _, existing := range reasons {
		if existing.Code == reason.Code && existing.Stage == reason.Stage {
			return reasons
		}
	}
	return append(reasons, reason)
}

func (r *InvestigationRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.InvestigationRequest{}).
		Complete(r)
}

func (r *InvestigationRequestReconciler) preflight(ctx context.Context, request *v1alpha1.InvestigationRequest) (investigation.PreflightResult, error) {
	if r.Service == nil {
		return investigation.PreflightResult{
			TargetIssue: &investigation.Issue{
				Reason:  "InvestigationServiceUnavailable",
				Message: "investigation service is not configured",
			},
		}, nil
	}
	return r.Service.Preflight(ctx, request.Namespace, request.Spec)
}

func (r *InvestigationRequestReconciler) collectEvidence(ctx context.Context, request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, now time.Time) (investigation.EvidenceCollectionResult, error) {
	if r.Service == nil {
		return investigation.EvidenceCollectionResult{
			Issue: &investigation.Issue{
				Reason:  "InvestigationServiceUnavailable",
				Message: "investigation service is not configured",
			},
		}, nil
	}
	return r.Service.CollectEvidence(ctx, request.Spec, preflight, now)
}

func (r *InvestigationRequestReconciler) generateRCA(ctx context.Context, request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, evidence investigation.EvidenceCollectionResult, now time.Time) (investigation.RCAResult, error) {
	if r.Service == nil {
		return investigation.RCAResult{
			Issue: &investigation.Issue{
				Reason:  "InvestigationServiceUnavailable",
				Message: "investigation service is not configured",
			},
		}, nil
	}
	return r.Service.GenerateRCA(ctx, request.Spec, preflight, evidence, now)
}

func emptyRCAResult() investigation.RCAResult {
	return investigation.RCAResult{}
}

func validateInvestigationRequestSpec(spec v1alpha1.InvestigationRequestSpec) string {
	missing := make([]string, 0, 4)
	if strings.TrimSpace(spec.Target.Namespace) == "" {
		missing = append(missing, "spec.target.namespace")
	}
	if strings.TrimSpace(spec.Target.Kind) == "" {
		missing = append(missing, "spec.target.kind")
	}
	if strings.TrimSpace(spec.Target.Name) == "" {
		missing = append(missing, "spec.target.name")
	}
	if len(missing) > 0 {
		return "missing required target fields: " + strings.Join(missing, ", ")
	}
	if mode := strings.TrimSpace(spec.Mode); mode != "" && mode != v1alpha1.InvestigationModeReadOnly {
		return "unsupported investigation mode: " + mode
	}
	if len(spec.DataSources) == 0 && len(spec.Queries) == 0 {
		return "spec.dataSources or spec.queries must include at least one datasource reference"
	}
	for _, query := range spec.Queries {
		if strings.TrimSpace(query.DatasourceRef.Name) == "" {
			return "investigation queries require spec.queries[].datasourceRef.name"
		}
		if strings.TrimSpace(query.QueryType) == "" {
			return "investigation queries require spec.queries[].queryType"
		}
	}
	return ""
}

func applyInvalidInvestigationStatus(request *v1alpha1.InvestigationRequest, message string, now time.Time) {
	setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseFailed, message, request.Generation, now)
	applyInvestigationFailure(
		request,
		"TargetInvalid",
		message,
		v1alpha1.InvestigationStageValidation,
	)
	completedAt := metav1.NewTime(now)
	request.Status.CompletedAt = &completedAt
	request.Status.Provider = ""
	setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionFalse, "TargetInvalid", message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionTargetResolved, metav1.ConditionFalse, "TargetInvalid", message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionDatasourceResolved, metav1.ConditionFalse, "InvestigationNotRunnable", "investigation request did not pass basic validation", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionQueryTypeSupported, metav1.ConditionFalse, "InvestigationNotRunnable", "investigation request did not pass basic validation", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionFalse, "InvestigationNotRunnable", "investigation request did not pass basic validation", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, "InvestigationNotRunnable", "investigation request did not pass basic validation", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "ValidationFailed", "request failed validation before evidence collection started", request.Generation, now)
}

func applyInvestigationExecutionStatus(request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, evidence investigation.EvidenceCollectionResult, rca investigation.RCAResult, message string, now time.Time) {
	request.Status.Provider = ""
	if preflight.Provider != nil {
		request.Status.Provider = preflight.Provider.Name
	}
	request.Status.EvidenceRefs = evidence.EvidenceRefs

	if preflight.TargetIssue != nil {
		setStatusCondition(&request.Status.Conditions, conditionTargetResolved, metav1.ConditionFalse, preflight.TargetIssue.Reason, preflight.TargetIssue.Message, request.Generation, now)
	} else {
		setStatusCondition(&request.Status.Conditions, conditionTargetResolved, metav1.ConditionTrue, "TargetResolved", "target resource was resolved successfully", request.Generation, now)
	}

	if preflight.DatasourceIssue != nil {
		setStatusCondition(&request.Status.Conditions, conditionDatasourceResolved, metav1.ConditionFalse, preflight.DatasourceIssue.Reason, preflight.DatasourceIssue.Message, request.Generation, now)
		setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionFalse, preflight.DatasourceIssue.Reason, preflight.DatasourceIssue.Message, request.Generation, now)
	} else {
		setStatusCondition(&request.Status.Conditions, conditionDatasourceResolved, metav1.ConditionTrue, "AllDatasourcesResolved", "all referenced datasources were resolved", request.Generation, now)
		if evidence.Issue != nil {
			setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionFalse, evidence.Issue.Reason, evidence.Issue.Message, request.Generation, now)
		} else {
			setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionTrue, "EvidenceCollected", evidence.Summary, request.Generation, now)
		}
	}
	if preflight.QueryTypeIssue != nil {
		setStatusCondition(&request.Status.Conditions, conditionQueryTypeSupported, metav1.ConditionFalse, preflight.QueryTypeIssue.Reason, preflight.QueryTypeIssue.Message, request.Generation, now)
	} else {
		setStatusCondition(&request.Status.Conditions, conditionQueryTypeSupported, metav1.ConditionTrue, "AllQueryTypesSupported", "all investigation query types were supported", request.Generation, now)
	}

	if preflight.ModelProviderIssue != nil {
		setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, preflight.ModelProviderIssue.Reason, preflight.ModelProviderIssue.Message, request.Generation, now)
	} else if evidence.Issue != nil {
		setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, evidence.Issue.Reason, "RCA execution is blocked until evidence collection succeeds", request.Generation, now)
	} else if rca.Issue != nil {
		setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, rca.Issue.Reason, rca.Issue.Message, request.Generation, now)
	} else {
		setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionTrue, "ProviderSucceeded", "RCA generated successfully for investigation request", request.Generation, now)
	}

	if issue := preflight.FirstIssue(); issue != nil {
		setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseFailed, issue.Message, request.Generation, now)
		applyInvestigationFailure(request, issue.Reason, issue.Message, investigationFailureStage(issue.Reason))
		completedAt := metav1.NewTime(now)
		request.Status.CompletedAt = &completedAt
		setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionFalse, issue.Reason, issue.Message, request.Generation, now)
		if shouldMarkInvestigationDegraded(issue.Reason) {
			setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionTrue, issue.Reason, issue.Message, request.Generation, now)
		} else {
			setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, issue.Reason, "request failed without an optional dependency degradation", request.Generation, now)
		}
		return
	}
	if evidence.Issue != nil {
		setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseFailed, evidence.Issue.Message, request.Generation, now)
		applyInvestigationFailure(request, evidence.Issue.Reason, evidence.Issue.Message, v1alpha1.InvestigationStageEvidenceCollection)
		request.Status.Summary = ""
		request.Status.Hypothesis = ""
		request.Status.Confidence = 0
		completedAt := metav1.NewTime(now)
		request.Status.CompletedAt = &completedAt
		setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionFalse, evidence.Issue.Reason, evidence.Issue.Message, request.Generation, now)
		if shouldMarkInvestigationDegraded(evidence.Issue.Reason) {
			setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionTrue, evidence.Issue.Reason, evidence.Issue.Message, request.Generation, now)
		} else {
			setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, evidence.Issue.Reason, "request failed during evidence collection without an optional dependency degradation", request.Generation, now)
		}
		return
	}
	if rca.Issue != nil {
		setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseFailed, rca.Issue.Message, request.Generation, now)
		applyInvestigationFailure(request, rca.Issue.Reason, rca.Issue.Message, v1alpha1.InvestigationStageReasoning)
		request.Status.Summary = evidence.Summary
		request.Status.Hypothesis = ""
		request.Status.Confidence = 0
		completedAt := metav1.NewTime(now)
		request.Status.CompletedAt = &completedAt
		setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionFalse, rca.Issue.Reason, rca.Issue.Message, request.Generation, now)
		if shouldMarkInvestigationDegraded(rca.Issue.Reason) {
			setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionTrue, rca.Issue.Reason, rca.Issue.Message, request.Generation, now)
		} else {
			setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, rca.Issue.Reason, "request failed during RCA generation without an optional dependency degradation", request.Generation, now)
		}
		return
	}

	if rca.Reasoning != nil {
		request.Status.Summary = rca.Reasoning.RiskSummary
		request.Status.Hypothesis = rca.Reasoning.RCA.Hypothesis
		request.Status.Confidence = float64(rca.Reasoning.Confidence.Score) / 100.0
		if strings.TrimSpace(rca.Reasoning.Provider) != "" {
			request.Status.Provider = rca.Reasoning.Provider
		}
		applyStructuredRCAStatus(request, preflight, evidence, rca, now)
	}
	completedAt := metav1.NewTime(now)
	request.Status.CompletedAt = &completedAt
	request.Status.Outcome = v1alpha1.InvestigationOutcomeConfirmed
	request.Status.Failure = nil
	setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseCompleted, request.Status.Summary, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionTrue, "InvestigationCompleted", "evidence collection and RCA generation completed successfully", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "NoDegradation", "request completed successfully without degradation", request.Generation, now)
}

func applyInvestigationFailure(request *v1alpha1.InvestigationRequest, code string, message string, stage string) {
	request.Status.Outcome = v1alpha1.InvestigationOutcomeUnknown
	request.Status.Failure = &v1alpha1.InvestigationFailure{
		Code:      code,
		Message:   message,
		Stage:     stage,
		Retryable: investigationFailureRetryable(code),
	}
	if shouldMarkInvestigationDegraded(code) {
		request.Status.Degradation = &v1alpha1.RCADegradation{
			Partial: true,
			Reasons: []v1alpha1.RCADegradationReason{
				{
					Code:    code,
					Stage:   stage,
					Message: message,
				},
			},
		}
	} else {
		request.Status.Degradation = nil
	}
}

func applyStructuredRCAStatus(request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, evidence investigation.EvidenceCollectionResult, rca investigation.RCAResult, now time.Time) {
	if rca.Reasoning == nil {
		return
	}

	confidence := float64(rca.Reasoning.Confidence.Score) / 100.0
	claims, verification := buildRCAClaims(rca, evidence)
	verifiedScore := confidence
	if verifiedScore > verification.CoverageScore {
		verifiedScore = verification.CoverageScore
	}
	request.Status.Verdict = &v1alpha1.RCAVerdict{
		Outcome:         v1alpha1.InvestigationOutcomeConfirmed,
		Summary:         rca.Reasoning.RiskSummary,
		RootCauseEntity: resourceToTargetRef(preflight.Target),
		RootCauseType:   inferRootCauseType(rca.Reasoning.RCA.Hypothesis, rca.Reasoning.RCA.Causes),
		Confidence:      confidence,
		ConfidenceDetail: &v1alpha1.RCAConfidence{
			ProviderScore: confidence,
			VerifiedScore: verifiedScore,
			Level:         confidenceLevel(verifiedScore),
			Method:        verification.Method,
		},
	}
	request.Status.Claims = claims
	request.Status.AlternativeHypotheses = nil
	request.Status.MissingEvidence = nil
	request.Status.Degradation = &v1alpha1.RCADegradation{Partial: false}
	request.Status.Execution = buildRCAExecution(request, preflight, evidence, rca, investigationExecutionID(request, preflight, evidence), executionStateFinalized, now)
}

func confidenceLevel(score float64) string {
	switch {
	case score >= 0.8:
		return "High"
	case score >= 0.5:
		return "Medium"
	case score > 0:
		return "Low"
	default:
		return "Unknown"
	}
}

func providerRef(provider *v1alpha1.ModelProvider) *v1alpha1.NamespacedObjectReference {
	if provider == nil {
		return nil
	}
	return &v1alpha1.NamespacedObjectReference{Name: provider.Name, Namespace: provider.Namespace}
}

func providerGeneration(provider *v1alpha1.ModelProvider) int64 {
	if provider == nil {
		return 0
	}
	return provider.Generation
}

func providerType(provider *v1alpha1.ModelProvider) string {
	if provider == nil {
		return ""
	}
	return provider.Spec.Provider
}

func providerModel(provider *v1alpha1.ModelProvider) string {
	if provider == nil {
		return ""
	}
	return provider.Spec.Model
}

func providerNameForStatus(request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, rca investigation.RCAResult) string {
	if rca.Reasoning != nil && strings.TrimSpace(rca.Reasoning.Provider) != "" {
		return rca.Reasoning.Provider
	}
	if request.Status.Provider != "" {
		return request.Status.Provider
	}
	if preflight.Provider != nil {
		return preflight.Provider.Name
	}
	return ""
}

func buildRCAExecution(request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, evidence investigation.EvidenceCollectionResult, rca investigation.RCAResult, executionID string, state string, now time.Time) *v1alpha1.RCAExecution {
	providerResult := providerResultFromReasoning(rca)
	return &v1alpha1.RCAExecution{
		ID:                      executionID,
		State:                   state,
		Provider:                providerNameForStatus(request, preflight, rca),
		ProviderRef:             providerRef(preflight.Provider),
		ProviderGeneration:      providerGeneration(preflight.Provider),
		ProviderType:            providerType(preflight.Provider),
		Model:                   providerModel(preflight.Provider),
		RCASchemaVersion:        rcaSchemaVersion,
		CanonicalizationVersion: rcaCanonicalizationVersion,
		ReasoningPolicyVersion:  reasoningPolicyVersion,
		ControllerVersion:       version.Current().Version,
		AttemptCount:            1,
		Attempts: []v1alpha1.RCAExecutionAttempt{
			{
				ID:             defaultExecutionAttemptID,
				IdempotencyKey: executionIdempotencyKey(request, preflight, evidence, defaultExecutionAttemptID),
				Result:         executionAttemptCompleted,
				StartedAt:      request.Status.StartedAt,
				CompletedAt:    &metav1.Time{Time: now},
			},
		},
		DurationSeconds: investigationDurationSeconds(request, now),
		ProviderResult:  providerResult,
	}
}

func providerResultFromReasoning(rca investigation.RCAResult) *v1alpha1.RCAProviderResult {
	if rca.Reasoning == nil {
		return nil
	}
	normalized := normalizedResultFromReasoning(*rca.Reasoning)
	digest := canonicaldigest.SHA256(canonicaldigest.RCAJSONV1, normalized)
	return &v1alpha1.RCAProviderResult{
		SchemaVersion: rcaSchemaVersion,
		Digest: &v1alpha1.RCADigest{
			Algorithm:        digest.Algorithm,
			Canonicalization: digest.Canonicalization,
			Value:            digest.Value,
		},
		NormalizedResult: &normalized,
	}
}

func normalizedResultFromReasoning(reasoning domain.ReasoningOutput) v1alpha1.RCANormalizedResult {
	return v1alpha1.RCANormalizedResult{
		RiskTitle:         reasoning.RiskTitle,
		RiskSummary:       reasoning.RiskSummary,
		Severity:          string(reasoning.Severity),
		ConfidenceScore:   reasoning.Confidence.Score,
		ConfidenceReason:  reasoning.Confidence.Rationale,
		EvidenceCoverage:  reasoning.Confidence.EvidenceCoverage,
		RCAHypothesis:     reasoning.RCA.Hypothesis,
		RCACauses:         append([]string(nil), reasoning.RCA.Causes...),
		RCAEvidence:       append([]string(nil), reasoning.RCA.Evidence...),
		ActionType:        reasoning.Remediation.ActionType,
		ActionDescription: reasoning.Remediation.Description,
		ActionParameters:  copyStringMap(reasoning.Remediation.Parameters),
		RollbackPlan:      append([]string(nil), reasoning.Remediation.RollbackPlan...),
		RunbookRefs:       append([]string(nil), reasoning.RunbookRefs...),
		ServiceDocs:       append([]string(nil), reasoning.ServiceDocs...),
		Provider:          reasoning.Provider,
	}
}

func reasoningFromProviderResult(result *v1alpha1.RCAProviderResult) *domain.ReasoningOutput {
	if result == nil || result.NormalizedResult == nil {
		return nil
	}
	normalized := result.NormalizedResult
	severity := domain.Severity(normalized.Severity)
	return &domain.ReasoningOutput{
		RiskTitle:   normalized.RiskTitle,
		RiskSummary: normalized.RiskSummary,
		Severity:    severity,
		Confidence: domain.Confidence{
			Score:            normalized.ConfidenceScore,
			Severity:         severity,
			Rationale:        normalized.ConfidenceReason,
			EvidenceCoverage: normalized.EvidenceCoverage,
		},
		RCA: domain.RCASummary{
			Hypothesis: normalized.RCAHypothesis,
			Causes:     append([]string(nil), normalized.RCACauses...),
			Evidence:   append([]string(nil), normalized.RCAEvidence...),
		},
		Remediation: domain.Remediation{
			ActionType:   normalized.ActionType,
			Description:  normalized.ActionDescription,
			Parameters:   copyStringMap(normalized.ActionParameters),
			RollbackPlan: append([]string(nil), normalized.RollbackPlan...),
		},
		RunbookRefs: append([]string(nil), normalized.RunbookRefs...),
		ServiceDocs: append([]string(nil), normalized.ServiceDocs...),
		Provider:    normalized.Provider,
	}
}

func reusableProviderResult(execution *v1alpha1.RCAExecution, executionID string) *v1alpha1.RCAProviderResult {
	if execution == nil || execution.ID != executionID || execution.ProviderResult == nil || execution.ProviderResult.NormalizedResult == nil {
		return nil
	}
	switch execution.State {
	case executionStateProviderCompleted, executionStateFinalized:
		return execution.ProviderResult
	default:
		return nil
	}
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func investigationExecutionID(request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, evidence investigation.EvidenceCollectionResult) string {
	input := map[string]any{
		"requestUID":              string(request.UID),
		"requestGeneration":       request.Generation,
		"target":                  preflight.Target,
		"targetUID":               lineageTargetUID(request.Status.Lineage),
		"evidenceBundleDigest":    evidenceBundleDigest(evidence.EvidenceRefs),
		"providerType":            providerType(preflight.Provider),
		"providerGeneration":      providerGeneration(preflight.Provider),
		"model":                   providerModel(preflight.Provider),
		"reasoningPolicyVersion":  reasoningPolicyVersion,
		"rcaSchemaVersion":        rcaSchemaVersion,
		"canonicalizationVersion": rcaCanonicalizationVersion,
	}
	return canonicaldigest.String(canonicaldigest.RCAJSONV1, input)
}

func executionIdempotencyKey(request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, evidence investigation.EvidenceCollectionResult, attemptID string) string {
	input := map[string]any{
		"executionID": investigationExecutionID(request, preflight, evidence),
		"attemptID":   attemptID,
	}
	return canonicaldigest.String(canonicaldigest.RCAJSONV1, input)
}

func evidenceBundleDigest(refs []v1alpha1.EvidenceRef) string {
	return canonicaldigest.String(canonicaldigest.ObservationJSONV1, refs)
}

func lineageTargetUID(lineage *v1alpha1.InvestigationLineage) string {
	if lineage == nil {
		return ""
	}
	return lineage.TargetUID
}

func lineageForReconcile(existing *v1alpha1.InvestigationLineage, annotations map[string]string) *v1alpha1.InvestigationLineage {
	if existing != nil {
		return copyInvestigationLineage(existing)
	}
	return lineageFromAnnotations(annotations)
}

func copyInvestigationLineage(in *v1alpha1.InvestigationLineage) *v1alpha1.InvestigationLineage {
	if in == nil {
		return nil
	}
	out := *in
	if in.FindingIdentity != nil {
		identity := *in.FindingIdentity
		out.FindingIdentity = &identity
	}
	return &out
}

func lineageFromAnnotations(annotations map[string]string) *v1alpha1.InvestigationLineage {
	sourceRef := strings.TrimSpace(annotations[annotationLineageSource])
	fingerprint := strings.TrimSpace(annotations[annotationFindingFingerprint])
	targetUID := strings.TrimSpace(annotations[annotationTargetUID])
	if sourceRef == "" && fingerprint == "" && targetUID == "" {
		return nil
	}

	namespace, name := splitNamespacedName(sourceRef)
	generation, _ := strconv.ParseInt(strings.TrimSpace(annotations[annotationLineageGeneration]), 10, 64)
	depth, _ := strconv.ParseInt(strings.TrimSpace(annotations[annotationInvestigationDepth]), 10, 32)
	sourceKind := strings.TrimSpace(annotations[annotationLineageSourceKind])
	if sourceKind == "" {
		sourceKind = "RiskRule"
	}
	sourceAPI := strings.TrimSpace(annotations[annotationLineageSourceAPI])
	if sourceAPI == "" {
		sourceAPI = v1alpha1.SchemeGroupVersion.String()
	}
	return &v1alpha1.InvestigationLineage{
		Source: v1alpha1.InvestigationLineageSource{
			APIVersion: sourceAPI,
			Kind:       sourceKind,
			Namespace:  namespace,
			Name:       name,
			UID:        strings.TrimSpace(annotations[annotationLineageSourceUID]),
			Generation: generation,
		},
		TargetUID:          targetUID,
		FindingFingerprint: fingerprint,
		FindingIdentity:    findingIdentityFromAnnotations(annotations),
		InvestigationDepth: int32(depth),
	}
}

func findingIdentityFromAnnotations(annotations map[string]string) *v1alpha1.FindingIdentity {
	if annotations == nil {
		return nil
	}
	identity := &v1alpha1.FindingIdentity{
		SchemaVersion:          strings.TrimSpace(annotations[annotationFindingSchema]),
		ObjectFindingIdentity:  strings.TrimSpace(annotations[annotationObjectFindingID]),
		LogicalFindingIdentity: strings.TrimSpace(annotations[annotationLogicalFindingID]),
		IncidentOccurrence:     strings.TrimSpace(annotations[annotationIncidentOccurrence]),
		FindingType:            strings.TrimSpace(annotations[annotationFindingType]),
		WindowBucket:           strings.TrimSpace(annotations[annotationWindowBucket]),
	}
	if targetGeneration, err := strconv.ParseInt(strings.TrimSpace(annotations[annotationTargetGeneration]), 10, 64); err == nil {
		identity.TargetGeneration = targetGeneration
	}
	if identity.SchemaVersion == "" && identity.ObjectFindingIdentity == "" && identity.LogicalFindingIdentity == "" && identity.IncidentOccurrence == "" {
		return nil
	}
	return identity
}

func splitNamespacedName(value string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(value), "/", 2)
	if len(parts) != 2 {
		return "", strings.TrimSpace(value)
	}
	return parts[0], parts[1]
}

func buildRCAClaims(rca investigation.RCAResult, evidence investigation.EvidenceCollectionResult) ([]v1alpha1.RCAClaim, verifier.Result) {
	claims := make([]v1alpha1.RCAClaim, 0, 1+len(rca.Reasoning.RCA.Causes))
	if strings.TrimSpace(rca.Reasoning.RiskSummary) != "" {
		claims = append(claims, v1alpha1.RCAClaim{
			ID:        "claim-001",
			Statement: rca.Reasoning.RiskSummary,
		})
	}
	for _, cause := range rca.Reasoning.RCA.Causes {
		if strings.TrimSpace(cause) == "" {
			continue
		}
		claims = append(claims, v1alpha1.RCAClaim{
			ID:        claimID(len(claims) + 1),
			Statement: cause,
		})
	}
	verification := verifier.VerifyClaims(verifierClaims(claims), verifierEvidenceRefs(evidence.EvidenceRefs))
	return applyClaimVerification(claims, verification), verification
}

func verifierClaims(claims []v1alpha1.RCAClaim) []verifier.Claim {
	out := make([]verifier.Claim, 0, len(claims))
	for _, claim := range claims {
		out = append(out, verifier.Claim{
			ID:        claim.ID,
			Statement: claim.Statement,
		})
	}
	return out
}

func verifierEvidenceRefs(refs []v1alpha1.EvidenceRef) []verifier.EvidenceRef {
	out := make([]verifier.EvidenceRef, 0, len(refs))
	for index, ref := range refs {
		id := strings.TrimSpace(ref.ID)
		if id == "" {
			id = evidenceID(index + 1)
		}
		out = append(out, verifier.EvidenceRef{
			ID:      id,
			Kind:    ref.Kind,
			Source:  ref.Source,
			Summary: ref.Summary,
		})
	}
	return out
}

func applyClaimVerification(claims []v1alpha1.RCAClaim, verification verifier.Result) []v1alpha1.RCAClaim {
	byID := make(map[string]verifier.ClaimResult, len(verification.Claims))
	for _, result := range verification.Claims {
		byID[result.ID] = result
	}
	for i := range claims {
		result, ok := byID[claims[i].ID]
		if !ok {
			continue
		}
		claims[i].EvidenceRefs = append([]string(nil), result.EvidenceRefs...)
		claims[i].Verification = result.Verification
	}
	return claims
}

func inferRootCauseType(hypothesis string, causes []string) string {
	text := strings.ToLower(hypothesis + " " + strings.Join(causes, " "))
	switch {
	case strings.Contains(text, "config"):
		return "ConfigurationMismatch"
	case strings.Contains(text, "backoff") || strings.Contains(text, "crash") || strings.Contains(text, "restart"):
		return "CrashLoop"
	case strings.Contains(text, "latency"):
		return "LatencyRegression"
	case strings.Contains(text, "memory") || strings.Contains(text, "oom"):
		return "ResourcePressure"
	default:
		return "WorkloadDegradation"
	}
}

func investigationDurationSeconds(request *v1alpha1.InvestigationRequest, now time.Time) int64 {
	if request.Status.StartedAt == nil {
		return 0
	}
	duration := now.Sub(request.Status.StartedAt.Time)
	if duration < 0 {
		return 0
	}
	return int64(duration.Seconds())
}

func claimID(index int) string {
	return "claim-" + zeroPad3(index)
}

func evidenceID(index int) string {
	return "evidence-" + zeroPad3(index)
}

func zeroPad3(index int) string {
	if index < 10 {
		return "00" + strconv.Itoa(index)
	}
	if index < 100 {
		return "0" + strconv.Itoa(index)
	}
	return strconv.Itoa(index)
}

func shouldMarkInvestigationDegraded(reason string) bool {
	switch reason {
	case "DataSourceNotSpecified", "DatasourceRegistryUnavailable", "DataSourceNotFound", "DatasourceQueryFailed", "DatasourceAuthFailed", "DatasourceRateLimited", "DatasourceUnavailable", "DatasourceRequestInvalid", "InvalidDatasourceResponse", "CapabilityMismatch", "ResolverUnavailable", "ProviderNotFound", "GatewayUnavailable", "ProviderUnavailable", "ProviderUnsupported", "ProviderAuthFailed", "ProviderRateLimited", "ProviderRequestInvalid", "ProviderFallbackLoop", "SecretReaderUnavailable", "SecretReadFailed", "SecretRefMissing", "SecretNotFound", "SecretKeyMissing", "SecretValueEmpty", "APIKeyMissing", "InvalidProviderResponse":
		return true
	default:
		return false
	}
}

func investigationFailureStage(reason string) string {
	switch reason {
	case "TargetNotFound", "UnsupportedTargetKind", "ResolverUnavailable":
		return v1alpha1.InvestigationStageTargetResolution
	case "DataSourceNotSpecified", "DatasourceRegistryUnavailable", "DataSourceNotFound", "DatasourceQueryFailed", "DatasourceAuthFailed", "DatasourceRateLimited", "DatasourceUnavailable", "DatasourceRequestInvalid", "InvalidDatasourceResponse", "CapabilityMismatch":
		return v1alpha1.InvestigationStageEvidenceCollection
	case "ProviderNotFound", "GatewayUnavailable", "ProviderUnavailable", "ProviderUnsupported", "ProviderAuthFailed", "ProviderRateLimited", "ProviderRequestInvalid", "ProviderFallbackLoop", "SecretReaderUnavailable", "SecretReadFailed", "SecretRefMissing", "SecretNotFound", "SecretKeyMissing", "SecretValueEmpty", "APIKeyMissing", "InvalidProviderResponse":
		return v1alpha1.InvestigationStageReasoning
	default:
		return v1alpha1.InvestigationStageReasoning
	}
}

func investigationFailureRetryable(reason string) bool {
	switch reason {
	case "ResolverUnavailable", "DatasourceRegistryUnavailable", "DatasourceQueryFailed", "DatasourceRateLimited", "DatasourceUnavailable", "GatewayUnavailable", "ProviderUnavailable", "ProviderRateLimited", "SecretReaderUnavailable", "SecretReadFailed":
		return true
	default:
		return false
	}
}

func shouldSkipInvestigationExecution(request *v1alpha1.InvestigationRequest) bool {
	return request.Status.ObservedGeneration == request.Generation && isTerminalInvestigationPhase(request.Status.Phase)
}

func isTerminalInvestigationPhase(phase string) bool {
	switch phase {
	case v1alpha1.PhaseCompleted, v1alpha1.PhaseFailed, v1alpha1.PhaseRejected:
		return true
	default:
		return false
	}
}

func (r *InvestigationRequestReconciler) handleTTL(ctx context.Context, request *v1alpha1.InvestigationRequest, now time.Time) (ctrl.Result, bool, error) {
	if request.Spec.TTLSeconds <= 0 || !request.DeletionTimestamp.IsZero() || !shouldSkipInvestigationExecution(request) {
		return ctrl.Result{}, false, nil
	}

	expiresAt := investigationExpiryTime(request, now)
	if !now.Before(expiresAt) {
		if err := r.Delete(ctx, request); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, true, err
		}
		return ctrl.Result{}, true, nil
	}

	return ctrl.Result{RequeueAfter: expiresAt.Sub(now)}, true, nil
}

func investigationExpiryTime(request *v1alpha1.InvestigationRequest, now time.Time) time.Time {
	baseTime := now
	switch {
	case request.Status.CompletedAt != nil:
		baseTime = request.Status.CompletedAt.Time
	case !request.Status.UpdatedAt.IsZero():
		baseTime = request.Status.UpdatedAt.Time
	case !request.CreationTimestamp.IsZero():
		baseTime = request.CreationTimestamp.Time
	}
	return baseTime.Add(time.Duration(request.Spec.TTLSeconds) * time.Second)
}

func (r *InvestigationRequestReconciler) promoteToRiskSignal(ctx context.Context, request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, evidence investigation.EvidenceCollectionResult, rca investigation.RCAResult, now time.Time) (*v1alpha1.NamespacedObjectReference, error) {
	if rca.Reasoning == nil {
		return nil, nil
	}

	riskSignal := &v1alpha1.RiskSignal{}
	riskSignal.Name = investigationRiskSignalName(request.Name, preflight.Target.Name)
	riskSignal.Namespace = preflight.Target.Namespace

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, riskSignal, func() error {
		if request.Namespace == riskSignal.Namespace {
			if err := controllerutil.SetControllerReference(request, riskSignal, r.Scheme); err != nil {
				return err
			}
		}
		if riskSignal.Labels == nil {
			riskSignal.Labels = map[string]string{}
		}
		if riskSignal.Annotations == nil {
			riskSignal.Annotations = map[string]string{}
		}
		riskSignal.Labels[labelManagedBy] = "investigationrequest-controller"
		riskSignal.Annotations[annotationTargetRef] = preflight.Target.Namespace + "/" + preflight.Target.Name
		riskSignal.Annotations[annotationDetectionSource] = "investigation-request"
		riskSignal.Annotations["fluxagent.aiops.platform/investigation-request"] = request.Namespace + "/" + request.Name
		if request.Status.Lineage != nil {
			applyFindingIdentityAnnotations(riskSignal.Annotations, request.Status.Lineage.FindingIdentity)
		}

		riskSignal.Spec.Target = resourceToTargetRef(preflight.Target)
		riskSignal.Spec.SignalType = investigationSignalType(preflight)
		if request.Status.Lineage != nil && request.Status.Lineage.FindingIdentity != nil {
			identity := *request.Status.Lineage.FindingIdentity
			riskSignal.Spec.FindingIdentity = &identity
		}
		riskSignal.Spec.Severity = string(rca.Reasoning.Severity)
		riskSignal.Spec.Confidence = rca.Reasoning.Confidence.Score
		riskSignal.Spec.DryRun = true
		riskSignal.Spec.TTLSeconds = 3600
		riskSignal.Spec.Evidence = append([]v1alpha1.EvidenceRef(nil), evidence.EvidenceRefs...)
		riskSignal.Spec.ActionType = "notification.sendSlack"
		riskSignal.Spec.Parameters = map[string]string{
			"channel":              "webhook",
			"mode":                 "read-only",
			"investigationRequest": request.Name,
			"targetRef":            preflight.Target.Namespace + "/" + preflight.Target.Name,
			"summaryMode":          "investigation-promoted",
		}
		if request.Status.Lineage != nil && request.Status.Lineage.FindingIdentity != nil {
			riskSignal.Spec.Parameters["objectFindingIdentity"] = request.Status.Lineage.FindingIdentity.ObjectFindingIdentity
			riskSignal.Spec.Parameters["logicalFindingIdentity"] = request.Status.Lineage.FindingIdentity.LogicalFindingIdentity
			riskSignal.Spec.Parameters["incidentOccurrence"] = request.Status.Lineage.FindingIdentity.IncidentOccurrence
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	original := riskSignal.DeepCopy()
	setRiskSignalStatus(&riskSignal.Status, v1alpha1.PhaseConfirmed, rca.Reasoning.RiskSummary, riskSignal.Generation, now)
	setStatusCondition(&riskSignal.Status.Conditions, conditionEvidenceReady, metav1.ConditionTrue, "EvidenceCollected", evidence.Summary, riskSignal.Generation, now)
	applyRCAResult(&riskSignal.Status, rcaResult{
		Reasoning:    rca.Reasoning,
		ProviderName: request.Status.Provider,
		Condition:    rcaCondition(metav1.ConditionTrue, "ProviderSucceeded", "RCA promoted from investigation request", now),
	})
	if statusChangedRiskSignal(original, riskSignal) {
		if err := r.Status().Update(ctx, riskSignal); err != nil && !apierrors.IsConflict(err) {
			return nil, err
		}
	}

	return &v1alpha1.NamespacedObjectReference{
		Name:      riskSignal.Name,
		Namespace: riskSignal.Namespace,
	}, nil
}

func investigationRiskSignalName(requestName, targetName string) string {
	return riskSignalName(requestName+"-investigation", targetName)
}

func investigationSignalType(preflight investigation.PreflightResult) string {
	if len(preflight.CollectionPlan) > 0 {
		return string(preflight.CollectionPlan[0].QueryType)
	}
	return "investigation"
}
