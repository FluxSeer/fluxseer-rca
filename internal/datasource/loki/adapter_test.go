package loki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
)

func TestAdapterQueryParsesLokiStreams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[{"stream":{"app":"demo"},"values":[["1718614800000000000","error timeout"]]}]}}`))
	}))
	defer server.Close()

	adapter := Adapter{BaseURL: server.URL}
	result, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Query:     "{app=\"demo\"}",
		StartTime: time.Now().Add(-time.Minute),
		EndTime:   time.Now(),
		Target:    domain.ResourceRef{Name: "demo"},
		QueryType: domain.QueryTypeLog,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("expected one record, got %d", len(result.Records))
	}
}
