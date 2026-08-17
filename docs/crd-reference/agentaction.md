# `AgentAction` Reference

`AgentAction` is the guarded execution contract in FluxSeer RCA.

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
| `spec.approvedBy` | string | no | Approval identity supplied by a human reviewer. On its own this does not authorize execution; see [`spec.approvedBy`](#specapprovedby) below. |
| `spec.dryRunResult` | string | no | Pre-execution validation or policy result. |
| `spec.ttlSeconds` | integer | no | Lifecycle hint. |
| `spec.approvalTimeoutSeconds` | integer | no | Optional timeout for human approval; a positive value enables escalation. |
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
| `status.approval` | object | no | Controller-observed approval state, including decision, approval, escalation, timestamps, source, generation, and action digest. |
| `status.notification` | object | no | Escalation notification attempt state. |
| `status.dryRunResult` | object | no | Controller-owned dry-run or guardrail result. |
| `status.execution` | object | no | Executor phase/outcome, execution and idempotency identity, attempt, failure reason, executor name, timing, external reference, retryability, and summary. |
| `status.effectiveness` | object | no | Post-action effectiveness status. `NotVerified` means execution succeeded but remediation impact was not verified. |

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

Approval identity supplied by a human reviewer. If empty, the reconciler keeps the object in `WaitingApproval`.

**This field alone does not authorize execution.** `spec.approvedBy` is a plain, user-writable spec field — a principal with RBAC write access to `AgentAction` can set it to anything without ever going through `RemediationPlan` or `guardrails.Engine`. The reconciler instead trusts `status.approval`, which only `RemediationPlanReconciler` writes after actually evaluating the action, and requires `status.approval.actionDigest` to match a digest recomputed from the current spec (excluding `approvedBy` itself) before treating the action as approved:

- `status.approval.approved: true` (source `GuardrailsAutoApproval`) — guardrails auto-approved it; `spec.approvedBy` is not consulted.
- `status.approval.source: ManualApprovalRequired` plus a non-empty `spec.approvedBy` — guardrails required human approval for this exact action content (proven by the digest match) and a reviewer supplied one; the reconciler then executes and records the confirmation as `ManualApprovalConfirmed`.
- Anything else — including an `AgentAction` created directly without ever going through `RemediationPlan`, or one whose spec changed after `status.approval` was written — is treated as unapproved and stays in `WaitingApproval`, regardless of what `spec.approvedBy` says.

RBAC implication: only principals who should be trusted to approve remediation need write access to the `agentactions/status` subresource (normally just the controller's own ServiceAccount); write access to the base `agentactions` resource does not by itself grant approval power.

### `spec.dryRunResult`

Guardrails or validation output that explains what was checked before execution.

This field remains a compatibility projection. Controller-owned dry-run observations are written to `status.dryRunResult`.

### `spec.ttlSeconds`

Lifecycle hint copied from the plan.

### `spec.approvalTimeoutSeconds`

Optional human-approval timeout copied from the remediation plan. Zero or
unset preserves the legacy behavior and does not schedule escalation. A
positive value causes a still-pending action to transition to `Escalated`
after the deadline. Escalation notifies through the configured notifier but
does not auto-reject or auto-execute the action.

### `status.approval`

The approval status is controller-owned and digest-bound to the action spec.

| Field | Meaning |
| --- | --- |
| `decidedAt` | Time at which the policy engine made the initial decision. |
| `decidedBy` | Policy actor, currently `fluxseer-rca-policy-engine`. |
| `source` | Decision source such as `GuardrailsAutoApproval` or `ManualApprovalRequired`. |
| `approvedBy` | Human or policy identity associated with approval. |
| `approvedAt` | Time at which approval was accepted. |
| `escalatedAt` | Time at which the pending approval entered `Escalated`. |

For a timed-out action, the durable audit sequence is:

```text
status.approval.decidedAt
  -> status.approval.escalatedAt
  -> status.approval.approvedAt       # only if a human later approves
```

`DecidedAt` and `DecidedBy` are not rewritten during escalation or later
approval.

### `status.notification`

| Field | Meaning |
| --- | --- |
| `lastAttemptAt` | Most recent notification attempt, successful or failed. |
| `retryCount` | Total number of notification attempts. |
| `lastError` | Most recent failed error; cleared after a successful attempt. |

Failed attempts emit a Kubernetes `Warning` Event with reason
`NotificationRetryFailed`. There is currently no retry-exhausted limit.

### `spec.rollbackPlan`

Rollback instructions attached to the action for auditability.

Typical phases:

- `WaitingApproval`
- `Approved`
- `Escalated`
- `Executing`
- `Succeeded`
- `Failed`
- `Rejected`

## Execution Model

`AgentActionReconciler` sends approved actions to the executor router. In the current repo, most executors simulate the result and persist a status summary.

Execution success is recorded separately from remediation effectiveness:

```text
status.execution.phase=Succeeded
status.effectiveness.phase=NotVerified
```

`Succeeded` means the executor completed the requested action. It does not mean the underlying incident was resolved.

The v0.5 contract adds these execution fields to `status.execution`:

| Field | Meaning |
| --- | --- |
| `executionID` | Stable identity for the execution record. |
| `idempotencyKey` | Stable identity used to prevent duplicate backend side effects. |
| `outcome` | Backend outcome such as `Succeeded`, `Failed`, `TimedOut`, or `Unknown`. |
| `failureReason` | Machine-readable terminal or diagnostic reason. |
| `startedAt` / `finishedAt` | Backend execution timing. |
| `externalRef` | Backend-side reference, when one exists. |
| `retryable` | Whether the result may be retried under the controller's bounded policy. |

Batch 1 defines and persists this shape. Batch 2 will populate deterministic
execution/idempotency identities and enforce the lifecycle semantics.

The current `v0.4.0-beta.3` controller stops at `NotVerified`. The v0.5
`Safe Remediation` target is defined in the
[Executor safety contract](../architecture/executor-safety-contract.md): a
successful execution must create or link a follow-up `InvestigationRequest`
and resolve effectiveness as `Effective`, `Ineffective`, `Regressed`, or
`Inconclusive`. These are planned v0.5 semantics, not current beta capability.

## Kubernetes Events

Events are emitted only after a phase status update succeeds and only when
the phase actually changes. Main lifecycle reasons include
`ApprovalRequired`, `ApprovalGranted`, `EscalationTriggered`,
`ApprovalDenied`, `ExecutionStarted`, `ExecutionSucceeded`, and
`ExecutionFailed`.

Events provide an operational timeline and are not permanent audit storage;
cluster retention policies may remove them. Approval timestamps in
`status.approval` are the durable contract.

## Sample

See [config/samples/agent-action.yaml](../../config/samples/agent-action.yaml).
