# Datasource Adapters

FluxAgent keeps observability systems behind `internal/datasource`.

Current scaffolds:

- `prometheus`: metric query templates and range-query shape
- `loki`: log query-range shape
- `kubernetes`: event lookup shape
- `opentelemetry`: trace lookup shape
- `cloudwatch`: cloud metric/log lookup shape

Each adapter implements:

```go
type DataSource interface {
    Name() string
    Type() domain.QueryType
    Query(ctx context.Context, req QueryRequest) (*QueryResult, error)
    HealthCheck(ctx context.Context) error
}
```

The core control loop should depend on this abstraction, not vendor SDKs.
