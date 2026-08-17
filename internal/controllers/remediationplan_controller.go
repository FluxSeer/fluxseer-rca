package controllers

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/guardrails"
	"github.com/FluxSeer/fluxseer-rca/internal/thresholds"
)

type RemediationPlanReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Guardrails        *guardrails.Engine
	PolicyEngine      *guardrails.PolicyEngine
	Thresholds        *thresholds.Enforcer
	PolicyPackEnabled bool
	EventRecorder     record.EventRecorder
	Now               func() time.Time
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

	targetNamespace := plan.Spec.Target.Namespace
	if targetNamespace == "" {
		targetNamespace = plan.Namespace
	}
	if r.Thresholds != nil {
		namespaceLabels, err := r.namespaceLabels(ctx, targetNamespace)
		if err != nil {
			return ctrl.Result{}, err
		}
		usage, err := r.namespaceUsage(ctx, targetNamespace)
		if err != nil {
			return ctrl.Result{}, err
		}
		result, err := r.Thresholds.Enforce(ctx, thresholds.ResolveRequest{
			Namespace:       targetNamespace,
			NamespaceLabels: namespaceLabels,
		}, usage)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !result.Allowed {
			return r.rejectForThreshold(ctx, &plan, result, now())
		}
		if result.Resolution != nil {
			threshold := result.Resolution.Threshold.Spec
			defaultsChanged := false
			if plan.Spec.TTLSeconds == 0 && threshold.DefaultTTLSeconds > 0 {
				plan.Spec.TTLSeconds = threshold.DefaultTTLSeconds
				defaultsChanged = true
			}
			if plan.Spec.ApprovalTimeoutSeconds == 0 && threshold.DefaultApprovalTimeoutSeconds > 0 {
				plan.Spec.ApprovalTimeoutSeconds = threshold.DefaultApprovalTimeoutSeconds
				defaultsChanged = true
			}
			if defaultsChanged {
				if err := r.persistNamespaceDefaults(ctx, &plan); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
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
		action.Labels = copyStringMap(plan.Labels)
		if action.Annotations == nil {
			action.Annotations = map[string]string{}
		}
		if evaluation.Escalation != nil && evaluation.Escalation.EscalationChainRef != "" {
			action.Annotations[annotationEscalationChainRef] = evaluation.Escalation.EscalationChainRef
		}
		if targetUID := plan.Annotations[annotationTargetUID]; targetUID != "" {
			action.Annotations[annotationTargetUID] = targetUID
		}
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

// persistNamespaceDefaults makes threshold-derived values visible on the
// RemediationPlan itself. This keeps the plan TTL controller and the generated
// AgentAction on the same effective contract, while preserving any explicit
// plan values supplied by the user.
func (r *RemediationPlanReconciler) persistNamespaceDefaults(ctx context.Context, plan *v1alpha1.RemediationPlan) error {
	if plan == nil {
		return nil
	}
	return r.Update(ctx, plan)
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

func (r *RemediationPlanReconciler) namespaceLabels(ctx context.Context, namespaceName string) (map[string]string, error) {
	if namespaceName == "" {
		return nil, nil
	}
	var namespace corev1.Namespace
	err := r.Get(ctx, client.ObjectKey{Name: namespaceName}, &namespace)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get target namespace %q: %w", namespaceName, err)
	}
	return copyStringMap(namespace.Labels), nil
}

func (r *RemediationPlanReconciler) namespaceUsage(ctx context.Context, namespaceName string) (thresholds.Usage, error) {
	var plans v1alpha1.RemediationPlanList
	if err := r.List(ctx, &plans, client.InNamespace(namespaceName)); err != nil {
		return thresholds.Usage{}, fmt.Errorf("list remediation plans in namespace %q: %w", namespaceName, err)
	}
	usage := thresholds.Usage{}
	for _, plan := range plans.Items {
		switch plan.Status.Phase {
		case v1alpha1.PhaseSucceeded, v1alpha1.PhaseFailed, v1alpha1.PhaseRejected, v1alpha1.PhaseCompleted:
			continue
		default:
			usage.ActivePlans++
		}
	}

	var actions v1alpha1.AgentActionList
	if err := r.List(ctx, &actions, client.InNamespace(namespaceName)); err != nil {
		return thresholds.Usage{}, fmt.Errorf("list agent actions in namespace %q: %w", namespaceName, err)
	}
	for _, action := range actions.Items {
		if action.Status.Phase == v1alpha1.PhaseWaitingApproval || action.Status.Phase == v1alpha1.PhaseEscalated {
			usage.PendingApprovals++
		}
	}
	return usage, nil
}

func (r *RemediationPlanReconciler) rejectForThreshold(ctx context.Context, plan *v1alpha1.RemediationPlan, result thresholds.EnforcementResult, now time.Time) (ctrl.Result, error) {
	parts := make([]string, 0, len(result.Violations))
	for _, violation := range result.Violations {
		parts = append(parts, fmt.Sprintf("%s=%d exceeds limit=%d", violation.Resource, violation.Current, violation.Limit))
	}
	message := "namespace threshold exceeded"
	if result.Resolution != nil {
		ref := result.Resolution.Reference
		message = fmt.Sprintf("namespace threshold %s/%s@%s exceeded", ref.Namespace, ref.Name, ref.Version)
	}
	original := plan.DeepCopy()
	setResourceStatus(&plan.Status.ResourceStatus, v1alpha1.PhaseRejected, message+": "+strings.Join(parts, "; "), plan.Generation, now)
	finishedAt := metav1.NewTime(now)
	plan.Status.FinishedAt = &finishedAt
	if !statusChangedPlan(original, plan) {
		return ctrl.Result{}, nil
	}
	if err := r.Status().Update(ctx, plan); err != nil && !recordStatusUpdateConflict("RemediationPlan", err) {
		return ctrl.Result{}, err
	}
	if r.EventRecorder != nil {
		r.EventRecorder.Event(plan, corev1.EventTypeWarning, "NamespaceThresholdExceeded", plan.Status.Message)
	}
	return ctrl.Result{}, nil
}

func approvalSource(evaluation guardrails.PolicyEvaluation, fallback string) string {
	if evaluation.Policy == nil {
		return fallback
	}
	return fmt.Sprintf("%s/%s/%s@%s", evaluation.Policy.Kind, evaluation.Policy.Namespace, evaluation.Policy.Name, evaluation.Policy.Version)
}

func (r *RemediationPlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RemediationPlan{}).
		Owns(&v1alpha1.AgentAction{})
	if r.PolicyPackEnabled {
		builder = builder.
			Watches(&v1alpha1.ApprovalPolicy{}, handler.EnqueueRequestsFromMapFunc(r.mapPendingPolicyPlans)).
			Watches(&v1alpha1.NamespaceThreshold{}, handler.EnqueueRequestsFromMapFunc(r.mapPendingPolicyPlans))
	}
	return builder.Complete(r)
}

// mapPendingPolicyPlans requeues only plans that have not received an initial decision.
// Terminal and in-flight decisions remain immutable after reconciliation.
func (r *RemediationPlanReconciler) mapPendingPolicyPlans(ctx context.Context, _ client.Object) []reconcile.Request {
	var plans v1alpha1.RemediationPlanList
	if err := r.List(ctx, &plans); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(plans.Items))
	for i := range plans.Items {
		if plans.Items[i].Status.Phase == "" {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&plans.Items[i])})
		}
	}
	return requests
}

func statusChangedPlan(before, after *v1alpha1.RemediationPlan) bool {
	return before.Status != after.Status
}

func statusChangedAction(before, after *v1alpha1.AgentAction) bool {
	return !reflect.DeepEqual(before.Status, after.Status)
}
