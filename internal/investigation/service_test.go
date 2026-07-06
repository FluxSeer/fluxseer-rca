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
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model"
	"fluxagent/internal/model/heuristic"
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
	if len(result.CollectionPlan) != 2 {
		t.Fatalf("expected two collection steps, got %#v", result.CollectionPlan)
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
		CollectionPlan: []CollectionStep{
			{
				Name:           "prometheus",
				DatasourceName: "prometheus",
				QueryType:      domain.QueryTypeMetric,
				Query:          "metric-query",
			},
			{
				Name:           "kubernetes-events",
				DatasourceName: "kubernetes-events",
				QueryType:      domain.QueryTypeEvent,
				Query:          "recent-events",
			},
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
	if result.Summary != "collected 2 evidence records from 2 investigation queries" {
		t.Fatalf("unexpected summary %q", result.Summary)
	}
}

func TestServiceCollectEvidenceUsesConfiguredQueryPlan(t *testing.T) {
	prom := &capturingDataSource{
		name:      "prometheus",
		queryType: domain.QueryTypeMetric,
		records: []map[string]any{
			{"metric": "custom_metric", "value": "3.14"},
		},
	}
	service := &Service{
		Registry: datasource.NewRegistry(prom),
	}

	result, err := service.CollectEvidence(context.Background(), v1alpha1.InvestigationRequestSpec{}, PreflightResult{
		Target: domain.ResourceRef{
			Namespace: "prod",
			Name:      "open-api",
			Kind:      "Deployment",
			Service:   "open-api",
		},
		Labels: map[string]string{"app": "open-api"},
		CollectionPlan: []CollectionStep{
			{
				Name:           "custom-metric",
				DatasourceName: "prometheus",
				QueryType:      domain.QueryTypeMetric,
				Query:          `sum(rate(custom_metric_total{namespace="prod"}[2m]))`,
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("collect evidence failed: %v", err)
	}
	if prom.lastQuery == nil || prom.lastQuery.Query != `sum(rate(custom_metric_total{namespace="prod"}[2m]))` {
		t.Fatalf("expected configured query to be used, got %#v", prom.lastQuery)
	}
	if len(result.EvidenceRefs) != 1 || result.EvidenceRefs[0].Query != `sum(rate(custom_metric_total{namespace="prod"}[2m]))` {
		t.Fatalf("expected evidence to carry configured query, got %#v", result.EvidenceRefs)
	}
}

func TestServicePreflightReportsCapabilityMismatchForConfiguredQueries(t *testing.T) {
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
	}

	result, err := service.Preflight(context.Background(), "prod", v1alpha1.InvestigationRequestSpec{
		Target: v1alpha1.TargetRef{
			Namespace: "prod",
			Kind:      "Deployment",
			Name:      "open-api",
		},
		Queries: []v1alpha1.InvestigationQuery{
			{
				Name: "bad-query",
				DatasourceRef: v1alpha1.LocalObjectReference{
					Name: "kubernetes-events",
				},
				QueryType: "metric",
				Query:     "up",
			},
		},
	})
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if result.QueryTypeIssue == nil || result.QueryTypeIssue.Reason != "CapabilityMismatch" {
		t.Fatalf("expected CapabilityMismatch, got %#v", result.QueryTypeIssue)
	}
}

func TestServiceGenerateRCAReturnsReasoningOutput(t *testing.T) {
	service := &Service{
		Gateway: &modelgateway.Gateway{
			Base: knowledge.NewBase(),
			Providers: model.NewRegistry(
				heuristic.Provider{},
			),
		},
	}

	reasoning, err := service.GenerateRCA(context.Background(), v1alpha1.InvestigationRequestSpec{
		Question: "Why is open-api crashing after rollout?",
	}, PreflightResult{
		Target: domain.ResourceRef{
			Namespace: "prod",
			Name:      "open-api",
			Kind:      "Deployment",
			Service:   "open-api",
		},
		Provider: &v1alpha1.ModelProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "heuristic-provider", Namespace: "fluxagent-system"},
			Spec: v1alpha1.ModelProviderSpec{
				Provider: "heuristic",
				Model:    "built-in",
			},
		},
	}, EvidenceCollectionResult{
		Summary: "collected 1 evidence records from 1 datasources",
		EvidenceRefs: []v1alpha1.EvidenceRef{
			{
				Kind:    "event",
				Source:  "kubernetes-events",
				Reason:  "BackOff",
				Summary: "container crashed",
			},
		},
	}, time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generate RCA failed: %v", err)
	}
	if reasoning.Issue != nil {
		t.Fatalf("expected no RCA issue, got %#v", reasoning.Issue)
	}
	if reasoning.Reasoning == nil {
		t.Fatal("expected reasoning output")
	}
	if reasoning.Reasoning.RCA.Hypothesis == "" {
		t.Fatalf("expected RCA hypothesis, got %#v", reasoning.Reasoning)
	}
	if reasoning.Reasoning.Confidence.Score <= 0 {
		t.Fatalf("expected positive confidence, got %#v", reasoning.Reasoning.Confidence)
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

type capturingDataSource struct {
	name      string
	queryType domain.QueryType
	records   []map[string]any
	lastQuery *datasource.QueryRequest
}

func (c *capturingDataSource) Name() string { return c.name }
func (c *capturingDataSource) Type() string { return string(c.queryType) }
func (c *capturingDataSource) Capabilities() datasource.Capabilities {
	switch c.queryType {
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
func (c *capturingDataSource) Query(_ context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	copied := req
	c.lastQuery = &copied
	return &datasource.QueryResult{Source: c.name, QueryType: c.queryType, Records: c.records}, nil
}
func (c *capturingDataSource) HealthCheck(context.Context) error { return nil }
