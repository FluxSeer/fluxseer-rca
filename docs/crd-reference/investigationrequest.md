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
- `evidenceRequirements.profile`: optional required evidence profile. Current profiles are `ImagePullBackOff`, `LatencyRegression`, and `RolloutLatencyRegression`.
- `evidenceRetention.mode`: external evidence retention mode. Current supported runtime behavior is `MetadataOnly`.
- `evidenceRetention.retention`: requested external payload retention duration for future snapshot modes.
- `evidenceRetention.storageRef.name`: external evidence storage configuration reference for future snapshot modes.
- `evidenceRetention.encryption.required`: whether future retained payloads must be encrypted.
- `evidenceRetention.deletionPolicy`: future external payload deletion behavior, `Retain` or `Delete`.
- `evidenceRetention.accessPolicy.namespaceScoped`: whether future payload access must remain namespace-scoped.
- `queryBudget.maxTimeRange`: maximum allowed `timeRange.lookback` before query execution starts
- `queryBudget.maxQueriesTotal`: maximum total datasource queries for this investigation
- `queryBudget.maxQueriesPerSource`: maximum queries referencing the same datasource
- `loopPolicy.maxDepth`: maximum allowed lineage depth before execution is blocked; default is `1`
- `loopPolicy.allowRiskSignalSource`: opt-in escape hatch for investigations sourced from `RiskSignal`; default is `false`

If `modelProviderRef.name` is empty, FluxAgent falls back to the built-in heuristic provider.

When an evidence requirements profile is configured, FluxAgent checks required evidence before calling the model provider. Missing required evidence produces `phase: Completed`, `outcome: Inconclusive`, `status.missingEvidence[]`, and a `RequiredEvidenceMissing` degradation reason. This is not a workflow failure. `ImagePullBackOff` requires event evidence. `LatencyRegression` and `RolloutLatencyRegression` require metric evidence. `NoIssueFound` is only valid after required evidence is complete.

External evidence storage is disabled by default. FluxAgent currently accepts only the default `MetadataOnly` retention behavior. `NormalizedSnapshot` and `RawSnapshot` are reserved contract values and are rejected by validation until a secure external evidence storage adapter is implemented.

When `queryBudget` is configured, FluxAgent rejects excessive lookback windows or query counts during validation before contacting datasources.

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
- `outcome`
- `failure`
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
- `execution`
- rich evidence provenance

These fields are the v0.3 target contract. New integrations should check the generated CRD YAML and `api/v1alpha1/types.go` before depending on any target field.

### Structured RCA Contract Target

`status.verdict` is the top-level RCA conclusion:

- `outcome`: RCA result semantics such as `Confirmed`, `Inconclusive`, `NoIssueFound`, or `Unknown`
- `summary`: compact human-readable conclusion
- `rootCauseEntity`: Kubernetes target most directly associated with the conclusion
- `rootCauseType`: coarse category such as `CrashLoop`, `LatencyRegression`, `ResourcePressure`, `ConfigurationMismatch`, or `WorkloadDegradation`
- `confidence`: compatibility normalized score from `0.0` to `1.0`; this is a ranking score, not a calibrated probability
- `confidenceDetail`: provider, verifier, confidence band, and scoring-method metadata

`status.claims[]` stores machine-addressable RCA claims:

- `id`: stable claim identifier such as `claim-001`
- `statement`: one conclusion or cause statement
- `evidenceRefs[]`: referenced evidence IDs
- `evidenceLinks[]`: evidence references with role such as `Supports` or `Contradicts` and strength such as `Direct`
- `verification`: current verification state: `Supported`, `Inferred`, `Unsupported`, `Contradicted`, or `Unverified`

FluxAgent applies a deterministic heuristic verifier before writing claims. In the current implementation, a claim is `Supported` only when compact evidence metadata is relevant to the claim text. Claims with contradictory compact evidence are `Contradicted`, claims with evidence in the bundle but no relevant match are `Unsupported`, and claims with no evidence are `Unverified`. `status.verdict.confidenceDetail.verifiedScore` is bounded by this evidence coverage and can be lower than the provider score. The final execution metadata records `status.execution.verifierVersion`.

`status.evidenceRefs[]` stores compact evidence references. Each entry may include:

- `id`: stable evidence identifier such as `evidence-001`
- `kind`
- `source`
- `summary`
- `query`
- `queryDigest`
- `contentDigest`
- `digestAlgorithm`
- `digestCanonicalization`
- `redactionProfile`
- `truncated`
- `originalCount`
- `retainedCount`
- `originalBytes`
- `retainedBytes`
- `collectedAt`
- `payloadRef`
- `reason`
- `link`

These fields are compact normalized-observation metadata. They let consumers audit which query and redacted observation supported the RCA without storing raw Prometheus payloads, large Loki excerpts, or unredacted Kubernetes objects in status.

When future retention adapters create external evidence payloads, `payloadRef` may record `scheme`, `digest`, `encrypted`, `expiresAt`, and `retentionClass`. `payloadRef` must not contain signed URLs, credentials, bearer tokens, inline secrets, or provider-specific authorization material. It is a verifiable pointer, not an access credential.

`queryDigest` and `contentDigest` use `digestAlgorithm: sha256` and `digestCanonicalization: fluxagent-observation-json-v1`. The v1 canonicalization contract uses deterministic JSON object keys, preserves array order unless a field-specific contract says otherwise, normalizes strings to Unicode NFC, excludes non-semantic collection timestamps from content digests, and expects timestamp producers to write UTC timestamps.

Status budget limits are intentionally conservative:

- `maxEvidenceRefs`: 32
- `maxClaims`: 16
- `maxEvidenceSummaryBytes`: 1024
- `maxClaimStatementBytes`: 1024
- `maxSummaryBytes`: 2048
- `maxStatusBytes`: 65536

When status budget enforcement is required, FluxAgent preserves canonical state before descriptive detail: `phase`, `outcome`, `failure.code`, verdict, execution identity, degradation metadata, evidence digests, claim IDs, claim verification, and truncation metadata have priority over long summaries, extra evidence refs, and extra claims.

`status.failure` records workflow failure details when an investigation cannot reach a completed RCA state:

- `code`: stable machine-readable reason
- `message`: human-readable detail
- `stage`: workflow stage such as `Validation`, `TargetResolution`, `EvidenceCollection`, or `Reasoning`
- `retryable`: whether a later reconcile or recreated request may reasonably succeed without a spec change

`status.alternativeHypotheses[]`, `status.missingEvidence[]`, and `status.degradation` are reserved for partial-failure and claim-hardening semantics. They let FluxAgent report uncertainty explicitly instead of silently presenting an incomplete RCA as fully proven. `status.degradation.reasons[]` uses structured `code`, `stage`, optional `sourceRef`, and `message` fields.

`status.execution` records RCA execution metadata:

- `id`
- `state`
- `provider`
- `providerRef`
- `providerGeneration`
- `providerType`
- `model`
- `rcaSchemaVersion`
- `canonicalizationVersion`
- `reasoningPolicyVersion`
- `controllerVersion`
- `attemptCount`
- `attempts[]`
- `durationSeconds`
- `inputTokens`
- `outputTokens`
- `providerResult`

`status.execution.id` is the logical execution identity for the RCA input. It is derived from the request generation, target identity available to the controller, compact evidence digest, provider identity, RCA schema version, canonicalization version, and reasoning policy version. `status.execution.attempts[]` records FluxAgent attempt identity, provider request IDs when adapters expose them, idempotency keys generated by FluxAgent, result, retry reason, and attempt timestamps. Provider request IDs are correlation metadata; they are not execution identity.

`status.execution.providerResult` is the durable normalized provider checkpoint. It stores the common RCA result, schema version, and digest used by FluxAgent after provider response parsing and validation. It does not store raw provider responses, prompts, chain-of-thought, or unclassified provider metadata.

FluxAgent guarantees persisted-completion idempotency: if the same `execution.id` already has a persisted `ProviderCompleted` or `Finalized` provider result, the controller reuses that normalized result instead of calling the provider again. This is not an exactly-once external invocation guarantee. If the controller crashes or loses a status write after provider success but before `ProviderCompleted` is persisted, a later reconcile may call the provider again unless provider-side idempotency is available and honored.

The v0.3 checkpoint state model is `NotStarted -> AttemptPrepared -> ProviderInFlight -> ProviderCompleted -> Verified -> Finalized`. The current controller persists the first durable cut at `ProviderCompleted` after parsing, normalization, validation, digesting, and status persistence succeed.

`status.phase` describes workflow lifecycle. `status.outcome` and `status.verdict.outcome` describe RCA result semantics.

Canonical lifecycle phases for `InvestigationRequest` are:

- `Pending`
- `Collecting`
- `Reasoning`
- `Verifying`
- `Completed`
- `Failed`

`Observed` is retained only as a compatibility phase for older resources and non-InvestigationRequest controllers.

Legal terminal combinations are:

- `phase: Completed`, `outcome: Confirmed`
- `phase: Completed`, `outcome: Inconclusive`
- `phase: Completed`, `outcome: NoIssueFound`
- `phase: Failed`, `outcome: Unknown`, `failure` set

`ExecutionFailed` is deprecated as an RCA outcome. Failed datasource or provider execution is represented as `phase: Failed`, `outcome: Unknown`, and a populated `status.failure`.

Non-terminal phases should leave `outcome`, `failure`, and `completedAt` unset. `status.degradation` is orthogonal to phase and outcome; a completed investigation may still be degraded if it reached a conclusion with partial evidence.

`status.lineage` records where the investigation came from when it was created by another FluxAgent workflow. For `RiskRule` routing, it includes the source rule reference, source UID and generation, target UID, compatibility finding fingerprint, structured finding identity, and investigation depth. This lets downstream consumers trace `RiskRule -> InvestigationRequest -> optional RiskSignal` without treating `RiskSignal` as the canonical RCA surface.

Lineage in `status.lineage` is the canonical loop-prevention signal after it has been initialized. Lineage annotations are auxiliary seed metadata used for newly created requests. By default, FluxAgent blocks `RiskSignal`-sourced investigations and any request whose `investigationDepth` reaches `spec.loopPolicy.maxDepth`; blocked requests finish as `phase: Failed`, `outcome: Unknown`, with a non-retryable validation failure.

When `createRiskSignal: true` succeeds, `status.linkedRiskSignalRef` points to the emitted `RiskSignal`. The RCA itself remains on `InvestigationRequest.status`; the linked `RiskSignal` represents a materialized finding for downstream workflows, not the canonical RCA result.

If `ttlSeconds` is greater than zero, FluxAgent keeps the completed request for that many seconds after `status.completedAt`, then deletes it automatically.

Compatibility note: `status.summary`, `status.hypothesis`, `status.confidence`, and `status.provider` remain available for the v0.2 path. New consumers should prefer `status.verdict`, `status.claims`, `status.degradation`, and `status.execution`.

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
