---
name: engineering-baseline
description: >-
  Apply FluxSeer RCA's mandatory engineering baseline for architecture,
  implementation, refactoring, testing, performance, compatibility, security,
  RCA and RiskRule changes, integrations, and release qualification. Do not
  use for purely editorial documentation changes with no runtime or public
  contract impact.
---

# FluxSeer RCA Engineering Baseline

Use this skill as the project's engineering decision framework. The objective
is a correct, safe, simple, compatible, observable, measurable, maintainable,
and automatable Kubernetes-native RCA control plane—not maximum feature count,
abstraction, or test count.

Apply the baseline to every meaningful change. Do not mechanically execute
every stage for trivial changes; mark each stage `APPLIED`, `NOT_APPLICABLE`, or
`BLOCKED` with a short reason.

## Priority order

```text
Correctness > Safety > Simplicity > Compatibility > Reliability
> Maintainability > Performance > Automation
```

Performance, convenience, or implementation speed must not weaken correctness,
evidence quality, safety, or public compatibility without an explicit decision.

## Project boundaries

FluxSeer RCA is a Kubernetes-native RCA control plane. It is not an
all-in-one monitoring stack, a replacement for Alertmanager, a general-purpose
cluster agent, or an autonomous mutation system.

Preserve these product boundaries:

- `InvestigationRequest` is the canonical read-only RCA workflow.
- `RiskRule` is recurring/bootstrap detection; it is not a claim of complete
  cluster-wide anomaly coverage.
- `RiskSignal` is a finding/materialization and compatibility surface.
- Kubernetes Events, workload status, Prometheus, and Loki evidence are
  bounded, policy-controlled datasource inputs.
- OpenTelemetry and CloudWatch adapters remain scaffold/unsupported for the
  supported beta contract unless the repository's support matrix changes.
- Remediation is optional, explicitly guarded, allowlisted, and auditable.
- A detected signal is not automatically a confirmed root cause.

When these boundaries change, update the relevant architecture and capability
documentation and review the public contract impact.

## Non-negotiable invariants

### RCA and evidence

- Never fabricate evidence, telemetry, causal relationships, or confidence.
- Keep `Fact`, `Evidence`, `Inference`, `Hypothesis`, `Finding`, `Conclusion`,
  and `Risk` distinguishable.
- Every causal claim must be traceable to supporting evidence.
- If evidence is insufficient, report `Inconclusive`/missing evidence rather
  than inventing certainty.
- Correlation must not be presented as causation without sufficient support.
- Normalized evidence and finding identity should be deterministic unless
  nondeterminism is an explicit design choice.
- Do not persist raw secrets, tokens, authorization headers, or unredacted
  provider-bound sensitive payloads in CRD status or audit artifacts.

### Safety

- Read-only analysis must remain read-only.
- Any mutation requires explicit authorization, policy/approval checks, an
  allowlisted action, bounded execution, and auditable identity.
- Mutation paths must be idempotent where feasible and must not be triggered
  solely by an unverified model conclusion.
- External dependency failure must not result in unsafe remediation.
- Do not weaken safety, security, or test checks merely to make CI pass.

### Kubernetes and controllers

- Reconciliation must tolerate retries, duplicate events, restarts, leader
  transitions, disappearing resources, and partial failures.
- Reconciliation should be idempotent and must not create unbounded duplicate
  RCA objects for the same incident occurrence.
- Avoid unnecessary kube-apiserver pressure; use bounded queries, existing
  clients/caches, and declared target scope.
- Use least-privilege RBAC. Do not add broad mutation or secret permissions for
  read-only behavior.
- Preserve graceful shutdown and restart safety.

### Public compatibility

Treat these as public contracts once released:

- CRD schemas and API versions
- CLI flags and configuration fields
- Helm values and rendered resource behavior
- exported metrics
- public Go APIs
- report schemas and persisted formats
- datasource and external integration contracts

Classify public-contract changes as `NON_BREAKING`, `DEPRECATION`, or
`BREAKING`. Do not silently remove or change a contract. For deprecations,
document the replacement, migration path, and removal target. Breaking changes
require explicit justification and a versioning strategy.

## Engineering evaluation

Evaluate this sequence for each meaningful task:

```text
0. Invariants
1. Challenge the requirement
2. Delete unnecessary complexity
3. Simplify and standardize
4. Measure before optimizing
5. Automate stable processes
6. Document and govern
```

### 0. Invariants

Write down what must remain true before editing. At minimum consider RCA
correctness, safety, idempotency, compatibility, RBAC, and evidence provenance.

### 1. Challenge the requirement

Confirm the problem, users, project responsibility, and success criteria.
Check whether an existing mechanism or established Kubernetes, Prometheus,
OpenMetrics, OpenTelemetry, Helm, Kustomize, Argo CD, Flux, OCI, or cloud
provider standard already solves it.

Prefer:

```text
general capability + provider adapter
```

over duplicating provider-specific behavior in the core. Reject speculative
features whose maintenance cost exceeds demonstrated project value unless
explicitly approved.

### 2. Delete

Before adding code, look for dead code, duplicate rules, redundant API calls,
duplicate parsing/normalization, unnecessary configuration, dependencies,
clients, caches, and compatibility shims that have reached an approved removal
point.

Do not delete an established public contract merely because its implementation
looks unnecessary; deprecate and migrate it first.

### 3. Simplify and standardize

Prefer the shared pipeline:

```text
Collect -> Normalize -> Correlate -> Evaluate -> Report
```

Reuse existing Kubernetes clients, HTTP transports, datasource registries,
query policies, evidence models, finding identities, retry/timeout policies,
and status contracts. Add an abstraction only when it isolates a stable domain
boundary, external integration, materially reduces coupling, or improves
testing.

Keep provider-specific logic out of the RCA core where an adapter boundary is
appropriate.

### 4. Measure before optimizing

Never claim a performance improvement from intuition alone:

```text
Baseline -> Measure -> Locate bottleneck -> Change -> Measure again -> Compare
```

Measure the applicable dimensions, including analysis/evidence latency,
p50/p95/p99, CPU, memory, allocations, goroutines, Kubernetes API request
count/throttling, datasource query count/latency, cache hit ratio, queue depth,
and concurrent incident throughput.

Performance-sensitive changes must record:

```text
Before: <measured result>
After: <measured result>
Trade-offs: <correctness, memory, complexity, compatibility effects>
```

Never trade RCA correctness for lower latency without explicit approval.

### 5. Automate stable processes

Automate only after the behavior is simplified and stable. Prefer deterministic,
inspectable checks such as formatting, static analysis, unit/controller tests,
envtest, kind E2E, incident replay, golden/schema tests, Helm install/upgrade
validation, compatibility matrices, benchmarks, dependency/secret/container
scanning, SBOM, and release qualification.

A green pipeline is insufficient when assertions do not prove intended
behavior.

### 6. Document and govern

For architecture or public behavior changes, assess updates to README,
architecture docs/ADR, CRD and API references, Helm values, compatibility and
migration docs, security guidance, runbooks, changelog, and release notes.

Create or update an ADR when a change affects system boundaries, evidence
semantics, public APIs/CRDs, persistence, provider architecture, execution
safety, remediation, compatibility policy, or a major dependency. Record:

```text
Context / Decision / Alternatives / Trade-offs / Consequences / Verification
```

## Testing baseline

Test behavior and failure modes, not only line coverage. Choose the lowest-cost
layer that proves the change, then add higher layers when infrastructure
interaction matters:

```text
L0 Static validation
L1 Unit
L2 Controller/envtest
L3 Component integration
L4 Real Kubernetes E2E
L5 Incident/failure replay
L6 Scale/benchmark
```

RCA, evidence normalization, risk scoring, safety decisions, and remediation
policy are critical logic and require stronger verification than ordinary glue.

When relevant, cover Kubernetes API timeout/429/unavailable, Prometheus/Loki
failure, missing or stale data, malformed resources, partial evidence,
duplicate events, retries, leader transition, disappearing resources, clock
skew, and partial provider failure.

Prefer partial but explicitly qualified results over fabricated certainty.

## Incident corpus and evidence contract

Treat incident fixtures as a regression asset. For each supported failure class,
add positive, negative, ambiguous, missing-data, stale-data, and conflicting-
evidence cases where practical. Expected artifacts should verify:

```text
correct evidence + correct classification + appropriate confidence
              + no unsupported conclusion
```

Useful categories for this project include OOM, CrashLoopBackOff, probe failure,
scheduling, image pull, Deployment rollout, service/probe configuration,
Prometheus symptoms, Loki symptoms, and dependency failures. Do not imply
Node/CNI/storage coverage unless the runtime and support matrix actually
support it.

## Kubernetes and ecosystem compatibility

Compilation alone does not prove Kubernetes compatibility. For the supported
version matrix, use the applicable combination of envtest, kind, clean install,
controller startup, CRD installation, reconciliation, upgrade, and incident
replay.

Do not assume EKS-, GKE-, or AKS-specific behavior is universal Kubernetes
behavior. Preserve interoperability with Kubernetes APIs, Prometheus,
OpenMetrics, OpenTelemetry, Helm, Kustomize, Argo CD, Flux, OCI registries,
and cloud providers. Do not invent a new schema/protocol when a suitable
standard exists.

## Security baseline

For runtime, build, dependency, release, or deployment changes, evaluate:

- least-privilege RBAC and mutation scope
- secret/evidence exposure and unsafe logging
- dependency and supply-chain risk
- container privileges and filesystem permissions
- network exposure
- image provenance and dependency pinning
- SBOM and release-integrity impact

Security checks must not be bypassed to make CI pass.

## Definition of Done

Do not declare a task complete merely because it compiles. Verify as applicable:

- intended behavior and relevant edge cases are demonstrated;
- evidence supports RCA conclusions and insufficiency is explicit;
- appropriate tests and regression coverage exist;
- no unnecessary duplication or abstraction was introduced;
- provider-specific logic remains isolated where appropriate;
- public contracts, Kubernetes compatibility, and integrations were reviewed;
- performance claims have before/after measurements;
- timeout, retry, partial-failure, and restart behavior were considered;
- RBAC remains minimal and no sensitive data is leaked;
- user-visible behavior and migrations are documented.

For this repository, prefer these validations when applicable:

```bash
GOWORK=off go test ./...
gofmt -w <changed-go-files>
helm lint charts/fluxseer-rca
helm template <release> charts/fluxseer-rca --namespace <namespace>
```

Also run the relevant `hack/verify-*`, `test/e2e`, PromQL, report, packaging,
and kind qualification checks for the changed surface. Do not claim EKS/GKE/
AKS compatibility, large-scale performance, production readiness, or full
cluster-wide anomaly coverage unless it was actually verified.

The final response must distinguish:

```text
Verified:
- <actual checks and results>

Not verified:
- <unrun environments, scales, providers, or scenarios>
```

## Required work summary

When this skill is used for architecture or implementation work, include:

```text
Decision: PASS | PASS_WITH_RISKS | NEEDS_CHANGES | BLOCKED

Critical invariants:
- ...

Engineering evaluation:
0. Invariants: APPLIED | NOT_APPLICABLE | BLOCKED — reason
1. Challenge: APPLIED | NOT_APPLICABLE | BLOCKED — reason
2. Delete: APPLIED | NOT_APPLICABLE | BLOCKED — reason
3. Simplify: APPLIED | NOT_APPLICABLE | BLOCKED — reason
4. Measure: APPLIED | NOT_APPLICABLE | BLOCKED — reason
5. Automate: APPLIED | NOT_APPLICABLE | BLOCKED — reason
6. Govern: APPLIED | NOT_APPLICABLE | BLOCKED — reason

Findings:
- [BLOCKER|HIGH|MEDIUM|LOW] issue
  Evidence:
  Impact:
  Recommendation:

Verification:
- Verified:
- Not verified:

Residual risks:
- ...
```

Do not invent findings merely to fill the template. A clean review may have no
findings.

## Review severity

- `BLOCKER`: incorrect RCA, fabricated evidence, unsafe remediation, security
  vulnerability, data corruption, severe compatibility break, or unrecoverable
  operational behavior. Must not ship.
- `HIGH`: material reliability/regression/compatibility/resource-exhaustion or
  operational risk. Fix before release unless explicitly waived.
- `MEDIUM`: material maintainability, testing, observability, or design
  weakness without immediate invalidation.
- `LOW`: minor cleanup, naming, documentation, or optimization opportunity.

Do not inflate severity.

## Core rule

> Prove the requirement is necessary. Remove unnecessary complexity. Simplify
> what remains. Measure before optimizing. Automate only stable behavior.
> Govern the resulting contract. Correctness, evidence, safety, and
> compatibility take precedence over implementation speed.

## Repository references

Read the relevant existing source of truth instead of duplicating it:

- `README.md` and `docs/capability-maturity.md` for product boundaries;
- `docs/architecture/` for system and workflow decisions;
- `docs/crd-reference/` for CRD contracts;
- `docs/helm-rulepacks.md` and `config/rule-packs/` for detection coverage;
- `SECURITY.md` for security reporting and constraints;
- `CONTRIBUTING.md`, `Makefile`, and `hack/` for project validation;
- `test/skill/engineering-baseline-evals.yaml` for behavioral qualification
  cases; it is a prepared corpus, not proof that an Agent eval runner has
  executed the cases;
- `cmd/engineering-baseline-eval` and `test/skill/README.md` for the structured
  capture contract and qualification runner;
- `ROADMAP.md` for planned, reserved, and intentionally unsupported scope.
