# Datasource Adapters

FluxAgent uses datasource adapters to collect evidence without binding the core workflow to one observability backend.

## Current Product Truth

- Kubernetes Events are the only datasource wired by default.
- Prometheus is optional and registered only when configured.
- Loki is optional and registered only when configured.
- the read-only path should remain usable when optional adapters are absent.

Current implementation entrypoint: [internal/datasource/interface.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/datasource/interface.go:1)

## Why Adapters Exist

The adapter boundary keeps FluxAgent from hard-coding Prometheus, Loki, or another vendor into the main workflow.

The design rule is:

```text
Collect evidence through adapters.
Normalize it before rule evaluation and reasoning.
```

That gives the project three properties:

- optional runtime integrations
- isolated provider-specific code
- future portability to other metrics, logs, or event backends

## Direction

Current interface:

```go
type DataSource interface {
    Name() string
    Type() string
    Capabilities() Capabilities
    Query(ctx context.Context, req QueryRequest) (*QueryResult, error)
    HealthCheck(ctx context.Context) error
}
```

Planned direction:

- richer normalized `QueryResult`
- template-driven queries from `RiskRule`
- eventual `DataSource` CRD for runtime configuration
- degraded conditions instead of hard failure when optional adapters are unavailable

The staged rationale is documented in [architecture/dependency-neutrality.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/dependency-neutrality.md:1).

## Query Ownership

Prometheus and Loki queries should be reviewable configuration, not arbitrary provider-generated commands.

Expected rule shape over time:

- `RiskRule` declares the signal intent
- query templates render against workload metadata
- datasource references determine which adapter executes the request

Current `RiskRule` direction in repo:

- `datasourceRef`
- `queryType`
- `queryTemplate`
- legacy `type` and `query` fields still accepted as a compatibility path

This keeps evidence collection auditable and compatible with GitOps review.

## Future Config Shape

The current first batch keeps datasource details inline in `RiskRule` for delivery speed.

The follow-up direction is:

1. `DataSource` CRD defines connection and auth details.
2. `RiskRule` points at datasource objects with `datasourceRef`.
3. adapter registry resolves the referenced datasource type.
4. unsupported or missing adapters surface through status, not panic.

## Adapter Docs

- [adapters/prometheus.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/adapters/prometheus.md:1)
- [adapters/loki.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/adapters/loki.md:1)
- [adapters/kubernetes-events.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/adapters/kubernetes-events.md:1)
- [adapters/model-providers.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/adapters/model-providers.md:1)
