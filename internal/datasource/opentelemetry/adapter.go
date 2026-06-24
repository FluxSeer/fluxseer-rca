package opentelemetry

import (
	"context"
	"fmt"
	"strings"

	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
)

type Adapter struct {
	Endpoint string
}

func (a Adapter) Name() string {
	return "opentelemetry"
}

func (a Adapter) Type() string {
	return "opentelemetry"
}

func (a Adapter) Capabilities() datasource.Capabilities {
	return datasource.Capabilities{
		Traces: true,
	}
}

func (a Adapter) Query(_ context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	if strings.TrimSpace(a.Endpoint) == "" {
		return nil, fmt.Errorf("opentelemetry endpoint is empty")
	}

	return &datasource.QueryResult{
		Source:    a.Name(),
		QueryType: domain.QueryTypeTrace,
		Summary:   fmt.Sprintf("trace lookup prepared for %s", req.Target.Service),
		Records: []map[string]any{
			{"endpoint": a.Endpoint, "service": req.Target.Service},
		},
	}, nil
}

func (a Adapter) HealthCheck(_ context.Context) error {
	if strings.TrimSpace(a.Endpoint) == "" {
		return fmt.Errorf("opentelemetry endpoint is empty")
	}
	return nil
}
