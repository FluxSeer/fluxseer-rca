package controllers

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/domain"
	"fluxagent/internal/guardrails"
)

type RemediationPlanReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Guardrails *guardrails.Engine
	Now        func() time.Time
}

func (r *RemediationPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var plan v1alpha1.RemediationPlan
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	decision := r.Guardrails.Evaluate(guardrails.ReviewInput{
		Resource: targetToResource(plan.Spec.Target),
		Reasoning: domain.ReasoningOutput{
			Severity:    domain.Severity(plan.Spec.Severity),
			Remediation: remediationFromPlan(&plan),
		},
	})

	action := &v1alpha1.AgentAction{}
	action.Name = plan.Name + "-action"
	action.Namespace = plan.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, action, func() error {
		if err := controllerutil.SetControllerReference(&plan, action, r.Scheme); err != nil {
			return err
		}

		step := remediationFromPlan(&plan)
		action.Spec.Target = plan.Spec.Target
		action.Spec.ActionType = step.ActionType
		action.Spec.Parameters = step.Parameters
		action.Spec.DryRunResult = decision.DryRunResult
		action.Spec.TTLSeconds = plan.Spec.TTLSeconds
		action.Spec.RollbackPlan = plan.Spec.RollbackPlan
		if decision.Action == domain.ApprovalAuto {
			action.Spec.ApprovedBy = decision.ApprovedBy
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	originalPlan := plan.DeepCopy()
	switch decision.Action {
	case domain.ApprovalAuto:
		setResourceStatus(&plan.Status, v1alpha1.PhaseApproved, "policy auto-approved remediation plan", plan.Generation, now())
	case domain.ApprovalManual:
		setResourceStatus(&plan.Status, v1alpha1.PhaseWaitingApproval, "human approval required before execution", plan.Generation, now())
	default:
		setResourceStatus(&plan.Status, v1alpha1.PhaseRejected, decision.Reason, plan.Generation, now())
	}
	if statusChangedPlan(originalPlan, &plan) {
		if err := r.Status().Update(ctx, &plan); err != nil && !recordStatusUpdateConflict("RemediationPlan", err) {
			return ctrl.Result{}, err
		}
	}

	originalAction := action.DeepCopy()
	switch decision.Action {
	case domain.ApprovalAuto:
		setResourceStatus(&action.Status, v1alpha1.PhaseApproved, decision.Reason, action.Generation, now())
	case domain.ApprovalManual:
		setResourceStatus(&action.Status, v1alpha1.PhaseWaitingApproval, decision.Reason, action.Generation, now())
	default:
		setResourceStatus(&action.Status, v1alpha1.PhaseRejected, decision.Reason, action.Generation, now())
	}
	if statusChangedAction(originalAction, action) {
		if err := r.Status().Update(ctx, action); err != nil && !recordStatusUpdateConflict("AgentAction", err) {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *RemediationPlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RemediationPlan{}).
		Owns(&v1alpha1.AgentAction{}).
		Complete(r)
}

func statusChangedPlan(before, after *v1alpha1.RemediationPlan) bool {
	return before.Status != after.Status
}

func statusChangedAction(before, after *v1alpha1.AgentAction) bool {
	return before.Status != after.Status
}
