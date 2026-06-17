# FluxAgent

A Kubernetes-native AI SRE Agent Operator for proactive risk detection, RCA assistance, and guarded remediation.

FluxAgent is an open, provider-neutral, Kubernetes-native AI SRE agent scaffold based on the layered architecture in the design diagrams.

It is intentionally built as a pragmatic starting point:

- `internal/ingestion` turns raw telemetry into AI-ready incident context.
- `internal/reasoning` produces risk, RCA, and remediation candidates.
- `api/v1alpha1` models the Kubernetes-native control loop with `RiskSignal`, `RemediationPlan`, and `AgentAction`.
- `internal/guardrails` enforces policy, risk thresholds, dry-run, and approval routing.
- `internal/executor` routes approved actions to Kubernetes, GitOps, runbook, or notification executors.

## Core Principles

- Kubernetes-native: CRD + Controller + Reconcile Loop
- Read-only first: AI does not directly mutate production by default
- Provider-neutral: OpenAI, Claude, Gemini, Bedrock, Local Model
- Observability-native: Prometheus, Loki, Kubernetes Events, OpenTelemetry
- Guardrails-first: policy, dry-run, approval and audit before execution
- GitOps-first: prefer pull requests over direct production patching

## Project Layout

- `cmd/fluxagent`: runnable demo that simulates the full pipeline.
- `cmd/manager`: canonical controller-runtime manager entrypoint.
- `api/v1alpha1`: CRD root types and status phases.
- `internal/datasource`: adapter interfaces and datasource implementations.
- `internal/model`: provider-neutral model gateway abstractions.
- `config/samples`: example custom resources.
- `docs/architecture.md`: mapping between the diagram and the codebase.
- `internal/controlplane`: orchestration between ingestion, reasoning, guardrails, and execution.

## Quick Start

```bash
cd FluxAgent
GOWORK=off go run ./cmd/fluxagent
GOWORK=off go test ./...
```

## Operator Mode

To run FluxAgent as a Kubernetes operator:

```bash
cd FluxAgent
GOWORK=off go run ./cmd/manager
```

By default, the manager runs in `v0.1 read-only` mode:

- watches annotated Deployments
- queries Kubernetes Events
- optionally queries Prometheus and Loki when URLs are configured
- creates `RiskSignal`
- sends webhook notifications
- does not create `RemediationPlan` or `AgentAction` unless `--enable-remediation=true`

To deploy the scaffolded operator resources into a cluster:

```bash
cd FluxAgent
kubectl create namespace fluxagent-system
kubectl apply -k config/default
```

To run a local kind-based demo:

```bash
cd FluxAgent
make demo-up
make inject-fault
make demo-status
make demo-down
```

## Current Scope

This repository is a compileable foundation, not a production operator yet. The current implementation focuses on:

- data model and control-loop shape
- policy-driven approvals
- executor routing
- auditable end-to-end demo flow
- controller-runtime manager and CRD reconcilers
- datasource and model adapter interfaces for OSS extensibility
- read-only `RiskSignal` detection flow with Prometheus, Loki, Kubernetes Events, and webhook notification

## Next Milestones

1. Turn read-only `RiskSignal` generation into the default v0.1 install path.
2. Add real Prometheus, Loki, and Kubernetes Events connectors behind the datasource interfaces.
3. Add model gateway schema validation, rate limits, and secret-driven provider config.
4. Add GitOps PR flows, approval webhooks, and safer executor backends.
