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
)

type InvestigationRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Now    func() time.Time
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
	message := "investigation request accepted; execution flow not implemented yet"
	setInvestigationRequestStatus(&investigation.Status, v1alpha1.PhasePending, message, investigation.Generation, now())
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
		setInvestigationRequestStatus(&investigation.Status, v1alpha1.PhaseFailed, invalidMessage, investigation.Generation, now())
		setStatusCondition(&investigation.Status.Conditions, conditionReady, metav1.ConditionFalse, "TargetInvalid", invalidMessage, investigation.Generation, now())
		setStatusCondition(&investigation.Status.Conditions, conditionTargetResolved, metav1.ConditionFalse, "TargetInvalid", invalidMessage, investigation.Generation, now())
		setStatusCondition(&investigation.Status.Conditions, conditionEvidenceReady, metav1.ConditionFalse, "InvestigationNotRunnable", "investigation request did not pass basic validation", investigation.Generation, now())
		setStatusCondition(&investigation.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, "InvestigationNotRunnable", "investigation request did not pass basic validation", investigation.Generation, now())
		setStatusCondition(&investigation.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "ValidationFailed", "request failed validation before evidence collection started", investigation.Generation, now())
	} else {
		setStatusCondition(&investigation.Status.Conditions, conditionReady, metav1.ConditionFalse, "InvestigationPending", message, investigation.Generation, now())
		setStatusCondition(&investigation.Status.Conditions, conditionTargetResolved, metav1.ConditionTrue, "TargetReferenceAccepted", "target reference passed controller-level validation", investigation.Generation, now())
		setStatusCondition(&investigation.Status.Conditions, conditionEvidenceReady, metav1.ConditionFalse, "InvestigationNotImplemented", "evidence collection orchestration is not implemented yet", investigation.Generation, now())
		setStatusCondition(&investigation.Status.Conditions, conditionRCAReady, metav1.ConditionFalse, "InvestigationNotImplemented", "RCA generation orchestration is not implemented yet", investigation.Generation, now())
		setStatusCondition(&investigation.Status.Conditions, conditionDegraded, metav1.ConditionFalse, "NoDegradation", "request is pending implementation work rather than degraded by a runtime dependency", investigation.Generation, now())
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

func validateInvestigationRequestSpec(spec v1alpha1.InvestigationRequestSpec) string {
	missing := make([]string, 0, 3)
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
