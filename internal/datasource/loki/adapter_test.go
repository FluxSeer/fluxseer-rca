package loki

import (
	"context"
	"errors"
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

func TestAdapterQueryRetriesTransientLokiFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`temporary failure`))
			return
		}
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
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if len(result.Records) != 1 {
		t.Fatalf("expected one record, got %d", len(result.Records))
	}
}

func TestAdapterQueryMapsLokiRateLimit(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`slow down`))
	}))
	defer server.Close()

	adapter := Adapter{BaseURL: server.URL}
	_, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Query:     "{app=\"demo\"}",
		StartTime: time.Now().Add(-time.Minute),
		EndTime:   time.Now(),
		Target:    domain.ResourceRef{Name: "demo"},
		QueryType: domain.QueryTypeLog,
	})
	assertQueryErrorReason(t, err, "DatasourceRateLimited")
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestAdapterQueryMapsInvalidLokiPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	adapter := Adapter{BaseURL: server.URL}
	_, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Query:     "{app=\"demo\"}",
		StartTime: time.Now().Add(-time.Minute),
		EndTime:   time.Now(),
		Target:    domain.ResourceRef{Name: "demo"},
		QueryType: domain.QueryTypeLog,
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
