# Loki Adapter

FluxSeer RCA uses Loki as an optional log datasource for read-only detection.

## Runtime Wiring

The adapter is registered when either:

- `FLUXSEER_RCA_LOKI_URL` is set
- a `DataSource` resource of type `loki` is present

Env-based example:

```bash
export FLUXSEER_RCA_LOKI_URL=http://your-loki.example
```

Implementation source: [internal/datasource/loki/adapter.go](../../internal/datasource/loki/adapter.go)
Resource loader: [internal/datasourceconfig/loader.go](../../internal/datasourceconfig/loader.go)

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

- `fluxseer-rca.aiops.platform/loki-query`

If no annotation is present, FluxSeer RCA falls back to:

```logql
{namespace="<ns>",app="<app>"} |= "error"
```

## Detection Behavior

When any matching log line is returned, FluxSeer RCA creates a medium-severity finding:

- signal type: `workload.error_logs`
- confidence: `68`
- evidence source: `loki`

The first matching line is used as the evidence summary.

## Demo Path

The kind demo points Loki traffic at the fake observability service:

```yaml
env:
  - name: FLUXSEER_RCA_LOKI_URL
    value: http://fluxseer-rca-observability:8080
```
