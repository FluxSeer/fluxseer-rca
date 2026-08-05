# `RiskSignal` Reference

`RiskSignal` is the core read-only output of FluxAgent `v0.1`.

## API

- Group: `aiops.platform`
- Version: `v1alpha1`
- Kind: `RiskSignal`

Source schema: [api/v1alpha1/types.go](../../api/v1alpha1/types.go)

## Purpose

Capture a detected workload risk with enough context for notification, review, and optional downstream planning.

## YAML Schema

### `spec`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `spec.target` | object | yes | Target workload or resource reference. |
| `spec.signalType` | string | yes | Semantic finding type such as event, logs, or metric regression. |
| `spec.findingIdentity` | object | no | Structured finding and incident occurrence identity used for deduplication, correlation, and lineage. |
| `spec.actionType` | string | no | Suggested downstream notification or planning contract. |
| `spec.severity` | string | yes | Severity string used by guardrails and downstream planning. |
| `spec.confidence` | integer | yes | Detection confidence score from `0` to `100` for the merged finding. |
| `spec.dryRun` | boolean | yes | Whether the signal is intended for non-mutating handling. |
| `spec.ttlSeconds` | integer | no | Lifecycle hint for retention or cleanup. |
| `spec.evidence` | array | no | List of evidence records attached to the signal. |
| `spec.parameters` | object | no | Optional key-value metadata for downstream consumers. |

### `spec.target`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cluster` | string | no | Cluster identifier. |
| `namespace` | string | no | Kubernetes namespace. |
| `kind` | string | no | Resource kind such as `Deployment`. |
| `name` | string | no | Resource name. |
| `apiVersion` | string | no | Kubernetes API version such as `apps/v1`. |
| `service` | string | no | Logical service name used by correlation and runbooks. |

### `spec.evidence[]`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `kind` | string | no | Evidence type such as `metric`, `log`, or `event`. |
| `source` | string | no | Datasource that produced the evidence. |
| `summary` | string | no | Human-readable summary. |
| `query` | string | no | Query used to retrieve the evidence. |
| `classification` | object | no | Computed compact data classification inherited from the source evidence. |
| `reason` | string | no | Event or classification reason. |
| `link` | string | no | External reference URL or logical link. |

### `status`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `status.phase` | string | no | Current lifecycle phase. |
| `status.message` | string | no | Human-readable status message. |
| `status.observedGeneration` | integer | no | Last generation observed by the controller. |
| `status.updatedAt` | string | no | Timestamp of the latest status update. |
| `status.rcaSummary` | string | no | High-level RCA summary persisted from the reasoning pipeline. |
| `status.rcaHypothesis` | string | no | Primary RCA hypothesis. |
| `status.rcaProvider` | string | no | `ModelProvider` name used for RCA generation. |
| `status.rcaCauses` | array | no | Ranked cause candidates with integer confidence scores from `0` to `100`. |
| `status.conditions` | array | no | Condition-based readiness for evidence collection and RCA enrichment. |

## Field Notes

### `spec.target`

Target workload reference used by notification, remediation planning, and execution routing.

### `spec.signalType`

Semantic type of the finding, for example:

- `workload.kubernetes_event`
- `workload.error_logs`
- `rollout.latency_regression`

### `spec.actionType`

Suggested follow-up notification or planning action type. In read-only mode this is contract metadata, not proof that execution happened. Existing `notification.sendSlack` values are a legacy notification alias and may be backed by a generic webhook sink; consumers should not treat the value as proof of a Slack-specific provider.

### `spec.severity`

Current severity strings used by the repo:

- `low`
- `medium`
- `high`
- `unsafe`

### `spec.confidence`

Integer detection confidence score from `0` to `100` for the merged finding. This is a heuristic or provider-derived ranking score, not a calibrated probability that the RCA is correct. It does not represent root-cause confidence; use `RCAReady`, `RemediationReady`, and canonical `InvestigationRequest.status.verdict` fields to decide whether RCA or remediation results are consumable.

> **Scale note:** this `0`-`100` integer scale is not the same as the canonical `InvestigationRequest.status.verdict.confidence` score, which is normalized `0.0`-`1.0`. Do not compare the raw values across CRDs without converting scales first.

### `spec.dryRun`

Whether the generated signal is intended for non-mutating or review-first handling. `v0.1` generated signals use `true`.

### `spec.ttlSeconds`

Time-to-live in seconds for `RiskSignal` retention.

Current controller behavior:

- if `ttlSeconds` is greater than zero, FluxAgent requeues the signal for expiry
- when the TTL window elapses, FluxAgent deletes the `RiskSignal`
- when remediation is enabled, owner-referenced downstream `RemediationPlan` resources are cleaned up with it

### `spec.evidence`

Evidence is intentionally lightweight. It holds enough metadata to explain why the signal exists without forcing large raw payloads into the CRD.

Recommended persisted evidence fields:

- datasource type and name
- query type, query digest, and time range
- compact summary
- resource references
- content digest
- digest algorithm and canonicalization version
- redaction profile
- computed classification level and sensitivity tags
- truncation metadata
- original and retained byte counts
- collection timestamp

Evidence digests use the same `sha256` / `fluxagent-observation-json-v1` contract as `InvestigationRequest.status.evidenceRefs`.

`spec.evidence[].classification` follows the same compact classification summary as `InvestigationRequest.status.evidenceRefs[]`: ordered `level`, `sensitivityTags[]`, `source`, and `policyVersion`. It helps downstream notification and review tools understand the data boundary without storing raw evidence.

Fields that should not be persisted:

- full Prometheus payloads
- large Loki log bodies
- full model prompts
- provider raw responses
- secrets, tokens, or authorization headers
- unredacted Kubernetes objects

### `spec.findingIdentity`

`spec.findingIdentity` is present when the signal is materialized from `RiskRule` routing or promoted from an `InvestigationRequest` that carries lineage. It separates three related identities:

- `objectFindingIdentity`: precise deduplication using source UID, target UID, finding type, and normalized evidence attributes.
- `logicalFindingIdentity`: dashboard and long-term correlation using apiVersion/kind/namespace/name references instead of object UIDs.
- `incidentOccurrence`: per-incident identity using object finding identity, source generation, target generation, and the rounded evidence window bucket.

### `spec.investigationRef` And RCA Projection

`spec.investigationRef` is set when a `RiskSignal` is materialized from a canonical `InvestigationRequest`. It points consumers back to the authoritative RCA execution record.

`status.projection` describes how the RCA compatibility fields were produced:

- `mode: InvestigationRequestProjection`: `RiskSignal` contains a compact projection of `InvestigationRequest.status`.
- `mode: DirectRiskSignalCompatibility`: direct `RiskRule` RCA wrote compatibility fields without a canonical `InvestigationRequest`.
- `projectedFrom`: namespaced reference to the canonical `InvestigationRequest` when available.
- `compatibilityPath`: `true` for legacy/direct RCA paths.

`status.rcaSummary` and other RCA compatibility fields remain projections. In the canonical v0.3 path, the complete RCA result is owned by `InvestigationRequest.status`; `RiskSignal` stores the materialized finding, lineage, compact evidence references, notification state, and compatibility projection fields required by the v0.2 path.

### `spec.parameters`

Optional key-value parameters carried into downstream planning.

Typical phases:

- `Confirmed`
- `Notified`
- `ReadyForApproval`

Current condition types:

- `FindingReady`
- `EvidenceCollectionReady`
- `RCAReady`
- `RemediationReady`

`RCAReady=True` means an RCA result is available. It does not indicate that the target workload is healthy, recovered, or remediated.

`status.phase` describes the finding lifecycle only. For example, `phase: Confirmed` means the `RiskSignal` finding was confirmed or materialized. It does not mean the RCA is verified. Consumers should use `RCAReady` for RCA verification and `RemediationReady` before starting remediation workflows.

## Sample

See [config/samples/risk-signal.yaml](../../config/samples/risk-signal.yaml).
