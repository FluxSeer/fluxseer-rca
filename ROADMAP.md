# FluxAgent Roadmap

FluxAgent is being built as a Kubernetes-native AI SRE Agent Operator with a safe default path and an optional guarded remediation path.

## Positioning

Current public truth:

- FluxAgent `v0.1` is a read-only `RiskSignal` operator.
- Prometheus, Loki, and Kubernetes Events adapters are part of the runnable demo path.
- Guarded remediation exists as a controller and contract path, but is not yet production-grade autonomous repair.

## Release Track

### `v0.1`

Focus:

- read-only `RiskSignal` generation
- webhook notification
- kind demo
- CRD contracts
- adapter abstractions

Exit criteria:

- operator runs in-cluster
- annotated `Deployment` objects produce `RiskSignal`
- Prometheus, Loki, and Kubernetes Events evidence merge correctly
- webhook notification works
- repo docs are understandable to open-source users

### `v0.2`

Focus:

- strengthen guarded remediation flow
- improve approval and policy semantics
- add richer status and audit fields
- add more realistic executor backends

Target outcomes:

- better manual approval lifecycle
- stable `RemediationPlan` to `AgentAction` path
- clearer dry-run and rollback semantics
- improved test coverage around remediation controllers

### `v0.3`

Focus:

- provider-backed reasoning integration
- GitOps-first remediation backends
- richer knowledge and runbook retrieval

Target outcomes:

- configurable model provider selection
- evidence-linked RCA summaries
- GitHub or GitLab PR style execution path
- provider-neutral prompt and response auditing

### `v0.4`

Focus:

- production hardening
- multi-cluster and tenancy boundaries
- stronger governance and auth

Target outcomes:

- namespace and environment segmentation
- retry, backoff, and auth hardening for adapters
- better audit persistence
- safer operator packaging for real clusters

## Workstreams

### Control Plane

- evolve CRD schemas without breaking the read-only entry point
- formalize status transitions and cleanup behavior
- add TTL handling and lifecycle reconciliation

### Adapters

- harden Prometheus and Loki auth and retry behavior
- add better Kubernetes Event filtering
- expand OpenTelemetry and CloudWatch from scaffold to usable integration

### Reasoning

- keep heuristic mode runnable without secrets
- add evidence-linked provider output
- separate RCA generation from action recommendation

### Guardrails and Approval

- explicit policy packs
- environment-specific thresholds
- approval timeout and escalation handling

### Execution

- GitOps PR executor
- safer Kubernetes mutating executor
- runbook backend integration

### Demo and DX

- maintain kind as the first-run path
- keep fake observability simple and inspectable
- expand tutorial coverage and expected outputs

## Non-Goals for the Current Phase

- claiming full autonomous production remediation
- coupling the project to one observability vendor
- requiring external LLM credentials for the basic demo

## Suggested Milestones

1. Tag `v0.1.0-alpha.1` once docs, demo, and read-only path are stable.
2. Tag `v0.1.0-beta.1` after controller and adapter tests are expanded.
3. Tag `v0.1.0` after quickstart and read-only operator behavior are repeatable in a clean environment.
4. Start `v0.2.0-alpha.1` only after `AgentAction` semantics are tightened.
