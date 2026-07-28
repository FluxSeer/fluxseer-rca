# Helm Rule Packs

FluxAgent Helm installs can create built-in `RiskRule` resources through `rulePacks`.

Rule packs are explicit configuration. They do not install Prometheus, Loki, hosted model providers, provider secrets, or any external agent.

Rule packs are bootstrap detection inputs, not the canonical RCA workflow. Long-term RCA orchestration should flow through `InvestigationRequest`, with generated `RiskSignal` resources acting as materialized risks and optional investigation triggers.

## Defaults

```yaml
rulePacks:
  defaultTargetSelector:
    namespaceSelector: {}
    workloadSelector:
      kinds:
        - Deployment
  kubernetesBaseline:
    enabled: true
    interval: 2m
    window: 10m
    severity: warning
    rcaEnabled: true
    providerRef:
      name: ""
  prometheusBaseline:
    enabled: false
    datasourceRef:
      name: prometheus
    interval: 2m
    window: 10m
    severity: warning
    rcaEnabled: true
    providerRef:
      name: ""
  lokiBaseline:
    enabled: false
    datasourceRef:
      name: loki
    interval: 2m
    window: 10m
    severity: warning
    rcaEnabled: true
    providerRef:
      name: ""
```

When `defaultTargetSelector.namespaceSelector.matchNames` is omitted, the chart renders the release namespace. Set it explicitly to control scope.

## Values Reference

| Value | Default | Description |
| --- | --- | --- |
| `rulePacks.defaultTargetSelector.namespaceSelector.matchNames` | release namespace | Namespaces scanned by generated rule packs. Use `[]` only when cluster-wide scanning is intended. |
| `rulePacks.defaultTargetSelector.workloadSelector.kinds` | `["Deployment"]` | Workload kinds discovered by generated rules. Current rule evaluation supports Deployment targets. |
| `rulePacks.defaultTargetSelector.workloadSelector.matchLabels` | `{}` | Optional labels that target workloads must match. |
| `rulePacks.kubernetesBaseline.enabled` | `true` | Creates `fluxagent-kubernetes-baseline`. |
| `rulePacks.kubernetesBaseline.interval` | `2m` | Rule reconciliation interval. |
| `rulePacks.kubernetesBaseline.window` | `10m` | Evidence lookback window. |
| `rulePacks.kubernetesBaseline.severity` | `warning` | Severity used on generated `RiskSignal` resources. |
| `rulePacks.kubernetesBaseline.rcaEnabled` | `true` | Enables RCA enrichment. With no providerRef, FluxAgent uses the built-in heuristic provider. |
| `rulePacks.kubernetesBaseline.providerRef.name` | `""` | Optional `ModelProvider` name for RCA. |
| `rulePacks.prometheusBaseline.enabled` | `false` | Creates `fluxagent-prometheus-baseline`. Requires a matching `DataSource`. |
| `rulePacks.prometheusBaseline.datasourceRef.name` | `prometheus` | `DataSource` name used by Prometheus signals. |
| `rulePacks.prometheusBaseline.interval` | `2m` | Rule reconciliation interval. |
| `rulePacks.prometheusBaseline.window` | `10m` | Evidence lookback window. |
| `rulePacks.prometheusBaseline.severity` | `warning` | Severity used on generated `RiskSignal` resources. |
| `rulePacks.prometheusBaseline.rcaEnabled` | `true` | Enables RCA enrichment. With no providerRef, FluxAgent uses the built-in heuristic provider. |
| `rulePacks.prometheusBaseline.providerRef.name` | `""` | Optional `ModelProvider` name for RCA. |
| `rulePacks.lokiBaseline.enabled` | `false` | Creates `fluxagent-loki-baseline`. Requires a matching `DataSource`. |
| `rulePacks.lokiBaseline.datasourceRef.name` | `loki` | `DataSource` name used by Loki signals. |
| `rulePacks.lokiBaseline.interval` | `2m` | Rule reconciliation interval. |
| `rulePacks.lokiBaseline.window` | `10m` | Evidence lookback window. |
| `rulePacks.lokiBaseline.severity` | `warning` | Severity used on generated `RiskSignal` resources. |
| `rulePacks.lokiBaseline.rcaEnabled` | `true` | Enables RCA enrichment. With no providerRef, FluxAgent uses the built-in heuristic provider. |
| `rulePacks.lokiBaseline.providerRef.name` | `""` | Optional `ModelProvider` name for RCA. |

## Override Examples

The Kubernetes baseline combines Kubernetes Events with `deploymentCondition` checks, so it can detect an unavailable Deployment even when the relevant Event stream is incomplete.

Limit scanning to app namespaces:

```yaml
rulePacks:
  defaultTargetSelector:
    namespaceSelector:
      matchNames:
        - fintrack-test
        - fluentgo-test
    workloadSelector:
      kinds:
        - Deployment
```

Limit scanning by workload labels:

```yaml
rulePacks:
  defaultTargetSelector:
    namespaceSelector:
      matchNames:
        - fintrack-test
    workloadSelector:
      kinds:
        - Deployment
      matchLabels:
        app.kubernetes.io/part-of: fintrack
```

Use a hosted provider only after a `ModelProvider` and secret are explicitly installed:

```yaml
rulePacks:
  kubernetesBaseline:
    providerRef:
      name: openai-provider
```

Enable Prometheus and Loki baselines against existing datasource resources:

```yaml
rulePacks:
  prometheusBaseline:
    enabled: true
    datasourceRef:
      name: platform-prometheus
  lokiBaseline:
    enabled: true
    datasourceRef:
      name: platform-loki
```

Disable external RCA enrichment but keep read-only signal generation:

```yaml
rulePacks:
  kubernetesBaseline:
    rcaEnabled: false
  prometheusBaseline:
    rcaEnabled: false
  lokiBaseline:
    rcaEnabled: false
```

Cluster-wide scanning is possible, but should be an explicit operator decision:

```yaml
rulePacks:
  defaultTargetSelector:
    namespaceSelector:
      matchNames: []
```

## Future Profiles

Rule packs should stay split between portable Kubernetes signals and application profiles.

Portable baseline examples:

- CrashLoopBackOff
- ImagePullBackOff
- readiness failure
- Deployment unavailable
- CPU throttling
- memory near limit
- restart increase

Application profile examples:

- HTTP request rate
- HTTP latency
- queue depth
- worker saturation
- database connection pool
- external API rate limiting

Application metric names and labels vary by service. Future profile values should parameterize query expressions instead of hard-coding controller assumptions:

```yaml
rulePacks:
  trafficAnomaly:
    enabled: true
    datasourceRef:
      name: prometheus
    queries:
      requestRate:
        expression: |
          sum(rate(http_requests_total{namespace="$namespace",app="$workload"}[5m]))
      errorRate:
        expression: |
          sum(rate(http_requests_total{namespace="$namespace",app="$workload",status=~"5.."}[5m]))
      latencyP95:
        expression: |
          histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket{namespace="$namespace",app="$workload"}[5m])) by (le))
```

Traffic anomaly detection should compare a current window against a baseline window and include a minimum-volume guard:

```yaml
rulePacks:
  trafficAnomaly:
    anomaly:
      evaluationWindow: 10m
      comparisonWindow: 30m
      increaseRatio: 3.0
      minimumCurrentRate: 10
```

This avoids treating low-volume noise as an operational incident just because the relative ratio is high.

## Verification

```sh
make verify-rule-packs
make verify-rule-packs-kind
```

`verify-rule-packs` checks rendered Helm output. `verify-rule-packs-kind` verifies Kubernetes Events, Prometheus, and Loki baselines in a real kind cluster with fake observability.
