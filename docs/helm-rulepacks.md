# Helm Rule Packs

FluxSeer RCA Helm installs can create built-in `RiskRule` resources through `rulePacks`.

Rule packs are explicit configuration. They do not install Prometheus, Loki, hosted model providers, provider secrets, or any external agent.

Rule packs are bootstrap detection inputs, not the canonical RCA workflow. Long-term RCA orchestration should flow through `InvestigationRequest`, with generated `RiskSignal` resources acting as materialized risks and optional investigation triggers.

## Built-in Detection Patterns

FluxSeer RCA currently maintains 21 built-in detection patterns:

| Rule pack | Patterns | Availability |
| --- | ---: | --- |
| Kubernetes baseline | 6 | Enabled by default; no additional observability backend required |
| Prometheus baseline | 8 | Disabled by default; requires a configured Prometheus `DataSource` |
| Loki baseline | 7 | Disabled by default; requires a configured Loki `DataSource` |

The 21 patterns are official rule-pack defaults, not the capability ceiling of
the generic `RiskRule` engine. Application Profile request-rate, error-rate,
p95-latency, and queue-depth entries are parameterized signal templates and
are not counted as four additional detection patterns.

The detailed terminology is defined in the [product and API
glossary](glossary.md). The machine-readable source of truth is the
[detection pattern catalog](../config/rule-packs/detection-patterns.json).

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
      baselineEpsilon: 0.001
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
| `rulePacks.defaultTargetSelector.workloadSelector.kinds` | `["Deployment"]` | Workload kinds discovered by generated rules. Rule evaluation supports `Deployment`, `StatefulSet`, `DaemonSet`, `Job`, `CronJob`, and `Pod`; Pod owner chains are canonicalized when possible. The default remains `Deployment`. |
| `rulePacks.defaultTargetSelector.workloadSelector.matchLabels` | `{}` | Optional labels that target workloads must match. |
| `rulePacks.kubernetesBaseline.enabled` | `true` | Creates `fluxseer-rca-kubernetes-baseline`. |
| `rulePacks.kubernetesBaseline.interval` | `2m` | Rule reconciliation interval. |
| `rulePacks.kubernetesBaseline.window` | `10m` | Evidence lookback window. |
| `rulePacks.kubernetesBaseline.severity` | `warning` | Severity used on generated `RiskSignal` resources. |
| `rulePacks.kubernetesBaseline.rcaEnabled` | `true` | Enables RCA enrichment. With no providerRef, FluxSeer RCA uses the built-in heuristic provider. |
| `rulePacks.kubernetesBaseline.providerRef.name` | `""` | Optional `ModelProvider` name for RCA. |
| `rulePacks.prometheusBaseline.enabled` | `false` | Creates `fluxseer-rca-prometheus-baseline`. Requires a matching `DataSource`. |
| `rulePacks.prometheusBaseline.datasourceRef.name` | `prometheus` | `DataSource` name used by Prometheus signals. |
| `rulePacks.prometheusBaseline.interval` | `2m` | Rule reconciliation interval. |
| `rulePacks.prometheusBaseline.window` | `10m` | Evidence lookback window. |
| `rulePacks.prometheusBaseline.severity` | `warning` | Severity used on generated `RiskSignal` resources. |
| `rulePacks.prometheusBaseline.rcaEnabled` | `true` | Enables RCA enrichment. With no providerRef, FluxSeer RCA uses the built-in heuristic provider. |
| `rulePacks.prometheusBaseline.trafficAnomaly.comparisonOffset` | `30m` | Offset used to compare current request rate against a previous baseline window. |
| `rulePacks.prometheusBaseline.trafficAnomaly.increaseRatio` | `3` | Current/request baseline ratio required for the request-rate-surge signal. |
| `rulePacks.prometheusBaseline.trafficAnomaly.minimumCurrentRate` | `10` | Minimum current request rate before request-rate-surge can trigger. |
| `rulePacks.prometheusBaseline.trafficAnomaly.baselineEpsilon` | `0.001` | Minimum usable historical request rate. Baselines at or below this value suppress ratio evaluation instead of being treated as a surge. |
| `rulePacks.prometheusBaseline.resourceThresholds.cpuUsageCores` | `0.8` | CPU usage cores threshold for the cpu-saturation signal. |
| `rulePacks.prometheusBaseline.resourceThresholds.cpuThrottlingRatio` | `0.2` | CPU throttled-period ratio threshold. |
| `rulePacks.prometheusBaseline.resourceThresholds.memoryWorkingSetBytes` | `1073741824` | Absolute memory working set fallback threshold. |
| `rulePacks.prometheusBaseline.resourceThresholds.memoryNearLimitRatio` | `0.9` | Memory working set divided by configured memory limit threshold. |
| `rulePacks.prometheusBaseline.providerRef.name` | `""` | Optional `ModelProvider` name for RCA. |
| `rulePacks.lokiBaseline.enabled` | `false` | Creates `fluxseer-rca-loki-baseline`. Requires a matching `DataSource`. |
| `rulePacks.lokiBaseline.datasourceRef.name` | `loki` | `DataSource` name used by Loki signals. |
| `rulePacks.lokiBaseline.interval` | `2m` | Rule reconciliation interval. |
| `rulePacks.lokiBaseline.window` | `10m` | Evidence lookback window. |
| `rulePacks.lokiBaseline.severity` | `warning` | Severity used on generated `RiskSignal` resources. |
| `rulePacks.lokiBaseline.rcaEnabled` | `true` | Enables RCA enrichment. With no providerRef, FluxSeer RCA uses the built-in heuristic provider. |
| `rulePacks.lokiBaseline.providerRef.name` | `""` | Optional `ModelProvider` name for RCA. |
| `rulePacks.applicationProfiles.enabled` | `false` | Creates user-defined application profile `RiskRule` resources. |
| `rulePacks.applicationProfiles.profiles[]` | `[]` | Application-specific metric profile definitions. |

When application profiles are enabled, each profile must enable at least one signal. This prevents Helm from rendering a `RiskRule` with no useful detector input.

## Override Examples

The Kubernetes baseline combines Kubernetes Events with `deploymentCondition` checks, so it can detect an unavailable Deployment even when the relevant Event stream is incomplete.

The six Kubernetes-native patterns are:

- CrashLoopBackOff or container restart backoff;
- image pull failure;
- failed scheduling;
- OOMKilled;
- unhealthy readiness or liveness probes;
- unavailable or failed Deployment rollout conditions.

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

- panic
- fatal
- exception
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

## Request-rate Surge Tuning Guide

The `trafficAnomaly` values currently tune only the built-in
`request-rate-surge` Detection Pattern. They do not change `high-error-rate` or
`high-latency`.

The pattern compares the current HTTP request rate with the same query at a
historical offset. All three conditions must pass:

```text
current / max(baseline, baselineEpsilon) > increaseRatio
current > minimumCurrentRate
baseline > baselineEpsilon
```

`baselineEpsilon` is the single source for both the denominator floor and the
baseline-validity guard.

### Parameters And Units

| Parameter | Meaning | Unit |
| --- | --- | --- |
| `comparisonOffset` | Historical distance used for the baseline comparison. | Prometheus duration, such as `30m` or `1h` |
| `increaseRatio` | Ratio that `current / baseline` must exceed. A value of `3` means current traffic must be greater than three times the baseline; it does not mean a 3% increase. | Ratio; dimensionless |
| `minimumCurrentRate` | Minimum absolute current traffic required to prevent low-volume ratio amplification. | Requests per second |
| `baselineEpsilon` | Minimum usable historical rate and the denominator floor. A baseline at or below this value suppresses evaluation. | Requests per second |

The defaults are:

```yaml
rulePacks:
  prometheusBaseline:
    trafficAnomaly:
      comparisonOffset: 30m
      increaseRatio: 3.0
      minimumCurrentRate: 10
      baselineEpsilon: 0.001
```

A high ratio alone does not constitute a request-rate surge when current
traffic is below `minimumCurrentRate`. A historical rate at or below
`baselineEpsilon` is an insufficient baseline: evaluation is suppressed and no
incident is created. Traffic onset from an absent baseline is a separate
detection semantic and is not folded into `request-rate-surge`.

### Match Examples

With the defaults above:

| Example | Baseline | Current | Evaluation | Result |
| --- | ---: | ---: | --- | --- |
| Valid surge | 20 req/s | 80 req/s | Ratio `4 > 3`, current `80 > 10`, baseline `20 > 0.001` | `Matched` |
| Low-volume ratio spike | 1 req/s | 4 req/s | Ratio passes, but current `4 <= 10` | `NotMatched` |
| Missing baseline | 0 req/s | 100 req/s | Baseline `0 <= 0.001`; ratio evaluation is suppressed | `NotMatched`; internal reason `InsufficientBaseline` |

The missing-baseline case is not traffic-onset detection. The
`request-rate-surge` pattern deliberately does not create an incident without
a usable historical baseline.

### Choose And Apply Values

Prefer a version-controlled values file so the tuning decision is reviewable.
For example, save this as `traffic-tuning.yaml`:

```yaml
rulePacks:
  prometheusBaseline:
    trafficAnomaly:
      comparisonOffset: 1h
      increaseRatio: 5
      minimumCurrentRate: 50
      baselineEpsilon: 0.01
```

Apply it to an existing release, substituting the actual release name,
namespace, and chart reference when they differ:

```sh
helm upgrade fluxseer-rca charts/fluxseer-rca \
  --namespace fluxseer-rca-system \
  --reuse-values \
  -f traffic-tuning.yaml
```

For a one-off experiment, the equivalent command is:

```sh
helm upgrade fluxseer-rca charts/fluxseer-rca \
  --namespace fluxseer-rca-system \
  --reuse-values \
  --set rulePacks.prometheusBaseline.trafficAnomaly.comparisonOffset=1h \
  --set rulePacks.prometheusBaseline.trafficAnomaly.increaseRatio=5 \
  --set rulePacks.prometheusBaseline.trafficAnomaly.minimumCurrentRate=50 \
  --set rulePacks.prometheusBaseline.trafficAnomaly.baselineEpsilon=0.01
```

### Inspect The Rendered RiskRule

After upgrading, inspect the live Helm manifest:

```sh
helm get manifest fluxseer-rca \
  --namespace fluxseer-rca-system |
  sed -n '/name: request-rate-surge/,/name: high-latency/p'
```

For the example values, confirm that the block contains:

```text
offset 1h
threshold value = 5
current-rate guard = 50
clamp_min(..., 0.01)
baseline guard = 0.01
```

The configured epsilon must appear in both `clamp_min` and the baseline
validity guard. You can also inspect the live resource and resulting product
objects:

```sh
kubectl get riskrule fluxseer-rca-prometheus-baseline \
  --namespace fluxseer-rca-system -o yaml
kubectl get investigationrequest,risksignal \
  --namespace fluxseer-rca-system \
  --selector fluxseer-rca.aiops.platform/risk-rule=fluxseer-rca-prometheus-baseline
```

Observe at least one full `comparisonOffset` period when practical before
tightening the values again. To revert a problematic tuning change, select the
previous revision and roll it back:

```sh
helm history fluxseer-rca --namespace fluxseer-rca-system
helm rollback fluxseer-rca PREVIOUS_REVISION \
  --namespace fluxseer-rca-system
```

## Verification

```sh
make verify-rule-packs
make verify-traffic-pattern-promql
make verify-prometheus-pattern-promql
make verify-rule-packs-kind
```

Run the retained cluster conformance suite against an explicitly authorized
cluster with:

```sh
KUBECONFIG=/path/to/test-kubeconfig \
  make verify-runtime-traffic-pattern-conformance-cluster
```

The `request-rate-surge` slice retains five Internal Validation cases and one
User-facing Report for the matched case. Negative cases do not create product
incidents, and the main 15-example User-facing Report catalog remains
unchanged.

The first two additional Prometheus pattern checks are now runtime-conformant:
`high-error-rate` and `high-latency`. Their retained cluster runner covers
matched, boundary/negative, side-effect, and datasource failure controls:

```sh
KUBECONFIG=/path/to/test-kubeconfig \
  make verify-runtime-prometheus-pattern-conformance-cluster
```

The runner retains a `fluxseer-test-report/v1` 10-case summary and two
`fluxseer-riskrule-report/v1` User-facing Reports. The recorded source commit,
controller image, and artifact paths are listed in `reports/runtime/BASELINES.json`.

`verify-rule-packs` checks rendered Helm output. `verify-rule-packs-kind` verifies Kubernetes Events, Prometheus, and Loki baselines in a real kind cluster with fake observability.

`rulePacks.defaultTargetSelector.workloadSelector.kinds` can include
`Deployment`, `StatefulSet`, `DaemonSet`, `Job`, and `CronJob`. Pod-level
events are attributed to the owning workload when FluxSeer RCA can resolve the
owner chain. This is Kubernetes workload RCA coverage, not complete
cluster-wide RCA coverage for every Kubernetes resource kind.
