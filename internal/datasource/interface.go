package datasource

import (
	"context"
	"time"

	"fluxagent/internal/domain"
)

type QueryRequest struct {
	Query     string
	StartTime time.Time
	EndTime   time.Time
	Step      time.Duration
	Labels    map[string]string
	Target    domain.ResourceRef
	QueryType domain.QueryType
}

type QueryResult struct {
	Source              string
	QueryType           domain.QueryType
	Summary             string
	Records             []map[string]any
	Truncated           bool
	OriginalRecordCount int
	RetainedRecordCount int
}

type Capabilities struct {
	Metrics              bool
	Logs                 bool
	Events               bool
	DeploymentConditions bool
	Traces               bool
	RangeQuery           bool
	InstantQuery         bool
	LabelQuery           bool
}

func (c Capabilities) SupportsQueryType(queryType domain.QueryType) bool {
	switch queryType {
	case domain.QueryTypeMetric:
		return c.Metrics
	case domain.QueryTypeLog:
		return c.Logs
	case domain.QueryTypeEvent:
		return c.Events
	case domain.QueryTypeDeploymentCondition:
		return c.DeploymentConditions
	case domain.QueryTypeTrace:
		return c.Traces
	default:
		return false
	}
}

type DataSource interface {
	Name() string
	Type() string
	Capabilities() Capabilities
	Query(ctx context.Context, req QueryRequest) (*QueryResult, error)
	HealthCheck(ctx context.Context) error
}

type Registry struct {
	sources map[string]DataSource
}

func NewRegistry(items ...DataSource) *Registry {
	registry := &Registry{sources: map[string]DataSource{}}
	for _, item := range items {
		registry.sources[item.Name()] = item
	}
	return registry
}

func (r *Registry) Register(source DataSource) {
	r.RegisterNamed(source.Name(), source)
}

func (r *Registry) RegisterNamed(name string, source DataSource) {
	if r.sources == nil {
		r.sources = map[string]DataSource{}
	}
	r.sources[name] = source
}

func (r *Registry) Get(name string) (DataSource, bool) {
	source, ok := r.sources[name]
	return source, ok
}

func (r *Registry) Unregister(name string) {
	if r == nil || r.sources == nil {
		return
	}
	delete(r.sources, name)
}
