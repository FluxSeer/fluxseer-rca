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
)

func TestInvestigationRequestReconcilerMarksPendingSkeletonState(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	now := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)

	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "investigate-open-api", Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Namespace:  "prod",
				Kind:       "Deployment",
				Name:       "open-api",
				APIVersion: "apps/v1",
			},
			ModelProviderRef: v1alpha1.LocalObjectReference{Name: "heuristic-provider"},
			Mode:             v1alpha1.InvestigationModeReadOnly,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}).
		WithObjects(request).
		Build()

	reconciler := &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Now:    func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhasePending {
		t.Fatalf("expected pending phase, got %s", stored.Status.Phase)
	}
	if stored.Status.StartedAt == nil || !stored.Status.StartedAt.Equal(&metav1.Time{Time: now}) {
		t.Fatalf("expected startedAt %s, got %#v", now.Format(time.RFC3339), stored.Status.StartedAt)
	}
	if cond := findCondition(stored.Status.Conditions, conditionTargetResolved); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected TargetResolved true condition, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionEvidenceReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "InvestigationNotImplemented" {
		t.Fatalf("expected EvidenceCollectionReady false InvestigationNotImplemented, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionRCAReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "InvestigationNotImplemented" {
		t.Fatalf("expected RCAReady false InvestigationNotImplemented, got %#v", cond)
	}
}

func TestInvestigationRequestReconcilerRejectsInvalidTarget(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	now := time.Date(2026, 7, 6, 10, 5, 0, 0, time.UTC)

	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "invalid-request", Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Kind: "Deployment",
			},
			Mode: v1alpha1.InvestigationModeReadOnly,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}).
		WithObjects(request).
		Build()

	reconciler := &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Now:    func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.InvestigationRequest
	if err := client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
		t.Fatalf("get request: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseFailed {
		t.Fatalf("expected failed phase, got %s", stored.Status.Phase)
	}
	if cond := findCondition(stored.Status.Conditions, conditionReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "TargetInvalid" {
		t.Fatalf("expected Ready false TargetInvalid, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionTargetResolved); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "TargetInvalid" {
		t.Fatalf("expected TargetResolved false TargetInvalid, got %#v", cond)
	}
}
