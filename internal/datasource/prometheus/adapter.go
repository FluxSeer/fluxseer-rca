package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"fluxseer/api/v1alpha1"
	"fluxseer/internal/datasource"
	"fluxseer/internal/domain"
)

type Adapter struct {
	BaseURL string
	Client  *http.Client
}

func (a Adapter) Name() string {
	return "prometheus"
}

func (a Adapter) Type() string {
	return "prometheus"
}

func (a Adapter) Capabilities() datasource.Capabilities {
	return datasource.Capabilities{
		Metrics:      true,
		RangeQuery:   true,
		InstantQuery: true,
	}
}

func (a Adapter) Query(ctx context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	if err := a.HealthCheck(ctx); err != nil {
		return nil, err
	}

	endpoint, err := url.Parse(strings.TrimRight(a.BaseURL, "/") + "/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("build prometheus endpoint: %w", err)
	}

	query := endpoint.Query()
	query.Set("query", req.Query)
	query.Set("start", req.StartTime.UTC().Format("2006-01-02T15:04:05Z"))
	query.Set("end", req.EndTime.UTC().Format("2006-01-02T15:04:05Z"))
	if req.Step > 0 {
		query.Set("step", req.Step.String())
	}
	endpoint.RawQuery = query.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create prometheus request: %w", err)
	}

	body, err := datasource.DoRequestWithRetry(ctx, a.Name(), a.Client, func(context.Context) (*http.Request, error) {
		return httpReq, nil
	})
	if err != nil {
		return nil, err
	}

	var payload promResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &datasource.QueryError{
			Reason:  "InvalidDatasourceResponse",
			Message: fmt.Sprintf("decode prometheus response: %v", err),
		}
	}
	if payload.Status != "" && payload.Status != "success" {
		return nil, &datasource.QueryError{
			Reason:  "InvalidDatasourceResponse",
			Message: fmt.Sprintf("prometheus response status %q", payload.Status),
		}
	}

	records, counts, limit := prometheusRecords(payload.Data.ResultType, payload.Data.Result, req.ResultLimits.Metrics)
	result := &datasource.QueryResult{
		Source:       a.Name(),
		QueryType:    domain.QueryTypeMetric,
		Summary:      fmt.Sprintf("Prometheus returned %d series for %s", counts.Series, req.Target.Name),
		Records:      records,
		NativeCounts: counts,
	}
	if limit != nil {
		result.Truncated = true
		result.TruncationReason = limit.Reason
		result.LimitDimension = limit.Dimension
		result.Limit = limit.Limit
		result.OriginalRecordCount = int(limit.OriginalCount)
		result.RetainedRecordCount = int(limit.RetainedCount)
		result.NativeLimit = limit
		result.Summary = fmt.Sprintf("%s; native %s limit retained %d of %d", result.Summary, limit.Dimension, limit.RetainedCount, limit.OriginalCount)
	}
	return result, nil
}

func (a Adapter) HealthCheck(_ context.Context) error {
	if strings.TrimSpace(a.BaseURL) == "" {
		return fmt.Errorf("prometheus base URL is empty")
	}
	return nil
}

type promResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

type promSeries struct {
	Metric map[string]any `json:"metric"`
	Values [][]any        `json:"values"`
	Value  []any          `json:"value"`
}

func prometheusRecords(resultType string, raw json.RawMessage, limits v1alpha1.MetricResultLimits) ([]map[string]any, datasource.NativeResultCounts, *datasource.NativeResultLimit) {
	counts := datasource.NativeResultCounts{ResultType: resultType}
	switch resultType {
	case "matrix", "":
		var series []promSeries
		if err := json.Unmarshal(raw, &series); err != nil {
			return nil, counts, nil
		}
		counts.Series = len(series)
		for _, item := range series {
			counts.Samples += len(item.Values)
		}
		records, retainedCounts := matrixRecords(series, limits)
		return records, counts, prometheusLimit(counts, retainedCounts, limits)
	case "vector":
		var series []promSeries
		if err := json.Unmarshal(raw, &series); err != nil {
			return nil, counts, nil
		}
		counts.Series = len(series)
		counts.Samples = len(series)
		records, retainedCounts := vectorRecords(series, limits)
		return records, counts, prometheusLimit(counts, retainedCounts, limits)
	case "scalar", "string":
		var value []any
		if err := json.Unmarshal(raw, &value); err != nil || len(value) < 2 {
			return nil, counts, nil
		}
		counts.Series = 1
		counts.Samples = 1
		records := []map[string]any{{"metric": resultType, "labels": map[string]any{"resultType": resultType}, "value": value[1]}}
		return records, counts, prometheusLimit(counts, counts, limits)
	default:
		return nil, counts, nil
	}
}

func matrixRecords(series []promSeries, limits v1alpha1.MetricResultLimits) ([]map[string]any, datasource.NativeResultCounts) {
	records := make([]map[string]any, 0, len(series))
	retained := datasource.NativeResultCounts{ResultType: "matrix"}
	for _, item := range series {
		if limits.MaxSeries > 0 && int64(retained.Series) >= limits.MaxSeries {
			break
		}
		values := sortedPromValues(item.Values)
		if len(values) == 0 {
			continue
		}
		if limits.MaxSamples > 0 {
			remaining := int(limits.MaxSamples) - retained.Samples
			if remaining <= 0 {
				break
			}
			if len(values) > remaining {
				values = values[:remaining]
			}
		}
		last := values[len(values)-1]
		metricName, _ := item.Metric["__name__"].(string)
		records = append(records, map[string]any{
			"metric": metricName,
			"labels": item.Metric,
			"value":  last[1],
		})
		retained.Series++
		retained.Samples += len(values)
	}
	return records, retained
}

func vectorRecords(series []promSeries, limits v1alpha1.MetricResultLimits) ([]map[string]any, datasource.NativeResultCounts) {
	records := make([]map[string]any, 0, len(series))
	retained := datasource.NativeResultCounts{ResultType: "vector"}
	for _, item := range series {
		if limits.MaxSeries > 0 && int64(retained.Series) >= limits.MaxSeries {
			break
		}
		if limits.MaxSamples > 0 && int64(retained.Samples) >= limits.MaxSamples {
			break
		}
		if len(item.Value) < 2 {
			continue
		}
		metricName, _ := item.Metric["__name__"].(string)
		records = append(records, map[string]any{
			"metric": metricName,
			"labels": item.Metric,
			"value":  item.Value[1],
		})
		retained.Series++
		retained.Samples++
	}
	return records, retained
}

func prometheusLimit(original, retained datasource.NativeResultCounts, limits v1alpha1.MetricResultLimits) *datasource.NativeResultLimit {
	if limits.MaxSeries > 0 && int64(original.Series) > limits.MaxSeries {
		return &datasource.NativeResultLimit{
			Reason:        "NativeResultLimitExceeded",
			Dimension:     "series",
			Limit:         limits.MaxSeries,
			OriginalCount: int64(original.Series),
			RetainedCount: int64(retained.Series),
		}
	}
	if limits.MaxSamples > 0 && int64(original.Samples) > limits.MaxSamples {
		return &datasource.NativeResultLimit{
			Reason:        "NativeResultLimitExceeded",
			Dimension:     "samples",
			Limit:         limits.MaxSamples,
			OriginalCount: int64(original.Samples),
			RetainedCount: int64(retained.Samples),
		}
	}
	return nil
}

func sortedPromValues(values [][]any) [][]any {
	out := append([][]any(nil), values...)
	sort.SliceStable(out, func(i, j int) bool {
		return fmt.Sprint(out[i][0]) < fmt.Sprint(out[j][0])
	})
	return out
}
