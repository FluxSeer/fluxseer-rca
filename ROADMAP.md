# FluxSeer RCA Roadmap

FluxSeer RCA is being built as a Kubernetes-native AI SRE Agent Operator with a safe default path and an optional guarded remediation path.

## Positioning

Current public truth:

- FluxSeer RCA `v0.1` is a read-only `RiskSignal` operator.
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

Current v0.4 beta implementation includes policy decision timestamps,
human-approval and escalation timestamps, notification retry status, and
Kubernetes Events for the guarded approval timeline. Kubernetes Events are an
operational timeline only; the durable approval timestamps remain in
`AgentAction.status.approval`.

### `v0.5`

Focus:

- **Safe Remediation**: prove that FluxSeer can safely execute an approved
  remediation and verify whether it actually fixed the incident
- Kubernetes executor for a small, explicitly allowlisted action set
- GitOps executor as the preferred mutation path where possible
- controller-owned policy status and stronger execution auditability

Target outcomes:

- complete the closed-loop path:
  `RiskSignal -> RemediationPlan -> Approval -> AgentAction -> Executor -> Verification Investigation`
- define an executor safety contract covering idempotency, retry budget,
  timeout/deadline, execution identity, target identity, and audit metadata
- execute only approved, allowlisted, low-risk Kubernetes actions and
  GitOps/PR-style changes; direct broad mutation is not part of the MVP
- persist effectiveness as a separate result from execution, with explicit
  `Effective`, `Ineffective`, `Regressed`, or `Unknown` outcomes
- reconcile `ApprovalPolicy`, `NamespaceThreshold`, and `EscalationChain`
  status enough for consumers to see readiness, observed generation, and
  validation errors
- make the action audit trail link the approval, executor attempt, target,
  policy snapshot, and post-action verification

The detailed scope, dependency order, and acceptance gates are tracked in
[`docs/backlog/v0.5-safe-remediation.md`](docs/backlog/v0.5-safe-remediation.md).

### `v0.6`

Focus:

- automation workflow around an already-safe executor

Target outcomes:

- multi-stage escalation progression, including timeout notification,
  reassignment, and auto-reject decisions where policy permits
- `RiskSignal` to `InvestigationRequest` alert-ingress workflow
- bounded reinvestigation, cooldown, deduplication, and loop prevention
- native alert receiver integrations only if a concrete product need is
  demonstrated; generic webhook remains the baseline

### `v0.7`

Focus:

- evidence governance and observability expansion

Target outcomes:

- OpenTelemetry as trace/evidence input rather than metrics-only scaffolding
- CloudWatch adapter, if its support contract is justified
- RawSnapshot lifecycle, encryption at rest, access policy, deletion policy,
  retention, and audit contracts
- production-grade evidence storage and lifecycle operations

### `v0.8`

Focus:

- advanced investigation and evaluation workflows

Target outcomes:

- runtime replay runner
- adaptive investigation runtime after bounded experiments establish a safe
  contract
- cross-investigation correlation and advanced causal analysis

The following are intentionally not committed to the v0.5 MVP: a general
Runbook executor, full multi-stage `EscalationChain` semantics, native
Slack/Email/PagerDuty receivers, arbitrary alert-ingress automation,
`NamespaceThreshold.spec.protectionLevel`, RawSnapshot storage, OTel or
CloudWatch production support, replay execution, and adaptive runtime.

## Workstreams

### Control Plane

- evolve CRD schemas without breaking the read-only entry point
- formalize status transitions and cleanup behavior
- add TTL handling and lifecycle reconciliation
- keep detection inputs, evidence models, and workflow outputs separate

### Adapters

- harden Prometheus and Loki auth and retry behavior
- add better Kubernetes Event filtering
- support templated queries driven by target metadata and rule configuration
- evolve toward capability-oriented datasource contracts and dedicated datasource configuration resources
- keep OpenTelemetry and CloudWatch explicitly scaffolded until the v0.7
  observability contract is approved

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
- use a follow-up `InvestigationRequest` as the v0.5 verification boundary for
  remediation effectiveness
- defer raw evidence governance, encryption, deletion, and access policy to
  the v0.7 evidence milestone

### Guardrails and Approval

- explicit policy packs
- environment-specific thresholds
- approval timeout and escalation handling
- reconcile policy status and validation conditions for the v0.5 executor
  contract
- keep full multi-stage escalation behavior out of v0.5

### Execution

- implement the v0.5 Safe Remediation checklist in dependency order
- use an allowlisted Kubernetes executor and a GitOps/PR executor as the first
  real backends
- keep Runbook execution, arbitrary mutation, and native notification
  integrations deferred
- require post-action verification before claiming remediation effectiveness

### Demo and DX

- maintain kind as the first-run path
- keep fake observability simple and inspectable
- expand tutorial coverage and expected outputs
- make validation repeatable for users who did not build the project themselves

## Non-Goals for v0.4 and v0.5 Safe Remediation

- claiming full autonomous production remediation
- coupling the project to one observability vendor
- requiring external LLM credentials for the basic demo
- letting provider output directly trigger production mutation
- storing unredacted logs or secrets in CRD status
- shipping Prometheus or Loki as mandatory bundled dependencies
- shipping a general-purpose Runbook executor
- implementing the complete multi-stage `EscalationChain` engine
- adding native Slack, Email, or PagerDuty integrations before a generic
  webhook contract proves insufficient
- treating `protectionLevel` as meaningful before its enforcement semantics
  are defined
- making RawSnapshot, encryption, deletion policy, or payload access policy a
  v0.5 prerequisite
- making OpenTelemetry, CloudWatch, replay, adaptive investigation, or
  automatic alert reinvestigation part of the v0.5 acceptance gate

## `v0.2` Definition of Done

1. Add `RiskRule` CRD.
2. Add `ModelProvider` CRD.
3. Add a `RiskRuleReconciler` that discovers targets via selectors.
4. Support query templating for Kubernetes Events, Prometheus, and Loki.
5. Build an internal `EvidenceBundle` model.
6. Redact evidence before any model-provider request.
7. Support heuristic mode as a first-class `ModelProvider` path.
8. Support at least one hosted API provider through `ModelProvider`.
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

See [docs/architecture/v0.2-adapter-neutral-backlog.md](docs/architecture/v0.2-adapter-neutral-backlog.md) for scope, acceptance, and non-goals of each batch.

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
