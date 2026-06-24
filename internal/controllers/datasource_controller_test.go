package controllers

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
)

func TestDataSourceReconcilerMarksDatasourceReady(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	now := time.Date(2026, 6, 24, 16, 0, 0, 0, time.UTC)

	dataSource := &v1alpha1.DataSource{
		ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "fluxagent-system"},
		Spec: v1alpha1.DataSourceSpec{
			Type:     "prometheus",
			Endpoint: "http://prometheus.example",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.DataSource{}).
		WithObjects(dataSource).
		Build()

	reconciler := &DataSourceReconciler{
		Client:    client,
		APIReader: client,
		Now:       func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: dataSource.Name, Namespace: dataSource.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.DataSource
	if err := client.Get(context.Background(), types.NamespacedName{Name: dataSource.Name, Namespace: dataSource.Namespace}, &stored); err != nil {
		t.Fatalf("get datasource: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseObserved {
		t.Fatalf("expected observed phase, got %s", stored.Status.Phase)
	}
	if cond := findCondition(stored.Status.Conditions, conditionReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected Ready true condition, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionUnsupported); cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("expected Unsupported false condition, got %#v", cond)
	}
}

func TestDataSourceReconcilerMarksUnsupportedDatasource(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	now := time.Date(2026, 6, 24, 16, 5, 0, 0, time.UTC)

	dataSource := &v1alpha1.DataSource{
		ObjectMeta: metav1.ObjectMeta{Name: "unknown", Namespace: "fluxagent-system"},
		Spec: v1alpha1.DataSourceSpec{
			Type: "madeUpBackend",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.DataSource{}).
		WithObjects(dataSource).
		Build()

	reconciler := &DataSourceReconciler{
		Client:    client,
		APIReader: client,
		Now:       func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: dataSource.Name, Namespace: dataSource.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var stored v1alpha1.DataSource
	if err := client.Get(context.Background(), types.NamespacedName{Name: dataSource.Name, Namespace: dataSource.Namespace}, &stored); err != nil {
		t.Fatalf("get datasource: %v", err)
	}
	if stored.Status.Phase != v1alpha1.PhaseFailed {
		t.Fatalf("expected failed phase, got %s", stored.Status.Phase)
	}
	if cond := findCondition(stored.Status.Conditions, conditionReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "AdapterNotRegistered" {
		t.Fatalf("expected Ready false AdapterNotRegistered, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionUnsupported); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "AdapterNotRegistered" {
		t.Fatalf("expected Unsupported true AdapterNotRegistered, got %#v", cond)
	}
}
