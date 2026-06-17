# FluxAgent Architecture Mapping

This document maps the provided architecture diagrams to the code layout in this repository.

## Layer 2-1: Signal Ingestion and Context Pipeline

Code:

- `internal/ingestion/pipeline.go`

Responsibilities:

- collect source signals
- normalize metadata
- deduplicate noisy events
- build enriched incident context
- assemble evidence bundle and resource timeline

## Layer 2-2: AI Reasoning, Decision Pipeline and Model Gateway

Code:

- `internal/reasoning/engine.go`
- `internal/knowledge/base.go`

Responsibilities:

- correlate metrics, logs, traces, and events
- estimate risk and severity
- draft RCA summary
- retrieve runbooks and references
- propose remediation actions
- keep provider abstraction separate from business logic

## Layer 2-3: Kubernetes-native CRD Control Loop

Code:

- `api/v1alpha1/types.go`
- `internal/controlplane/orchestrator.go`

CRDs:

- `RiskSignal`
- `RemediationPlan`
- `AgentAction`

The current scaffold materializes these resources in memory to make the end-to-end flow testable before wiring real reconcilers.

## Layer 2-4: Guardrails, Policy and Approval Flow

Code:

- `internal/guardrails/engine.go`

Responsibilities:

- allowlist and denylist validation
- blast-radius and severity checks
- auto-approve low-risk actions
- require approval for medium and high risk
- reject unsafe or unsupported actions
- preserve audit-ready reasoning

## Layer 2-5: Action Executor

Code:

- `internal/executor/router.go`

Executors:

- Kubernetes executor
- GitOps executor
- Runbook executor

Each executor returns execution metadata instead of mutating real infrastructure. That keeps the project safe while preserving the contract surface for future integrations.
