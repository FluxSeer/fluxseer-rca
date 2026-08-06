# Metrics

FluxSeer RCA exposes low-cardinality Prometheus metrics for RCA control-plane health.

## Controller Runtime Workqueue

Controller reconcile queue depth is provided by controller-runtime:

```text
workqueue_depth
workqueue_queue_duration_seconds
workqueue_work_duration_seconds
workqueue_retries_total
workqueue_unfinished_work_seconds
controller_runtime_active_workers
controller_runtime_max_concurrent_reconciles
```

FluxSeer RCA does not maintain a duplicate reconciler queue counter. If a FluxSeer RCA-prefixed compatibility metric is useful for dashboards, enable the Helm recording rule:

```yaml
metrics:
  prometheusRule:
    enabled: true
    workqueueDepthExpr: "sum by (name) (workqueue_depth)"
```

This creates:

```text
fluxseer_rca_queue_depth
```

as an alias backed by `workqueue_depth`.

## Datasource Scheduler

FluxSeer RCA-owned datasource scheduler metrics are separate from controller-runtime reconcile queues:

```text
fluxseer_rca_datasource_query_queue_depth
fluxseer_rca_datasource_queries_in_flight
```

`queue_depth` means datasource queries submitted to the bounded scheduler but still waiting for a slot. `in_flight` means queries that acquired a slot and have not completed.

## Dashboard Packaging

The Helm chart can package an opt-in Grafana dashboard ConfigMap:

```yaml
metrics:
  grafanaDashboard:
    enabled: true
```

The dashboard includes controller workqueue depth, datasource scheduler queue/in-flight gauges, queue/work duration, and RCA result rates.
