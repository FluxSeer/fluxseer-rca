# `InvestigationRequest`

`InvestigationRequest` is the ad-hoc, read-only investigation contract in the current operator-first path.

It complements `RiskRule`:

- `RiskRule`: recurring background detection
- `InvestigationRequest`: one-shot or externally triggered RCA request

## What It Does

`InvestigationRequest` lets you:

- target one workload now
- collect evidence from selected datasources
- run RCA through `ModelProvider` or the built-in heuristic provider
- optionally emit a discovered `RiskSignal` when the investigation finds a materialized risk

It does not execute remediation.

## Spec

Current spec fields:

- `target`
- `timeRange.lookback`
- `question`
- `dataSources[]`
- `queries[]`
- `modelProviderRef`
- `mode`
- `createRiskSignal`
- `ttlSeconds`

### Simple Mode: `dataSources[]`

Use `dataSources[]` when you want FluxAgent to infer a default investigation plan from datasource capabilities.

```yaml
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: investigate-open-api
  namespace: fluxagent-system
spec:
  target:
    namespace: prod
    kind: Deployment
    name: open-api
    apiVersion: apps/v1
  timeRange:
    lookback: 15m
  question: |
    Why did open-api error rate increase after the rollout?
  dataSources:
    - name: kubernetes-events
    - name: prometheus
    - name: loki
  mode: readOnly
```

Default behavior:

- event-capable datasource: collect recent events
- metric-capable datasource: query 5xx rate
- log-capable datasource: query error logs

### Advanced Mode: `queries[]`

Use `queries[]` when you want a fixed investigation plan.

```yaml
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: investigate-open-api
  namespace: fluxagent-system
spec:
  target:
    namespace: prod
    kind: Deployment
    name: open-api
    apiVersion: apps/v1
  timeRange:
    lookback: 15m
  question: |
    Why did open-api latency increase after the latest rollout?
  queries:
    - name: unhealthy-events
      datasourceRef:
        name: kubernetes-events
      queryType: event
      reasons:
        - Unhealthy
        - BackOff
        - Failed
    - name: error-rate
      datasourceRef:
        name: prometheus
      queryType: metric
      queryTemplate: |
        sum(rate(http_requests_total{
          namespace="{{ .namespace }}",
          app="{{ .app }}",
          status=~"5.."
        }[5m]))
    - name: error-logs
      datasourceRef:
        name: loki
      queryType: log
      queryTemplate: |
        {namespace="{{ .namespace }}",app="{{ .app }}"} |= "error"
  modelProviderRef:
    name: heuristic-provider
  mode: readOnly
  createRiskSignal: true
  ttlSeconds: 3600
```

Query field behavior:

- `datasourceRef.name`: required datasource reference
- `queryType`: required and must match datasource capability
- `query`: literal query text
- `queryTemplate`: templated query rendered against target metadata
- `reasons[]`: optional event reason filter for `queryType: event`
- `ttlSeconds`: optional retention window in seconds after the request reaches `Completed` or `Failed`

If `modelProviderRef.name` is empty, FluxAgent falls back to the built-in heuristic provider.

### Mode Contract

`dataSources[]` and `queries[]` represent different planning modes and should be mutually exclusive.

Contract hardening target:

- exactly one of `dataSources[]` or `queries[]` is set
- if both are set, the request is rejected or marked `InvalidSpec`
- if neither is set, the request is rejected or marked `InvalidSpec`
- controller behavior should not silently prefer one mode over the other

## Status

### Implemented In `v0.2.0-beta.1`

Implemented status fields:

- `phase`
- `message`
- `provider`
- `summary`
- `hypothesis`
- `confidence`
- `startedAt`
- `completedAt`
- `evidenceRefs`
- `linkedRiskSignalRef`
- `conditions`

These fields are the compatibility RCA surface for humans and existing scripts.

`status.confidence` is a provider- or heuristic-derived ranking score. It is not a calibrated probability that the RCA is correct.

### Target For `v0.3`

Target structured status fields:

- `verdict`
- `claims[]`
- `alternativeHypotheses[]`
- `missingEvidence[]`
- `degradation`
- `execution`
- rich evidence provenance

These fields are the v0.3 target contract. New integrations should check the generated CRD YAML and `api/v1alpha1/types.go` before depending on any target field.

### Structured RCA Contract Target

`status.verdict` is the top-level RCA conclusion:

- `summary`: compact human-readable conclusion
- `rootCauseEntity`: Kubernetes target most directly associated with the conclusion
- `rootCauseType`: coarse category such as `CrashLoop`, `LatencyRegression`, `ResourcePressure`, `ConfigurationMismatch`, or `WorkloadDegradation`
- `confidence`: normalized score from `0.0` to `1.0`; this is a ranking score, not a calibrated probability

`status.claims[]` stores machine-addressable RCA claims:

- `id`: stable claim identifier such as `claim-001`
- `statement`: one conclusion or cause statement
- `evidenceRefs[]`: referenced evidence IDs
- `verification`: current verification state, for example `Supported` or `Inferred`

`status.evidenceRefs[]` stores compact evidence references. Each entry may include:

- `id`: stable evidence identifier such as `evidence-001`
- `kind`
- `source`
- `summary`
- `query`
- `queryDigest`
- `contentDigest`
- `redactionProfile`
- `truncated`
- `originalCount`
- `retainedCount`
- `collectedAt`
- `reason`
- `link`

These fields are compact normalized-observation metadata. They let consumers audit which query and redacted observation supported the RCA without storing raw Prometheus payloads, large Loki excerpts, or unredacted Kubernetes objects in status.

`status.alternativeHypotheses[]`, `status.missingEvidence[]`, and `status.degradation` are reserved for partial-failure and claim-hardening semantics. They let FluxAgent report uncertainty explicitly instead of silently presenting an incomplete RCA as fully proven.

`status.execution` records RCA execution metadata:

- `provider`
- `attempts`
- `durationSeconds`
- `inputTokens`
- `outputTokens`

When `createRiskSignal: true` succeeds, `status.linkedRiskSignalRef` points to the emitted `RiskSignal`. The RCA itself remains on `InvestigationRequest.status`; the linked `RiskSignal` represents a materialized finding for downstream workflows, not the canonical RCA result.

If `ttlSeconds` is greater than zero, FluxAgent keeps the completed request for that many seconds after `status.completedAt`, then deletes it automatically.

Status hardening target:

- terminal phase should be explicit and should not require callers to infer completion from conditions alone
- future terminal phases should distinguish `Succeeded`, `Failed`, `PartiallySucceeded`, `Cancelled`, and `Expired`
- workflow completion, RCA readiness, notification, and discovered-signal emission should remain separate result dimensions
- `phase` should describe workflow lifecycle
- `outcome` should describe RCA result semantics, for example `Confirmed`, `Inconclusive`, or `NoIssueFound`

## Conditions

Condition types:

- `Ready`
- `TargetResolved`
- `DatasourceResolved`
- `QueryTypeSupported`
- `EvidenceCollectionReady`
- `RCAReady`
- `Degraded`

Common reasons:

- `TargetNotFound`
- `DataSourceNotFound`
- `CapabilityMismatch`
- `ProviderNotFound`
- `ProviderUnavailable`
- `InvalidProviderResponse`

`Degraded=True` is used when the request failed because an optional dependency or adapter path was unavailable or incompatible.

`Ready=True` means the investigation workflow reached a consumable terminal status. `RCAReady=True` means an RCA result is available. It does not indicate that the target workload is healthy or remediated.

## Execution Semantics

Current execution path:

1. resolve target
2. resolve datasources and collection plan
3. validate query types against datasource capabilities
4. collect and normalize evidence
5. run RCA
6. optionally emit a discovered `RiskSignal`

For the same object generation, terminal requests are not re-executed on later reconciles. Spec changes create a new generation and trigger a fresh run.

Boundaries:

- read-only investigation only
- no executor routing
- no `AgentAction`
- no direct remediation mutation

## Files And Examples

- [config/samples/investigation-request.yaml](../../config/samples/investigation-request.yaml)
- [config/samples/investigation-queries.yaml](../../config/samples/investigation-queries.yaml)
- [../tutorials/investigate-workload.md](../tutorials/investigate-workload.md)
- [../architecture/investigation-flow.md](../architecture/investigation-flow.md)
