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

Supported `spec.target.kind` values for direct `InvestigationRequest` resolution:

- `Deployment` (`apps/v1`)
- `StatefulSet` (`apps/v1`)
- `DaemonSet` (`apps/v1`)
- `ReplicaSet` (`apps/v1`)
- `Pod` (`v1`)

For workload controllers, FluxAgent merges object labels and pod-template labels when generating default metric and log queries. Template labels win when the same key appears in both places. `RiskRule` background target discovery remains a separate path and may support a narrower selector set.

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
- `evidenceRequirements.profile`: optional required evidence profile. Current profiles are `ImagePullBackOff`, `CrashLoopBackOff`, `OOMKilled`, `LatencyRegression`, and `RolloutLatencyRegression`.
- `evidenceRetention.mode`: external evidence retention mode. Current supported runtime behavior is `MetadataOnly` or `NormalizedSnapshot` with the built-in `local-filesystem` store.
- `evidenceRetention.retention`: requested external payload retention duration for retained normalized snapshots.
- `evidenceRetention.storageRef.name`: external evidence storage configuration reference. Current supported value for `NormalizedSnapshot` is `local-filesystem`.
- `evidenceRetention.encryption.required`: whether future retained payloads must be encrypted.
- `evidenceRetention.deletionPolicy`: future external payload deletion behavior, `Retain` or `Delete`.
- `evidenceRetention.accessPolicy.namespaceScoped`: whether future payload access must remain namespace-scoped.
- `queryBudget.maxTimeRange`: maximum allowed `timeRange.lookback` before query execution starts
- `queryBudget.maxQueriesTotal`: maximum total datasource queries for this investigation
- `queryBudget.maxQueriesPerSource`: maximum queries referencing the same datasource
- `queryBudget.maxConcurrentQueries`: maximum active datasource queries allowed for this investigation. Unset or zero keeps the default sequential collector; values above `1` enable bounded parallel datasource queries while preserving deterministic evidence ordering.
- `queryBudget.maxCumulativeDuration`: maximum cumulative datasource query runtime before evidence collection stops
- `queryBudget.maxCumulativeResponseBytes`: maximum cumulative datasource response payload size before evidence collection stops
- `queryBudget.resultLimits.metrics.maxSeries`: maximum retained native metric series before flattening
- `queryBudget.resultLimits.metrics.maxSamples`: maximum retained native metric samples before flattening
- `queryBudget.resultLimits.logs.maxStreams`: maximum retained native log streams before flattening
- `queryBudget.resultLimits.logs.maxEntries`: maximum retained native log entries before flattening
- `queryBudget.resultLimits.logs.maxLines`: legacy alias for `maxEntries`
- `queryBudget.resultLimits.events.maxRecords`: maximum retained Kubernetes event records, including deployment-condition records, before normalization
- `loopPolicy.maxDepth`: maximum allowed lineage depth before execution is blocked; default is `1`
- `loopPolicy.allowRiskSignalSource`: opt-in escape hatch for investigations sourced from `RiskSignal`; default is `false`

If `modelProviderRef.name` is empty, FluxAgent falls back to the built-in heuristic provider.

When an evidence requirements profile is configured, FluxAgent checks required evidence before calling the model provider. Missing required evidence produces `phase: Completed`, `outcome: Inconclusive`, `status.missingEvidence[]`, and a `RequiredEvidenceMissing` degradation reason. This is not a workflow failure.

Current required evidence profiles:

| Profile | Required Evidence |
| --- | --- |
| `ImagePullBackOff` | Kubernetes event evidence |
| `CrashLoopBackOff` | Kubernetes event evidence |
| `OOMKilled` | Kubernetes event evidence and metric evidence |
| `LatencyRegression` | Metric evidence |
| `RolloutLatencyRegression` | Metric evidence and deployment condition evidence |

If required evidence is complete and profile-specific checks find no matching abnormal signal, FluxAgent completes the request with `phase: Completed`, `outcome: NoIssueFound`, and does not call the model provider. `NoIssueFound` is only valid after required evidence is complete; inability to collect required evidence remains `Inconclusive`.

External evidence storage is disabled by default. `MetadataOnly` remains the default. `NormalizedSnapshot` is supported only with `storageRef.name: local-filesystem` and a controller runtime evidence store directory configured through `FLUXAGENT_EVIDENCE_STORE_DIR`. Snapshot payload references store only `scheme`, digest, expiry, encryption flag, and retention class; they do not expose local file paths or access credentials. `RawSnapshot` remains rejected because raw evidence retention requires a separate opt-in storage and security review.

`queryRetention.mode` controls whether rendered datasource query text is persisted in `status.evidenceRefs[]`:

| Mode | Behavior |
| --- | --- |
| unset / `DigestOnly` | Persist `queryDigest` only; do not persist rendered query text. |
| `Redacted` | Persist pattern-redacted rendered query text and `queryDigest`. |
| `Full` | Persist full rendered query text and `queryDigest`; use only when policy allows. |

The default is `DigestOnly` to reduce accidental leakage of tenant names, token-like matchers, internal hostnames, or other sensitive values embedded in query strings.

When `queryBudget` is configured, FluxAgent rejects invalid limits, excessive lookback windows, or excessive query counts during validation before contacting datasources. It stops evidence collection when cumulative datasource duration or response-byte limits are exceeded. Native result-kind limits are enforced after datasource responses are decoded and before flattening/normalization; the generic flat-record limit remains a compatibility fallback. Exceeded result limits retain bounded partial evidence, set truncation metadata, and preserve deterministic record order.

### Mode Contract

`dataSources[]` and `queries[]` represent different planning modes and are mutually exclusive at runtime.

Runtime contract:

- exactly one of `dataSources[]` or `queries[]` is set
- if both are set, the request is marked `InvalidSpec`
- if neither is set, the request is marked `InvalidSpec`
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
- `classification`
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

`classification` records the computed data boundary for the compact evidence reference:

- `level`: ordered sensitivity level, one of `Public`, `Internal`, `Confidential`, or `Restricted`
- `sensitivityTags[]`: closed tags such as `CredentialLike`, `PersonalData`, `CustomerData`, `SourceCode`, `InfrastructureMetadata`, or `SecuritySensitive`
- `source`: classification origin, such as `Default`, `Explicit`, `Inherited`, `ContentDetection`, or `RedactionPolicy`
- `policyVersion`: classification policy version, currently `fluxagent-data-classification-v1`

Normalized observations carry the same computed classification summary. When multiple evidence items contribute to an observation or provider bundle, FluxAgent uses the highest classification level and the union of sensitivity tags. Redaction does not automatically lower classification; a redacted credential-like log sample remains classified conservatively.

When retention adapters create external evidence payloads, `payloadRef` records `scheme`, `digest`, `encrypted`, `expiresAt`, and `retentionClass`. `payloadRef` must not contain signed URLs, credentials, bearer tokens, inline secrets, filesystem paths, or provider-specific authorization material. It is a verifiable pointer, not an access credential.

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
- `egressAudit`
- `egressAttempts[]`
- `providerResult`

`status.execution.id` is the logical execution identity for the RCA input. It is derived from the request generation, target identity available to the controller, compact evidence digest, provider identity, RCA schema version, canonicalization version, and reasoning policy version. `status.execution.attempts[]` records FluxAgent attempt identity, provider request IDs when adapters expose them, idempotency keys generated by FluxAgent, result, retry reason, and attempt timestamps. Provider request IDs are correlation metadata; they are not execution identity.

`status.execution.egressAudit` records the hosted-provider transmission decision without preserving sensitive payloads:

- `decision`: `Allowed` or `Rejected`
- `reason`: closed reason such as `Allowed`, `ExternalTransmissionDisabled`, `ClassificationExceeded`, `SensitivityTagDenied`, or `RedactionRequired`
- `providerType`: hosted provider family such as `openai`, `claude`, or `gemini`
- `evidenceBundleDigest`: digest of the compact, policy-filtered evidence bundle
- `evidenceKinds[]`: evidence kinds considered for transmission
- `sensitivityTagsSent[]`: sensitivity tags in the transmitted bundle when allowed
- `logSamplesIncluded`: whether log sample text was retained
- `maximumClassificationObserved`: highest classification observed in the policy-filtered bundle
- `maximumClassificationAllowed`: provider policy maximum
- `maximumClassificationSent`: highest classification actually transmitted; empty when rejected
- `classificationPolicyVersion`: classification policy version

`status.execution.egressAttempts[]` records bounded per-provider transmission decisions. It uses the same compact policy fields as `egressAudit` and adds attempt `ordinal`, `providerRef`, `providerGeneration`, and `result`. The existing `egressAudit` field remains a compatibility summary for the primary canonical provider decision.

`status.execution.providerResult` is the durable normalized provider checkpoint. It stores the common RCA result, schema version, provider request ID when available, provider result classification, and digest used by FluxAgent after provider response parsing and validation. It does not store raw provider responses, prompts, chain-of-thought, or unclassified provider metadata.

Hosted provider results inherit at least the transmitted evidence bundle classification because a provider summary may restate sensitive input. The built-in heuristic provider is local and does not require hosted-provider egress.

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

Current top-level conditions are the supported external integration surface for common workflow health checks. Consumers should prefer condition type, status, reason, and observedGeneration over provider-specific execution internals whenever possible.

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
