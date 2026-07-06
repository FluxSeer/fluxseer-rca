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

	original := investigation.DeepCopy()
	message := "investigation preflight succeeded; evidence collection and RCA execution are not implemented yet"
	setInvestigationRequestStatus(&investigation.Status, v1alpha1.PhaseObserved, message, investigation.Generation, now())
	if investigation.Status.StartedAt == nil {
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
		applyInvestigationExecutionStatus(&investigation, preflight, evidence, message, now())
	}

	if !reflect.DeepEqual(original.Status, investigation.Status) {
		if err := r.Status().Update(ctx, &investigation); err != nil && !apierrors.IsConflict(err) {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
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
	setStatusCondition(&request.Status.Conditions, conditionEvidenceReady, metav1.ConditionFalse, "InvestigationNotRunnable", "investigation request did not pass basic validation", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, "InvestigationNotRunnable", "investigation request did not pass basic validation", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "ValidationFailed", "request failed validation before evidence collection started", request.Generation, now)
}

func applyInvestigationExecutionStatus(request *v1alpha1.InvestigationRequest, preflight investigation.PreflightResult, evidence investigation.EvidenceCollectionResult, message string, now time.Time) {
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

	if preflight.ModelProviderIssue != nil {
		setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, preflight.ModelProviderIssue.Reason, preflight.ModelProviderIssue.Message, request.Generation, now)
	} else if evidence.Issue != nil {
		setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, evidence.Issue.Reason, "RCA execution is blocked until evidence collection succeeds", request.Generation, now)
	} else {
		setStatusCondition(&request.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, "InvestigationNotImplemented", "RCA generation orchestration is not implemented yet", request.Generation, now)
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

	request.Status.CompletedAt = nil
	request.Status.Summary = evidence.Summary
	setInvestigationRequestStatus(&request.Status, v1alpha1.PhaseObserved, evidence.Summary, request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionReady, metav1.ConditionFalse, "RCAReadyPending", "evidence collection succeeded but RCA generation is not implemented yet", request.Generation, now)
	setStatusCondition(&request.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "NoDegradation", "request passed preflight and collected evidence successfully", request.Generation, now)
}

func shouldMarkInvestigationDegraded(reason string) bool {
	switch reason {
	case "DataSourceNotSpecified", "DatasourceRegistryUnavailable", "DataSourceNotFound", "DatasourceQueryFailed", "CapabilityMismatch", "ResolverUnavailable", "ProviderNotFound":
		return true
	default:
		return false
	}
}
