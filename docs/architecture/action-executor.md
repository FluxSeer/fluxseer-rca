# Action Executor

FluxAgent separates decision-making from execution. The executor layer only runs actions that have already passed policy and approval.

## Router Contract

Source: [internal/executor/router.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/executor/router.go:1)

The router dispatches by action prefix:

- `kubernetes.*`
- `gitops.*`
- `runbook.*`
- `notification.*`

This keeps the `AgentAction` CRD stable while allowing multiple execution backends.

## Current Executors

### Kubernetes Executor

- simulates rollout pause, scale, or other Kubernetes-style actions
- returns execution metadata instead of mutating a live workload

### GitOps Executor

- simulates a pull request style change
- returns branch and action metadata

### Runbook Executor

- simulates workflow or SOP execution
- returns workflow metadata

### Notification Executor

- can send a real webhook when `FLUXAGENT_WEBHOOK_URL` is configured
- source: [internal/executor/notification.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/executor/notification.go:1)

## Why This Layer Exists

Without a dedicated executor layer, the controller or model logic would need to know:

- how to perform a GitOps change
- how to call a runbook system
- how to notify chat systems
- how to persist execution results

That would couple reasoning, policy, and side effects together. FluxAgent avoids that.

## Current Production Posture

The executors expose the right interfaces, but only notification has a real outbound path in the current repo. Kubernetes, GitOps, and runbook execution are still simulation-oriented.

That is intentional for `v0.1` because the project should lead with safe contracts, auditable flow, and local demoability.
