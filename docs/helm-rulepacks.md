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
    trafficAnomaly:
      comparisonOffset: 30m
      increaseRatio: 3
      minimumCurrentRate: 10
    resourceThresholds:
      cpuUsageCores: 0.8
      cpuThrottlingRatio: 0.2
      memoryWorkingSetBytes: 1073741824
      memoryNearLimitRatio: 0.9
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
| `rulePacks.prometheusBaseline.trafficAnomaly.comparisonOffset` | `30m` | Offset used to compare current request rate against a previous baseline window. |
| `rulePacks.prometheusBaseline.trafficAnomaly.increaseRatio` | `3` | Current/request baseline ratio required for the request-rate-surge signal. |
| `rulePacks.prometheusBaseline.trafficAnomaly.minimumCurrentRate` | `10` | Minimum current request rate before request-rate-surge can trigger. |
| `rulePacks.prometheusBaseline.resourceThresholds.cpuUsageCores` | `0.8` | CPU usage cores threshold for the cpu-saturation signal. |
| `rulePacks.prometheusBaseline.resourceThresholds.cpuThrottlingRatio` | `0.2` | CPU throttled-period ratio threshold. |
| `rulePacks.prometheusBaseline.resourceThresholds.memoryWorkingSetBytes` | `1073741824` | Absolute memory working set fallback threshold. |
| `rulePacks.prometheusBaseline.resourceThresholds.memoryNearLimitRatio` | `0.9` | Memory working set divided by configured memory limit threshold. |
| `rulePacks.prometheusBaseline.providerRef.name` | `""` | Optional `ModelProvider` name for RCA. |
| `rulePacks.lokiBaseline.enabled` | `false` | Creates `fluxagent-loki-baseline`. Requires a matching `DataSource`. |
| `rulePacks.lokiBaseline.datasourceRef.name` | `loki` | `DataSource` name used by Loki signals. |
| `rulePacks.lokiBaseline.interval` | `2m` | Rule reconciliation interval. |
| `rulePacks.lokiBaseline.window` | `10m` | Evidence lookback window. |
| `rulePacks.lokiBaseline.severity` | `warning` | Severity used on generated `RiskSignal` resources. |
| `rulePacks.lokiBaseline.rcaEnabled` | `true` | Enables RCA enrichment. With no providerRef, FluxAgent uses the built-in heuristic provider. |
| `rulePacks.lokiBaseline.providerRef.name` | `""` | Optional `ModelProvider` name for RCA. |
| `rulePacks.applicationProfiles.enabled` | `false` | Creates user-defined application profile `RiskRule` resources. |
| `rulePacks.applicationProfiles.profiles[]` | `[]` | Application-specific metric profile definitions. |

When application profiles are enabled, each profile must enable at least one signal. This prevents Helm from rendering a `RiskRule` with no useful detector input.

## Override Examples

The Kubernetes baseline combines Kubernetes Events with `deploymentCondition` checks, so it can detect an unavailable Deployment even when the relevant Event stream is incomplete.

The Prometheus baseline includes portable traffic and resource signals:

- 5xx rate
- request-rate surge compared to an offset baseline with minimum-volume guard
- p95 latency
- pod restart rate
- CPU usage
- CPU throttling ratio
- memory working set
- memory near configured limit

The Loki baseline includes common log symptoms:

- panic, fatal, and exception
- timeout
- retry
- rate-limit / 429
- connection refused

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

## Additional Profiles

Rule packs should stay split between portable Kubernetes signals and application profiles.

Portable baseline examples:

- CrashLoopBackOff
- ImagePullBackOff
- readiness failure
- Deployment unavailable
- CPU throttling
- memory near limit
- request-rate surge with minimum-volume guard
- restart increase

Application profile examples:

- HTTP request rate
- HTTP latency
- queue depth
- worker saturation
- database connection pool
- external API rate limiting

Application metric names and labels vary by service. Application profiles parameterize query expressions in Helm values instead of hard-coding controller assumptions:

```yaml
rulePacks:
  applicationProfiles:
    enabled: true
    profiles:
      - name: checkout-web
        datasourceRef:
          name: prometheus
        signals:
          requestRate:
            enabled: true
            queryTemplate: |
              sum(rate(http_requests_total{namespace="{{ .namespace }}",app="{{ .app }}"}[5m]))
            threshold:
              operator: ">"
              value: 100
          errorRate:
            enabled: true
            queryTemplate: |
              sum(rate(http_requests_total{namespace="{{ .namespace }}",app="{{ .app }}",status=~"5.."}[5m]))
            threshold:
              operator: ">"
              value: 1
          latencyP95:
            enabled: true
            queryTemplate: |
              histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket{namespace="{{ .namespace }}",app="{{ .app }}"}[5m])) by (le))
            threshold:
              operator: ">"
              value: 1
          queueDepth:
            enabled: true
            queryTemplate: |
              max(queue_depth{namespace="{{ .namespace }}",app="{{ .app }}"})
            threshold:
              operator: ">"
              value: 100
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
