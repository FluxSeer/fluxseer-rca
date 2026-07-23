package controllers

import (
	"context"
	"reflect"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/investigation"
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
	setInvestigationRequestStatus(&investigation.Status, v1alpha1.PhaseObserved, message, investigation.Generation, now())
	if investigation.Status.StartedAt == nil || restarting {
		startedAt := metav1.NewTime(now())
		investigation.Status.StartedAt = &startedAt
	}
	investigation.Status.CompletedAt = nil
	investigation.Status.Summary = ""
	investigation.Status.Hypothesis = ""
	investigation.Status.Confidence = 0
	investigation.Status.Provider = ""
	investigation.Status.EvidenceRefs = nil
	investigation.Status.LinkedRiskSignalRef = nil

	if invalidMessage := validateInvestigationRequestSpec(investigation.Spec); invalidMessage != "" {
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
		rca, rcaErr := r.generateRCA(ctx, &investigation, preflight, evidence, now())
		if rcaErr != nil {
			return ctrl.Result{}, rcaErr
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

	if !reflect.DeepEqual(original.Status, investigation.Status) {
		if err := r.Status().Update(ctx, &investigation); err != nil && !apierrors.IsConflict(err) {
			return ctrl.Result{}, err
		}
	}
	return ttlResult, nil
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
	}
	completedAt := metav1.NewTime(now)
	request.Status.CompletedAt = &completedAt
	setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseCompleted, request.Status.Summary, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionTrue, "InvestigationCompleted", "evidence collection and RCA generation completed successfully", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "NoDegradation", "request completed successfully without degradation", request.Generation, now)
}

func shouldMarkInvestigationDegraded(reason string) bool {
	switch reason {
	case "DataSourceNotSpecified", "DatasourceRegistryUnavailable", "DataSourceNotFound", "DatasourceQueryFailed", "CapabilityMismatch", "ResolverUnavailable", "ProviderNotFound", "GatewayUnavailable", "ProviderUnavailable", "ProviderUnsupported", "ProviderAuthFailed", "ProviderRateLimited", "ProviderRequestInvalid", "ProviderFallbackLoop", "SecretReaderUnavailable", "SecretReadFailed", "SecretRefMissing", "SecretNotFound", "SecretKeyMissing", "SecretValueEmpty", "APIKeyMissing", "InvalidProviderResponse":
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

		riskSignal.Spec.Target = resourceToTargetRef(preflight.Target)
		riskSignal.Spec.SignalType = investigationSignalType(preflight)
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
