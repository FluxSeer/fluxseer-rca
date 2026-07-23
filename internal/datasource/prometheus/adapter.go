package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
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

	records := make([]map[string]any, 0)
	for _, result := range payload.Data.Result {
		if len(result.Values) == 0 {
			continue
		}
		last := result.Values[len(result.Values)-1]
		metricName, _ := result.Metric["__name__"].(string)
		records = append(records, map[string]any{
			"metric": metricName,
			"labels": result.Metric,
			"value":  last[1],
		})
	}

	return &datasource.QueryResult{
		Source:    a.Name(),
		QueryType: domain.QueryTypeMetric,
		Summary:   fmt.Sprintf("Prometheus returned %d series for %s", len(records), req.Target.Name),
		Records:   records,
	}, nil
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
		ResultType string       `json:"resultType"`
		Result     []promSeries `json:"result"`
	} `json:"data"`
}

type promSeries struct {
	Metric map[string]any `json:"metric"`
	Values [][]any        `json:"values"`
}
