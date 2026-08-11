package controllers

import (
	"context"
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/guardrails"
)

type RemediationPlanReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Guardrails    *guardrails.Engine
	PolicyEngine  *guardrails.PolicyEngine
	EventRecorder record.EventRecorder
	Now           func() time.Time
}

const approvalDecisionActor = "fluxseer-rca-policy-engine"

func (r *RemediationPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var plan v1alpha1.RemediationPlan
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// The guardrails decision is derived once from an immutable plan spec.
	// Re-deriving it on every reconcile would both self-requeue forever (the
	// timestamp this loop stamps into plan.Status always differs from the
	// previous pass) and repeatedly overwrite the owned AgentAction's status
	// back to this initial decision -- undoing whatever AgentActionReconciler
	// has since done (approval, execution, escalation), because Owns(&AgentAction{})
	// re-triggers this reconciler on every AgentAction status change.
	if plan.Status.Phase != "" {
		return ctrl.Result{}, nil
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	evaluation, err := r.evaluatePolicy(ctx, &plan)
	if err != nil {
		return ctrl.Result{}, err
	}
	decision := evaluation.Decision
	approvalTimeoutSeconds := plan.Spec.ApprovalTimeoutSeconds
	if evaluation.ApprovalTimeoutSeconds > 0 {
		approvalTimeoutSeconds = evaluation.ApprovalTimeoutSeconds
	}

	action := &v1alpha1.AgentAction{}
	action.Name = plan.Name + "-action"
	action.Namespace = plan.Namespace
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, action, func() error {
		if err := controllerutil.SetControllerReference(&plan, action, r.Scheme); err != nil {
			return err
		}

		step := remediationFromPlan(&plan)
		action.Spec.Target = plan.Spec.Target
		action.Spec.ActionType = step.ActionType
		action.Spec.Parameters = step.Parameters
		action.Spec.DryRunResult = decision.DryRunResult
		action.Spec.TTLSeconds = plan.Spec.TTLSeconds
		action.Spec.ApprovalTimeoutSeconds = approvalTimeoutSeconds
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
		setResourceStatus(&plan.Status.ResourceStatus, v1alpha1.PhaseApproved, "policy auto-approved remediation plan", plan.Generation, now())
	case domain.ApprovalManual:
		setResourceStatus(&plan.Status.ResourceStatus, v1alpha1.PhaseWaitingApproval, "human approval required before execution", plan.Generation, now())
	default:
		setResourceStatus(&plan.Status.ResourceStatus, v1alpha1.PhaseRejected, decision.Reason, plan.Generation, now())
		finishedAt := metav1.NewTime(now())
		plan.Status.FinishedAt = &finishedAt
	}
	if statusChangedPlan(originalPlan, &plan) {
		if err := r.Status().Update(ctx, &plan); err != nil && !recordStatusUpdateConflict("RemediationPlan", err) {
			return ctrl.Result{}, err
		}
	}

	originalAction := action.DeepCopy()
	recordedAt := metav1.NewTime(now())
	action.Status.DryRunResult = &v1alpha1.AgentActionDryRunStatus{
		Result:     decision.DryRunResult,
		RecordedAt: &recordedAt,
	}
	switch decision.Action {
	case domain.ApprovalAuto:
		setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseApproved, decision.Reason, action.Generation, recordedAt.Time)
		action.Status.Approval = &v1alpha1.AgentActionApprovalStatus{
			Approved:           true,
			ApprovedBy:         decision.ApprovedBy,
			Source:             approvalSource(evaluation, "GuardrailsAutoApproval"),
			ActionDigest:       agentActionSpecDigest(action),
			ApprovedGeneration: action.Generation,
			DecidedAt:          &recordedAt,
			DecidedBy:          approvalDecisionActor,
		}
	case domain.ApprovalManual:
		setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseWaitingApproval, decision.Reason, action.Generation, recordedAt.Time)
		action.Status.Approval = &v1alpha1.AgentActionApprovalStatus{
			Approved:           false,
			Source:             approvalSource(evaluation, "ManualApprovalRequired"),
			ActionDigest:       agentActionSpecDigest(action),
			ApprovedGeneration: action.Generation,
			DecidedAt:          &recordedAt,
			DecidedBy:          approvalDecisionActor,
		}
	default:
		setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseRejected, decision.Reason, action.Generation, recordedAt.Time)
		action.Status.FinishedAt = &recordedAt
		action.Status.Approval = &v1alpha1.AgentActionApprovalStatus{
			Approved:           false,
			Source:             approvalSource(evaluation, "GuardrailsRejected"),
			ActionDigest:       agentActionSpecDigest(action),
			ApprovedGeneration: action.Generation,
			DecidedAt:          &recordedAt,
			DecidedBy:          approvalDecisionActor,
		}
	}
	if statusChangedAction(originalAction, action) {
		if err := r.Status().Update(ctx, action); err != nil && !recordStatusUpdateConflict("AgentAction", err) {
			return ctrl.Result{}, err
		}
		// Emit Event only after successful status update (using nil-safe helper)
		recordPhaseTransition(r.EventRecorder, action, originalAction.Status.Phase, action.Status.Phase)
	}

	return ctrl.Result{}, nil
}

func (r *RemediationPlanReconciler) evaluatePolicy(ctx context.Context, plan *v1alpha1.RemediationPlan) (guardrails.PolicyEvaluation, error) {
	if r.PolicyEngine == nil {
		if r.Guardrails == nil {
			return guardrails.PolicyEvaluation{}, fmt.Errorf("remediation plan reconciler requires a guardrails evaluator")
		}
		return guardrails.PolicyEvaluation{
			Decision: r.Guardrails.Evaluate(guardrails.ReviewInput{
				Resource: targetToResource(plan.Spec.Target),
				Reasoning: domain.ReasoningOutput{
					Severity:    domain.Severity(plan.Spec.Severity),
					Remediation: remediationFromPlan(plan),
				},
			}),
			Source: guardrails.PolicySourceLegacy,
		}, nil
	}

	input := guardrails.PolicyReviewInput{
		ReviewInput: guardrails.ReviewInput{
			Resource: targetToResource(plan.Spec.Target),
			Reasoning: domain.ReasoningOutput{
				Severity:    domain.Severity(plan.Spec.Severity),
				Remediation: remediationFromPlan(plan),
			},
		},
		ResourceLabels: copyStringMap(plan.Labels),
	}

	namespaceName := plan.Spec.Target.Namespace
	if namespaceName == "" {
		namespaceName = plan.Namespace
	}
	if namespaceName != "" {
		var namespace corev1.Namespace
		err := r.Get(ctx, client.ObjectKey{Name: namespaceName}, &namespace)
		if err == nil {
			input.NamespaceLabels = copyStringMap(namespace.Labels)
		} else if !apierrors.IsNotFound(err) {
			return guardrails.PolicyEvaluation{}, fmt.Errorf("get target namespace %q: %w", namespaceName, err)
		}
	}

	return r.PolicyEngine.Evaluate(ctx, input)
}

func approvalSource(evaluation guardrails.PolicyEvaluation, fallback string) string {
	if evaluation.Policy == nil {
		return fallback
	}
	return fmt.Sprintf("%s/%s/%s@%s", evaluation.Policy.Kind, evaluation.Policy.Namespace, evaluation.Policy.Name, evaluation.Policy.Version)
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
	return !reflect.DeepEqual(before.Status, after.Status)
}
