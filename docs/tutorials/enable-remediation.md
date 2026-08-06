# Enable Guarded Remediation

FluxSeer RCA ships with remediation controllers, but they are disabled by default.

## Start the Manager in Remediation Mode

```bash
GOWORK=off go run ./cmd/manager --enable-remediation=true
```

## What Changes

When remediation is enabled:

- `RiskSignalReconciler` creates `RemediationPlan`
- `RemediationPlanReconciler` runs guardrails and creates `AgentAction`
- `AgentActionReconciler` executes only approved actions

## Default Guardrail Policy

The current manager policy allowlists:

- `kubernetes.scaleDeployment`
- `kubernetes.rolloutPause`
- `gitops.createPullRequest`
- `runbook.triggerWorkflow`
- `notification.sendSlack`

Approval behavior:

- low severity: auto-approve
- medium severity: waiting for approval
- high severity: waiting for approval
- unsupported or unsafe: reject

Protected namespaces:

- `prod`
- `kube-system`

## Important Limitation

This mode demonstrates the controller flow and approval boundary, but only notification has a real outbound path today. Other executors are still simulation-oriented.
