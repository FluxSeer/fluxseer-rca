# `RemediationPlan` Reference

`RemediationPlan` is the reviewable bridge between risk detection and executable action.

## API

- Group: `aiops.platform`
- Version: `v1alpha1`
- Kind: `RemediationPlan`

Source schema: [api/v1alpha1/types.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/api/v1alpha1/types.go:1)

## Purpose

Represent a proposed mitigation strategy with summary, references, rollback instructions, and one or more action steps.

## YAML Schema

### `spec`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `spec.target` | object | yes | Target workload or resource reference. |
| `spec.recommendedBy` | string | no | Identity of the component or provider that generated the plan. |
| `spec.severity` | string | yes | Severity inherited from the upstream risk. |
| `spec.confidence` | integer | yes | Confidence score inherited from the upstream risk. |
| `spec.dryRun` | boolean | yes | Whether this plan remains in review-first mode. |
| `spec.ttlSeconds` | integer | no | Lifecycle hint copied into downstream actions. |
| `spec.summary` | string | no | Human-readable remediation summary. |
| `spec.rollbackPlan` | array | no | Ordered rollback instructions. |
| `spec.references` | array | no | Supporting references such as runbooks or docs. |
| `spec.steps` | array | no | Ordered `RemediationStep` entries. |

### `spec.target`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cluster` | string | no | Cluster identifier. |
| `namespace` | string | no | Kubernetes namespace. |
| `kind` | string | no | Target resource kind. |
| `name` | string | no | Target resource name. |
| `apiVersion` | string | no | Kubernetes API version. |
| `service` | string | no | Logical service name. |

### `spec.steps[]`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Stable step identifier. |
| `actionType` | string | yes | Executor route key. |
| `description` | string | no | Human-readable step description. |
| `parameters` | object | no | Backend-specific parameters. |

### `status`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `status.phase` | string | no | Current approval or rejection phase. |
| `status.message` | string | no | Human-readable guardrail outcome. |
| `status.observedGeneration` | integer | no | Last generation observed by the controller. |
| `status.updatedAt` | string | no | Timestamp of the latest status update. |

## Field Notes

### `spec.target`

The workload or resource that the plan applies to.

### `spec.recommendedBy`

The planner identity, such as a controller or provider.

### `spec.severity`

Severity inherited from the originating `RiskSignal`.

### `spec.confidence`

Confidence score inherited from the upstream risk analysis.

### `spec.dryRun`

Whether the plan should still be handled in guarded or simulation-first mode.

### `spec.ttlSeconds`

Time-to-live hint copied into downstream action objects.

### `spec.summary`

Human-readable remediation summary.

### `spec.rollbackPlan`

Ordered rollback or recovery instructions.

### `spec.references`

Runbook, doc, or evidence references used to justify the plan.

### `spec.steps`

Each step is intentionally simple in `v0.1`: one route key plus parameters. The richer workflow semantics should live in higher-level planning and approval logic, not in ad hoc executor-specific schemas.

Typical phases:

- `Approved`
- `WaitingApproval`
- `Rejected`

The phase is set by the guardrails evaluation result.

## Sample

See [config/samples/remediation-plan.yaml](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/config/samples/remediation-plan.yaml:1).
