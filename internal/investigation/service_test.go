package investigation

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
	"fluxagent/internal/modelgateway"
)

func TestServicePreflightResolvesTargetDatasourcesAndProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add aiops scheme: %v", err)
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&appsv1.Deployment{
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
			},
			&v1alpha1.ModelProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "heuristic-provider", Namespace: "fluxagent-system"},
				Spec: v1alpha1.ModelProviderSpec{
					Provider: "heuristic",
					Model:    "built-in",
				},
			},
		).
		Build()

	service := &Service{
		Client: client,
		Registry: datasource.NewRegistry(
			fakeDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent},
			fakeDataSource{name: "prometheus", queryType: domain.QueryTypeMetric},
		),
		Resolver: modelgateway.KubeResolver{Client: client},
	}

	result, err := service.Preflight(context.Background(), "prod", v1alpha1.InvestigationRequestSpec{
		Target: v1alpha1.TargetRef{
			Namespace: "prod",
			Kind:      "Deployment",
			Name:      "open-api",
		},
		DataSources: []v1alpha1.LocalObjectReference{
			{Name: "kubernetes-events"},
			{Name: "prometheus"},
		},
		ModelProviderRef: v1alpha1.LocalObjectReference{Name: "heuristic-provider"},
	})
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if !result.Successful() {
		t.Fatalf("expected successful preflight, got %#v", result)
	}
	if result.Target.Name != "open-api" || result.Target.Namespace != "prod" {
		t.Fatalf("unexpected target %#v", result.Target)
	}
	if result.Provider == nil || result.Provider.Name != "heuristic-provider" {
		t.Fatalf("unexpected provider %#v", result.Provider)
	}
	if len(result.DatasourceNames) != 2 {
		t.Fatalf("expected two datasource names, got %#v", result.DatasourceNames)
	}
}

func TestServicePreflightReportsMissingDatasource(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add aiops scheme: %v", err)
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "open-api", Namespace: "prod"},
			},
		).
		Build()

	service := &Service{
		Client: client,
		Registry: datasource.NewRegistry(
			fakeDataSource{name: "kubernetes-events", queryType: domain.QueryTypeEvent},
		),
		Resolver: modelgateway.KubeResolver{Client: client},
	}

	result, err := service.Preflight(context.Background(), "prod", v1alpha1.InvestigationRequestSpec{
		Target: v1alpha1.TargetRef{
			Namespace: "prod",
			Kind:      "Deployment",
			Name:      "open-api",
		},
		DataSources: []v1alpha1.LocalObjectReference{
			{Name: "missing-ds"},
		},
	})
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if result.DatasourceIssue == nil || result.DatasourceIssue.Reason != "DataSourceNotFound" {
		t.Fatalf("expected DataSourceNotFound, got %#v", result.DatasourceIssue)
	}
}

func TestServiceCollectEvidenceNormalizesResults(t *testing.T) {
	service := &Service{
		Registry: datasource.NewRegistry(
			fakeDataSource{
				name:      "prometheus",
				queryType: domain.QueryTypeMetric,
				records: []map[string]any{
					{"metric": "http_requests_total", "value": "0.95"},
				},
			},
			fakeDataSource{
				name:      "kubernetes-events",
				queryType: domain.QueryTypeEvent,
				records: []map[string]any{
					{"reason": "BackOff", "message": "container crashed"},
				},
			},
		),
	}

	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{
		TimeRange: v1alpha1.InvestigationTimeRange{Lookback: metav1.Duration{Duration: 15 * time.Minute}},
	}, PreflightResult{
		Target: domain.ResourceRef{
			Namespace: "prod",
			Name:      "open-api",
			Kind:      "Deployment",
			Service:   "open-api",
		},
		Labels: map[string]string{"app": "open-api"},
		DatasourceNames: []string{
			"prometheus",
			"kubernetes-events",
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if result.Issue != nil {
		t.Fatalf("expected no issue, got %#v", result.Issue)
	}
	if len(result.EvidenceRefs) != 2 {
		t.Fatalf("expected two evidence refs, got %#v", result.EvidenceRefs)
	}
	if result.EvidenceRefs[0].Kind != "metric" {
		t.Fatalf("expected first evidence kind metric, got %#v", result.EvidenceRefs[0])
	}
	if result.EvidenceRefs[1].Kind != "event" || result.EvidenceRefs[1].Reason != "BackOff" {
		t.Fatalf("expected event evidence with reason BackOff, got %#v", result.EvidenceRefs[1])
	}
	if result.Summary != "collected 2 evidence records from 2 datasources" {
		t.Fatalf("unexpected summary %q", result.Summary)
	}
}

type fakeDataSource struct {
	name      string
	queryType domain.QueryType
	records   []map[string]any
	queryErr  error
}

func (f fakeDataSource) Name() string { return f.name }

func (f fakeDataSource) Type() string { return string(f.queryType) }

func (f fakeDataSource) Capabilities() datasource.Capabilities {
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

func (f fakeDataSource) Query(context.Context, datasource.QueryRequest) (*datasource.QueryResult, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return &datasource.QueryResult{Source: f.name, QueryType: f.queryType, Records: f.records, Summary: fmt.Sprintf("%s returned %d records", f.name, len(f.records))}, nil
}

func (f fakeDataSource) HealthCheck(context.Context) error { return nil }
