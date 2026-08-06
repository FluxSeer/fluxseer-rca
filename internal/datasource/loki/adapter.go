package loki

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
	return "loki"
}

func (a Adapter) Type() string {
	return "loki"
}

func (a Adapter) Capabilities() datasource.Capabilities {
	return datasource.Capabilities{
		Logs:       true,
		RangeQuery: true,
		LabelQuery: true,
	}
}

func (a Adapter) Query(ctx context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	if err := a.HealthCheck(ctx); err != nil {
		return nil, err
	}

	endpoint, err := url.Parse(strings.TrimRight(a.BaseURL, "/") + "/loki/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("build loki endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("query", req.Query)
	query.Set("start", req.StartTime.UTC().Format(timeRFC3339Nano))
	query.Set("end", req.EndTime.UTC().Format(timeRFC3339Nano))
	query.Set("limit", "50")
	endpoint.RawQuery = query.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create loki request: %w", err)
	}

	body, err := datasource.DoRequestWithRetry(ctx, a.Name(), a.Client, func(context.Context) (*http.Request, error) {
		return httpReq, nil
	})
	if err != nil {
		return nil, err
	}

	var payload lokiResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &datasource.QueryError{
			Reason:  "InvalidDatasourceResponse",
			Message: fmt.Sprintf("decode loki response: %v", err),
		}
	}
	if payload.Status != "" && payload.Status != "success" {
		return nil, &datasource.QueryError{
			Reason:  "InvalidDatasourceResponse",
			Message: fmt.Sprintf("loki response status %q", payload.Status),
		}
	}

	records, counts, limit := lokiRecords(payload.Data.ResultType, payload.Data.Result, req.ResultLimits.Logs)
	result := &datasource.QueryResult{
		Source:       a.Name(),
		QueryType:    domain.QueryTypeLog,
		Summary:      fmt.Sprintf("Loki returned %d log lines for %s", counts.Entries, req.Target.Name),
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

func lokiRecords(resultType string, streams []lokiStream, limits v1alpha1.LogResultLimits) ([]map[string]any, datasource.NativeResultCounts, *datasource.NativeResultLimit) {
	counts := datasource.NativeResultCounts{ResultType: resultType, Streams: len(streams)}
	for _, stream := range streams {
		counts.Entries += len(stream.Values)
	}
	streams = sortedLokiStreams(streams)

	maxEntries := limits.MaxEntries
	if maxEntries <= 0 {
		maxEntries = limits.MaxLines
	}
	records := make([]map[string]any, 0)
	retained := datasource.NativeResultCounts{ResultType: resultType}
	for _, stream := range streams {
		if limits.MaxStreams > 0 && int64(retained.Streams) >= limits.MaxStreams {
			break
		}
		values := sortedLokiValues(stream.Values)
		if maxEntries > 0 {
			remaining := int(maxEntries) - retained.Entries
			if remaining <= 0 {
				break
			}
			if len(values) > remaining {
				values = values[:remaining]
			}
		}
		if len(values) == 0 {
			continue
		}
		for _, value := range values {
			if len(value) < 2 {
				continue
			}
			records = append(records, map[string]any{
				"labels": stream.Stream,
				"ts":     value[0],
				"line":   value[1],
			})
			retained.Entries++
		}
		retained.Streams++
	}

	return records, counts, lokiLimit(counts, retained, limits)
}

func (a Adapter) HealthCheck(_ context.Context) error {
	if strings.TrimSpace(a.BaseURL) == "" {
		return fmt.Errorf("loki base URL is empty")
	}
	return nil
}

const timeRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

type lokiResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string       `json:"resultType"`
		Result     []lokiStream `json:"result"`
	} `json:"data"`
}

type lokiStream struct {
	Stream map[string]any `json:"stream"`
	Values [][]string     `json:"values"`
}

func lokiLimit(original, retained datasource.NativeResultCounts, limits v1alpha1.LogResultLimits) *datasource.NativeResultLimit {
	if limits.MaxStreams > 0 && int64(original.Streams) > limits.MaxStreams {
		return &datasource.NativeResultLimit{
			Reason:        "NativeResultLimitExceeded",
			Dimension:     "streams",
			Limit:         limits.MaxStreams,
			OriginalCount: int64(original.Streams),
			RetainedCount: int64(retained.Streams),
		}
	}
	maxEntries := limits.MaxEntries
	if maxEntries <= 0 {
		maxEntries = limits.MaxLines
	}
	if maxEntries > 0 && int64(original.Entries) > maxEntries {
		return &datasource.NativeResultLimit{
			Reason:        "NativeResultLimitExceeded",
			Dimension:     "entries",
			Limit:         maxEntries,
			OriginalCount: int64(original.Entries),
			RetainedCount: int64(retained.Entries),
		}
	}
	return nil
}

func sortedLokiStreams(streams []lokiStream) []lokiStream {
	out := append([]lokiStream(nil), streams...)
	sort.SliceStable(out, func(i, j int) bool {
		return lokiStreamKey(out[i]) < lokiStreamKey(out[j])
	})
	return out
}

func lokiStreamKey(stream lokiStream) string {
	keys := make([]string, 0, len(stream.Stream))
	for key := range stream.Stream {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, stream.Stream[key]))
	}
	if len(stream.Values) > 0 && len(stream.Values[0]) > 0 {
		parts = append(parts, stream.Values[0][0])
	}
	return strings.Join(parts, "\xff")
}

func sortedLokiValues(values [][]string) [][]string {
	out := append([][]string(nil), values...)
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i]) == 0 {
			return true
		}
		if len(out[j]) == 0 {
			return false
		}
		return out[i][0] < out[j][0]
	})
	return out
}
