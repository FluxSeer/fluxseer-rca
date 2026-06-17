# Loki Adapter

FluxAgent uses Loki as an optional log datasource for read-only detection.

## Runtime Wiring

The adapter is registered only when:

```bash
export FLUXAGENT_LOKI_URL=http://your-loki.example
```

Implementation source: [internal/datasource/loki/adapter.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/datasource/loki/adapter.go:1)

## Endpoint Shape

The adapter queries:

```text
GET /loki/api/v1/query_range
```

Current request behavior:

- passes `query`
- uses RFC3339Nano `start` and `end`
- limits results to `50`

## Per-Workload Annotation

- `fluxagent.aiops.platform/loki-query`

If no annotation is present, FluxAgent falls back to:

```logql
{namespace="<ns>",app="<app>"} |= "error"
```

## Detection Behavior

When any matching log line is returned, FluxAgent creates a medium-severity finding:

- signal type: `workload.error_logs`
- confidence: `68`
- evidence source: `loki`

The first matching line is used as the evidence summary.

## Demo Path

The kind demo points Loki traffic at the fake observability service:

```yaml
env:
  - name: FLUXAGENT_LOKI_URL
    value: http://fluxagent-observability:8080
```
