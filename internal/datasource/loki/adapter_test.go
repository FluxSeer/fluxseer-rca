package loki

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fluxagent/api/v1alpha1"
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
	if result.NativeCounts.ResultType != "streams" || result.NativeCounts.Streams != 1 || result.NativeCounts.Entries != 1 {
		t.Fatalf("expected native stream counts, got %#v", result.NativeCounts)
	}
}

func TestAdapterQueryEnforcesLokiNativeStreamAndEntryLimits(t *testing.T) {
	body := `{"status":"success","data":{"resultType":"streams","result":[` +
		`{"stream":{"app":"b"},"values":[["3","b3"],["1","b1"]]},` +
		`{"stream":{"app":"a"},"values":[["2","a2"],["1","a1"]]},` +
		`{"stream":{"app":"c"},"values":[["1","c1"]]}` +
		`]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	adapter := Adapter{BaseURL: server.URL}
	result, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Query:     `{app=~".+"}`,
		StartTime: time.Now().Add(-time.Minute),
		EndTime:   time.Now(),
		Target:    domain.ResourceRef{Name: "demo"},
		QueryType: domain.QueryTypeLog,
		ResultLimits: v1alpha1.QueryResultLimits{
			Logs: v1alpha1.LogResultLimits{MaxStreams: 2, MaxEntries: 3},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NativeCounts.Streams != 3 || result.NativeCounts.Entries != 5 {
		t.Fatalf("expected original native counts, got %#v", result.NativeCounts)
	}
	if len(result.Records) != 3 {
		t.Fatalf("expected three retained log entries, got %d", len(result.Records))
	}
	if result.NativeLimit == nil || result.NativeLimit.Dimension != "streams" || result.NativeLimit.OriginalCount != 3 || result.NativeLimit.RetainedCount != 2 {
		t.Fatalf("expected stream limit metadata, got %#v", result.NativeLimit)
	}
	lines := []string{result.Records[0]["line"].(string), result.Records[1]["line"].(string), result.Records[2]["line"].(string)}
	if strings.Join(lines, ",") != "a1,a2,b1" {
		t.Fatalf("expected canonical retained log order, got %#v", lines)
	}
}

func TestAdapterQueryUsesLegacyMaxLinesAsEntryLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[{"stream":{"app":"demo"},"values":[["1","one"],["2","two"]]}]}}`))
	}))
	defer server.Close()

	adapter := Adapter{BaseURL: server.URL}
	result, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Query:     `{app="demo"}`,
		StartTime: time.Now().Add(-time.Minute),
		EndTime:   time.Now(),
		Target:    domain.ResourceRef{Name: "demo"},
		QueryType: domain.QueryTypeLog,
		ResultLimits: v1alpha1.QueryResultLimits{
			Logs: v1alpha1.LogResultLimits{MaxLines: 1},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Records) != 1 || result.NativeLimit == nil || result.NativeLimit.Dimension != "entries" {
		t.Fatalf("expected maxLines legacy entry limit, got records=%#v limit=%#v", result.Records, result.NativeLimit)
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
