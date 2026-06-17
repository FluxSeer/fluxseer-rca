package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
)

type Adapter struct {
	BaseURL string
	Client  *http.Client
}

func (a Adapter) Name() string {
	return "loki"
}

func (a Adapter) Type() domain.QueryType {
	return domain.QueryTypeLog
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

	resp, err := datasource.DefaultDo(a.Client, httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("loki query failed: %s", strings.TrimSpace(string(body)))
	}

	var payload lokiResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode loki response: %w", err)
	}

	records := make([]map[string]any, 0)
	for _, stream := range payload.Data.Result {
		for _, value := range stream.Values {
			if len(value) < 2 {
				continue
			}
			records = append(records, map[string]any{
				"labels": stream.Stream,
				"ts":     value[0],
				"line":   value[1],
			})
		}
	}

	return &datasource.QueryResult{
		Source:    a.Name(),
		QueryType: domain.QueryTypeLog,
		Summary:   fmt.Sprintf("Loki returned %d log lines for %s", len(records), req.Target.Name),
		Records:   records,
	}, nil
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
