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
| `spec.actionType` | string | no | Suggested downstream action contract. |
| `spec.severity` | string | yes | Severity string used by guardrails and downstream planning. |
| `spec.confidence` | integer | yes | Confidence score from `0` to `100` for the merged finding. |
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

Suggested follow-up action type. In read-only mode this is contract metadata, not proof that execution happened.

### `spec.severity`

Current severity strings used by the repo:

- `low`
- `medium`
- `high`
- `unsafe`

### `spec.confidence`

Integer confidence score from `0` to `100` for the merged finding. This is a heuristic or provider-derived ranking score, not a calibrated probability that the RCA is correct.

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
- redaction profile
- truncation metadata
- collection timestamp

Fields that should not be persisted:

- full Prometheus payloads
- large Loki log bodies
- full model prompts
- provider raw responses
- secrets, tokens, or authorization headers
- unredacted Kubernetes objects

### `spec.parameters`

Optional key-value parameters carried into downstream planning.

Typical phases:

- `Confirmed`
- `Notified`
- `ReadyForApproval`

Current condition types:

- `EvidenceCollectionReady`
- `RCAReady`

`RCAReady=True` means an RCA result is available. It does not indicate that the target workload is healthy, recovered, or remediated.

## Sample

See [config/samples/risk-signal.yaml](../../config/samples/risk-signal.yaml).
