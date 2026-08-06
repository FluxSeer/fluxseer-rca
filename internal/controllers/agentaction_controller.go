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

	if !isAgentActionApproved(&action) {
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
	approvedBy := action.Status.Approval.ApprovedBy
	source := action.Status.Approval.Source
	if !action.Status.Approval.Approved {
		// Manual-approval path: guardrails already evaluated this exact
		// action content (proven by the digest match in
		// isAgentActionApproved) and required human approval rather than
		// auto-approving or rejecting it. A human has since supplied
		// spec.approvedBy, so we can now treat it as approved.
		approvedBy = action.Spec.ApprovedBy
		source = "ManualApprovalConfirmed"
	}
	action.Status.Approval = &v1alpha1.AgentActionApprovalStatus{
		Approved:           true,
		ApprovedBy:         approvedBy,
		Source:             source,
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
		ApprovedBy:   approvedBy,
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

// isAgentActionApproved reports whether an AgentAction is safe to execute.
// It never trusts spec.approvedBy on its own: that field is a plain,
// user-writable CR spec field, so treating it as sufficient proof of
// approval would let anyone with RBAC write access to AgentAction bypass
// guardrails.Engine entirely (create or edit an AgentAction directly,
// skipping RemediationPlan, with spec.approvedBy pre-filled). Approval must
// instead trace back to status.approval, which only RemediationPlanReconciler
// writes after actually evaluating the action against guardrails -- and the
// digest comparison ensures the action content guardrails evaluated is the
// same content about to execute, so editing spec after approval (or
// approving) invalidates it rather than silently executing changed
// parameters under a stale approval.
func isAgentActionApproved(action *v1alpha1.AgentAction) bool {
	approval := action.Status.Approval
	if approval == nil {
		return false
	}
	if approval.ActionDigest != agentActionSpecDigest(action) {
		return false
	}
	if approval.Approved {
		return true
	}
	// Manual approval is only honored once guardrails has already evaluated
	// this exact action and required human approval -- not for actions
	// guardrails rejected, and not for actions that never went through
	// guardrails at all (e.g. an AgentAction created directly, bypassing
	// RemediationPlan).
	return approval.Source == "ManualApprovalRequired" && action.Spec.ApprovedBy != ""
}

// agentActionSpecDigest fingerprints the action content that guardrails
// evaluated (target, action type, parameters, dry-run result, rollback
// plan). ApprovedBy is deliberately excluded: it identifies who approved the
// action, not what the action does, and a human is expected to fill it in
// after the digest was first computed during a ManualApprovalRequired
// evaluation, so including it would make the digest never match again once
// approved.
func agentActionSpecDigest(action *v1alpha1.AgentAction) string {
	if action == nil {
		return ""
	}
	spec := action.Spec
	spec.ApprovedBy = ""
	return canonicaldigest.String(canonicaldigest.RCAJSONV1, spec)
}

func (r *AgentActionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.AgentAction{}).
		Complete(r)
}
