package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/escalation"
	"github.com/FluxSeer/fluxseer-rca/internal/guardrails"
	"github.com/FluxSeer/fluxseer-rca/internal/notifier"
	"github.com/FluxSeer/fluxseer-rca/internal/thresholds"
)

func TestRemediationPlanReconcilerUsesEnabledPolicyPack(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add API scheme: %v", err)
	}

	plan := &v1alpha1.RemediationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "payments-plan",
			Namespace:  "prod",
			Generation: 1,
			Labels:     map[string]string{"app": "payments"},
		},
		Spec: v1alpha1.RemediationPlanSpec{
			Target: v1alpha1.TargetRef{
				Namespace: "prod",
				Kind:      "Deployment",
				Name:      "payments-api",
			},
			Severity: "low",
			Steps: []v1alpha1.RemediationStep{{
				Name:       "pause-rollout",
				ActionType: "kubernetes.rolloutPause",
			}},
		},
	}
	policy := &v1alpha1.ApprovalPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "payments-policy",
			Namespace:       "prod",
			Generation:      2,
			ResourceVersion: "17",
		},
		Spec: v1alpha1.ApprovalPolicySpec{
			Enabled:                true,
			DefaultApprovalTimeout: 90,
			Escalation:             &v1alpha1.EscalationConfig{EscalationChainRef: "payments-chain"},
			ActionTypeRules: []v1alpha1.ActionTypeRule{{
				ActionType: "kubernetes.rolloutPause",
				Action:     v1alpha1.ApprovalActionManual,
				Reason:     "payments rollout requires review",
			}},
		},
	}
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "prod",
			Labels: map[string]string{"team": "payments"},
		},
	}
	chain := &v1alpha1.EscalationChain{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-chain", Namespace: "prod", ResourceVersion: "21"},
		Spec: v1alpha1.EscalationChainSpec{
			Enabled: true,
			Stages:  []v1alpha1.EscalationStage{{Name: "on-call"}},
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}).
		WithObjects(plan, policy, namespace, chain).
		Build()

	legacy := guardrails.NewEngine(guardrails.Policy{
		AllowedActionTypes:       []string{"kubernetes.rolloutPause"},
		AutoApproveMaxSeverity:   domain.SeverityLow,
		RequireApprovalAtOrAbove: domain.SeverityMedium,
	})
	reconciler := &RemediationPlanReconciler{
		Client:       fakeClient,
		Scheme:       scheme,
		Guardrails:   legacy,
		PolicyEngine: guardrails.NewPolicyEngine(fakeClient, legacy, true),
		Now: func() time.Time {
			return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
		},
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(plan)}); err != nil {
		t.Fatalf("reconcile plan: %v", err)
	}

	var storedPlan v1alpha1.RemediationPlan
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(plan), &storedPlan); err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if storedPlan.Status.Phase != v1alpha1.PhaseWaitingApproval {
		t.Fatalf("expected policy-driven waiting approval, got %s", storedPlan.Status.Phase)
	}

	var action v1alpha1.AgentAction
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "prod", Name: "payments-plan-action"}, &action); err != nil {
		t.Fatalf("get generated action: %v", err)
	}
	if action.Spec.ApprovalTimeoutSeconds != 90 {
		t.Fatalf("expected policy timeout 90, got %d", action.Spec.ApprovalTimeoutSeconds)
	}
	if action.Status.Approval == nil || !strings.HasPrefix(action.Status.Approval.Source, "ApprovalPolicy/prod/payments-policy@") {
		t.Fatalf("expected policy audit source, got %#v", action.Status.Approval)
	}
	if action.Annotations[annotationEscalationChainRef] != "payments-chain" {
		t.Fatalf("expected policy escalation chain reference on action, got %#v", action.Annotations)
	}
}

func TestRemediationPlanPolicyWatchMapsOnlyUndecidedPlans(t *testing.T) {
	scheme := policyIntegrationScheme(t)
	pending := &v1alpha1.RemediationPlan{ObjectMeta: metav1.ObjectMeta{Name: "pending", Namespace: "prod"}}
	decided := &v1alpha1.RemediationPlan{
		ObjectMeta: metav1.ObjectMeta{Name: "decided", Namespace: "prod"},
		Status:     v1alpha1.RemediationPlanStatus{ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseWaitingApproval}},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pending, decided).Build()
	reconciler := &RemediationPlanReconciler{Client: fakeClient, PolicyPackEnabled: true}
	requests := reconciler.mapPendingPolicyPlans(context.Background(), &v1alpha1.ApprovalPolicy{})
	if len(requests) != 1 || requests[0].NamespacedName != client.ObjectKeyFromObject(pending) {
		t.Fatalf("expected only pending plan to be mapped, got %#v", requests)
	}
}

func TestRemediationPlanReconcilerAppliesNamespaceThresholdDefaults(t *testing.T) {
	scheme := policyIntegrationScheme(t)
	newPlan := func(name string, ttlSeconds, approvalTimeoutSeconds int64) *v1alpha1.RemediationPlan {
		return &v1alpha1.RemediationPlan{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "prod", Generation: 1},
			Spec: v1alpha1.RemediationPlanSpec{
				Target:                 v1alpha1.TargetRef{Namespace: "prod", Kind: "Deployment", Name: name},
				Severity:               "low",
				TTLSeconds:             ttlSeconds,
				ApprovalTimeoutSeconds: approvalTimeoutSeconds,
				Steps: []v1alpha1.RemediationStep{{
					Name:       "scale-down",
					ActionType: "kubernetes.scaleDeployment",
				}},
			},
		}
	}
	defaultedPlan := newPlan("defaulted", 0, 0)
	explicitPlan := newPlan("explicit", 300, 45)
	threshold := &v1alpha1.NamespaceThreshold{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-defaults", Namespace: "prod"},
		Spec: v1alpha1.NamespaceThresholdSpec{
			Enabled:                       true,
			DefaultTTLSeconds:             7200,
			DefaultApprovalTimeoutSeconds: 180,
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}).
		WithObjects(defaultedPlan, explicitPlan, threshold).
		Build()
	legacy := guardrails.NewEngine(guardrails.Policy{
		AllowedActionTypes:       []string{"kubernetes.scaleDeployment"},
		AutoApproveMaxSeverity:   domain.SeverityLow,
		RequireApprovalAtOrAbove: domain.SeverityMedium,
	})
	reconciler := &RemediationPlanReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Guardrails: legacy,
		Thresholds: thresholds.NewEnforcer(fakeClient),
	}

	for _, plan := range []*v1alpha1.RemediationPlan{defaultedPlan, explicitPlan} {
		if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(plan)}); err != nil {
			t.Fatalf("reconcile %s plan: %v", plan.Name, err)
		}
	}

	var storedDefaultedPlan v1alpha1.RemediationPlan
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(defaultedPlan), &storedDefaultedPlan); err != nil {
		t.Fatalf("get defaulted plan: %v", err)
	}
	if storedDefaultedPlan.Spec.TTLSeconds != 7200 || storedDefaultedPlan.Spec.ApprovalTimeoutSeconds != 180 {
		t.Fatalf("expected namespace defaults on plan, got ttl=%d approvalTimeout=%d", storedDefaultedPlan.Spec.TTLSeconds, storedDefaultedPlan.Spec.ApprovalTimeoutSeconds)
	}

	var defaultedAction v1alpha1.AgentAction
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "prod", Name: "defaulted-action"}, &defaultedAction); err != nil {
		t.Fatalf("get defaulted action: %v", err)
	}
	if defaultedAction.Spec.TTLSeconds != 7200 || defaultedAction.Spec.ApprovalTimeoutSeconds != 180 {
		t.Fatalf("expected namespace defaults on action, got ttl=%d approvalTimeout=%d", defaultedAction.Spec.TTLSeconds, defaultedAction.Spec.ApprovalTimeoutSeconds)
	}

	var storedExplicitPlan v1alpha1.RemediationPlan
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(explicitPlan), &storedExplicitPlan); err != nil {
		t.Fatalf("get explicit plan: %v", err)
	}
	if storedExplicitPlan.Spec.TTLSeconds != 300 || storedExplicitPlan.Spec.ApprovalTimeoutSeconds != 45 {
		t.Fatalf("expected explicit plan values to win, got ttl=%d approvalTimeout=%d", storedExplicitPlan.Spec.TTLSeconds, storedExplicitPlan.Spec.ApprovalTimeoutSeconds)
	}

	var explicitAction v1alpha1.AgentAction
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "prod", Name: "explicit-action"}, &explicitAction); err != nil {
		t.Fatalf("get explicit action: %v", err)
	}
	if explicitAction.Spec.TTLSeconds != 300 || explicitAction.Spec.ApprovalTimeoutSeconds != 45 {
		t.Fatalf("expected explicit action values to win, got ttl=%d approvalTimeout=%d", explicitAction.Spec.TTLSeconds, explicitAction.Spec.ApprovalTimeoutSeconds)
	}
}

func TestRemediationPlanReconcilerRejectsNamespaceThresholdViolation(t *testing.T) {
	scheme := policyIntegrationScheme(t)
	currentPlan := &v1alpha1.RemediationPlan{
		ObjectMeta: metav1.ObjectMeta{Name: "current", Namespace: "prod", Generation: 1},
		Spec: v1alpha1.RemediationPlanSpec{
			Target:   v1alpha1.TargetRef{Namespace: "prod", Kind: "Deployment", Name: "current"},
			Severity: "low",
			Steps:    []v1alpha1.RemediationStep{{ActionType: "kubernetes.scaleDeployment"}},
		},
	}
	otherPlan := &v1alpha1.RemediationPlan{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "prod", Generation: 1},
		Spec: v1alpha1.RemediationPlanSpec{
			Target:   v1alpha1.TargetRef{Namespace: "prod", Kind: "Deployment", Name: "other"},
			Severity: "low",
		},
		Status: v1alpha1.RemediationPlanStatus{ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseWaitingApproval}},
	}
	threshold := &v1alpha1.NamespaceThreshold{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-limit", Namespace: "prod"},
		Spec: v1alpha1.NamespaceThresholdSpec{
			Enabled:          true,
			ActivePlansLimit: 1,
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}).
		WithObjects(currentPlan, otherPlan, threshold).
		Build()
	legacy := guardrails.NewEngine(guardrails.Policy{AllowedActionTypes: []string{"kubernetes.scaleDeployment"}})
	reconciler := &RemediationPlanReconciler{
		Client:     fakeClient,
		Scheme:     scheme,
		Guardrails: legacy,
		Thresholds: thresholds.NewEnforcer(fakeClient),
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(currentPlan)}); err != nil {
		t.Fatalf("reconcile threshold-limited plan: %v", err)
	}
	var stored v1alpha1.RemediationPlan
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(currentPlan), &stored); err != nil {
		t.Fatalf("get rejected plan: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseRejected || !strings.Contains(stored.Status.Message, "activePlans") {
		t.Fatalf("expected threshold rejection, got %#v", stored.Status)
	}
	var action v1alpha1.AgentAction
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "prod", Name: "current-action"}, &action); !apierrors.IsNotFound(err) {
		t.Fatalf("expected no action for threshold rejection, got action=%#v err=%v", action, err)
	}
}

func TestAgentActionReconcilerRoutesEscalationChainOnTimeout(t *testing.T) {
	scheme := policyIntegrationScheme(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	decidedAt := metav1.NewTime(now.Add(-2 * time.Minute))
	action := &v1alpha1.AgentAction{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "payments-action",
			Namespace:   "prod",
			Annotations: map[string]string{annotationEscalationChainRef: "payments-chain"},
			Labels:      map[string]string{"app": "payments"},
			Generation:  1,
		},
		Spec: v1alpha1.AgentActionSpec{
			Target:                 v1alpha1.TargetRef{Namespace: "prod", Kind: "Deployment", Name: "payments"},
			ActionType:             "kubernetes.scaleDeployment",
			ApprovalTimeoutSeconds: 60,
		},
		Status: v1alpha1.AgentActionStatus{
			ResourceStatus: v1alpha1.ResourceStatus{
				Phase:     v1alpha1.PhaseWaitingApproval,
				UpdatedAt: metav1.NewTime(now.Add(-2 * time.Minute)),
			},
			Approval: &v1alpha1.AgentActionApprovalStatus{
				Approved:  false,
				Source:    "ManualApprovalRequired",
				DecidedAt: &decidedAt,
			},
		},
	}
	action.Status.Approval.ActionDigest = agentActionSpecDigest(action)
	chain := &v1alpha1.EscalationChain{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-chain", Namespace: "prod", ResourceVersion: "21"},
		Spec: v1alpha1.EscalationChainSpec{
			Enabled: true,
			Stages: []v1alpha1.EscalationStage{{
				Name:      "on-call",
				Assignees: []v1alpha1.Assignee{{Type: v1alpha1.AssigneeTypeTeam, Name: "payments-oncall"}},
			}},
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.AgentAction{}).
		WithObjects(action, chain).
		Build()
	spy := &policyIntegrationNotifier{}
	reconciler := &AgentActionReconciler{
		Client:           fakeClient,
		Scheme:           scheme,
		EscalationRouter: escalation.NewRouter(fakeClient),
		Notifier:         spy,
		Now:              func() time.Time { return now },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(action)}); err != nil {
		t.Fatalf("reconcile timed-out action: %v", err)
	}
	if len(spy.Messages) != 1 {
		t.Fatalf("expected one escalation notification, got %d", len(spy.Messages))
	}
	if spy.Messages[0].Fields["escalationChain"] != "payments-chain" || spy.Messages[0].Fields["escalationChainVersion"] != "21" {
		t.Fatalf("expected chain provenance in notification, got %#v", spy.Messages[0].Fields)
	}
}

func policyIntegrationScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add API scheme: %v", err)
	}
	return scheme
}

type policyIntegrationNotifier struct {
	Messages []notifier.Message
}

func (n *policyIntegrationNotifier) Name() string {
	return "policy-integration"
}

func (n *policyIntegrationNotifier) Notify(_ context.Context, message notifier.Message) error {
	n.Messages = append(n.Messages, message)
	return nil
}
