package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/guardrails"
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
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}).
		WithObjects(plan, policy, namespace).
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
}
