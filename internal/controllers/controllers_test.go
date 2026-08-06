package controllers

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxseer/api/v1alpha1"
	"fluxseer/internal/domain"
	"fluxseer/internal/executor"
	"fluxseer/internal/guardrails"
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
	if action.Status.Approval == nil || !action.Status.Approval.Approved || action.Status.Approval.Source != "LegacySpecApprovedBy" || action.Status.Approval.ActionDigest == "" {
		t.Fatalf("expected legacy spec approval projection, got %#v", action.Status.Approval)
	}
	if action.Status.Execution == nil || action.Status.Execution.Phase != "Succeeded" || action.Status.Execution.Executor == "" {
		t.Fatalf("expected execution status, got %#v", action.Status.Execution)
	}
	if action.Status.Effectiveness == nil || action.Status.Effectiveness.Phase != "NotVerified" {
		t.Fatalf("expected NotVerified effectiveness status, got %#v", action.Status.Effectiveness)
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
