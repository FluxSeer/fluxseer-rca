package controllers

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/canonicaldigest"
	"github.com/FluxSeer/fluxseer-rca/internal/dataclassification"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	evidencepkg "github.com/FluxSeer/fluxseer-rca/internal/evidence"
	"github.com/FluxSeer/fluxseer-rca/internal/investigation"
	"github.com/FluxSeer/fluxseer-rca/internal/rcametrics"
	"github.com/FluxSeer/fluxseer-rca/internal/statusbudget"
	"github.com/FluxSeer/fluxseer-rca/internal/verifier"
	"github.com/FluxSeer/fluxseer-rca/internal/version"
)

const (
	rcaSchemaVersion                       = "fluxseer-rca-result-v1"
	rcaCanonicalizationVersion             = canonicaldigest.RCAJSONV1
	reasoningPolicyVersion                 = "rca-v2-compat"
	executionStateProviderCompleted        = "ProviderCompleted"
	executionStateFinalized                = "Finalized"
	executionAttemptCompleted              = "Completed"
	defaultExecutionAttemptID              = "attempt-001"
	defaultMaxInvestigationDepth           = int32(1)
	investigationStageDataSourceResolution = "DataSourceResolution"
	investigationStageQueryValidation      = "QueryValidation"
	investigationStageProviderResolution   = "ProviderResolution"
	investigationStageProviderEgressPolicy = "ProviderEgressPolicy"
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
	investigation.Status.EvidenceCoverage = nil
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
	} else if invalidIssue := validateInvestigationRequestSpecIssue(investigation.Spec); invalidIssue != nil {
		applyInvalidInvestigationStatus(&investigation, invalidIssue.Reason, invalidIssue.Message, now())
	} else {
		preflight, preflightErr := r.preflight(ctx, &investigation)
		if preflightErr != nil {
			return ctrl.Result{}, preflightErr
		}
		evidence, evidenceErr := r.collectEvidence(ctx, &investigation, preflight, now())
		if evidenceErr != nil {
			return ctrl.Result{}, evidenceErr
		}
		if gate := evaluateEvidenceRequirements(investigation.Spec, evidence); gate != nil {
			switch {
			case !gate.Complete:
				applyEvidenceRequirementInconclusiveStatus(&investigation, evidence, *gate, now())
				applyInvestigationStatusBudget(&investigation, now())
				if !reflect.DeepEqual(original.Status, investigation.Status) {
					if err := r.Status().Update(ctx, &investigation); err != nil && !recordStatusUpdateConflict("InvestigationRequest", err) {
						return ctrl.Result{}, err
					}
				}
				return ttlResult, nil
			case gate.NoIssueFound:
				applyEvidenceRequirementNoIssueFoundStatus(&investigation, evidence, *gate, now())
				applyInvestigationStatusBudget(&investigation, now())
				if !reflect.DeepEqual(original.Status, investigation.Status) {
					if err := r.Status().Update(ctx, &investigation); err != nil && !recordStatusUpdateConflict("InvestigationRequest", err) {
						return ctrl.Result{}, err
					}
				}
				return ttlResult, nil
			}
		}
		executionID := investigationExecutionID(&investigation, preflight, evidence)
		rca := emptyRCAResult()
		if providerResult := reusableProviderResult(original.Status.Execution, executionID); providerResult != nil {
			rcametrics.RecordDeduplicationHit("provider_checkpoint")
			rca.Reasoning = reasoningFromProviderResult(providerResult)
		} else {
			generatedRCA, rcaErr := r.generateRCA(ctx, &investigation, preflight, evidence, now())
			if rcaErr != nil {
				return ctrl.Result{}, rcaErr
			}
			rca = generatedRCA
			if rca.Reasoning != nil {
				persistErr := r.persistProviderCompletedCheckpoint(ctx, &investigation, preflight, evidence, rca, executionID, now())
				if persistErr != nil && !recordStatusUpdateConflict("InvestigationRequest", persistErr) {
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
		if err := r.Status().Update(ctx, &investigation); err != nil && !recordStatusUpdateConflict("InvestigationRequest", err) {
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

type evidenceRequirementGate struct {
	Profile      string
	Complete     bool
	NoIssueFound bool
	Missing      []v1alpha1.RCAMissingEvidence
	Coverage     *v1alpha1.RCAEvidenceCoverage
	Message      string
}

func evaluateEvidenceRequirements(spec v1alpha1.InvestigationRequestSpec, evidence investigation.EvidenceCollectionResult) *evidenceRequirementGate {
	profile := strings.TrimSpace(spec.EvidenceRequirements.Profile)
	if profile == "" || evidence.Issue != nil {
		return nil
	}
	requiredKinds := requiredEvidenceKindsForProfile(profile)
	if len(requiredKinds) == 0 {
		return nil
	}

	present := map[string]bool{}
	for _, query := range spec.Queries {
		kind := strings.TrimSpace(query.QueryType)
		if kind != "" {
			present[kind] = true
		}
	}
	for _, ref := range evidence.EvidenceRefs {
		kind := strings.TrimSpace(ref.Kind)
		if kind != "" {
			present[kind] = true
		}
	}
	missing := make([]v1alpha1.RCAMissingEvidence, 0)
	for _, kind := range requiredKinds {
		if !present[kind] {
			missing = append(missing, v1alpha1.RCAMissingEvidence{
				Source: kind,
				Reason: "RequiredEvidenceMissing",
			})
		}
	}
	missing = append(missing, missingSemanticEvidenceCoverage(profile, spec, evidence)...)
	coverage := evidenceCoverageAudit(profile, spec, evidence, missing)
	if len(missing) == 0 {
		return &evidenceRequirementGate{
			Profile:      profile,
			Complete:     true,
			NoIssueFound: evidenceProfileHasNoIssue(profile, spec, evidence),
			Coverage:     coverage,
			Message:      fmt.Sprintf("required evidence profile %q is complete", profile),
		}
	}
	return &evidenceRequirementGate{
		Profile:  profile,
		Complete: false,
		Missing:  missing,
		Coverage: coverage,
		Message:  fmt.Sprintf("required evidence profile %q is incomplete", profile),
	}
}

func evidenceCoverageAudit(profile string, spec v1alpha1.InvestigationRequestSpec, evidence investigation.EvidenceCollectionResult, missing []v1alpha1.RCAMissingEvidence) *v1alpha1.RCAEvidenceCoverage {
	checks := requiredEvidenceChecksForProfile(profile)
	if len(checks) == 0 {
		return nil
	}
	completed := make([]string, 0, len(checks))
	incomplete := make([]string, 0, len(checks))
	for _, check := range checks {
		if evidenceCoverageCheckComplete(check, spec, evidence, missing) {
			completed = append(completed, check)
			continue
		}
		incomplete = append(incomplete, check)
	}
	return &v1alpha1.RCAEvidenceCoverage{
		Profile:          strings.TrimSpace(profile),
		RequiredChecks:   append([]string(nil), checks...),
		CompletedChecks:  completed,
		IncompleteChecks: incomplete,
		IssueMatches:     int32(evidenceProfileIssueMatchCount(profile, evidence)),
	}
}

func requiredEvidenceChecksForProfile(profile string) []string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "crashloopbackoff":
		return []string{"event:CrashLoopBackOff"}
	case "imagepullbackoff":
		return []string{"event:ImagePullBackOff"}
	case "oomkilled":
		return []string{"event:OOMKilled", "metric:Memory"}
	case "latencyregression":
		return []string{"metric:Latency"}
	case "rolloutlatencyregression":
		return []string{"metric:Latency", "deploymentCondition:Rollout"}
	default:
		return nil
	}
}

func evidenceCoverageCheckComplete(check string, spec v1alpha1.InvestigationRequestSpec, evidence investigation.EvidenceCollectionResult, missing []v1alpha1.RCAMissingEvidence) bool {
	switch check {
	case "event:CrashLoopBackOff":
		return !missingReasonPresent(missing, "CrashLoopEvidenceCoverageMissing") &&
			eventCoveragePresent(spec, evidence, "crashloopbackoff", "backoff", "back-off", "unhealthy", "killing", "container crashed")
	case "event:ImagePullBackOff":
		return !missingReasonPresent(missing, "ImagePullEvidenceCoverageMissing") &&
			eventCoveragePresent(spec, evidence, "imagepullbackoff", "errimagepull", "failed to pull image", "pull access denied")
	case "event:OOMKilled":
		return !missingReasonPresent(missing, "OOMEventEvidenceCoverageMissing") &&
			eventCoveragePresent(spec, evidence, "oomkilled", "out of memory", "memory pressure", "memory limit")
	case "metric:Memory":
		return evidenceKindPresent(spec, evidence, string(domain.QueryTypeMetric), "memory", "oom", "container_memory")
	case "metric:Latency":
		return evidenceKindPresent(spec, evidence, string(domain.QueryTypeMetric), "latency", "duration", "http_request")
	case "deploymentCondition:Rollout":
		return evidenceKindPresent(spec, evidence, "deploymentCondition", "rollout", "deployment", "progressing", "available")
	default:
		return false
	}
}

func missingReasonPresent(missing []v1alpha1.RCAMissingEvidence, reason string) bool {
	for _, item := range missing {
		if item.Reason == reason {
			return true
		}
	}
	return false
}

func evidenceKindPresent(spec v1alpha1.InvestigationRequestSpec, evidence investigation.EvidenceCollectionResult, kind string, needles ...string) bool {
	normalizedKind := strings.ToLower(strings.TrimSpace(kind))
	for _, ref := range evidence.EvidenceRefs {
		if strings.ToLower(strings.TrimSpace(ref.Kind)) != normalizedKind {
			continue
		}
		if len(needles) == 0 || containsAny(strings.ToLower(strings.Join([]string{ref.Source, ref.Reason, ref.Summary, ref.Query}, " ")), needles...) {
			return true
		}
	}
	for _, query := range spec.Queries {
		if strings.ToLower(strings.TrimSpace(query.QueryType)) != normalizedKind {
			continue
		}
		values := append([]string{query.Name, query.Query, query.QueryTemplate}, query.Reasons...)
		if len(needles) == 0 || containsAny(strings.ToLower(strings.Join(values, " ")), needles...) {
			return true
		}
	}
	return false
}

func evidenceProfileIssueMatchCount(profile string, evidence investigation.EvidenceCollectionResult) int {
	profileKey := strings.ToLower(strings.TrimSpace(profile))
	if profileKey == "" {
		return 0
	}
	matches := 0
	for _, ref := range evidence.EvidenceRefs {
		if evidenceRefRelevantForProfile(profileKey, ref) && evidenceRefMatchesProfileIssue(profileKey, ref) {
			matches++
		}
	}
	return matches
}

func missingSemanticEvidenceCoverage(profile string, spec v1alpha1.InvestigationRequestSpec, evidence investigation.EvidenceCollectionResult) []v1alpha1.RCAMissingEvidence {
	profileKey := strings.ToLower(strings.TrimSpace(profile))
	switch profileKey {
	case "crashloopbackoff":
		if eventCoveragePresent(spec, evidence, "crashloopbackoff", "backoff", "back-off", "unhealthy", "killing", "container crashed") {
			return nil
		}
		return []v1alpha1.RCAMissingEvidence{{Source: string(domain.QueryTypeEvent), Reason: "CrashLoopEvidenceCoverageMissing"}}
	case "imagepullbackoff":
		if eventCoveragePresent(spec, evidence, "imagepullbackoff", "errimagepull", "failed to pull image", "pull access denied") {
			return nil
		}
		return []v1alpha1.RCAMissingEvidence{{Source: string(domain.QueryTypeEvent), Reason: "ImagePullEvidenceCoverageMissing"}}
	case "oomkilled":
		if eventCoveragePresent(spec, evidence, "oomkilled", "out of memory", "memory pressure", "memory limit") {
			return nil
		}
		return []v1alpha1.RCAMissingEvidence{{Source: string(domain.QueryTypeEvent), Reason: "OOMEventEvidenceCoverageMissing"}}
	default:
		return nil
	}
}

func eventCoveragePresent(spec v1alpha1.InvestigationRequestSpec, evidence investigation.EvidenceCollectionResult, needles ...string) bool {
	for _, ref := range evidence.EvidenceRefs {
		if strings.ToLower(strings.TrimSpace(ref.Kind)) != string(domain.QueryTypeEvent) {
			continue
		}
		if containsAny(strings.ToLower(strings.Join([]string{ref.Reason, ref.Summary, ref.Query}, " ")), needles...) {
			return true
		}
	}
	for _, query := range spec.Queries {
		if strings.ToLower(strings.TrimSpace(query.QueryType)) != string(domain.QueryTypeEvent) {
			continue
		}
		values := append([]string{query.Name, query.Query, query.QueryTemplate}, query.Reasons...)
		if containsAny(strings.ToLower(strings.Join(values, " ")), needles...) {
			return true
		}
	}
	return false
}

func requiredEvidenceKindsForProfile(profile string) []string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "crashloopbackoff", "imagepullbackoff":
		return []string{string(domain.QueryTypeEvent)}
	case "oomkilled":
		return []string{string(domain.QueryTypeEvent), string(domain.QueryTypeMetric)}
	case "latencyregression":
		return []string{string(domain.QueryTypeMetric)}
	case "rolloutlatencyregression":
		return []string{string(domain.QueryTypeMetric), "deploymentCondition"}
	default:
		return nil
	}
}

func evidenceProfileHasNoIssue(profile string, spec v1alpha1.InvestigationRequestSpec, evidence investigation.EvidenceCollectionResult) bool {
	profileKey := strings.ToLower(strings.TrimSpace(profile))
	if profileKey == "" {
		return false
	}
	relevant := 0
	for _, ref := range evidence.EvidenceRefs {
		if !evidenceRefRelevantForProfile(profileKey, ref) {
			continue
		}
		relevant++
		if evidenceRefMatchesProfileIssue(profileKey, ref) {
			return false
		}
	}
	if relevant > 0 {
		return true
	}
	switch profileKey {
	case "crashloopbackoff":
		return eventCoveragePresent(spec, evidence, "crashloopbackoff", "backoff", "back-off", "unhealthy", "killing", "container crashed")
	case "imagepullbackoff":
		return eventCoveragePresent(spec, evidence, "imagepullbackoff", "errimagepull", "failed to pull image", "pull access denied")
	case "oomkilled":
		return eventCoveragePresent(spec, evidence, "oomkilled", "out of memory", "memory pressure", "memory limit")
	default:
		return false
	}
}

func evidenceRefRelevantForProfile(profile string, ref v1alpha1.EvidenceRef) bool {
	kind := strings.ToLower(strings.TrimSpace(ref.Kind))
	switch profile {
	case "crashloopbackoff", "imagepullbackoff":
		return kind == string(domain.QueryTypeEvent)
	case "oomkilled":
		return kind == string(domain.QueryTypeEvent) || kind == string(domain.QueryTypeMetric)
	case "latencyregression":
		return kind == string(domain.QueryTypeMetric)
	case "rolloutlatencyregression":
		return kind == string(domain.QueryTypeMetric) || kind == strings.ToLower("deploymentCondition")
	default:
		return false
	}
}

func evidenceRefMatchesProfileIssue(profile string, ref v1alpha1.EvidenceRef) bool {
	text := strings.ToLower(strings.Join([]string{ref.Kind, ref.Source, ref.Reason, ref.Summary}, " "))
	switch profile {
	case "imagepullbackoff":
		return containsAny(text, "imagepullbackoff", "errimagepull", "failed to pull image", "pull access denied")
	case "crashloopbackoff":
		return containsAny(text, "crashloopbackoff", "backoff", "back-off", "container crashed", "unhealthy", "killing")
	case "oomkilled":
		return containsAny(text, "oomkilled", "out of memory", "memory pressure", "memory limit") || metricEvidenceValueAboveZero(ref)
	case "latencyregression":
		return metricEvidenceValueAboveZero(ref)
	case "rolloutlatencyregression":
		return metricEvidenceValueAboveZero(ref) ||
			containsAny(text, "progressdeadlinexceeded", "unavailable", "replicafailure", "available=false", "progressing=false")
	default:
		return false
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func metricEvidenceValueAboveZero(ref v1alpha1.EvidenceRef) bool {
	if strings.ToLower(strings.TrimSpace(ref.Kind)) != string(domain.QueryTypeMetric) {
		return false
	}
	var lastValue *float64
	for _, token := range strings.FieldsFunc(ref.Summary, func(r rune) bool {
		return !(r == '.' || r == '-' || r >= '0' && r <= '9')
	}) {
		value, err := strconv.ParseFloat(strings.TrimSpace(token), 64)
		if err == nil {
			lastValue = &value
		}
	}
	return lastValue != nil && *lastValue > 0
}

func applyEvidenceRequirementInconclusiveStatus(request *v1alpha1.InvestigationRequest, evidence investigation.EvidenceCollectionResult, gate evidenceRequirementGate, now time.Time) {
	request.Status.Provider = ""
	request.Status.EvidenceRefs = evidence.EvidenceRefs
	request.Status.Summary = gate.Message
	request.Status.Hypothesis = ""
	request.Status.Confidence = 0
	request.Status.Outcome = v1alpha1.InvestigationOutcomeInconclusive
	request.Status.Failure = nil
	request.Status.MissingEvidence = gate.Missing
	request.Status.Degradation = &v1alpha1.RCADegradation{
		Partial: true,
		Reasons: []v1alpha1.RCADegradationReason{
			{
				Code:    "RequiredEvidenceMissing",
				Stage:   v1alpha1.InvestigationStageEvidenceCollection,
				Message: gate.Message,
			},
		},
	}
	request.Status.Verdict = &v1alpha1.RCAVerdict{
		Outcome:    v1alpha1.InvestigationOutcomeInconclusive,
		Summary:    gate.Message,
		Confidence: 0,
		ConfidenceDetail: &v1alpha1.RCAConfidence{
			ProviderScore: 0,
			VerifiedScore: 0,
			Level:         "None",
			Method:        "RequiredEvidenceProfileV1",
		},
	}
	request.Status.EvidenceCoverage = gate.Coverage
	request.Status.Claims = nil
	request.Status.AlternativeHypotheses = nil
	request.Status.Execution = nil
	completedAt := metav1.NewTime(now)
	request.Status.CompletedAt = &completedAt
	setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseCompleted, gate.Message, request.Generation, now)
	rcametrics.RecordInvestigation(request.Namespace, "heuristic", request.Status.Outcome, "unknown")
	setStatusCondition(&request.Status.Conditions, conditionTargetResolved, metav1.ConditionTrue, "TargetResolved", "target resource was resolved successfully", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionDatasourceResolved, metav1.ConditionTrue, "AllDatasourcesResolved", "all referenced datasources were resolved", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionQueryTypeSupported, metav1.ConditionTrue, "AllQueryTypesSupported", "all investigation query types were supported", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionQueryPolicyReady, metav1.ConditionTrue, "AllQueriesAllowed", "all investigation queries were allowed by datasource policy", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionFalse, "RequiredEvidenceMissing", gate.Message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, "RequiredEvidenceMissing", "RCA is inconclusive because required evidence is incomplete", request.Generation, now)
	setInvestigationRemediationBlocked(request, "RCAUnavailable", "remediation is unavailable because required RCA evidence is incomplete", now)
	setInvestigationVerificationUnknown(request, "RCAUnavailable", "verification was not performed because required RCA evidence is incomplete", now)
	setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionTrue, "InvestigationInconclusive", gate.Message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionTrue, "RequiredEvidenceMissing", gate.Message, request.Generation, now)
}

func applyEvidenceRequirementNoIssueFoundStatus(request *v1alpha1.InvestigationRequest, evidence investigation.EvidenceCollectionResult, gate evidenceRequirementGate, now time.Time) {
	message := fmt.Sprintf("required evidence profile %q is complete and no matching issue evidence was found", gate.Profile)
	request.Status.Provider = ""
	request.Status.EvidenceRefs = evidence.EvidenceRefs
	request.Status.Summary = message
	request.Status.Hypothesis = ""
	request.Status.Confidence = 0
	request.Status.Outcome = v1alpha1.InvestigationOutcomeNoIssueFound
	request.Status.Failure = nil
	request.Status.MissingEvidence = nil
	request.Status.Degradation = nil
	request.Status.Verdict = &v1alpha1.RCAVerdict{
		Outcome:    v1alpha1.InvestigationOutcomeNoIssueFound,
		Summary:    message,
		Confidence: 0,
		ConfidenceDetail: &v1alpha1.RCAConfidence{
			ProviderScore: 0,
			VerifiedScore: 0,
			Level:         "None",
			Method:        "RequiredEvidenceProfileV1",
		},
	}
	request.Status.EvidenceCoverage = gate.Coverage
	request.Status.Claims = nil
	request.Status.AlternativeHypotheses = nil
	request.Status.Execution = nil
	completedAt := metav1.NewTime(now)
	request.Status.CompletedAt = &completedAt
	setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseCompleted, message, request.Generation, now)
	rcametrics.RecordInvestigation(request.Namespace, "heuristic", request.Status.Outcome, "unknown")
	setStatusCondition(&request.Status.Conditions, conditionTargetResolved, metav1.ConditionTrue, "TargetResolved", "target resource was resolved successfully", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionDatasourceResolved, metav1.ConditionTrue, "AllDatasourcesResolved", "all referenced datasources were resolved", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionQueryTypeSupported, metav1.ConditionTrue, "AllQueryTypesSupported", "all investigation query types were supported", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionQueryPolicyReady, metav1.ConditionTrue, "AllQueriesAllowed", "all investigation queries were allowed by datasource policy", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionTrue, "RequiredEvidenceComplete", gate.Message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionTrue, "NoIssueFound", message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionVerified, metav1.ConditionTrue, "NoIssueFindingSupported", "required evidence coverage supports the absence of a matching issue", request.Generation, now)
	setInvestigationRemediationBlocked(request, "NoIssueFound", "remediation is not available because no issue was found", now)
	setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionTrue, "NoIssueFound", message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "NoDegradation", "investigation completed without degradation", request.Generation, now)
}

func applyLoopPreventedInvestigationStatus(request *v1alpha1.InvestigationRequest, failure *v1alpha1.InvestigationFailure, now time.Time) {
	request.Status.Phase = v1alpha1.PhaseFailed
	request.Status.Message = failure.Message
	request.Status.Outcome = v1alpha1.InvestigationOutcomeUnknown
	request.Status.Failure = failure
	rcametrics.RecordLoopPrevention(failure.Code)
	completedAt := metav1.NewTime(now)
	request.Status.CompletedAt = &completedAt
	setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionFalse, failure.Code, failure.Message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionTargetResolved, metav1.ConditionUnknown, "InvestigationNotRunnable", "target resolution was not evaluated because loop prevention blocked execution", request.Generation, now)
	setInvestigationNotRunnableConditions(request, "InvestigationNotRunnable", "investigation request was blocked before target, datasource, query, or evidence execution", now)
	setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, failure.Code, failure.Message, request.Generation, now)
	setInvestigationRemediationBlocked(request, "RCAUnavailable", "remediation is unavailable because RCA execution was blocked: "+failure.Code, now)
	setInvestigationVerificationUnknown(request, "RCAUnavailable", "verification was not performed because RCA execution was blocked", now)
	setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "ValidationFailed", "request failed validation before evidence collection started", request.Generation, now)
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
		recordStatusUpdateConflict("InvestigationRequest", err)
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

type specValidationIssue struct {
	Reason  string
	Message string
}

func validateInvestigationRequestSpec(spec v1alpha1.InvestigationRequestSpec) string {
	if issue := validateInvestigationRequestSpecIssue(spec); issue != nil {
		return issue.Message
	}
	return ""
}

func validateInvestigationRequestSpecIssue(spec v1alpha1.InvestigationRequestSpec) *specValidationIssue {
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
		return &specValidationIssue{Reason: "TargetInvalid", Message: "missing required target fields: " + strings.Join(missing, ", ")}
	}
	if mode := strings.TrimSpace(spec.Mode); mode != "" && mode != v1alpha1.InvestigationModeReadOnly {
		return &specValidationIssue{Reason: "UnsupportedInvestigationMode", Message: "unsupported investigation mode: " + mode}
	}
	if len(spec.DataSources) == 0 && len(spec.Queries) == 0 {
		return &specValidationIssue{Reason: "InvalidSpec", Message: "spec.dataSources or spec.queries must include at least one datasource reference"}
	}
	if len(spec.DataSources) > 0 && len(spec.Queries) > 0 {
		return &specValidationIssue{Reason: "InvalidSpec", Message: "spec.dataSources and spec.queries are mutually exclusive; use dataSources for controller-planned evidence or queries for user-planned evidence"}
	}
	if message := validateEvidenceRetention(spec.EvidenceRetention); message != "" {
		reason := "InvalidSpec"
		if spec.EvidenceRetention.Mode == v1alpha1.EvidenceRetentionModeRawSnapshot {
			reason = "UnsupportedRetentionMode"
		}
		return &specValidationIssue{Reason: reason, Message: message}
	}
	if message := validateQueryRetention(spec.QueryRetention); message != "" {
		return &specValidationIssue{Reason: "InvalidSpec", Message: message}
	}
	if message := validateInvestigationQueryBudget(spec); message != "" {
		return &specValidationIssue{Reason: "InvalidSpec", Message: message}
	}
	for _, query := range spec.Queries {
		if strings.TrimSpace(query.DatasourceRef.Name) == "" {
			return &specValidationIssue{Reason: "InvalidSpec", Message: "investigation queries require spec.queries[].datasourceRef.name"}
		}
		if strings.TrimSpace(query.QueryType) == "" {
			return &specValidationIssue{Reason: "InvalidSpec", Message: "investigation queries require spec.queries[].queryType"}
		}
	}
	return nil
}

func validateEvidenceRetention(policy v1alpha1.EvidenceRetentionPolicy) string {
	mode := strings.TrimSpace(policy.Mode)
	if mode == "" || mode == v1alpha1.EvidenceRetentionModeMetadataOnly {
		return ""
	}
	if policy.Encryption.Required {
		return "spec.evidenceRetention.encryption.required is not supported by the current evidence retention adapters"
	}
	switch deletionPolicy := strings.TrimSpace(policy.DeletionPolicy); deletionPolicy {
	case "", v1alpha1.EvidenceRetentionDeletionPolicyRetain, v1alpha1.EvidenceRetentionDeletionPolicyDelete:
	default:
		return "unsupported evidence retention deletionPolicy: " + deletionPolicy
	}
	switch mode {
	case v1alpha1.EvidenceRetentionModeNormalizedSnapshot:
		if strings.TrimSpace(policy.StorageRef.Name) != evidencepkg.LocalFilesystemStoreName {
			return "spec.evidenceRetention.mode=NormalizedSnapshot requires spec.evidenceRetention.storageRef.name=local-filesystem"
		}
		return ""
	case v1alpha1.EvidenceRetentionModeRawSnapshot:
		return "spec.evidenceRetention.mode=RawSnapshot requires explicit raw evidence retention support and is not supported in this release"
	default:
		return "unsupported evidence retention mode: " + mode
	}
}

func validateQueryRetention(policy v1alpha1.QueryRetentionPolicy) string {
	switch mode := strings.TrimSpace(policy.Mode); mode {
	case "", v1alpha1.QueryRetentionModeDigestOnly, v1alpha1.QueryRetentionModeRedacted, v1alpha1.QueryRetentionModeFull:
		return ""
	default:
		return "unsupported queryRetention mode: " + mode
	}
}

func validateInvestigationQueryBudget(spec v1alpha1.InvestigationRequestSpec) string {
	budget := spec.QueryBudget
	if budget.MaxTimeRange.Duration < 0 {
		return "queryBudget.maxTimeRange must not be negative"
	}
	if budget.MaxQueriesTotal < 0 {
		return "queryBudget.maxQueriesTotal must not be negative"
	}
	if budget.MaxQueriesPerSource < 0 {
		return "queryBudget.maxQueriesPerSource must not be negative"
	}
	if budget.MaxConcurrentQueries < 0 {
		return "queryBudget.maxConcurrentQueries must not be negative"
	}
	if budget.MaxCumulativeDuration.Duration < 0 {
		return "queryBudget.maxCumulativeDuration must not be negative"
	}
	if budget.MaxCumulativeResponseBytes < 0 {
		return "queryBudget.maxCumulativeResponseBytes must not be negative"
	}
	if budget.ResultLimits.Metrics.MaxSeries < 0 {
		return "queryBudget.resultLimits.metrics.maxSeries must not be negative"
	}
	if budget.ResultLimits.Metrics.MaxSamples < 0 {
		return "queryBudget.resultLimits.metrics.maxSamples must not be negative"
	}
	if budget.ResultLimits.Logs.MaxLines < 0 {
		return "queryBudget.resultLimits.logs.maxLines must not be negative"
	}
	if budget.ResultLimits.Logs.MaxStreams < 0 {
		return "queryBudget.resultLimits.logs.maxStreams must not be negative"
	}
	if budget.ResultLimits.Logs.MaxEntries < 0 {
		return "queryBudget.resultLimits.logs.maxEntries must not be negative"
	}
	if budget.ResultLimits.Events.MaxRecords < 0 {
		return "queryBudget.resultLimits.events.maxRecords must not be negative"
	}
	if budget.MaxTimeRange.Duration > 0 && spec.TimeRange.Lookback.Duration > budget.MaxTimeRange.Duration {
		return fmt.Sprintf("spec.timeRange.lookback %s exceeds queryBudget.maxTimeRange %s", spec.TimeRange.Lookback.Duration, budget.MaxTimeRange.Duration)
	}
	totalQueries := len(spec.Queries)
	if totalQueries == 0 {
		totalQueries = len(spec.DataSources)
	}
	if budget.MaxQueriesTotal > 0 && int32(totalQueries) > budget.MaxQueriesTotal {
		return fmt.Sprintf("investigation query count %d exceeds queryBudget.maxQueriesTotal %d", totalQueries, budget.MaxQueriesTotal)
	}
	if budget.MaxQueriesPerSource > 0 {
		counts := map[string]int32{}
		for _, datasourceRef := range spec.DataSources {
			name := strings.TrimSpace(datasourceRef.Name)
			if name == "" {
				continue
			}
			counts[name]++
			if counts[name] > budget.MaxQueriesPerSource {
				return fmt.Sprintf("datasource %q query count %d exceeds queryBudget.maxQueriesPerSource %d", name, counts[name], budget.MaxQueriesPerSource)
			}
		}
		for _, query := range spec.Queries {
			name := strings.TrimSpace(query.DatasourceRef.Name)
			if name == "" {
				continue
			}
			counts[name]++
			if counts[name] > budget.MaxQueriesPerSource {
				return fmt.Sprintf("datasource %q query count %d exceeds queryBudget.maxQueriesPerSource %d", name, counts[name], budget.MaxQueriesPerSource)
			}
		}
	}
	return ""
}

func applyInvalidInvestigationStatus(request *v1alpha1.InvestigationRequest, reason string, message string, now time.Time) {
	if strings.TrimSpace(reason) == "" {
		reason = "InvalidSpec"
	}
	setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseFailed, message, request.Generation, now)
	applyInvestigationFailure(
		request,
		reason,
		message,
		v1alpha1.InvestigationStageValidation,
	)
	completedAt := metav1.NewTime(now)
	request.Status.CompletedAt = &completedAt
	request.Status.Provider = ""
	setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionFalse, reason, message, request.Generation, now)
	if validationFailureIsTargetResolution(reason) {
		setStatusCondition(&request.Status.Conditions, conditionTargetResolved, metav1.ConditionFalse, reason, message, request.Generation, now)
	} else {
		setStatusCondition(&request.Status.Conditions, conditionTargetResolved, metav1.ConditionUnknown, "InvestigationNotRunnable", "target resolution was not evaluated because request validation failed", request.Generation, now)
	}
	setInvestigationNotRunnableConditions(request, "InvestigationNotRunnable", "investigation request did not pass basic validation", now)
	setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, "InvestigationNotRunnable", "RCA execution is blocked because request validation failed", request.Generation, now)
	setInvestigationRemediationBlocked(request, "RCAUnavailable", "remediation is unavailable because the investigation request is not runnable", now)
	setInvestigationVerificationUnknown(request, "RCAUnavailable", "verification was not performed because the investigation request is not runnable", now)
	setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "ValidationFailed", "request failed validation before evidence collection started", request.Generation, now)
}

func validationFailureIsTargetResolution(reason string) bool {
	switch reason {
	case "TargetInvalid", "UnsupportedTargetKind":
		return true
	default:
		return false
	}
}

func setInvestigationNotRunnableConditions(request *v1alpha1.InvestigationRequest, reason string, message string, now time.Time) {
	setStatusCondition(&request.Status.Conditions, conditionDatasourceResolved, metav1.ConditionUnknown, reason, message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionQueryTypeSupported, metav1.ConditionUnknown, reason, message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionQueryPolicyReady, metav1.ConditionUnknown, reason, message, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionUnknown, reason, message, request.Generation, now)
}

func applyInvestigationExecutionStatus(request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, evidence investigation.EvidenceCollectionResult, rca investigation.RCAResult, message string, now time.Time) {
	request.Status.Provider = ""
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
		if preflight.ModelProviderIssue != nil {
			setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionUnknown, "ProviderUnavailableBeforeCollection", "evidence collection was not evaluated because provider resolution failed", request.Generation, now)
		} else if preflight.QueryTypeIssue != nil {
			setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionUnknown, "QueryValidationFailed", "evidence collection was not evaluated because query validation failed", request.Generation, now)
		} else if evidence.Issue != nil {
			setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionFalse, evidence.Issue.Reason, evidence.Issue.Message, request.Generation, now)
		} else {
			setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionTrue, "EvidenceCollected", evidence.Summary, request.Generation, now)
		}
	}
	if preflight.QueryTypeIssue != nil {
		if preflight.QueryTypeIssue.Reason == "QueryPolicyRejected" {
			setStatusCondition(&request.Status.Conditions, conditionQueryTypeSupported, metav1.ConditionTrue, "AllQueryTypesSupported", "all investigation query types were supported", request.Generation, now)
			setStatusCondition(&request.Status.Conditions, conditionQueryPolicyReady, metav1.ConditionFalse, preflight.QueryTypeIssue.Reason, preflight.QueryTypeIssue.Message, request.Generation, now)
		} else {
			setStatusCondition(&request.Status.Conditions, conditionQueryTypeSupported, metav1.ConditionFalse, preflight.QueryTypeIssue.Reason, preflight.QueryTypeIssue.Message, request.Generation, now)
			setStatusCondition(&request.Status.Conditions, conditionQueryPolicyReady, metav1.ConditionUnknown, "QueryTypeUnsupported", "query policy was not evaluated because query type validation failed", request.Generation, now)
		}
	} else if preflight.DatasourceIssue != nil {
		setStatusCondition(&request.Status.Conditions, conditionQueryTypeSupported, metav1.ConditionUnknown, "DataSourceUnavailable", "query type capability was not evaluated because datasource resolution failed", request.Generation, now)
		setStatusCondition(&request.Status.Conditions, conditionQueryPolicyReady, metav1.ConditionUnknown, "DataSourceUnavailable", "query policy was not evaluated because datasource resolution failed", request.Generation, now)
	} else {
		setStatusCondition(&request.Status.Conditions, conditionQueryTypeSupported, metav1.ConditionTrue, "AllQueryTypesSupported", "all investigation query types were supported", request.Generation, now)
		setStatusCondition(&request.Status.Conditions, conditionQueryPolicyReady, metav1.ConditionTrue, "AllQueriesAllowed", "all investigation queries were allowed by datasource policy", request.Generation, now)
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
		rcametrics.RecordInvestigation(request.Namespace, providerType(preflight.Provider), "failed", "unknown")
		completedAt := metav1.NewTime(now)
		request.Status.CompletedAt = &completedAt
		setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionFalse, issue.Reason, issue.Message, request.Generation, now)
		if shouldMarkInvestigationDegraded(issue.Reason) {
			setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionTrue, issue.Reason, issue.Message, request.Generation, now)
		} else {
			setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, issue.Reason, "request failed without an optional dependency degradation", request.Generation, now)
		}
		setInvestigationRemediationBlocked(request, "RCAUnavailable", "remediation is unavailable because RCA execution was blocked: "+issue.Reason, now)
		setInvestigationVerificationUnknown(request, "RCAUnavailable", "verification was not performed because RCA execution was blocked", now)
		return
	}
	if evidence.Issue != nil {
		setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseFailed, evidence.Issue.Message, request.Generation, now)
		applyInvestigationFailure(request, evidence.Issue.Reason, evidence.Issue.Message, v1alpha1.InvestigationStageEvidenceCollection)
		rcametrics.RecordInvestigation(request.Namespace, providerType(preflight.Provider), "failed", "unknown")
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
		setInvestigationRemediationBlocked(request, "RCAUnavailable", "remediation is unavailable because evidence collection did not complete: "+evidence.Issue.Reason, now)
		return
	}
	if rca.Issue != nil {
		setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseFailed, rca.Issue.Message, request.Generation, now)
		applyInvestigationFailure(request, rca.Issue.Reason, rca.Issue.Message, investigationFailureStage(rca.Issue.Reason))
		if rca.Issue.EgressAudit != nil || len(rca.EgressAttempts) > 0 {
			audit := rca.Issue.EgressAudit
			if audit == nil {
				audit = rca.PrimaryEgress
			}
			request.Status.Execution = buildRejectedRCAExecution(request, preflight, evidence, audit, rca.EgressAttempts, investigationExecutionID(request, preflight, evidence), now)
			request.Status.Execution.State = rca.Issue.Reason
		}
		rcametrics.RecordInvestigation(request.Namespace, providerType(preflight.Provider), "failed", "unknown")
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
		setInvestigationRemediationBlocked(request, "RCAUnavailable", "remediation is unavailable because RCA generation failed: "+rca.Issue.Reason, now)
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
	evidenceDegradation := evidenceNativeLimitDegradation(evidence)
	if evidenceDegradation != nil {
		request.Status.Degradation = evidenceDegradation
	}
	completedAt := metav1.NewTime(now)
	request.Status.CompletedAt = &completedAt
	if request.Status.Outcome == "" {
		request.Status.Outcome = v1alpha1.InvestigationOutcomeUnknown
	}
	request.Status.Failure = nil
	setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseCompleted, request.Status.Summary, request.Generation, now)
	rcametrics.RecordInvestigation(request.Namespace, providerType(preflight.Provider), request.Status.Outcome, rootCauseTypeForMetrics(request))
	switch request.Status.Outcome {
	case v1alpha1.InvestigationOutcomeConfirmed:
		setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionTrue, "ProviderSucceeded", "RCA generated successfully for investigation request", request.Generation, now)
		setStatusCondition(&request.Status.Conditions, conditionVerified, metav1.ConditionTrue, "RootCauseClaimsSupported", "root-cause claims are supported by collected evidence", request.Generation, now)
		setStatusCondition(&request.Status.Conditions, conditionRemediationReady, metav1.ConditionTrue, "RootCauseVerified", "remediation planning is allowed after verified root-cause evidence", request.Generation, now)
		setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionTrue, "InvestigationCompleted", "evidence collection and RCA generation completed successfully", request.Generation, now)
	case v1alpha1.InvestigationOutcomeInconclusive:
		setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, "RCAUnverified", "RCA completed but proposed root-cause claims were not supported by collected evidence", request.Generation, now)
		setStatusCondition(&request.Status.Conditions, conditionVerified, metav1.ConditionFalse, "NoSupportedRootCauseClaims", "no proposed root-cause claim was supported by collected evidence", request.Generation, now)
		setInvestigationRemediationBlocked(request, "RCAUnverified", "remediation is not allowed without verified root-cause evidence", now)
		setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionTrue, "InvestigationInconclusive", request.Status.Summary, request.Generation, now)
	default:
		setInvestigationRemediationBlocked(request, "RCAUnavailable", "remediation is unavailable because the investigation outcome is not confirmed", now)
		setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionTrue, "InvestigationCompleted", "evidence collection and RCA generation completed successfully", request.Generation, now)
	}
	if evidenceDegradation != nil {
		setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionTrue, "NativeResultLimitExceeded", "evidence collection completed with bounded datasource result truncation", request.Generation, now)
	} else {
		setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "NoDegradation", "request completed successfully without degradation", request.Generation, now)
	}
}

func setInvestigationRemediationBlocked(request *v1alpha1.InvestigationRequest, reason string, message string, now time.Time) {
	setStatusCondition(&request.Status.Conditions, conditionRemediationReady, metav1.ConditionFalse, reason, message, request.Generation, now)
}

func setInvestigationVerificationUnknown(request *v1alpha1.InvestigationRequest, reason string, message string, now time.Time) {
	setStatusCondition(&request.Status.Conditions, conditionVerified, metav1.ConditionUnknown, reason, message, request.Generation, now)
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
	for _, claim := range claims {
		rcametrics.RecordClaimVerification(claim.Verification)
	}
	evaluation := evaluateCanonicalVerdict(rca, evidence, claims, confidence)
	request.Status.Verdict = &v1alpha1.RCAVerdict{
		Outcome:         evaluation.Outcome,
		Summary:         evaluation.Summary,
		RootCauseEntity: resourceToTargetRef(preflight.Target),
		RootCauseType:   evaluation.RootCauseType,
		Confidence:      evaluation.Confidence,
		ConfidenceDetail: &v1alpha1.RCAConfidence{
			ProviderScore: confidence,
			VerifiedScore: evaluation.VerifiedScore,
			Level:         confidenceLevel(evaluation.VerifiedScore),
			Method:        verification.Method,
		},
	}
	request.Status.Summary = evaluation.Summary
	request.Status.Confidence = evaluation.Confidence
	request.Status.Outcome = evaluation.Outcome
	request.Status.Claims = claims
	request.Status.AlternativeHypotheses = nil
	request.Status.MissingEvidence = evaluation.MissingEvidence
	request.Status.Degradation = &v1alpha1.RCADegradation{Partial: false}
	request.Status.Execution = buildRCAExecution(request, preflight, evidence, rca, investigationExecutionID(request, preflight, evidence), executionStateFinalized, now)
	request.Status.Execution.VerifierVersion = verification.Method
}

type canonicalVerdictEvaluation struct {
	Outcome         string
	Summary         string
	RootCauseType   string
	Confidence      float64
	VerifiedScore   float64
	MissingEvidence []v1alpha1.RCAMissingEvidence
}

func evaluateCanonicalVerdict(rca investigation.RCAResult, evidence investigation.EvidenceCollectionResult, claims []v1alpha1.RCAClaim, providerConfidence float64) canonicalVerdictEvaluation {
	causalClaims := rootCauseClaims(rca, claims)
	supported, contradicted := countVerifiedRootCauseClaims(causalClaims)
	supportedClaims := supportedRootCauseClaims(causalClaims)
	verifiedScore := 0.0
	if len(causalClaims) > 0 {
		verifiedScore = float64(supported) / float64(len(causalClaims))
	}
	if verifiedScore > providerConfidence {
		verifiedScore = providerConfidence
	}

	evaluation := canonicalVerdictEvaluation{
		Outcome:       v1alpha1.InvestigationOutcomeConfirmed,
		Summary:       verifiedRCASummary(evidence, supportedClaims),
		RootCauseType: inferRootCauseTypeFromClaims(supportedClaims),
		Confidence:    verifiedScore,
		VerifiedScore: verifiedScore,
	}
	if len(causalClaims) == 0 || supported == 0 || contradicted > 0 {
		evaluation.Outcome = v1alpha1.InvestigationOutcomeInconclusive
		evaluation.Summary = unverifiedRCASummary(evidence, rca.Reasoning.RiskSummary)
		evaluation.RootCauseType = ""
		evaluation.Confidence = 0
		evaluation.VerifiedScore = 0
		evaluation.MissingEvidence = missingEvidenceForClaims(causalClaims)
	}
	return evaluation
}

func supportedRootCauseClaims(claims []v1alpha1.RCAClaim) []v1alpha1.RCAClaim {
	out := make([]v1alpha1.RCAClaim, 0, len(claims))
	for _, claim := range claims {
		if claim.Verification == verifier.VerificationSupported {
			out = append(out, claim)
		}
	}
	return out
}

func verifiedRCASummary(evidence investigation.EvidenceCollectionResult, supportedClaims []v1alpha1.RCAClaim) string {
	statements := make([]string, 0, len(supportedClaims))
	for _, claim := range supportedClaims {
		statement := strings.TrimSpace(claim.Statement)
		if statement != "" {
			statements = append(statements, statement)
		}
	}
	evidenceSummary := observedEvidenceSummary(evidence)
	switch {
	case len(statements) > 0 && evidenceSummary != "":
		return fmt.Sprintf("Verified root-cause evidence supports: %s. Observed evidence: %s.", strings.Join(statements, "; "), evidenceSummary)
	case len(statements) > 0:
		return fmt.Sprintf("Verified root-cause evidence supports: %s.", strings.Join(statements, "; "))
	case evidenceSummary != "":
		return fmt.Sprintf("Observed evidence: %s.", evidenceSummary)
	default:
		return "Root-cause claims were verified by collected evidence."
	}
}

func observedEvidenceSummary(evidence investigation.EvidenceCollectionResult) string {
	parts := make([]string, 0, len(evidence.EvidenceRefs))
	for _, ref := range evidence.EvidenceRefs {
		kind := strings.TrimSpace(ref.Kind)
		reason := strings.TrimSpace(ref.Reason)
		source := strings.TrimSpace(ref.Source)
		detail := strings.TrimSpace(ref.Summary)
		label := strings.TrimSpace(strings.Join(nonEmptyStrings(kind, reason, source), " "))
		switch {
		case label != "" && detail != "":
			parts = append(parts, fmt.Sprintf("%s: %s", label, detail))
		case detail != "":
			parts = append(parts, detail)
		case label != "":
			parts = append(parts, label)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return strings.Join(parts, "; ")
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func rootCauseClaims(rca investigation.RCAResult, claims []v1alpha1.RCAClaim) []v1alpha1.RCAClaim {
	if len(claims) == 0 {
		return nil
	}
	start := 0
	if strings.TrimSpace(rca.Reasoning.RiskSummary) != "" {
		start = 1
	}
	if start >= len(claims) {
		return nil
	}
	return claims[start:]
}

func countVerifiedRootCauseClaims(claims []v1alpha1.RCAClaim) (int, int) {
	supported := 0
	contradicted := 0
	for _, claim := range claims {
		switch claim.Verification {
		case verifier.VerificationSupported:
			supported++
		case verifier.VerificationContradicted:
			contradicted++
		}
	}
	return supported, contradicted
}

func inferRootCauseTypeFromClaims(claims []v1alpha1.RCAClaim) string {
	statements := make([]string, 0, len(claims))
	for _, claim := range claims {
		statements = append(statements, claim.Statement)
	}
	text := strings.ToLower(strings.Join(statements, " "))
	switch {
	case containsAny(text, "imagepullbackoff", "errimagepull", "image pull", "pull image", "failed to pull"):
		return "ImagePullFailure"
	case containsAny(text, "memory", "oom", "resource pressure", "safe threshold"):
		return "ResourcePressure"
	case containsAny(text, "latency", "timeout", "http", "5xx"):
		return "LatencyRegression"
	case containsAny(text, "rollout", "release", "pod template", "replicaset", "image digest"):
		return "DeploymentRegression"
	case containsAny(text, "crash", "restart", "backoff", "startup"):
		return "CrashLoop"
	default:
		return "WorkloadDegradation"
	}
}

func unverifiedRCASummary(evidence investigation.EvidenceCollectionResult, providerSummary string) string {
	for _, ref := range rankedEvidenceRefsForSummary(evidence.EvidenceRefs) {
		detail := firstNonEmptyString(strings.TrimSpace(ref.Summary), strings.TrimSpace(ref.Reason), strings.TrimSpace(ref.Source))
		if detail != "" {
			return fmt.Sprintf("Observed %s, but collected evidence is insufficient to confirm the proposed root cause.", detail)
		}
	}
	if strings.TrimSpace(providerSummary) != "" {
		return fmt.Sprintf("Collected evidence is insufficient to confirm the proposed root cause: %s", strings.TrimSpace(providerSummary))
	}
	return "Collected evidence is insufficient to confirm the proposed root cause."
}

func rankedEvidenceRefsForSummary(refs []v1alpha1.EvidenceRef) []v1alpha1.EvidenceRef {
	ranked := append([]v1alpha1.EvidenceRef(nil), refs...)
	sort.SliceStable(ranked, func(i, j int) bool {
		return evidenceSummaryRank(ranked[i]) > evidenceSummaryRank(ranked[j])
	})
	return ranked
}

func evidenceSummaryRank(ref v1alpha1.EvidenceRef) int {
	reason := strings.ToLower(strings.TrimSpace(ref.Reason))
	text := strings.ToLower(strings.TrimSpace(ref.Summary + " " + ref.Kind))
	switch {
	case containsAny(reason, "crashloopbackoff", "backoff", "imagepullbackoff", "errimagepull", "invalidimagename", "failedscheduling", "oomkilled", "oom", "unhealthy", "failedcreate", "failedreplicacreate", "progressdeadlineexceeded", "minimumreplicasunavailable"):
		return 2
	case containsAny(text, "crashloopbackoff", "backoff", "imagepullbackoff", "errimagepull", "invalidimagename", "failedscheduling", "oomkilled", "oom", "unhealthy", "failedcreate", "failedreplicacreate", "progressdeadlineexceeded", "minimumreplicasunavailable"):
		return 1
	default:
		return 0
	}
}

func missingEvidenceForClaims(claims []v1alpha1.RCAClaim) []v1alpha1.RCAMissingEvidence {
	missing := make([]v1alpha1.RCAMissingEvidence, 0)
	seen := map[string]bool{}
	for _, claim := range claims {
		if claim.Verification == verifier.VerificationSupported {
			continue
		}
		for _, item := range missingEvidenceForClaim(claim.Statement) {
			key := item.Source + "/" + item.Reason
			if seen[key] {
				continue
			}
			seen[key] = true
			missing = append(missing, item)
		}
	}
	return missing
}

func missingEvidenceForClaim(statement string) []v1alpha1.RCAMissingEvidence {
	text := strings.ToLower(statement)
	switch {
	case containsAny(text, "rollout", "release", "image", "config", "configuration"):
		return []v1alpha1.RCAMissingEvidence{{Source: "deployment-history", Reason: "RolloutEvidenceRequired"}}
	case containsAny(text, "memory", "oom", "resource pressure"):
		return []v1alpha1.RCAMissingEvidence{{Source: "prometheus", Reason: "MemoryMetricsRequired"}, {Source: "kubernetes-events", Reason: "OOMEventRequired"}}
	case containsAny(text, "crash", "restart", "backoff"):
		return []v1alpha1.RCAMissingEvidence{{Source: "kubernetes-events", Reason: "CrashLoopEventRequired"}}
	case containsAny(text, "latency", "timeout", "http", "5xx"):
		return []v1alpha1.RCAMissingEvidence{{Source: "prometheus", Reason: "LatencyMetricsRequired"}, {Source: "loki", Reason: "ErrorLogEvidenceRequired"}}
	default:
		return []v1alpha1.RCAMissingEvidence{{Source: "evidence", Reason: "SupportingEvidenceRequired"}}
	}
}

func evidenceNativeLimitDegradation(evidence investigation.EvidenceCollectionResult) *v1alpha1.RCADegradation {
	reasons := make([]v1alpha1.RCADegradationReason, 0)
	for _, ref := range evidence.EvidenceRefs {
		if ref.TruncationReason != "NativeResultLimitExceeded" {
			continue
		}
		message := fmt.Sprintf("evidence %s was truncated by native %s limit", ref.ID, firstNonEmptyString(ref.LimitDimension, "result"))
		reasons = append(reasons, v1alpha1.RCADegradationReason{
			Code:    "NativeResultLimitExceeded",
			Stage:   v1alpha1.InvestigationStageEvidenceCollection,
			Message: message,
		})
	}
	if len(reasons) == 0 {
		return nil
	}
	return &v1alpha1.RCADegradation{
		Partial: true,
		Reasons: reasons,
	}
}

func rootCauseTypeForMetrics(request *v1alpha1.InvestigationRequest) string {
	if request.Status.Verdict == nil {
		return "unknown"
	}
	return request.Status.Verdict.RootCauseType
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
	if providerResult != nil && preflight.Provider != nil && isHostedProviderType(preflight.Provider.Spec.Provider) {
		classification := dataclassification.BundleClassification(egressEvidenceRefsForAudit(evidence.EvidenceRefs, preflight.Provider.Spec.DataPolicy))
		providerResult.Classification = &classification
	}
	egressAudit := providerEgressAudit(preflight.Provider, evidence)
	if rca.PrimaryEgress != nil {
		egressAudit = rca.PrimaryEgress
	}
	egressAttempts := providerEgressAttempts(preflight.Provider, egressAudit, "Allowed")
	if len(rca.EgressAttempts) > 0 {
		egressAttempts = copyProviderEgressAttempts(rca.EgressAttempts)
	}
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
		EgressAudit:             egressAudit,
		EgressAttempts:          egressAttempts,
		ControllerVersion:       version.Current().Version,
		AttemptCount:            1,
		Attempts: []v1alpha1.RCAExecutionAttempt{
			{
				ID:                defaultExecutionAttemptID,
				ProviderRequestID: providerRequestIDFromRCA(rca),
				IdempotencyKey:    executionIdempotencyKey(request, preflight, evidence, defaultExecutionAttemptID),
				Result:            executionAttemptCompleted,
				StartedAt:         request.Status.StartedAt,
				CompletedAt:       &metav1.Time{Time: now},
			},
		},
		DurationSeconds: investigationDurationSeconds(request, now),
		ProviderResult:  providerResult,
	}
}

func buildRejectedRCAExecution(request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, evidence investigation.EvidenceCollectionResult, audit *v1alpha1.ProviderEgressAudit, attempts []v1alpha1.ProviderEgressAttempt, executionID string, now time.Time) *v1alpha1.RCAExecution {
	egressAttempts := providerEgressAttempts(preflight.Provider, audit, "Rejected")
	if len(attempts) > 0 {
		egressAttempts = copyProviderEgressAttempts(attempts)
	}
	return &v1alpha1.RCAExecution{
		ID:                      executionID,
		State:                   "ProviderDataPolicyRejected",
		Provider:                providerNameForStatus(request, preflight, investigation.RCAResult{}),
		ProviderRef:             providerRef(preflight.Provider),
		ProviderGeneration:      providerGeneration(preflight.Provider),
		ProviderType:            providerType(preflight.Provider),
		Model:                   providerModel(preflight.Provider),
		RCASchemaVersion:        rcaSchemaVersion,
		CanonicalizationVersion: rcaCanonicalizationVersion,
		ReasoningPolicyVersion:  reasoningPolicyVersion,
		EgressAudit:             audit,
		EgressAttempts:          egressAttempts,
		ControllerVersion:       version.Current().Version,
		DurationSeconds:         investigationDurationSeconds(request, now),
	}
}

func copyProviderEgressAttempts(in []v1alpha1.ProviderEgressAttempt) []v1alpha1.ProviderEgressAttempt {
	if len(in) > 8 {
		in = in[:8]
	}
	out := make([]v1alpha1.ProviderEgressAttempt, len(in))
	copy(out, in)
	for i := range out {
		if in[i].ProviderRef != nil {
			ref := *in[i].ProviderRef
			out[i].ProviderRef = &ref
		}
		out[i].EvidenceKinds = append([]string(nil), in[i].EvidenceKinds...)
		out[i].SensitivityTagsSent = append([]string(nil), in[i].SensitivityTagsSent...)
	}
	return out
}

func providerRequestIDFromRCA(rca investigation.RCAResult) string {
	if rca.Reasoning == nil {
		return ""
	}
	return strings.TrimSpace(rca.Reasoning.ProviderRequestID)
}

func providerEgressAudit(provider *v1alpha1.ModelProvider, evidence investigation.EvidenceCollectionResult) *v1alpha1.ProviderEgressAudit {
	if provider == nil || !isHostedProviderType(provider.Spec.Provider) {
		return nil
	}
	filteredRefs := egressEvidenceRefsForAudit(evidence.EvidenceRefs, provider.Spec.DataPolicy)
	decision := dataclassification.EvaluateProviderPolicy(provider.Spec.DataPolicy, filteredRefs)
	return &v1alpha1.ProviderEgressAudit{
		Decision:                      decision.Decision,
		Reason:                        decision.Reason,
		ProviderType:                  strings.ToLower(strings.TrimSpace(provider.Spec.Provider)),
		EvidenceBundleDigest:          evidenceBundleDigest(filteredRefs),
		EvidenceKinds:                 evidenceKinds(filteredRefs),
		SensitivityTagsSent:           decision.SensitivityTagsSent,
		LogSamplesIncluded:            logSamplesIncluded(filteredRefs, provider.Spec.DataPolicy),
		MaximumClassificationObserved: decision.MaximumObserved,
		MaximumClassificationAllowed:  decision.MaximumAllowed,
		MaximumClassificationSent:     decision.MaximumSent,
		ClassificationPolicyVersion:   decision.ClassificationVersion,
	}
}

func providerEgressAttempts(provider *v1alpha1.ModelProvider, audit *v1alpha1.ProviderEgressAudit, result string) []v1alpha1.ProviderEgressAttempt {
	if audit == nil {
		return nil
	}
	return []v1alpha1.ProviderEgressAttempt{providerEgressAttempt(provider, audit, 1, result)}
}

func providerEgressAttempt(provider *v1alpha1.ModelProvider, audit *v1alpha1.ProviderEgressAudit, ordinal int32, result string) v1alpha1.ProviderEgressAttempt {
	attempt := v1alpha1.ProviderEgressAttempt{
		Ordinal:                       ordinal,
		ProviderRef:                   providerRef(provider),
		ProviderGeneration:            providerGeneration(provider),
		ProviderType:                  audit.ProviderType,
		Decision:                      audit.Decision,
		Result:                        result,
		Reason:                        audit.Reason,
		EvidenceBundleDigest:          audit.EvidenceBundleDigest,
		EvidenceKinds:                 append([]string(nil), audit.EvidenceKinds...),
		SensitivityTagsSent:           append([]string(nil), audit.SensitivityTagsSent...),
		LogSamplesIncluded:            audit.LogSamplesIncluded,
		MaximumClassificationObserved: audit.MaximumClassificationObserved,
		MaximumClassificationAllowed:  audit.MaximumClassificationAllowed,
		MaximumClassificationSent:     audit.MaximumClassificationSent,
		ClassificationPolicyVersion:   audit.ClassificationPolicyVersion,
	}
	if attempt.ProviderType == "" {
		attempt.ProviderType = providerType(provider)
	}
	return attempt
}

func egressEvidenceRefsForAudit(refs []v1alpha1.EvidenceRef, policy v1alpha1.ModelProviderDataPolicy) []v1alpha1.EvidenceRef {
	out := make([]v1alpha1.EvidenceRef, 0, len(refs))
	for _, ref := range refs {
		if !egressEvidenceKindAllowed(ref.Kind, policy.AllowedEvidenceKinds) {
			continue
		}
		filtered := ref
		if strings.EqualFold(filtered.Kind, "log") && !policy.AllowLogSamples {
			filtered.Summary = "log sample omitted by provider data policy"
			filtered.Query = ""
			filtered.Link = ""
		}
		out = append(out, filtered)
	}
	return out
}

func egressEvidenceKindAllowed(kind string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	normalizedKind := normalizeEgressEvidenceKind(kind)
	for _, candidate := range allowed {
		if normalizeEgressEvidenceKind(candidate) == normalizedKind {
			return true
		}
	}
	return false
}

func normalizeEgressEvidenceKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.TrimSuffix(kind, "observation")
	switch kind {
	case "kubernetesevent":
		return "event"
	default:
		return kind
	}
}

func evidenceKinds(refs []v1alpha1.EvidenceRef) []string {
	seen := map[string]struct{}{}
	kinds := make([]string, 0)
	for _, ref := range refs {
		kind := strings.TrimSpace(ref.Kind)
		if kind == "" {
			continue
		}
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func logSamplesIncluded(refs []v1alpha1.EvidenceRef, policy v1alpha1.ModelProviderDataPolicy) bool {
	if !policy.AllowLogSamples {
		return false
	}
	for _, ref := range refs {
		if strings.EqualFold(ref.Kind, "log") && strings.TrimSpace(ref.Summary) != "" {
			return true
		}
	}
	return false
}

func isHostedProviderType(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "openai", "claude", "gemini":
		return true
	default:
		return false
	}
}

func providerResultFromReasoning(rca investigation.RCAResult) *v1alpha1.RCAProviderResult {
	if rca.Reasoning == nil {
		return nil
	}
	normalized := normalizedResultFromReasoning(*rca.Reasoning)
	digest := canonicaldigest.SHA256(canonicaldigest.RCAJSONV1, normalized)
	return &v1alpha1.RCAProviderResult{
		SchemaVersion:     rcaSchemaVersion,
		ProviderRequestID: strings.TrimSpace(rca.Reasoning.ProviderRequestID),
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
		RiskTitle:         normalized.RiskTitle,
		RiskSummary:       normalized.RiskSummary,
		Severity:          severity,
		ProviderRequestID: strings.TrimSpace(result.ProviderRequestID),
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
			Reason:  ref.Reason,
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
		claims[i].EvidenceLinks = verifierEvidenceLinks(result.EvidenceLinks)
		claims[i].Verification = result.Verification
	}
	return claims
}

func verifierEvidenceLinks(links []verifier.EvidenceLink) []v1alpha1.RCAEvidenceLink {
	out := make([]v1alpha1.RCAEvidenceLink, 0, len(links))
	for _, link := range links {
		out = append(out, v1alpha1.RCAEvidenceLink{
			EvidenceRef: link.EvidenceRef,
			Role:        link.Role,
			Strength:    link.Strength,
		})
	}
	return out
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
	case "DatasourceQueryFailed", "DatasourceAuthFailed", "DatasourceRateLimited", "DatasourceUnavailable", "DatasourceRequestInvalid", "InvalidDatasourceResponse", "ResolverUnavailable", "GatewayUnavailable", "ProviderUnavailable", "ProviderUnsupported", "ProviderAuthFailed", "ProviderRateLimited", "ProviderRequestInvalid", "ProviderFallbackLoop", "SecretReaderUnavailable", "SecretReadFailed", "SecretRefMissing", "SecretNotFound", "SecretKeyMissing", "SecretValueEmpty", "APIKeyMissing", "InvalidProviderResponse":
		return true
	default:
		return false
	}
}

func investigationFailureStage(reason string) string {
	switch reason {
	case "TargetNotFound", "UnsupportedTargetKind", "ResolverUnavailable":
		return v1alpha1.InvestigationStageTargetResolution
	case "DataSourceNotSpecified", "DatasourceRegistryUnavailable", "DataSourceNotFound":
		return investigationStageDataSourceResolution
	case "CapabilityMismatch", "QueryPolicyRejected", "QueryTemplateInvalid":
		return investigationStageQueryValidation
	case "DatasourceQueryFailed", "DatasourceAuthFailed", "DatasourceRateLimited", "DatasourceUnavailable", "DatasourceRequestInvalid", "InvalidDatasourceResponse":
		return v1alpha1.InvestigationStageEvidenceCollection
	case "ProviderNotFound":
		return investigationStageProviderResolution
	case "ProviderDataPolicyDenied", "ProviderDataPolicyRejected":
		return investigationStageProviderEgressPolicy
	case "GatewayUnavailable", "ProviderUnavailable", "ProviderUnsupported", "ProviderAuthFailed", "ProviderRateLimited", "ProviderRequestInvalid", "ProviderFallbackLoop", "SecretReaderUnavailable", "SecretReadFailed", "SecretRefMissing", "SecretNotFound", "SecretKeyMissing", "SecretValueEmpty", "APIKeyMissing", "InvalidProviderResponse":
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
		riskSignal.Annotations["fluxseer-rca.aiops.platform/investigation-request"] = request.Namespace + "/" + request.Name
		if request.Status.Lineage != nil {
			applyFindingIdentityAnnotations(riskSignal.Annotations, request.Status.Lineage.FindingIdentity)
		}

		riskSignal.Spec.Target = resourceToTargetRef(preflight.Target)
		riskSignal.Spec.SignalType = investigationSignalType(preflight)
		riskSignal.Spec.InvestigationRef = &v1alpha1.NamespacedObjectReference{Name: request.Name, Namespace: request.Namespace}
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
	phase := request.Status.Outcome
	if phase == "" {
		phase = v1alpha1.InvestigationOutcomeUnknown
	}
	message := request.Status.Summary
	if strings.TrimSpace(message) == "" {
		message = rca.Reasoning.RiskSummary
	}
	setRiskSignalStatus(&riskSignal.Status, phase, message, riskSignal.Generation, now)
	setStatusCondition(&riskSignal.Status.Conditions, conditionEvidenceReady, metav1.ConditionTrue, "EvidenceCollected", evidence.Summary, riskSignal.Generation, now)
	applyProjectedInvestigationRCAResult(&riskSignal.Status, request, riskSignal.Generation, rca, now)
	if statusChangedRiskSignal(original, riskSignal) {
		if err := r.Status().Update(ctx, riskSignal); err != nil && !recordStatusUpdateConflict("RiskSignal", err) {
			return nil, err
		}
	}

	return &v1alpha1.NamespacedObjectReference{
		Name:      riskSignal.Name,
		Namespace: riskSignal.Namespace,
	}, nil
}

func applyProjectedInvestigationRCAResult(status *v1alpha1.RiskSignalStatus, request *v1alpha1.InvestigationRequest, generation int64, rca investigation.RCAResult, now time.Time) {
	if request.Status.Verdict == nil {
		applyRCAResult(status, rcaResult{
			Reasoning:    rca.Reasoning,
			ProviderName: request.Status.Provider,
			Condition:    rcaCondition(metav1.ConditionFalse, "RCAUnverified", "RCA projection is unavailable because canonical verdict is missing", now),
			Projection: &v1alpha1.RiskSignalRCAProjectionStatus{
				Mode:          "InvestigationRequestProjection",
				ProjectedFrom: &v1alpha1.NamespacedObjectReference{Name: request.Name, Namespace: request.Namespace},
				Message:       "RiskSignal contains a compact projection; canonical RCA is owned by InvestigationRequest.status.",
			},
		}, generation, now)
		return
	}

	reasoning := *rca.Reasoning
	reasoning.RiskSummary = request.Status.Verdict.Summary
	reasoning.RCA.Hypothesis = request.Status.Hypothesis
	reasoning.RCA.Causes = verifiedCauseStatements(rootCauseClaims(rca, request.Status.Claims))
	if request.Status.Outcome != v1alpha1.InvestigationOutcomeConfirmed {
		reasoning.RCA.Hypothesis = ""
	}
	if request.Status.Verdict.ConfidenceDetail != nil {
		reasoning.Confidence.Score = int(request.Status.Verdict.Confidence * 100)
	}
	conditionStatus := metav1.ConditionTrue
	conditionReason := "RootCauseVerified"
	conditionMessage := "RCA promoted from verified investigation request"
	if request.Status.Outcome != v1alpha1.InvestigationOutcomeConfirmed {
		conditionStatus = metav1.ConditionFalse
		conditionReason = "RCAUnverified"
		conditionMessage = "canonical investigation verdict is inconclusive"
	}
	applyRCAResult(status, rcaResult{
		Reasoning:    &reasoning,
		ProviderName: request.Status.Provider,
		Condition:    rcaCondition(conditionStatus, conditionReason, conditionMessage, now),
		Projection: &v1alpha1.RiskSignalRCAProjectionStatus{
			Mode:          "InvestigationRequestProjection",
			ProjectedFrom: &v1alpha1.NamespacedObjectReference{Name: request.Name, Namespace: request.Namespace},
			Message:       "RiskSignal contains a compact projection; canonical RCA is owned by InvestigationRequest.status.",
		},
	}, generation, now)
}

func verifiedCauseStatements(claims []v1alpha1.RCAClaim) []string {
	out := make([]string, 0, len(claims))
	for _, claim := range claims {
		if claim.Verification == verifier.VerificationSupported {
			out = append(out, claim.Statement)
		}
	}
	return out
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
