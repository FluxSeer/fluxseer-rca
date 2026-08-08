package controllers

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/canonicaldigest"
	"github.com/FluxSeer/fluxseer-rca/internal/executor"
	"github.com/FluxSeer/fluxseer-rca/internal/notifier"
)

type AgentActionReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Executor *executor.Router
	Notifier notifier.Notifier
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
		return r.reconcilePending(ctx, &action, now())
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

// reconcilePending handles an AgentAction that isAgentActionApproved has
// rejected. It distinguishes two cases that must not be treated the same
// way. An action that guardrails never validly evaluated (no approval record
// yet, or the digest no longer matches the current spec because the action
// was created directly or edited after approval was requested) is reset to a
// fresh Pending/WaitingApproval state, same as before this method existed.
// An action that guardrails did evaluate and correctly left waiting on a
// human must NOT be reset the same way: isAgentActionApproved only honors
// spec.approvedBy once status.approval.source is still
// "ManualApprovalRequired", so overwriting that source to "Pending" on every
// reconcile -- which the previous, single-branch version of this code did --
// would permanently strip the action's ability to ever be approved as soon
// as anything (a resync, the timeout check below) reconciles it again before
// a human acts. That case instead only advances the approval timeout.
func (r *AgentActionReconciler) reconcilePending(ctx context.Context, action *v1alpha1.AgentAction, now time.Time) (ctrl.Result, error) {
	digest := agentActionSpecDigest(action)
	if action.Status.Approval == nil || action.Status.Approval.ActionDigest != digest {
		original := action.DeepCopy()
		setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseWaitingApproval, "waiting for human approval", action.Generation, now)
		action.Status.Approval = &v1alpha1.AgentActionApprovalStatus{
			Approved:           false,
			Source:             "Pending",
			ActionDigest:       digest,
			ApprovedGeneration: action.Generation,
		}
		if statusChangedAction(original, action) {
			if err := r.Status().Update(ctx, action); err != nil {
				recordStatusUpdateConflict("AgentAction", err)
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	return r.reconcileApprovalTimeout(ctx, action, now)
}

// reconcileApprovalTimeout escalates an AgentAction that has been validly
// WaitingApproval for longer than spec.approvalTimeoutSeconds. It anchors
// the wait window to status.updatedAt -- the timestamp already recorded when
// the action first entered WaitingApproval -- and, as long as the deadline
// has not passed, returns a RequeueAfter without writing any status at all.
// Writing status on every pass (even "no change" writes that only refresh a
// timestamp) would slide status.updatedAt forward each time and the timeout
// would never trip.
func (r *AgentActionReconciler) reconcileApprovalTimeout(ctx context.Context, action *v1alpha1.AgentAction, now time.Time) (ctrl.Result, error) {
	if action.Status.Phase == v1alpha1.PhaseEscalated || action.Spec.ApprovalTimeoutSeconds <= 0 || action.Status.UpdatedAt.IsZero() {
		return ctrl.Result{}, nil
	}

	deadline := action.Status.UpdatedAt.Add(time.Duration(action.Spec.ApprovalTimeoutSeconds) * time.Second)
	if now.Before(deadline) {
		return ctrl.Result{RequeueAfter: deadline.Sub(now)}, nil
	}

	if r.Notifier != nil {
		// Notify before persisting Escalated, mirroring
		// RiskSignalNotificationReconciler's notify-then-mark-done ordering:
		// a failed delivery leaves the phase as WaitingApproval so the next
		// reconcile retries the notification instead of losing it silently.
		if err := r.Notifier.Notify(ctx, notifier.Message{
			Title:   "FluxSeer RCA approval escalation",
			Summary: fmt.Sprintf("%s exceeded its %ds approval timeout", targetRefString(action.Spec.Target), action.Spec.ApprovalTimeoutSeconds),
			Body:    action.Status.Message,
			Fields: map[string]any{
				"namespace":  action.Namespace,
				"name":       action.Name,
				"actionType": action.Spec.ActionType,
			},
		}); err != nil {
			return ctrl.Result{}, err
		}
	}

	original := action.DeepCopy()
	setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseEscalated, "approval timeout exceeded, escalation notified", action.Generation, now)
	escalatedAt := metav1.NewTime(now)
	action.Status.Approval.EscalatedAt = &escalatedAt
	if statusChangedAction(original, action) {
		if err := r.Status().Update(ctx, action); err != nil {
			recordStatusUpdateConflict("AgentAction", err)
			return ctrl.Result{}, err
		}
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
