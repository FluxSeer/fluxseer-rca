# `AgentAction` Reference

`AgentAction` is the guarded execution contract in FluxAgent.

## API

- Group: `aiops.platform`
- Version: `v1alpha1`
- Kind: `AgentAction`

Source schema: [api/v1alpha1/types.go](../../api/v1alpha1/types.go)

## Purpose

Represent one executable action after policy review and, when required, human approval.

## YAML Schema

### `spec`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `spec.target` | object | yes | Execution target. |
| `spec.actionType` | string | yes | Executor route key such as `kubernetes.rolloutPause`. |
| `spec.parameters` | object | no | Backend-specific parameters. |
| `spec.approvedBy` | string | no | Approval identity. Empty means execution must wait. |
| `spec.dryRunResult` | string | no | Pre-execution validation or policy result. |
| `spec.ttlSeconds` | integer | no | Lifecycle hint. |
| `spec.rollbackPlan` | array | no | Rollback steps attached for auditability. |

### `spec.target`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cluster` | string | no | Cluster identifier. |
| `namespace` | string | no | Kubernetes namespace. |
| `kind` | string | no | Target resource kind. |
| `name` | string | no | Target resource name. |
| `apiVersion` | string | no | Kubernetes API version. |
| `service` | string | no | Logical service name. |

### `status`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `status.phase` | string | no | Current execution lifecycle phase. |
| `status.message` | string | no | Human-readable execution summary or error. |
| `status.observedGeneration` | integer | no | Last generation observed by the controller. |
| `status.updatedAt` | string | no | Timestamp of the latest status update. |

## Field Notes

### `spec.target`

The execution target.

### `spec.actionType`

Execution route key. Current prefixes:

- `kubernetes.*`
- `gitops.*`
- `runbook.*`
- `notification.*`

### `spec.parameters`

Backend-specific parameters passed to the executor.

### `spec.approvedBy`

Approval identity. If this is empty, the reconciler keeps the object in `WaitingApproval`.

### `spec.dryRunResult`

Guardrails or validation output that explains what was checked before execution.

### `spec.ttlSeconds`

Lifecycle hint copied from the plan.

### `spec.rollbackPlan`

Rollback instructions attached to the action for auditability.

Typical phases:

- `WaitingApproval`
- `Approved`
- `Executing`
- `Succeeded`
- `Failed`
- `Rejected`

## Execution Model

`AgentActionReconciler` sends approved actions to the executor router. In the current repo, most executors simulate the result and persist a status summary.

## Sample

See [config/samples/agent-action.yaml](../../config/samples/agent-action.yaml).
