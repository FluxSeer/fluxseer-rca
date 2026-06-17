package prometheus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
)

func TestAdapterQueryParsesPrometheusSeries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/v1/query_range") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"http_requests_total","app":"demo"},"values":[[1718614800,"0.95"]]}]}}`))
	}))
	defer server.Close()

	adapter := Adapter{BaseURL: server.URL}
	result, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Query:     "demo",
		StartTime: time.Now().Add(-time.Minute),
		EndTime:   time.Now(),
		Target:    domain.ResourceRef{Name: "demo"},
		QueryType: domain.QueryTypeMetric,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("expected one record, got %d", len(result.Records))
	}
}
