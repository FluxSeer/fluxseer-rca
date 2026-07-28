# Prometheus Adapter

FluxAgent uses Prometheus as an optional metric datasource for read-only detection.

## Runtime Wiring

The adapter is registered when either:

- `FLUXAGENT_PROMETHEUS_URL` is set
- a `DataSource` resource of type `prometheus` is present

Env-based example:

```bash
export FLUXAGENT_PROMETHEUS_URL=http://your-prometheus.example
```

Registration source: [internal/operatorapp/run.go](../../internal/operatorapp/run.go)
Resource loader: [internal/datasourceconfig/loader.go](../../internal/datasourceconfig/loader.go)

Implementation source: [internal/datasource/prometheus/adapter.go](../../internal/datasource/prometheus/adapter.go)

## Endpoint Shape

The adapter queries:

```text
GET /api/v1/query_range
```

Request parameters:

- `query`
- `start`
- `end`
- `step`

## Per-Workload Annotations

- `fluxagent.aiops.platform/prometheus-query`
- `fluxagent.aiops.platform/prometheus-threshold`

If no query is provided, FluxAgent falls back to:

```promql
sum(rate(http_requests_total{namespace="<ns>",app="<app>",status=~"5.."}[5m]))
```

Default threshold is `0.2`.

## Detection Behavior

When a returned metric value exceeds the threshold, FluxAgent creates a medium-severity finding:

- signal type: `rollout.latency_regression`
- confidence: `72`
- evidence source: `prometheus`

## Demo Path

The kind demo sets:

```yaml
env:
  - name: FLUXAGENT_PROMETHEUS_URL
    value: http://fluxagent-observability:8080
```

See [examples/kind/manager-demo-patch.yaml](../../examples/kind/manager-demo-patch.yaml).
