package prometheus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fluxseer/api/v1alpha1"
	"fluxseer/internal/datasource"
	"fluxseer/internal/domain"
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
	if result.NativeCounts.ResultType != "matrix" || result.NativeCounts.Series != 1 || result.NativeCounts.Samples != 1 {
		t.Fatalf("expected native matrix counts, got %#v", result.NativeCounts)
	}
}

func TestAdapterQueryEnforcesPrometheusNativeSeriesAndSampleLimits(t *testing.T) {
	body := `{"status":"success","data":{"resultType":"matrix","result":[` +
		`{"metric":{"__name__":"latency","pod":"a"},"values":[[1,"1"],[3,"3"],[2,"2"]]},` +
		`{"metric":{"__name__":"latency","pod":"b"},"values":[[1,"4"],[2,"5"]]},` +
		`{"metric":{"__name__":"latency","pod":"c"},"values":[[1,"6"]]}` +
		`]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	adapter := Adapter{BaseURL: server.URL}
	result, err := adapter.Query(context.Background(), datasource.QueryRequest{
		Query:     "latency",
		StartTime: time.Now().Add(-time.Minute),
		EndTime:   time.Now(),
		Target:    domain.ResourceRef{Name: "demo"},
		QueryType: domain.QueryTypeMetric,
		ResultLimits: v1alpha1.QueryResultLimits{
			Metrics: v1alpha1.MetricResultLimits{MaxSeries: 2, MaxSamples: 4},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Records) != 2 {
		t.Fatalf("expected two retained records, got %d", len(result.Records))
	}
	if result.NativeCounts.Series != 3 || result.NativeCounts.Samples != 6 {
		t.Fatalf("expected original native counts, got %#v", result.NativeCounts)
	}
	if result.NativeLimit == nil || result.NativeLimit.Dimension != "series" || result.NativeLimit.OriginalCount != 3 || result.NativeLimit.RetainedCount != 2 {
		t.Fatalf("expected series native limit metadata, got %#v", result.NativeLimit)
	}
	if result.Records[0]["value"] != "3" || result.Records[1]["value"] != "4" {
		t.Fatalf("expected deterministic retained sample values, got %#v", result.Records)
	}
}

func TestAdapterQueryCountsPrometheusVectorAndScalarResults(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantSeries int
		wantSample int
	}{
		{
			name:       "vector",
			body:       `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up"},"value":[1,"1"]},{"metric":{"__name__":"up"},"value":[1,"0"]}]}}`,
			wantSeries: 2,
			wantSample: 2,
		},
		{
			name:       "scalar",
			body:       `{"status":"success","data":{"resultType":"scalar","result":[1,"42"]}}`,
			wantSeries: 1,
			wantSample: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
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
			if result.NativeCounts.Series != tt.wantSeries || result.NativeCounts.Samples != tt.wantSample {
				t.Fatalf("expected native counts series=%d samples=%d, got %#v", tt.wantSeries, tt.wantSample, result.NativeCounts)
			}
		})
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
