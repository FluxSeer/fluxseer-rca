# Use Subscription Codex For RCA Analysis

This tutorial describes the homelab path for using an existing Codex subscription with FluxAgent RCA output.

This path is intentionally separate from `AgentExecutor` Kubernetes Jobs.

## When To Use This Path

Use this when:

- you want to reuse an existing Codex subscription
- you have a trusted persistent self-hosted runner
- you do not want to create `codex-secret` or use `CODEX_API_KEY`
- you want second-pass analysis after FluxAgent has already produced RCA

Do not use this for shared production automation unless the runner identity, Codex account, repository access, and Kubernetes access are intentionally governed.

## Architecture

```text
FluxAgent RiskSignal / RCA
-> manual workflow_dispatch
-> persistent self-hosted runner
-> logged-in Codex CLI
-> read-only Kubernetes context and repository checkout
-> Markdown RCA analysis artifact or GitHub issue
```

The controller does not run Codex CLI and does not receive a Codex session token.

## Runner Requirements

Create or choose a persistent runner with this label:

```yaml
runs-on: [self-hosted, Linux, X64, fluxagent-codex-subscription]
```

The runner must have:

- `codex`
- `kubectl`
- read-only kubeconfig for the target cluster
- access to this repository
- persistent home directory for Codex auth state

One-time setup on the runner:

```sh
npm install -g @openai/codex
codex login
codex exec "Say ready"
kubectl --context admin@homelab-test get ns
```

Do not copy `~/.codex/auth.json` into Kubernetes Secrets, images, GitHub secrets, or Git.

## Run The Workflow

Use the GitHub Actions workflow:

```text
FluxAgent Codex RCA
```

Inputs:

```text
risk_signal_namespace: fluxagent-test
risk_signal_name: <RiskSignal name>
cluster_context: admin@homelab-test
create_issue: false
```

The workflow collects:

- the `RiskSignal` YAML and JSON
- target workload YAML
- target workload `describe`
- recent namespace events
- repository context

Then it runs:

```sh
codex exec "<bounded RCA prompt>"
```

The result is uploaded as a workflow artifact and written into the GitHub step summary. If `create_issue=true`, the workflow creates a GitHub issue.

## Relationship To AgentExecutor

`AgentExecutor` is the Kubernetes Job path:

```text
AgentExecutor
-> Kubernetes Job
-> CODEX_API_KEY / enterprise token / workload credential
```

The subscription runner path is different:

```text
persistent runner
-> interactive Codex login already present
-> no CODEX_API_KEY required
```

Both paths can coexist. Use subscription runner mode for personal homelab workflows, and keep `AgentExecutor` for workload-scoped automation.
