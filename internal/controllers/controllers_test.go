package controllers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/executor"
	"github.com/FluxSeer/fluxseer-rca/internal/guardrails"
	"github.com/FluxSeer/fluxseer-rca/internal/notifier"
)

func TestControllerChainCreatesPlanActionAndExecutesAfterApproval(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	riskSignal := &v1alpha1.RiskSignal{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskSignal",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "payments-api-risk",
			Namespace:  "payments",
			Generation: 1,
		},
		Spec: v1alpha1.RiskSignalSpec{
			Target: v1alpha1.TargetRef{
				Cluster:    "prod",
				Namespace:  "payments",
				Kind:       "Deployment",
				Name:       "payments-api",
				APIVersion: "apps/v1",
				Service:    "payments-api",
			},
			SignalType: "incident",
			ActionType: "kubernetes.rolloutPause",
			Severity:   "high",
			Confidence: 90,
			DryRun:     true,
			TTLSeconds: 1800,
			Evidence: []v1alpha1.EvidenceRef{
				{Kind: "event", Summary: "Pod entered OOMKilled"},
			},
			Parameters: map[string]string{"reason": "auto-generated"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskSignal{}, &v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}).
		WithObjects(riskSignal).
		Build()

	riskReconciler := &RiskSignalReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Enabled: true,
		Now:     func() time.Time { return now },
	}
	if _, err := riskReconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	}); err != nil {
		t.Fatalf("risk reconcile failed: %v", err)
	}

	var plan v1alpha1.RemediationPlan
	if err := fakeClient.Get(context.Background(), types.NamespacedName{
		Name:      "payments-api-risk-plan",
		Namespace: "payments",
	}, &plan); err != nil {
		t.Fatalf("expected remediation plan: %v", err)
	}

	planReconciler := &RemediationPlanReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Guardrails: guardrails.NewEngine(guardrails.Policy{
			AllowedActionTypes:       []string{"kubernetes.rolloutPause", "kubernetes.scaleDeployment"},
			ProtectedNamespaces:      []string{"payments"},
			AutoApproveMaxSeverity:   domain.SeverityLow,
			RequireApprovalAtOrAbove: domain.SeverityMedium,
		}),
		Now: func() time.Time { return now },
	}
	if _, err := planReconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace},
	}); err != nil {
		t.Fatalf("plan reconcile failed: %v", err)
	}

	var action v1alpha1.AgentAction
	if err := fakeClient.Get(context.Background(), types.NamespacedName{
		Name:      "payments-api-risk-plan-action",
		Namespace: "payments",
	}, &action); err != nil {
		t.Fatalf("expected agent action: %v", err)
	}
	if action.Status.Phase != v1alpha1.PhaseWaitingApproval {
		t.Fatalf("expected waiting approval, got %s", action.Status.Phase)
	}
	if action.Status.DryRunResult == nil || action.Status.DryRunResult.Result == "" {
		t.Fatalf("expected controller-owned dryRunResult status, got %#v", action.Status.DryRunResult)
	}
	if action.Status.Approval == nil || action.Status.Approval.Approved || action.Status.Approval.Source != "ManualApprovalRequired" || action.Status.Approval.ActionDigest == "" {
		t.Fatalf("expected manual approval status, got %#v", action.Status.Approval)
	}
	if action.Status.Approval.DecidedAt == nil || !action.Status.Approval.DecidedAt.Time.Equal(now) {
		t.Fatalf("expected policy decision timestamp %v, got %#v", now, action.Status.Approval.DecidedAt)
	}
	if action.Status.Approval.DecidedBy != approvalDecisionActor {
		t.Fatalf("expected policy decision actor %q, got %q", approvalDecisionActor, action.Status.Approval.DecidedBy)
	}

	action.Spec.ApprovedBy = "sre-oncall@example.com"
	if err := fakeClient.Update(context.Background(), &action); err != nil {
		t.Fatalf("failed to simulate approval: %v", err)
	}

	actionReconciler := &AgentActionReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Executor: executor.NewRouter(
			executor.KubernetesExecutor{Now: func() time.Time { return now }},
			executor.GitOpsExecutor{Now: func() time.Time { return now }},
			executor.RunbookExecutor{Now: func() time.Time { return now }},
			executor.NotificationExecutor{Now: func() time.Time { return now }},
		),
		Now: func() time.Time { return now },
	}
	if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: action.Name, Namespace: action.Namespace},
	}); err != nil {
		t.Fatalf("action reconcile failed: %v", err)
	}

	if err := fakeClient.Get(context.Background(), types.NamespacedName{
		Name:      action.Name,
		Namespace: action.Namespace,
	}, &action); err != nil {
		t.Fatalf("failed to fetch action after execution: %v", err)
	}
	if action.Status.Phase != v1alpha1.PhaseSucceeded {
		t.Fatalf("expected succeeded phase, got %s", action.Status.Phase)
	}
	if action.Status.Approval == nil || !action.Status.Approval.Approved || action.Status.Approval.Source != "ManualApprovalConfirmed" || action.Status.Approval.ActionDigest == "" {
		t.Fatalf("expected confirmed manual approval projection, got %#v", action.Status.Approval)
	}
	if action.Status.Approval.ApprovedAt == nil || !action.Status.Approval.ApprovedAt.Time.Equal(now) {
		t.Fatalf("expected manual approval timestamp %v, got %#v", now, action.Status.Approval.ApprovedAt)
	}
	if action.Status.Approval.DecidedAt == nil || !action.Status.Approval.DecidedAt.Time.Equal(now) || action.Status.Approval.DecidedBy != approvalDecisionActor {
		t.Fatalf("expected decision audit fields to survive manual approval, got %#v", action.Status.Approval)
	}
	if action.Status.Execution == nil || action.Status.Execution.Phase != "Succeeded" || action.Status.Execution.Executor == "" {
		t.Fatalf("expected execution status, got %#v", action.Status.Execution)
	}
	if action.Status.Effectiveness == nil || action.Status.Effectiveness.Phase != "NotVerified" {
		t.Fatalf("expected NotVerified effectiveness status, got %#v", action.Status.Effectiveness)
	}
}

func TestApprovalAuditTimelineAndEventsAcrossEscalation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	decisionAt := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	plan := &v1alpha1.RemediationPlan{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api-plan", Namespace: "payments", Generation: 1},
		Spec: v1alpha1.RemediationPlanSpec{
			Target:                 v1alpha1.TargetRef{Namespace: "payments", Kind: "Deployment", Name: "payments-api", APIVersion: "apps/v1"},
			Severity:               "high",
			Confidence:             90,
			DryRun:                 true,
			ApprovalTimeoutSeconds: 60,
			Steps: []v1alpha1.RemediationStep{{
				Name:       "pause-rollout",
				ActionType: "kubernetes.rolloutPause",
			}},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}).
		WithObjects(plan).
		Build()
	eventRecorder := record.NewFakeRecorder(20)
	planReconciler := &RemediationPlanReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Guardrails: guardrails.NewEngine(guardrails.Policy{
			AllowedActionTypes:       []string{"kubernetes.rolloutPause"},
			ProtectedNamespaces:      []string{"payments"},
			AutoApproveMaxSeverity:   domain.SeverityLow,
			RequireApprovalAtOrAbove: domain.SeverityMedium,
		}),
		EventRecorder: eventRecorder,
		Now:           func() time.Time { return decisionAt },
	}

	planKey := types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}
	if _, err := planReconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: planKey}); err != nil {
		t.Fatalf("plan reconcile failed: %v", err)
	}

	actionKey := types.NamespacedName{Name: plan.Name + "-action", Namespace: plan.Namespace}
	var action v1alpha1.AgentAction
	if err := fakeClient.Get(context.Background(), actionKey, &action); err != nil {
		t.Fatalf("expected AgentAction: %v", err)
	}
	if action.Status.Phase != v1alpha1.PhaseWaitingApproval || action.Status.Approval == nil {
		t.Fatalf("expected waiting approval action, got phase=%s approval=%#v", action.Status.Phase, action.Status.Approval)
	}
	if action.Status.Approval.DecidedAt == nil || !action.Status.Approval.DecidedAt.Time.Equal(decisionAt) || action.Status.Approval.DecidedBy != approvalDecisionActor {
		t.Fatalf("expected decision audit at %v by %q, got %#v", decisionAt, approvalDecisionActor, action.Status.Approval)
	}

	current := decisionAt.Add(61 * time.Second)
	spy := &spyNotifier{}
	actionReconciler := &AgentActionReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Executor:      executor.NewRouter(executor.KubernetesExecutor{}, executor.GitOpsExecutor{}, executor.RunbookExecutor{}, executor.NotificationExecutor{}),
		Notifier:      spy,
		EventRecorder: eventRecorder,
		Now:           func() time.Time { return current },
	}
	if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		t.Fatalf("escalation reconcile failed: %v", err)
	}
	if err := fakeClient.Get(context.Background(), actionKey, &action); err != nil {
		t.Fatalf("fetch after escalation: %v", err)
	}
	if action.Status.Phase != v1alpha1.PhaseEscalated || action.Status.Approval.EscalatedAt == nil || !action.Status.Approval.EscalatedAt.Time.Equal(current) {
		t.Fatalf("expected escalated audit at %v, got phase=%s approval=%#v", current, action.Status.Phase, action.Status.Approval)
	}
	if action.Status.Approval.DecidedAt == nil || !action.Status.Approval.DecidedAt.Time.Equal(decisionAt) {
		t.Fatalf("expected decision timestamp to survive escalation, got %#v", action.Status.Approval)
	}

	approvedAt := current.Add(time.Second)
	action.Spec.ApprovedBy = "sre-oncall@example.com"
	if err := fakeClient.Update(context.Background(), &action); err != nil {
		t.Fatalf("simulate human approval: %v", err)
	}
	current = approvedAt
	if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		t.Fatalf("post-escalation approval reconcile failed: %v", err)
	}
	if err := fakeClient.Get(context.Background(), actionKey, &action); err != nil {
		t.Fatalf("fetch after approval: %v", err)
	}
	if action.Status.Phase != v1alpha1.PhaseSucceeded || action.Status.Approval.ApprovedAt == nil || !action.Status.Approval.ApprovedAt.Time.Equal(approvedAt) {
		t.Fatalf("expected approved timeline ending in success at %v, got phase=%s approval=%#v", approvedAt, action.Status.Phase, action.Status.Approval)
	}
	if action.Status.Approval.DecidedAt == nil || action.Status.Approval.EscalatedAt == nil {
		t.Fatalf("expected all audit timestamps to remain available, got %#v", action.Status.Approval)
	}

	events := drainRecorderEvents(eventRecorder)
	for _, expected := range []string{"Normal ApprovalRequired", "Warning EscalationTriggered", "Normal ExecutionSucceeded"} {
		found := false
		for _, event := range events {
			if strings.Contains(event, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected event containing %q, got %v", expected, events)
		}
	}
}

func drainRecorderEvents(recorder *record.FakeRecorder) []string {
	var events []string
	for {
		select {
		case event := <-recorder.Events:
			events = append(events, event)
		default:
			return events
		}
	}
}

// spyExecutor records whether it was ever invoked, so tests can prove an
// AgentAction was never executed rather than only inferring it from status
// fields.
type spyExecutor struct {
	name    string
	invoked bool
}

func (s *spyExecutor) Name() string { return s.name }

func (s *spyExecutor) Execute(_ context.Context, _ executor.ApprovedAction) (domain.ExecutionResult, error) {
	s.invoked = true
	return domain.ExecutionResult{Executor: s.name, Status: "succeeded"}, nil
}

func TestAgentActionReconcilerRejectsDirectlyCreatedActionDespiteApprovedBy(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

	// Simulates an attacker (or anyone with RBAC write access to
	// AgentAction) creating one directly, bypassing RemediationPlan and
	// guardrails.Engine entirely, with spec.approvedBy pre-filled.
	action := &v1alpha1.AgentAction{
		ObjectMeta: metav1.ObjectMeta{Name: "attacker-action", Namespace: "payments"},
		Spec: v1alpha1.AgentActionSpec{
			Target:     v1alpha1.TargetRef{Namespace: "payments", Kind: "Deployment", Name: "payments-api", APIVersion: "apps/v1"},
			ActionType: "kubernetes.deleteDeployment",
			ApprovedBy: "attacker@example.com",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.AgentAction{}).
		WithObjects(action).
		Build()

	spy := &spyExecutor{name: "kubernetes-executor"}
	actionReconciler := &AgentActionReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Executor: executor.NewRouter(spy, spy, spy, spy),
		Now:      func() time.Time { return now },
	}
	if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: action.Name, Namespace: action.Namespace},
	}); err != nil {
		t.Fatalf("action reconcile failed: %v", err)
	}

	if spy.invoked {
		t.Fatal("expected executor to never be invoked for an action that never went through guardrails")
	}

	if err := fakeClient.Get(context.Background(), types.NamespacedName{
		Name:      action.Name,
		Namespace: action.Namespace,
	}, action); err != nil {
		t.Fatalf("failed to fetch action: %v", err)
	}
	if action.Status.Phase != v1alpha1.PhaseWaitingApproval {
		t.Fatalf("expected action to stay waiting for approval, got %s", action.Status.Phase)
	}
	if action.Status.Approval == nil || action.Status.Approval.Approved {
		t.Fatalf("expected action to remain unapproved, got %#v", action.Status.Approval)
	}
}

func TestAgentActionReconcilerRejectsTamperedActionAfterManualApprovalRequired(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	riskSignal := &v1alpha1.RiskSignal{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api-risk", Namespace: "payments", Generation: 1},
		Spec: v1alpha1.RiskSignalSpec{
			Target:     v1alpha1.TargetRef{Cluster: "prod", Namespace: "payments", Kind: "Deployment", Name: "payments-api", APIVersion: "apps/v1", Service: "payments-api"},
			SignalType: "incident",
			ActionType: "kubernetes.rolloutPause",
			Severity:   "high",
			Confidence: 90,
			DryRun:     true,
			TTLSeconds: 1800,
			Evidence:   []v1alpha1.EvidenceRef{{Kind: "event", Summary: "Pod entered OOMKilled"}},
			Parameters: map[string]string{"reason": "auto-generated"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskSignal{}, &v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}).
		WithObjects(riskSignal).
		Build()

	riskReconciler := &RiskSignalReconciler{Client: fakeClient, Scheme: scheme, Enabled: true, Now: func() time.Time { return now }}
	if _, err := riskReconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	}); err != nil {
		t.Fatalf("risk reconcile failed: %v", err)
	}

	var plan v1alpha1.RemediationPlan
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "payments-api-risk-plan", Namespace: "payments"}, &plan); err != nil {
		t.Fatalf("expected remediation plan: %v", err)
	}

	planReconciler := &RemediationPlanReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Guardrails: guardrails.NewEngine(guardrails.Policy{
			AllowedActionTypes:       []string{"kubernetes.rolloutPause", "kubernetes.scaleDeployment"},
			ProtectedNamespaces:      []string{"payments"},
			AutoApproveMaxSeverity:   domain.SeverityLow,
			RequireApprovalAtOrAbove: domain.SeverityMedium,
		}),
		Now: func() time.Time { return now },
	}
	if _, err := planReconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace},
	}); err != nil {
		t.Fatalf("plan reconcile failed: %v", err)
	}

	var action v1alpha1.AgentAction
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "payments-api-risk-plan-action", Namespace: "payments"}, &action); err != nil {
		t.Fatalf("expected agent action: %v", err)
	}
	if action.Status.Approval == nil || action.Status.Approval.Source != "ManualApprovalRequired" {
		t.Fatalf("expected manual approval required, got %#v", action.Status.Approval)
	}

	// A human approves it, but the action content is swapped out from what
	// guardrails actually reviewed -- either a compromised credential or a
	// racing edit. The digest guardrails recorded no longer matches.
	action.Spec.ApprovedBy = "sre-oncall@example.com"
	action.Spec.ActionType = "kubernetes.deleteDeployment"
	if err := fakeClient.Update(context.Background(), &action); err != nil {
		t.Fatalf("failed to simulate tampered approval: %v", err)
	}

	spy := &spyExecutor{name: "kubernetes-executor"}
	actionReconciler := &AgentActionReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Executor: executor.NewRouter(spy, spy, spy, spy),
		Now:      func() time.Time { return now },
	}
	if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: action.Name, Namespace: action.Namespace},
	}); err != nil {
		t.Fatalf("action reconcile failed: %v", err)
	}

	if spy.invoked {
		t.Fatal("expected executor to never be invoked for an action whose content changed after guardrails approved it")
	}

	if err := fakeClient.Get(context.Background(), types.NamespacedName{
		Name:      action.Name,
		Namespace: action.Namespace,
	}, &action); err != nil {
		t.Fatalf("failed to fetch action: %v", err)
	}
	if action.Status.Phase != v1alpha1.PhaseWaitingApproval {
		t.Fatalf("expected tampered action to fall back to waiting for approval, got %s", action.Status.Phase)
	}
}

func TestRiskSignalReconcilerDisabledDoesNotCreateRemediationResources(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	riskSignal := &v1alpha1.RiskSignal{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskSignal",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payments-api-risk",
			Namespace: "payments",
		},
		Spec: v1alpha1.RiskSignalSpec{
			Target: v1alpha1.TargetRef{
				Namespace: "payments",
				Name:      "payments-api",
				Kind:      "Deployment",
			},
			ActionType: "kubernetes.rolloutPause",
			Severity:   "high",
			Confidence: 90,
			DryRun:     true,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskSignal{}, &v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}).
		WithObjects(riskSignal).
		Build()

	reconciler := &RiskSignalReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Enabled: false,
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	}); err != nil {
		t.Fatalf("disabled reconcile failed: %v", err)
	}

	var plans v1alpha1.RemediationPlanList
	if err := fakeClient.List(context.Background(), &plans); err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans.Items) != 0 {
		t.Fatalf("expected no remediation plans, got %d", len(plans.Items))
	}

	var actions v1alpha1.AgentActionList
	if err := fakeClient.List(context.Background(), &actions); err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if len(actions.Items) != 0 {
		t.Fatalf("expected no agent actions, got %d", len(actions.Items))
	}
}

func TestRiskSignalReconcilerSchedulesTTLRequeueWhenDisabled(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 6, 17, 9, 10, 0, 0, time.UTC)
	createdAt := metav1.NewTime(now.Add(-30 * time.Second))
	riskSignal := &v1alpha1.RiskSignal{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskSignal",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "payments-api-risk",
			Namespace:         "payments",
			CreationTimestamp: createdAt,
		},
		Spec: v1alpha1.RiskSignalSpec{
			Target: v1alpha1.TargetRef{
				Namespace: "payments",
				Name:      "payments-api",
				Kind:      "Deployment",
			},
			Severity:   "high",
			Confidence: 90,
			DryRun:     true,
			TTLSeconds: 120,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskSignal{}).
		WithObjects(riskSignal).
		Build()

	reconciler := &RiskSignalReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Enabled: false,
		Now:     func() time.Time { return now },
	}
	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	})
	if err != nil {
		t.Fatalf("disabled reconcile failed: %v", err)
	}
	if result.RequeueAfter != 90*time.Second {
		t.Fatalf("expected ttl requeue after 90s, got %s", result.RequeueAfter)
	}
}

func TestRiskSignalReconcilerDeletesExpiredSignal(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 6, 17, 9, 10, 0, 0, time.UTC)
	createdAt := metav1.NewTime(now.Add(-2 * time.Minute))
	riskSignal := &v1alpha1.RiskSignal{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskSignal",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "payments-api-risk",
			Namespace:         "payments",
			CreationTimestamp: createdAt,
		},
		Spec: v1alpha1.RiskSignalSpec{
			Target: v1alpha1.TargetRef{
				Namespace: "payments",
				Name:      "payments-api",
				Kind:      "Deployment",
			},
			Severity:   "high",
			Confidence: 90,
			DryRun:     true,
			TTLSeconds: 60,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskSignal{}).
		WithObjects(riskSignal).
		Build()

	reconciler := &RiskSignalReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Enabled: false,
		Now:     func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	}); err != nil {
		t.Fatalf("ttl delete reconcile failed: %v", err)
	}

	var stored v1alpha1.RiskSignal
	err := fakeClient.Get(context.Background(), types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace}, &stored)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected expired RiskSignal to be deleted, got err=%v", err)
	}
}

func TestRiskSignalReconcilerCreatesPlanAndPreservesTTLRequeue(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	createdAt := metav1.NewTime(now.Add(-10 * time.Minute))
	riskSignal := &v1alpha1.RiskSignal{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskSignal",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "payments-api-risk",
			Namespace:         "payments",
			Generation:        1,
			CreationTimestamp: createdAt,
		},
		Spec: v1alpha1.RiskSignalSpec{
			Target: v1alpha1.TargetRef{
				Cluster:    "prod",
				Namespace:  "payments",
				Kind:       "Deployment",
				Name:       "payments-api",
				APIVersion: "apps/v1",
				Service:    "payments-api",
			},
			SignalType: "incident",
			ActionType: "kubernetes.rolloutPause",
			Severity:   "high",
			Confidence: 90,
			DryRun:     true,
			TTLSeconds: 1800,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskSignal{}, &v1alpha1.RemediationPlan{}).
		WithObjects(riskSignal).
		Build()

	reconciler := &RiskSignalReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Enabled: true,
		Now:     func() time.Time { return now },
	}
	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	})
	if err != nil {
		t.Fatalf("risk reconcile failed: %v", err)
	}
	if result.RequeueAfter != 20*time.Minute {
		t.Fatalf("expected ttl requeue after 20m, got %s", result.RequeueAfter)
	}

	var plan v1alpha1.RemediationPlan
	if err := fakeClient.Get(context.Background(), types.NamespacedName{
		Name:      "payments-api-risk-plan",
		Namespace: "payments",
	}, &plan); err != nil {
		t.Fatalf("expected remediation plan: %v", err)
	}
}

// spyNotifier records every Notify call so tests can assert exactly how many
// times (and whether) an escalation notification fired, and can be made to
// fail on demand to test retry behavior.
type spyNotifier struct {
	calls    int
	failNext bool
}

func (s *spyNotifier) Name() string { return "spy-notifier" }

func (s *spyNotifier) Notify(_ context.Context, _ notifier.Message) error {
	s.calls++
	if s.failNext {
		s.failNext = false
		return fmt.Errorf("simulated notifier failure")
	}
	return nil
}

func TestRemediationPlanReconcilerDoesNotOverwriteTerminalAgentActionStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	riskSignal := &v1alpha1.RiskSignal{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api-risk", Namespace: "payments", Generation: 1},
		Spec: v1alpha1.RiskSignalSpec{
			Target:     v1alpha1.TargetRef{Cluster: "prod", Namespace: "payments", Kind: "Deployment", Name: "payments-api", APIVersion: "apps/v1", Service: "payments-api"},
			SignalType: "incident",
			ActionType: "kubernetes.rolloutPause",
			Severity:   "high",
			Confidence: 90,
			DryRun:     true,
			TTLSeconds: 1800,
			Evidence:   []v1alpha1.EvidenceRef{{Kind: "event", Summary: "Pod entered OOMKilled"}},
			Parameters: map[string]string{"reason": "auto-generated"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskSignal{}, &v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}).
		WithObjects(riskSignal).
		Build()

	riskReconciler := &RiskSignalReconciler{Client: fakeClient, Scheme: scheme, Enabled: true, Now: func() time.Time { return now }}
	if _, err := riskReconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	}); err != nil {
		t.Fatalf("risk reconcile failed: %v", err)
	}

	var plan v1alpha1.RemediationPlan
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "payments-api-risk-plan", Namespace: "payments"}, &plan); err != nil {
		t.Fatalf("expected remediation plan: %v", err)
	}

	planReconciler := &RemediationPlanReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Guardrails: guardrails.NewEngine(guardrails.Policy{
			AllowedActionTypes:       []string{"kubernetes.rolloutPause", "kubernetes.scaleDeployment"},
			ProtectedNamespaces:      []string{"payments"},
			AutoApproveMaxSeverity:   domain.SeverityLow,
			RequireApprovalAtOrAbove: domain.SeverityMedium,
		}),
		Now: func() time.Time { return now },
	}
	planReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace}}
	if _, err := planReconciler.Reconcile(context.Background(), planReq); err != nil {
		t.Fatalf("first plan reconcile failed: %v", err)
	}

	var action v1alpha1.AgentAction
	actionKey := types.NamespacedName{Name: "payments-api-risk-plan-action", Namespace: "payments"}
	if err := fakeClient.Get(context.Background(), actionKey, &action); err != nil {
		t.Fatalf("expected agent action: %v", err)
	}

	action.Spec.ApprovedBy = "sre-oncall@example.com"
	if err := fakeClient.Update(context.Background(), &action); err != nil {
		t.Fatalf("failed to simulate approval: %v", err)
	}

	actionReconciler := &AgentActionReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Executor: executor.NewRouter(
			executor.KubernetesExecutor{Now: func() time.Time { return now }},
			executor.GitOpsExecutor{Now: func() time.Time { return now }},
			executor.RunbookExecutor{Now: func() time.Time { return now }},
			executor.NotificationExecutor{Now: func() time.Time { return now }},
		),
		Now: func() time.Time { return now },
	}
	if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		t.Fatalf("action reconcile failed: %v", err)
	}

	if err := fakeClient.Get(context.Background(), actionKey, &action); err != nil {
		t.Fatalf("fetch executed action: %v", err)
	}
	if action.Status.Phase != v1alpha1.PhaseSucceeded {
		t.Fatalf("expected succeeded phase before re-reconciling the plan, got %s", action.Status.Phase)
	}

	// This simulates what a live cluster does automatically:
	// RemediationPlanReconciler.Owns(&v1alpha1.AgentAction{}) re-triggers the
	// plan reconcile whenever the owned AgentAction's status changes -- and
	// it just changed, to Succeeded.
	if _, err := planReconciler.Reconcile(context.Background(), planReq); err != nil {
		t.Fatalf("second plan reconcile failed: %v", err)
	}

	if err := fakeClient.Get(context.Background(), actionKey, &action); err != nil {
		t.Fatalf("fetch action after second plan reconcile: %v", err)
	}
	if action.Status.Phase != v1alpha1.PhaseSucceeded {
		t.Fatalf("expected plan reconcile to leave a terminal AgentAction untouched, got %s", action.Status.Phase)
	}
	if action.Status.Approval == nil || !action.Status.Approval.Approved {
		t.Fatalf("expected approval to remain intact after second plan reconcile, got %#v", action.Status.Approval)
	}
	if action.Status.Execution == nil || action.Status.Execution.Phase != "Succeeded" {
		t.Fatalf("expected execution status to remain intact after second plan reconcile, got %#v", action.Status.Execution)
	}
}

func TestAgentActionReconcilerAllowsApprovalAfterRepeatedPendingReconciles(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	riskSignal := &v1alpha1.RiskSignal{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api-risk", Namespace: "payments", Generation: 1},
		Spec: v1alpha1.RiskSignalSpec{
			Target:     v1alpha1.TargetRef{Cluster: "prod", Namespace: "payments", Kind: "Deployment", Name: "payments-api", APIVersion: "apps/v1", Service: "payments-api"},
			SignalType: "incident",
			ActionType: "kubernetes.rolloutPause",
			Severity:   "high",
			Confidence: 90,
			DryRun:     true,
			TTLSeconds: 1800,
			Evidence:   []v1alpha1.EvidenceRef{{Kind: "event", Summary: "Pod entered OOMKilled"}},
			Parameters: map[string]string{"reason": "auto-generated"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskSignal{}, &v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}).
		WithObjects(riskSignal).
		Build()

	riskReconciler := &RiskSignalReconciler{Client: fakeClient, Scheme: scheme, Enabled: true, Now: func() time.Time { return now }}
	if _, err := riskReconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	}); err != nil {
		t.Fatalf("risk reconcile failed: %v", err)
	}

	var plan v1alpha1.RemediationPlan
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "payments-api-risk-plan", Namespace: "payments"}, &plan); err != nil {
		t.Fatalf("expected remediation plan: %v", err)
	}

	planReconciler := &RemediationPlanReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Guardrails: guardrails.NewEngine(guardrails.Policy{
			AllowedActionTypes:       []string{"kubernetes.rolloutPause", "kubernetes.scaleDeployment"},
			ProtectedNamespaces:      []string{"payments"},
			AutoApproveMaxSeverity:   domain.SeverityLow,
			RequireApprovalAtOrAbove: domain.SeverityMedium,
		}),
		Now: func() time.Time { return now },
	}
	if _, err := planReconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: plan.Name, Namespace: plan.Namespace},
	}); err != nil {
		t.Fatalf("plan reconcile failed: %v", err)
	}

	actionKey := types.NamespacedName{Name: "payments-api-risk-plan-action", Namespace: "payments"}
	actionReconciler := &AgentActionReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Executor: executor.NewRouter(
			executor.KubernetesExecutor{Now: func() time.Time { return now }},
			executor.GitOpsExecutor{Now: func() time.Time { return now }},
			executor.RunbookExecutor{Now: func() time.Time { return now }},
			executor.NotificationExecutor{Now: func() time.Time { return now }},
		),
		Now: func() time.Time { return now },
	}

	// Simulate several resyncs/requeues landing before any human acts. None
	// of them may strip the action's ability to be approved later.
	for i := 0; i < 3; i++ {
		if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
			t.Fatalf("pending reconcile #%d failed: %v", i, err)
		}
		var pending v1alpha1.AgentAction
		if err := fakeClient.Get(context.Background(), actionKey, &pending); err != nil {
			t.Fatalf("fetch pending action #%d: %v", i, err)
		}
		if pending.Status.Phase != v1alpha1.PhaseWaitingApproval {
			t.Fatalf("expected WaitingApproval on pass #%d, got %s", i, pending.Status.Phase)
		}
		if pending.Status.Approval == nil || pending.Status.Approval.Source != "ManualApprovalRequired" {
			t.Fatalf("expected source to stay ManualApprovalRequired on pass #%d, got %#v", i, pending.Status.Approval)
		}
	}

	var action v1alpha1.AgentAction
	if err := fakeClient.Get(context.Background(), actionKey, &action); err != nil {
		t.Fatalf("fetch action before approval: %v", err)
	}
	action.Spec.ApprovedBy = "sre-oncall@example.com"
	if err := fakeClient.Update(context.Background(), &action); err != nil {
		t.Fatalf("failed to simulate approval: %v", err)
	}

	if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		t.Fatalf("approved reconcile failed: %v", err)
	}
	if err := fakeClient.Get(context.Background(), actionKey, &action); err != nil {
		t.Fatalf("fetch approved action: %v", err)
	}
	if action.Status.Phase != v1alpha1.PhaseSucceeded {
		t.Fatalf("expected approval after repeated pending reconciles to still execute, got %s", action.Status.Phase)
	}
}

func TestAgentActionReconcilerEscalatesAfterApprovalTimeout(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	waitStart := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	action := &v1alpha1.AgentAction{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api-action", Namespace: "payments"},
		Spec: v1alpha1.AgentActionSpec{
			Target:                 v1alpha1.TargetRef{Namespace: "payments", Kind: "Deployment", Name: "payments-api", APIVersion: "apps/v1"},
			ActionType:             "kubernetes.rolloutPause",
			ApprovalTimeoutSeconds: 300,
		},
	}
	setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseWaitingApproval, "waiting for human approval", action.Generation, waitStart)
	action.Status.Approval = &v1alpha1.AgentActionApprovalStatus{
		Approved:           false,
		Source:             "ManualApprovalRequired",
		ActionDigest:       agentActionSpecDigest(action),
		ApprovedGeneration: action.Generation,
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.AgentAction{}).
		WithObjects(action).
		Build()

	actionKey := types.NamespacedName{Name: action.Name, Namespace: action.Namespace}
	spy := &spyNotifier{}
	current := waitStart.Add(100 * time.Second)
	reconciler := &AgentActionReconciler{
		Client: fakeClient,
		Scheme: scheme,
		Executor: executor.NewRouter(
			executor.KubernetesExecutor{Now: func() time.Time { return current }},
			executor.GitOpsExecutor{Now: func() time.Time { return current }},
			executor.RunbookExecutor{Now: func() time.Time { return current }},
			executor.NotificationExecutor{Now: func() time.Time { return current }},
		),
		Notifier: spy,
		Now:      func() time.Time { return current },
	}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey})
	if err != nil {
		t.Fatalf("reconcile before deadline failed: %v", err)
	}
	if result.RequeueAfter <= 0 {
		t.Fatalf("expected a RequeueAfter before the approval deadline, got %#v", result)
	}
	if spy.calls != 0 {
		t.Fatalf("expected no notification before the deadline, got %d calls", spy.calls)
	}

	var fetched v1alpha1.AgentAction
	if err := fakeClient.Get(context.Background(), actionKey, &fetched); err != nil {
		t.Fatalf("fetch before deadline: %v", err)
	}
	if fetched.Status.Phase != v1alpha1.PhaseWaitingApproval {
		t.Fatalf("expected phase to remain WaitingApproval before the timeout, got %s", fetched.Status.Phase)
	}

	current = waitStart.Add(301 * time.Second)
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		t.Fatalf("reconcile at deadline failed: %v", err)
	}

	if err := fakeClient.Get(context.Background(), actionKey, &fetched); err != nil {
		t.Fatalf("fetch after escalation: %v", err)
	}
	if fetched.Status.Phase != v1alpha1.PhaseEscalated {
		t.Fatalf("expected Escalated phase, got %s", fetched.Status.Phase)
	}
	if fetched.Status.Approval == nil || fetched.Status.Approval.EscalatedAt == nil {
		t.Fatalf("expected escalatedAt to be recorded, got %#v", fetched.Status.Approval)
	}
	if fetched.Status.Approval.Source != "ManualApprovalRequired" {
		t.Fatalf("expected source to remain ManualApprovalRequired through escalation, got %s", fetched.Status.Approval.Source)
	}
	if spy.calls != 1 {
		t.Fatalf("expected exactly one escalation notification, got %d", spy.calls)
	}

	// Reconciling again after escalation must stay idempotent.
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		t.Fatalf("reconcile after escalation failed: %v", err)
	}
	if spy.calls != 1 {
		t.Fatalf("expected escalation notification to stay idempotent, got %d calls", spy.calls)
	}

	// A human can still approve after escalation.
	if err := fakeClient.Get(context.Background(), actionKey, &fetched); err != nil {
		t.Fatalf("fetch before post-escalation approval: %v", err)
	}
	fetched.Spec.ApprovedBy = "sre-oncall@example.com"
	if err := fakeClient.Update(context.Background(), &fetched); err != nil {
		t.Fatalf("approve after escalation: %v", err)
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		t.Fatalf("reconcile after post-escalation approval failed: %v", err)
	}
	if err := fakeClient.Get(context.Background(), actionKey, &fetched); err != nil {
		t.Fatalf("fetch after post-escalation approval: %v", err)
	}
	if fetched.Status.Phase != v1alpha1.PhaseSucceeded {
		t.Fatalf("expected approval after escalation to still execute, got %s", fetched.Status.Phase)
	}
}

func TestAgentActionReconcilerRetriesEscalationNotificationAfterFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	waitStart := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	action := &v1alpha1.AgentAction{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api-action", Namespace: "payments"},
		Spec: v1alpha1.AgentActionSpec{
			Target:                 v1alpha1.TargetRef{Namespace: "payments", Kind: "Deployment", Name: "payments-api", APIVersion: "apps/v1"},
			ActionType:             "kubernetes.rolloutPause",
			ApprovalTimeoutSeconds: 300,
		},
	}
	setResourceStatus(&action.Status.ResourceStatus, v1alpha1.PhaseWaitingApproval, "waiting for human approval", action.Generation, waitStart)
	action.Status.Approval = &v1alpha1.AgentActionApprovalStatus{
		Approved:           false,
		Source:             "ManualApprovalRequired",
		ActionDigest:       agentActionSpecDigest(action),
		ApprovedGeneration: action.Generation,
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.AgentAction{}).
		WithObjects(action).
		Build()

	actionKey := types.NamespacedName{Name: action.Name, Namespace: action.Namespace}
	spy := &spyNotifier{failNext: true}
	eventRecorder := record.NewFakeRecorder(10)
	current := waitStart.Add(301 * time.Second)
	reconciler := &AgentActionReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		Executor:      executor.NewRouter(executor.KubernetesExecutor{}, executor.GitOpsExecutor{}, executor.RunbookExecutor{}, executor.NotificationExecutor{}),
		Notifier:      spy,
		EventRecorder: eventRecorder,
		Now:           func() time.Time { return current },
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err == nil {
		t.Fatal("expected reconcile to surface the notifier failure so it gets retried")
	}
	if spy.calls != 1 {
		t.Fatalf("expected one failed notification attempt, got %d", spy.calls)
	}

	var fetched v1alpha1.AgentAction
	if err := fakeClient.Get(context.Background(), actionKey, &fetched); err != nil {
		t.Fatalf("fetch after failed notification: %v", err)
	}
	if fetched.Status.Phase != v1alpha1.PhaseWaitingApproval {
		t.Fatalf("expected phase to stay WaitingApproval after a failed notification, got %s", fetched.Status.Phase)
	}
	if fetched.Status.Approval.EscalatedAt != nil {
		t.Fatalf("expected escalatedAt to stay unset after a failed notification, got %v", fetched.Status.Approval.EscalatedAt)
	}
	if fetched.Status.Notification == nil || fetched.Status.Notification.RetryCount != 1 || fetched.Status.Notification.LastError == "" || fetched.Status.Notification.LastAttemptAt == nil {
		t.Fatalf("expected persisted first notification retry status, got %#v", fetched.Status.Notification)
	}
	select {
	case event := <-eventRecorder.Events:
		if !strings.Contains(event, "Warning NotificationRetryFailed") || !strings.Contains(event, "attempt 1") {
			t.Fatalf("expected NotificationRetryFailed event for attempt 1, got %q", event)
		}
	default:
		t.Fatal("expected NotificationRetryFailed event after failed notification")
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		t.Fatalf("expected retry to succeed once the notifier stops failing: %v", err)
	}
	if spy.calls != 2 {
		t.Fatalf("expected the retry to call the notifier again, got %d calls", spy.calls)
	}
	if err := fakeClient.Get(context.Background(), actionKey, &fetched); err != nil {
		t.Fatalf("fetch after retried notification: %v", err)
	}
	if fetched.Status.Phase != v1alpha1.PhaseEscalated {
		t.Fatalf("expected Escalated phase after the retry succeeds, got %s", fetched.Status.Phase)
	}
	if fetched.Status.Notification == nil || fetched.Status.Notification.RetryCount != 2 || fetched.Status.Notification.LastError != "" {
		t.Fatalf("expected successful retry to record two attempts and clear lastError, got %#v", fetched.Status.Notification)
	}
}
