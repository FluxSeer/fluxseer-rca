package cloudwatch

import (
	"context"
	"fmt"
	"strings"

	"github.com/FluxSeer/fluxseer-rca/internal/datasource"
)

type Adapter struct {
	Region string
}

func (a Adapter) Name() string {
	return "cloudwatch"
}

func (a Adapter) Type() string {
	return "cloudwatch"
}

func (a Adapter) Capabilities() datasource.Capabilities {
	return datasource.Capabilities{
		Metrics:      true,
		RangeQuery:   true,
		InstantQuery: true,
	}
}

func (a Adapter) Query(_ context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	if strings.TrimSpace(a.Region) == "" {
		return nil, fmt.Errorf("cloudwatch region is empty")
	}

	return &datasource.QueryResult{
		Source:    a.Name(),
		QueryType: req.QueryType,
		Summary:   fmt.Sprintf("cloudwatch query prepared for %s in %s", req.Target.Name, a.Region),
		Records: []map[string]any{
			{"region": a.Region, "query": req.Query},
		},
	}, nil
}

func (a Adapter) HealthCheck(_ context.Context) error {
	if strings.TrimSpace(a.Region) == "" {
		return fmt.Errorf("cloudwatch region is empty")
	}
	return nil
}
