package controllers

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

const remediationPlanRetainAnnotation = "fluxseer-rca.aiops.platform/retain"

type RemediationPlanTTLReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Now    func() time.Time
}

func (r *RemediationPlanTTLReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var plan v1alpha1.RemediationPlan
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	if plan.Spec.TTLSeconds <= 0 || hasRetainAnnotation(&plan) {
		return ctrl.Result{}, nil
	}

	if plan.Status.FinishedAt == nil {
		finishedAt, ready, err := r.terminalFinishedAt(ctx, &plan)
		if err != nil || !ready {
			return ctrl.Result{}, err
		}

		plan.Status.FinishedAt = finishedAt
		if err := r.Status().Update(ctx, &plan); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, err
		}
	}

	expiresAt := plan.Status.FinishedAt.Add(time.Duration(plan.Spec.TTLSeconds) * time.Second)
	if !now().Before(expiresAt) {
		if err := r.Delete(ctx, &plan); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	return ctrl.Result{RequeueAfter: expiresAt.Sub(now())}, nil
}

func (r *RemediationPlanTTLReconciler) terminalFinishedAt(ctx context.Context, plan *v1alpha1.RemediationPlan) (*metav1.Time, bool, error) {
	if plan.Status.Phase == v1alpha1.PhaseRejected {
		return nil, false, nil
	}

	var actions v1alpha1.AgentActionList
	if err := r.List(ctx, &actions, client.InNamespace(plan.Namespace)); err != nil {
		return nil, false, err
	}

	var latest time.Time
	actionCount := 0
	for i := range actions.Items {
		action := &actions.Items[i]
		if !ownedByPlan(action, plan) {
			continue
		}
		actionCount++
		if !isTerminalAgentActionPhase(action.Status.Phase) || action.Status.FinishedAt == nil {
			return nil, false, nil
		}
		if action.Status.FinishedAt.After(latest) {
			latest = action.Status.FinishedAt.Time
		}
	}

	if actionCount == 0 || latest.IsZero() {
		return nil, false, nil
	}

	finishedAt := metav1.NewTime(latest)
	return &finishedAt, true, nil
}

func (r *RemediationPlanTTLReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("remediationplan-ttl").
		For(&v1alpha1.RemediationPlan{}).
		Owns(&v1alpha1.AgentAction{}).
		Complete(r)
}

func hasRetainAnnotation(plan *v1alpha1.RemediationPlan) bool {
	if plan.Annotations == nil {
		return false
	}
	_, ok := plan.Annotations[remediationPlanRetainAnnotation]
	return ok
}

func ownedByPlan(action *v1alpha1.AgentAction, plan *v1alpha1.RemediationPlan) bool {
	for _, owner := range action.OwnerReferences {
		if owner.Kind == "RemediationPlan" && owner.Name == plan.Name {
			return true
		}
	}

	return action.Name == plan.Name+"-action"
}

func isTerminalAgentActionPhase(phase string) bool {
	switch phase {
	case v1alpha1.PhaseSucceeded, v1alpha1.PhaseFailed, v1alpha1.PhaseRejected:
		return true
	default:
		return false
	}
}
