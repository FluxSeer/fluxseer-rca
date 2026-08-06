package controllers

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fluxseer/api/v1alpha1"
	"fluxseer/internal/canonicaldigest"
	"fluxseer/internal/executor"
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
		setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseWaitingApproval, "waiting for human approval", action.Generation, now())
		action.Status.Approval = &v1alpha1.AgentActionApprovalStatus{
			Approved:           false,
			Source:             "Pending",
			ActionDigest:       agentActionSpecDigest(&action),
			ApprovedGeneration: action.Generation,
		}
		if statusChangedAction(original, &action) {
			if err := r.Status().Update(ctx, &action); err != nil {
				recordStatusUpdateConflict("AgentAction", err)
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	original := action.DeepCopy()
	startedAt := metav1.NewTime(now())
	setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseExecuting, "approved action is executing", action.Generation, startedAt.Time)
	action.Status.Approval = &v1alpha1.AgentActionApprovalStatus{
		Approved:           true,
		ApprovedBy:         action.Spec.ApprovedBy,
		Source:             "LegacySpecApprovedBy",
		ActionDigest:       agentActionSpecDigest(&action),
		ApprovedGeneration: action.Generation,
		ApprovedAt:         &startedAt,
	}
	action.Status.Execution = &v1alpha1.AgentActionExecutionStatus{
		Phase: "Executing",
	}
	if statusChangedAction(original, &action) {
		if err := r.Status().Update(ctx, &action); err != nil {
			recordStatusUpdateConflict("AgentAction", err)
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
		finishedAt := metav1.NewTime(now())
		setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseFailed, err.Error(), action.Generation, finishedAt.Time)
		action.Status.Execution = &v1alpha1.AgentActionExecutionStatus{
			Phase:      "Failed",
			Summary:    err.Error(),
			FinishedAt: &finishedAt,
		}
		if updateErr := r.Status().Update(ctx, &action); updateErr != nil {
			recordStatusUpdateConflict("AgentAction", updateErr)
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	if err := r.Get(ctx, req.NamespacedName, &action); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	finishedAt := metav1.NewTime(now())
	setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseSucceeded, result.Summary, action.Generation, finishedAt.Time)
	action.Status.Execution = &v1alpha1.AgentActionExecutionStatus{
		Phase:      "Succeeded",
		Executor:   result.Executor,
		Summary:    result.Summary,
		FinishedAt: &finishedAt,
	}
	action.Status.Effectiveness = &v1alpha1.AgentActionEffectivenessStatus{
		Phase:   "NotVerified",
		Message: "post-action remediation effectiveness verification is not configured for this experimental action",
	}
	if err := r.Status().Update(ctx, &action); err != nil {
		recordStatusUpdateConflict("AgentAction", err)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func agentActionSpecDigest(action *v1alpha1.AgentAction) string {
	if action == nil {
		return ""
	}
	return canonicaldigest.String(canonicaldigest.RCAJSONV1, action.Spec)
}

func (r *AgentActionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.AgentAction{}).
		Complete(r)
}
