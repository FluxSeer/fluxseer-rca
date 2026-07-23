package prometheus

import (
	"context"
	"errors"
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

func TestAdapterQueryRetriesTransientPrometheusFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`temporary failure`))
			return
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
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if len(result.Records) != 1 {
		t.Fatalf("expected one record, got %d", len(result.Records))
	}
}

func TestAdapterQueryMapsPrometheusAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`bad token`))
	}))
	defer server.Close()

	adapter := Adapter{BaseURL: server.URL}
	_, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Query:     "demo",
		StartTime: time.Now().Add(-time.Minute),
		EndTime:   time.Now(),
		Target:    domain.ResourceRef{Name: "demo"},
		QueryType: domain.QueryTypeMetric,
	})
	assertQueryErrorReason(t, err, "DatasourceAuthFailed")
}

func TestAdapterQueryMapsInvalidPrometheusPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","error":"bad query"}`))
	}))
	defer server.Close()

	adapter := Adapter{BaseURL: server.URL}
	_, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Query:     "demo",
		StartTime: time.Now().Add(-time.Minute),
		EndTime:   time.Now(),
		Target:    domain.ResourceRef{Name: "demo"},
		QueryType: domain.QueryTypeMetric,
	})
	assertQueryErrorReason(t, err, "InvalidDatasourceResponse")
}

func assertQueryErrorReason(t *testing.T, err error, reason string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	var queryErr *datasource.QueryError
	if !errors.As(err, &queryErr) {
		t.Fatalf("expected QueryError, got %T", err)
	}
	if queryErr.Reason != reason {
		t.Fatalf("expected %s, got %s", reason, queryErr.Reason)
	}
}
