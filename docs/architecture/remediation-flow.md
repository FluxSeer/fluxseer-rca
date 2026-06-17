# Guarded Remediation Flow

This document describes the optional path that starts after a `RiskSignal` exists and `--enable-remediation=true` is enabled.

## Goal

Turn a risk observation into a reviewable remediation plan, then into a guarded `AgentAction`.

## Enablement

Remediation controllers are only active when the manager is started with:

```bash
GOWORK=off go run ./cmd/manager --enable-remediation=true
```

The wiring lives in [internal/operatorapp/run.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/operatorapp/run.go:1).

## Controller Chain

### 1. `RiskSignalReconciler`

Source: [internal/controllers/risksignal_controller.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/controllers/risksignal_controller.go:1)

Responsibilities:

- watch `RiskSignal`
- derive one `RemediationPlan`
- carry over severity, confidence, TTL, target, and evidence references
- build an initial step from `spec.actionType`

### 2. `RemediationPlanReconciler`

Source: [internal/controllers/remediationplan_controller.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/controllers/remediationplan_controller.go:1)

Responsibilities:

- evaluate the proposed step through the guardrails engine
- create one `AgentAction`
- attach dry-run result and rollback plan
- set plan and action status to `Approved`, `WaitingApproval`, or `Rejected`

### 3. `AgentActionReconciler`

Source: [internal/controllers/agentaction_controller.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/controllers/agentaction_controller.go:1)

Responsibilities:

- hold unapproved actions in `WaitingApproval`
- execute approved actions through the executor router
- persist `Executing`, `Succeeded`, or `Failed`

## Decision Boundary

The main safety boundary is in [internal/guardrails/engine.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/guardrails/engine.go:1).

Current policy behavior:

- reject action types that are not in the allowlist
- auto-approve low-severity actions
- require manual approval for medium and high severity
- require manual approval for high severity against protected namespaces
- reject unsupported or unsafe severities

Default allowlisted action types:

- `kubernetes.scaleDeployment`
- `kubernetes.rolloutPause`
- `gitops.createPullRequest`
- `runbook.triggerWorkflow`
- `notification.sendSlack`

## Status Model

Typical status progression:

1. `RiskSignal`: `Confirmed` or `Notified`
2. `RiskSignal`: `ReadyForApproval`
3. `RemediationPlan`: `Approved` or `WaitingApproval` or `Rejected`
4. `AgentAction`: `Approved` or `WaitingApproval` or `Rejected`
5. `AgentAction`: `Executing`
6. `AgentAction`: `Succeeded` or `Failed`

## Sequence Diagram

```mermaid
sequenceDiagram
    participant RS as RiskSignal
    participant RSR as RiskSignalReconciler
    participant RP as RemediationPlan
    participant RPR as RemediationPlanReconciler
    participant G as Guardrails
    participant AA as AgentAction
    participant AAR as AgentActionReconciler
    participant X as Executor Router

    RS->>RSR: reconcile
    RSR->>RP: create or update plan
    RP->>RPR: reconcile
    RPR->>G: evaluate(policy, severity, target)
    G-->>RPR: auto / manual / reject
    RPR->>AA: create or update action
    AA->>AAR: reconcile
    alt approved
        AAR->>X: execute(actionType)
        X-->>AAR: execution result
    else waiting approval
        AAR-->>AA: keep WaitingApproval
    end
```

## Important Limitation

This path exists and is testable, but the executors are still simulation-oriented. The repo exposes the contracts, policy seams, and controller flow before claiming production-grade autonomous remediation.
