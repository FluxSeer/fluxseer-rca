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

- configurable read-only RCA platform
- move detection configuration from ad hoc annotations toward Kubernetes-native rule resources
- formalize evidence collection, redaction, and RCA summary generation
- keep remediation disabled by default and out of the main success path

Target outcomes:

- add `RiskRule` as the primary read-only detection contract
- add `ModelProvider` as a provider-neutral runtime config contract
- generate evidence-linked RCA summaries without mutating workloads
- keep heuristic mode runnable without external credentials
- make the kind demo `RiskRule`-driven instead of annotation-driven only
- improve open-source installability and repeatable validation
- preserve adapter neutrality while moving detection config out of ad hoc annotations

### `v0.3`

Focus:

- `RemediationPlan` draft generation only
- richer provider-backed reasoning and knowledge retrieval
- convert RCA output into reviewable remediation proposals without execution

Target outcomes:

- `RiskSignal` to `RemediationPlan` flow is stable
- recommended actions become structured draft plans
- no `AgentAction` execution in the default `v0.3` success path
- provider-neutral prompt and response auditing improves

### `v0.4`

Focus:

- guardrails and approval lifecycle
- production governance boundaries
- stronger policy and audit semantics

Target outcomes:

- explicit policy decisions and approval requests
- approval timeout and escalation behavior
- richer audit persistence and status transitions
- safer handoff from remediation planning to approved action intent

### `v0.5`

Focus:

- safe executor enablement
- low-risk action backends first
- GitOps-first mutation paths where possible

Target outcomes:

- enable only low-risk diagnostic and notification actions first
- add safer GitOps PR style mutation backends before direct workload mutation
- preserve dry-run, rollback, and audit guarantees for every executable action

## Workstreams

### Control Plane

- evolve CRD schemas without breaking the read-only entry point
- formalize status transitions and cleanup behavior
- add TTL handling and lifecycle reconciliation
- keep detection inputs, evidence models, and workflow outputs separate

### Adapters

- harden Prometheus and Loki auth and retry behavior
- add better Kubernetes Event filtering
- expand OpenTelemetry and CloudWatch from scaffold to usable integration
- support templated queries driven by target metadata and rule configuration
- evolve toward capability-oriented datasource contracts and dedicated datasource configuration resources

### Reasoning

- keep heuristic mode runnable without secrets
- add evidence-linked provider output
- separate RCA generation from action recommendation
- require structured outputs that can be validated before persistence

### Evidence

- introduce an internal `EvidenceBundle` model before adding new evidence CRDs
- summarize evidence into `RiskSignal` status instead of storing raw large payloads in etcd
- add redaction before any provider-bound reasoning call
- preserve references to datasource evidence without leaking secrets

### Guardrails and Approval

- explicit policy packs
- environment-specific thresholds
- approval timeout and escalation handling

### Execution

- delay broad executor expansion until the read-only RCA path is stable
- prefer low-risk diagnostics and GitOps-oriented actions first
- keep mutating Kubernetes executors out of the default product truth

### Demo and DX

- maintain kind as the first-run path
- keep fake observability simple and inspectable
- expand tutorial coverage and expected outputs
- make validation repeatable for users who did not build the project themselves

## Non-Goals for the Current Phase

- claiming full autonomous production remediation
- coupling the project to one observability vendor
- requiring external LLM credentials for the basic demo
- letting provider output directly trigger production mutation
- storing unredacted logs or secrets in CRD status
- shipping Prometheus or Loki as mandatory bundled dependencies

## `v0.2` Definition of Done

1. Add `RiskRule` CRD.
2. Add `ModelProvider` CRD.
3. Add a `RiskRuleReconciler` that discovers targets via selectors.
4. Support query templating for Kubernetes Events, Prometheus, and Loki.
5. Build an internal `EvidenceBundle` model.
6. Redact evidence before any model-provider request.
7. Support heuristic mode as a first-class `ModelProvider` path.
8. Support at least one non-heuristic provider or local model endpoint.
9. Generate structured RCA summaries and persist them into `RiskSignal`.
10. Include RCA summary content in notification output.
11. Update the kind demo to be `RiskRule`-driven.
12. Keep `go test ./...` green.
13. Guarantee that `enable-remediation=false` does not create `RemediationPlan` or `AgentAction`.
14. Document runtime, compile-time, and deployment dependency boundaries clearly for open-source users.

## `v0.2` Next Implementation Batches

The dependency-neutral follow-up work is intentionally split into three PR-sized batches:

1. `DataSource` CRD
2. capability-based datasource contract
3. degraded status conditions

See [docs/architecture/v0.2-adapter-neutral-backlog.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/v0.2-adapter-neutral-backlog.md:1) for scope, acceptance, and non-goals of each batch.

### Batch 1: `DataSource` CRD

Focus:

- add datasource runtime config as a first-class Kubernetes resource
- keep current env-based demo wiring working during migration
- avoid making Prometheus or Loki mandatory

Exit criteria:

- `DataSource` CRD exists with Prometheus, Loki, and Kubernetes Events support
- datasource registry can be built from resource specs
- demo and tests remain green

### Batch 2: Capability-based Datasource Contract

Focus:

- add adapter capability declaration
- normalize query output further
- prepare `RiskRule` for `datasourceRef` and `queryTemplate` oriented evaluation

Exit criteria:

- adapters declare capabilities explicitly
- rule evaluation can validate datasource capability against requested query type
- template-driven query path remains intact

### Batch 3: Degraded Status Conditions

Focus:

- make optional dependency failure visible through status
- preserve partial-evidence success where possible
- keep mutation-related paths fail-closed

Exit criteria:

- missing or unhealthy optional integrations do not crash reconciliation
- `DataSource`, `RiskRule`, and relevant `RiskSignal` conditions expose degraded states clearly
- tests cover partial-evidence and missing-provider scenarios

## Suggested Milestones

1. Tag `v0.1.0-alpha.1` once docs, demo, and read-only path are stable.
2. Tag `v0.1.0-beta.1` after controller and adapter tests are expanded.
3. Tag `v0.1.0` after quickstart and read-only operator behavior are repeatable in a clean environment.
4. Start `v0.2.0-alpha.1` when `RiskRule`, evidence redaction, and `RiskSignal` RCA summaries are working end to end.
