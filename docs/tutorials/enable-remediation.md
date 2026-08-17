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
- `kubernetes.rolloutRestart` (the first real alpha.1 Kubernetes backend)
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

This mode demonstrates the controller flow and approval boundary. It keeps
Kubernetes, GitOps, and Runbook routes simulation-oriented unless the
experimental executor flag and matching RBAC profile are explicitly enabled:

```bash
GOWORK=off go run ./cmd/manager \
  --enable-remediation=true \
  --enable-experimental-executor=true
```

The first real Kubernetes path is the allowlisted `kubernetes.rolloutRestart`
action for a Deployment with a verified target UID. Notification remains the
only real outbound path outside that alpha.1 Kubernetes slice.
