package controllers

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/executor"
)

type AgentActionReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Executor *executor.Router
	Now      func() time.Time
}

func (r *AgentActionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var action v1alpha1.AgentAction
	if err := r.Get(ctx, req.NamespacedName, &action); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	if action.Status.Phase == v1alpha1.PhaseSucceeded || action.Status.Phase == v1alpha1.PhaseFailed || action.Status.Phase == v1alpha1.PhaseRejected {
		return ctrl.Result{}, nil
	}

	if action.Spec.ApprovedBy == "" {
		original := action.DeepCopy()
		setResourceStatus(&action.Status, v1alpha1.PhaseWaitingApproval, "waiting for human approval", action.Generation, now())
		if statusChangedAction(original, &action) {
			if err := r.Status().Update(ctx, &action); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	original := action.DeepCopy()
	setResourceStatus(&action.Status, v1alpha1.PhaseExecuting, "approved action is executing", action.Generation, now())
	if statusChangedAction(original, &action) {
		if err := r.Status().Update(ctx, &action); err != nil {
			return ctrl.Result{}, err
		}
	}

	result, err := r.Executor.Execute(ctx, executor.ApprovedAction{
		Resource:     targetToResource(action.Spec.Target),
		ActionType:   action.Spec.ActionType,
		Parameters:   action.Spec.Parameters,
		ApprovedBy:   action.Spec.ApprovedBy,
		DryRunResult: action.Spec.DryRunResult,
		RollbackPlan: action.Spec.RollbackPlan,
	})
	if err != nil {
		if err := r.Get(ctx, req.NamespacedName, &action); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		setResourceStatus(&action.Status, v1alpha1.PhaseFailed, err.Error(), action.Generation, now())
		if updateErr := r.Status().Update(ctx, &action); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	if err := r.Get(ctx, req.NamespacedName, &action); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	setResourceStatus(&action.Status, v1alpha1.PhaseSucceeded, result.Summary, action.Generation, now())
	if err := r.Status().Update(ctx, &action); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AgentActionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.AgentAction{}).
		Complete(r)
}
