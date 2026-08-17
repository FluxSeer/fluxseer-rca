# Executor Safety Contract

This document defines the v0.5 execution boundary before FluxSeer adds a real
Kubernetes or GitOps side effect. The contract is intentionally backend
neutral: the controller owns authorization and lifecycle; an executor owns a
bounded side effect and its durable result.

## v0.5-alpha.1 Boundary

The first release checkpoint proves one path only:

```text
Approved AgentAction
        -> policy revalidation
        -> allowlisted Kubernetes mutation
        -> post-action Verification InvestigationRequest
        -> Effective / Ineffective / Regressed / Inconclusive
```

GitOps uses the same contract in a later v0.5 increment. Runbook execution and
arbitrary autonomous mutation are not covered by this design.

Implementation status: the typed request/result contract, deterministic
identity guard, and the gated Kubernetes Deployment restart backend are now
present. Effectiveness verification remains the next alpha.1 batch.

## Ownership Boundary

| Concern | Owner | Rule |
| --- | --- | --- |
| Approval authority | `RemediationPlanReconciler` / controller-owned status | A writable spec field or model output never authorizes execution. |
| Policy revalidation | controller before dispatch | Current action digest, target, and policy snapshot must still match the approved decision. |
| Side effect | backend executor | Only the selected allowlist action may be performed. |
| Retry and duplicate handling | controller plus executor | Retry uses the same idempotency key; uncertain outcomes are looked up, never blindly replayed. |
| Execution result | executor, persisted by controller | Result includes identity, outcome, failure reason, and target metadata; no raw secrets or payloads. |
| Effectiveness | follow-up investigation workflow | Execution success is not remediation success. |

## Target Interface

Batch 1 now provides the typed request/result starting point in
`internal/executor/router.go`:

```go
type Executor interface {
    Name() string
    Execute(ctx context.Context, request ExecutorRequest) (ExecutorResult, error)
}
```

The contract is intentionally additive: the current shape already carries
execution identity, idempotency, policy snapshot, timeout, attempt, outcome,
failure, timing, external reference, and retryability fields. Batch 2 now
derives deterministic identities and guards against re-dispatch while an
identity-bearing execution is already in progress. The target shape still
adds backend capability declaration and lookup-based lifecycle recovery
without allowing backend code to bypass policy:

```go
type ExecutorRequest struct {
    ExecutionID            string
    IdempotencyKey         string
    ActionDigest           string
    ActionType             string
    ActionIndex            int
    Target                 domain.ResourceRef
    TargetUID              string
    Parameters             map[string]any
    ApprovedPolicySnapshot PolicySnapshot
    Timeout                time.Duration
    Attempt                int
}

type ExecutorResult struct {
    ExecutionID  string
    Outcome      ExecutionOutcome
    FailureReason string
    StartedAt    time.Time
    FinishedAt   time.Time
    ExternalRef  string
    Retryable    bool
}

type Executor interface {
    Name() string
    Supports(actionType string) bool
    Execute(ctx context.Context, request ExecutorRequest) (ExecutorResult, error)
}
```

The `Supports` method and lookup-based recovery remain future additions.
`Router` remains responsible for route selection and the controller remains
responsible for approval, policy revalidation, status updates, and verification
orchestration.

## Identity and Idempotency

The execution identity must remain stable across reconciles and change when
the desired action changes:

```text
actionDigest = digest(AgentAction.spec, excluding approvedBy)
executionID = sha256(AgentAction.uid | generation | actionDigest | executor)
idempotencyKey = sha256(AgentAction.uid | generation | actionDigest | actionType)
```

The exact serialization and hash version must be named in the audit record.
The existing approval digest is the starting input; it is not sufficient by
itself because the executor also needs backend and attempt identity.

Required behavior:

- same key and same request returns or observes the existing result;
- a changed spec or generation produces a new key and requires revalidation;
- a timeout after dispatch enters an uncertain state and must be resolved by
  backend lookup using the same key;
- no executor may generate a fresh key merely because the controller
  reconciled again.

## Safety Inputs

Before dispatch, the controller must establish all of the following:

- approval status is controller-owned and digest-bound;
- the action type is in the backend allowlist;
- namespace, kind, name, and API version match the policy target;
- policy and threshold generations/digests still match the approval snapshot;
- parameters pass action-specific validation and do not widen the target;
- a positive timeout and bounded retry policy are available;
- the action has an execution identity, idempotency key, and audit context.

Any failed check is terminal for that execution attempt and must not call the
backend.

## Execution State Machine

`AgentAction.status.phase` remains the coarse Kubernetes resource lifecycle.
`AgentAction.status.execution.phase` carries the executor state needed by the
alpha contract.

```text
WaitingApproval
      |
      v
Approved -> PolicyRevalidationFailed
      |
      v
Validating -> ValidationFailed
      |
      v
Executing
   |       |            |
   v       v            v
Succeeded  Failed       Unknown
   |       (terminal)   (lookup; no blind retry)
   v
VerificationPending
   |          |          |             |
   v          v          v             v
Effective  Ineffective  Regressed  Inconclusive
```

`PartiallySucceeded` is an execution outcome, not permission to retry. It
must include completed sub-operations and a backend resume/lookup identity;
until the controller can safely resume it, it is treated as terminal for the
current action and requires operator review.

The existing `AgentAction` coarse phases remain compatible during migration:

- approval and validation failures do not dispatch a backend;
- `Executing -> Succeeded` means only that the executor completed its side
  effect;
- `Executing -> Failed` includes a stable terminal failure reason;
- `Succeeded` creates or links verification rather than writing a permanent
  `NotVerified` success as the final v0.5 result.

## Retry, Timeout, and Failure Semantics

| Situation | State | Retry rule |
| --- | --- | --- |
| validation or policy rejection | `ValidationFailed` | No backend call; fix spec or policy and create a new generation. |
| timeout before dispatch | `Failed` | Retry may use the same key within budget. |
| retryable backend error before side effect | `Failed`/retryable | Retry with the same key and bounded backoff. |
| timeout or connection loss after dispatch | `Unknown` | Lookup by idempotency key; never issue a blind second mutation. |
| all sub-operations complete | `Succeeded` | Start verification. |
| some sub-operations complete | `PartiallySucceeded` | No automatic replay; persist completed work and require a safe resume decision. |
| permanent backend rejection or exhausted retry budget | `Failed` | Persist terminal reason and stop. |

Terminal failure reasons must be machine-readable, for example:
`PolicyRevalidationFailed`, `TargetValidationFailed`, `UnsupportedAction`,
`PreconditionFailed`, `TimeoutBeforeDispatch`, `TimeoutAfterDispatch`,
`RetryExhausted`, `BackendRejected`, `PartialMutation`, and
`UnknownOutcome`.

## Effectiveness Verification

The verification workflow must capture a baseline before mutation and a
follow-up `InvestigationRequest` after the action. The follow-up is correlated
to the `AgentAction` through the execution identity and a durable
`status.effectiveness.verificationRef`.

| Outcome | Meaning |
| --- | --- |
| `Effective` | The targeted incident signal cleared within the verification window and evidence supports the change. |
| `Ineffective` | The targeted signal remains or evidence shows the action did not address the cause. |
| `Regressed` | The signal cleared, then returned during the observation window. |
| `Inconclusive` | Evidence, timing, or datasource availability is insufficient to decide. |

The executor must not calculate these outcomes from a local API success
alone. They belong to the verification investigation and are recorded
separately from `status.execution.phase`.

## Audit Event Model

The alpha audit record must be sufficient to answer:

1. What exact action was approved?
2. Under which policy and generations was it revalidated?
3. Which executor identity and idempotency key were used?
4. What target and precondition were observed?
5. Was the outcome complete, partial, failed, or uncertain?
6. Which verification investigation produced the effectiveness result?

Minimum event fields:

```text
eventType
eventTime
agentActionRef
executionID
idempotencyKey
actionDigest
actionType
targetRef
executor
attempt
policySnapshot
outcome
failureReason
verificationRef
```

Kubernetes Events may mirror lifecycle transitions, but they are not the
durable audit store because cluster event retention is bounded.

## Alpha.1 Acceptance Tests

- the same approved action reconciled repeatedly invokes the backend at most
  once for the same idempotency key;
- a changed action digest, target, generation, or policy snapshot cannot reuse
  the old approval;
- an allowlist or target validation failure produces no Kubernetes mutation;
- timeout-before-dispatch and timeout-after-dispatch produce different failure
  handling;
- a partial mutation is persisted and is not blindly replayed;
- a successful mutation creates a linked verification investigation;
- verification can produce all four outcomes;
- the default read-only installation remains unchanged.
