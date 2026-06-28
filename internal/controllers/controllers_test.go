package controllers

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/domain"
	"fluxagent/internal/executor"
	"fluxagent/internal/guardrails"
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
