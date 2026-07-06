package controllers

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
	"fluxagent/internal/investigation"
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model"
	"fluxagent/internal/model/heuristic"
	"fluxagent/internal/modelgateway"
)

func TestInvestigationRequestReconcilerCompletesWithRCA(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
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
			DataSources: []v1alpha1.LocalObjectReference{
				{Name: "kubernetes-events"},
			},
			ModelProviderRef: v1alpha1.LocalObjectReference{Name: "heuristic-provider"},
			Mode:             v1alpha1.InvestigationModeReadOnly,
		},
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "open-api",
			Namespace: "prod",
			Labels:    map[string]string{"app": "open-api"},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "open-api"},
				},
			},
		},
	}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "heuristic-provider", Namespace: "fluxagent-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "heuristic",
			Model:    "built-in",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}).
		WithObjects(request, deployment, provider).
		Build()

	reconciler := &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Service: &investigation.Service{
			Client: client,
			Registry: datasource.NewRegistry(
				fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent},
			),
			Resolver: modelgateway.KubeResolver{Client: client},
			Gateway: &modelgateway.Gateway{
				Base: knowledge.NewBase(),
				Providers: model.NewRegistry(
					heuristic.Provider{},
				),
			},
		},
		Now: func() time.Time { return now },
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
	if stored.Status.Phase != v1alpha1.PhaseCompleted {
		t.Fatalf("expected completed phase, got %s", stored.Status.Phase)
	}
	if stored.Status.StartedAt == nil || !stored.Status.StartedAt.Equal(&metav1.Time{Time: now}) {
		t.Fatalf("expected startedAt %s, got %#v", now.Format(time.RFC3339), stored.Status.StartedAt)
	}
	if stored.Status.CompletedAt == nil || !stored.Status.CompletedAt.Equal(&metav1.Time{Time: now}) {
		t.Fatalf("expected completedAt %s, got %#v", now.Format(time.RFC3339), stored.Status.CompletedAt)
	}
	if stored.Status.Provider != "heuristic" {
		t.Fatalf("expected provider heuristic, got %q", stored.Status.Provider)
	}
	if stored.Status.Summary == "" || stored.Status.Hypothesis == "" {
		t.Fatalf("expected RCA summary and hypothesis, got summary=%q hypothesis=%q", stored.Status.Summary, stored.Status.Hypothesis)
	}
	if stored.Status.Confidence <= 0 {
		t.Fatalf("expected positive confidence, got %f", stored.Status.Confidence)
	}
	if len(stored.Status.EvidenceRefs) != 1 || stored.Status.EvidenceRefs[0].Kind != "event" {
		t.Fatalf("expected one event evidence ref, got %#v", stored.Status.EvidenceRefs)
	}
	if cond := findCondition(stored.Status.Conditions, conditionTargetResolved); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected TargetResolved true condition, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionDatasourceResolved); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected DatasourceResolved true condition, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionReady); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "InvestigationCompleted" {
		t.Fatalf("expected Ready true InvestigationCompleted, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionEvidenceReady); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "EvidenceCollected" {
		t.Fatalf("expected EvidenceCollectionReady true EvidenceCollected, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionRCAReady); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "ProviderSucceeded" {
		t.Fatalf("expected RCAReady true ProviderSucceeded, got %#v", cond)
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
			DataSources: []v1alpha1.LocalObjectReference{
				{Name: "kubernetes-events"},
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
		Service: &investigation.Service{
			Client:   client,
			Registry: datasource.NewRegistry(fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent}),
			Gateway: &modelgateway.Gateway{
				Base: knowledge.NewBase(),
				Providers: model.NewRegistry(
					heuristic.Provider{},
				),
			},
		},
		Now: func() time.Time { return now },
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

func TestInvestigationRequestReconcilerMarksDatasourceResolutionFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}
	now := time.Date(2026, 7, 6, 10, 10, 0, 0, time.UTC)

	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "missing-ds", Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Namespace: "prod",
				Kind:      "Deployment",
				Name:      "open-api",
			},
			DataSources: []v1alpha1.LocalObjectReference{
				{Name: "prometheus"},
			},
			Mode: v1alpha1.InvestigationModeReadOnly,
		},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod"},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}).
		WithObjects(request, deployment).
		Build()

	reconciler := &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Service: &investigation.Service{
			Client:   client,
			Registry: datasource.NewRegistry(fakeInvestigationDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent}),
			Resolver: modelgateway.KubeResolver{Client: client},
			Gateway: &modelgateway.Gateway{
				Base: knowledge.NewBase(),
				Providers: model.NewRegistry(
					heuristic.Provider{},
				),
			},
		},
		Now: func() time.Time { return now },
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
	if cond := findCondition(stored.Status.Conditions, conditionDatasourceResolved); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "DataSourceNotFound" {
		t.Fatalf("expected DatasourceResolved false DataSourceNotFound, got %#v", cond)
	}
	if cond := findCondition(stored.Status.Conditions, conditionDegraded); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "DataSourceNotFound" {
		t.Fatalf("expected Degraded true DataSourceNotFound, got %#v", cond)
	}
}

type fakeInvestigationDataSource struct {
	name      string
	queryType domain.QueryType
}

func (f fakeInvestigationDataSource) Name() string { return f.name }
func (f fakeInvestigationDataSource) Type() string { return string(f.queryType) }
func (f fakeInvestigationDataSource) Capabilities() datasource.Capabilities {
	switch f.queryType {
	case domain.QueryTypeMetric:
		return datasource.Capabilities{Metrics: true}
	case domain.QueryTypeLog:
		return datasource.Capabilities{Logs: true}
	case domain.QueryTypeEvent:
		return datasource.Capabilities{Events: true}
	default:
		return datasource.Capabilities{}
	}
}
func (f fakeInvestigationDataSource) Query(context.Context, datasource.QueryRequest) (*datasource.QueryResult, error) {
	records := []map[string]any{}
	switch f.queryType {
	case domain.QueryTypeEvent:
		records = []map[string]any{
			{"reason": "BackOff", "message": "container crashed"},
		}
	case domain.QueryTypeMetric:
		records = []map[string]any{
			{"metric": "http_requests_total", "value": "0.95"},
		}
	case domain.QueryTypeLog:
		records = []map[string]any{
			{"line": "error timeout"},
		}
	}
	return &datasource.QueryResult{Source: f.name, QueryType: f.queryType, Records: records}, nil
}
func (f fakeInvestigationDataSource) HealthCheck(context.Context) error { return nil }
