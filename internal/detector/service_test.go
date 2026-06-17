package detector

import (
	"context"
	"testing"
	"time"

	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
)

type fakeSource struct {
	name   string
	result *datasource.QueryResult
}

func (f fakeSource) Name() string                      { return f.name }
func (f fakeSource) Type() domain.QueryType            { return f.result.QueryType }
func (f fakeSource) HealthCheck(context.Context) error { return nil }
func (f fakeSource) Query(context.Context, datasource.QueryRequest) (*datasource.QueryResult, error) {
	return f.result, nil
}

func TestServiceDetectMergesEventAndLogSignals(t *testing.T) {
	registry := datasource.NewRegistry(
		fakeSource{name: "prometheus", result: &datasource.QueryResult{Source: "prometheus", QueryType: domain.QueryTypeMetric, Records: []map[string]any{{"value": "0.5"}}}},
		fakeSource{name: "loki", result: &datasource.QueryResult{Source: "loki", QueryType: domain.QueryTypeLog, Records: []map[string]any{{"line": "error timeout"}}}},
		fakeSource{name: "kubernetes-events", result: &datasource.QueryResult{Source: "kubernetes-events", QueryType: domain.QueryTypeEvent, Records: []map[string]any{{"reason": "BackOff", "message": "container restart loop"}}}},
	)

	service := &Service{Registry: registry, Now: func() time.Time { return time.Now() }}
	finding, err := service.Detect(context.Background(), Request{
		Target:      domain.ResourceRef{Name: "demo", Namespace: "demo"},
		Labels:      map[string]string{"app": "demo"},
		Annotations: map[string]string{AnnotationEnabled: "true"},
		Window:      time.Minute,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if finding == nil {
		t.Fatalf("expected finding")
	}
	if finding.Severity != domain.SeverityHigh {
		t.Fatalf("expected high severity, got %s", finding.Severity)
	}
	if len(finding.Evidence) < 2 {
		t.Fatalf("expected merged evidence, got %d", len(finding.Evidence))
	}
}
