package controllers

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxseer/api/v1alpha1"
	"fluxseer/internal/datasource"
	"fluxseer/internal/domain"
)

func TestDataSourceReconcilerRegistersValidatedSource(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add aiops scheme: %v", err)
	}

	resource := &v1alpha1.DataSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prometheus",
			Namespace: "fluxagent-system",
		},
		Spec: v1alpha1.DataSourceSpec{
			Type:     "prometheus",
			Endpoint: "http://prometheus.example",
		},
	}

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.DataSource{}).
		WithObjects(resource).
		Build()
	registry := datasource.NewRegistry()

	reconciler := &DataSourceReconciler{
		Client:    kubeClient,
		APIReader: kubeClient,
		Registry:  registry,
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(resource)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if _, ok := registry.Get("prometheus"); !ok {
		t.Fatalf("expected prometheus datasource to be registered")
	}

	var stored v1alpha1.DataSource
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(resource), &stored); err != nil {
		t.Fatalf("get datasource: %v", err)
	}
	if cond := findCondition(stored.Status.Conditions, conditionReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected Ready true condition, got %#v", cond)
	}
}

func TestDataSourceReconcilerUnregistersInvalidSource(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add aiops scheme: %v", err)
	}

	resource := &v1alpha1.DataSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prometheus",
			Namespace: "fluxagent-system",
		},
		Spec: v1alpha1.DataSourceSpec{
			Type: "prometheus",
		},
	}

	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.DataSource{}).
		WithObjects(resource).
		Build()
	registry := datasource.NewRegistry(fakeDataSource{
		name: "prometheus",
		result: &datasource.QueryResult{
			Source:    "prometheus",
			QueryType: domain.QueryTypeMetric,
		},
	})

	reconciler := &DataSourceReconciler{
		Client:    kubeClient,
		APIReader: kubeClient,
		Registry:  registry,
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(resource)}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if _, ok := registry.Get("prometheus"); ok {
		t.Fatalf("expected invalid datasource to be removed from registry")
	}

	var stored v1alpha1.DataSource
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(resource), &stored); err != nil {
		t.Fatalf("get datasource: %v", err)
	}
	if cond := findCondition(stored.Status.Conditions, conditionReady); cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("expected Ready false condition, got %#v", cond)
	}
}
