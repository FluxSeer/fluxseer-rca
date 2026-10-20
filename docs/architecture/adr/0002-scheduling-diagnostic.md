# ADR 0002: Scheduling Diagnostic and Replay-first Composition

- **Status:** Phase 2F closed; local qualification complete; release pending; PASS_WITH_RISKS
- **Date:** 2026-10-20
- **Branch:** `feat/scheduling-diagnostic`
- **Related:** [ADR 0001](0001-composable-diagnostics.md)

## Context

Phase 1 and Phase 1.5 established a pure ImagePull diagnostic, bounded scope
resolution, planner reuse, claim separation, provider evidence filtering, and
live reuse across Deployment and Pod targets. The next architectural question
is cross-diagnostic reuse: whether a second failure domain can use the same
scope, collector, planner, verifier, and report path without copying the RCA
pipeline.

Scheduling is a suitable second domain because its evidence is structurally
different from image retrieval while still being target-scoped and
evidence-driven. A Pending Pod alone is not a scheduling failure. A Pod can be
intentionally held by `spec.schedulingGates`, and scheduler queue admission
can be blocked before an `Unschedulable` condition exists.

The first Phase 2 slice therefore uses structured Pod state and correlated
`FailedScheduling` events. It does not inspect every Node in the cluster or
claim that a later Node snapshot reconstructs the scheduler's historical
decision.

## Decision

Add a provider-neutral, pure `SchedulingDiagnostic` behind the existing
`Diagnostic` and bounded Planner interfaces.

The collection boundary supplies normalized, target-scoped evidence. The
diagnostic evaluates that evidence and proposes claims. Claim Verification
remains the only authority that can expose a supported RCA conclusion.

```text
InvestigationRequest (Deployment or Pod)
        |
        v
existing Scope Resolver
        |
        v
existing bounded evidence collection
        |
        +-- Pod status: phase, nodeName, PodScheduled condition
        +-- Pod spec: schedulingGates and declared scheduling constraints
        +-- Kubernetes Events: FailedScheduling, same Pod UID and time window
        |
        v
existing Planner
        |
        v
SchedulingDiagnostic
        |
        v
existing Claim Verification and RCA status
```

### Claim contract

| Claim | Type | Evidence threshold |
| --- | --- | --- |
| `PodUnassigned` | Observation | normalized Pod state has empty `spec.nodeName` |
| `SchedulingUnschedulable` | FailureMechanism | `PodScheduled=False` with an unschedulable reason, or a target-correlated `FailedScheduling` event |
| `InsufficientResources` | Candidate RootCause | Scheduler evidence explicitly identifies CPU, memory, or another supported resource predicate |
| `TaintMismatch` | Candidate RootCause | Scheduler evidence explicitly identifies an untolerated taint or equivalent predicate |
| `NodeAffinityMismatch` | Candidate RootCause | Scheduler evidence explicitly identifies node selector or affinity mismatch |
| `SchedulingGated` | Observation / BlockingMechanism | `spec.schedulingGates` contains an effective gate |

`SchedulingGated` is not automatically a failure. It may be intentional and
must not be converted into `SchedulingUnschedulable` without independent
evidence. A Pod may have multiple scheduling candidates at once; the
diagnostic preserves all supported candidates and does not select a unique
root cause by preference.

### Evidence identity and freshness

Every normalized scheduling observation must retain the target Pod UID, source,
collection time, incident window, and truncation or stale state. An Event with
a different involved-object UID, or an Event outside the declared window,
cannot support a target claim. Missing status, stale evidence, permission
failure, and budget exhaustion remain explicit outcomes.

### Scope and collection boundary

- Direct Pod targets are supported through the existing InvestigationRequest
  target resolver.
- Deployment targets reuse the existing UID-authoritative
  Deployment → ReplicaSet → Pod scope resolver.
- The first slice does not list Nodes or infer allocatable capacity from a
  cluster-wide snapshot.
- Node labels, taints, and allocatable data require a later, separately
  budgeted Node Collector with an explicit RBAC review.

### Composition boundary

`SchedulingDiagnostic` and `ImagePullDiagnostic` are registered in the same
Planner. Each diagnostic matches only its own normalized evidence and emits
its own candidate claims and evidence references. One diagnostic must not
consume, rewrite, or suppress another diagnostic's claims.

The first deterministic composition fixture will contain multiple Pod records
under one workload scope:

- one Pod with image-pull failure evidence;
- one Pod with unschedulable evidence;
- one healthy Pod;
- distinct Pod UIDs and evidence references.

This fixture simulates composition without assuming that one ReplicaSet can
reliably create incompatible Pod templates. Kubernetes live qualification will
use independent, intentionally constructed scenarios.

## Phase 2B bounded evidence collector

Phase 2B adds a read-only collector contract. The collector accepts only already authorized,
scope-resolved Pod references and uses the Adapter's existing `APIReader` (or
its existing client fallback). It does not create a new Kubernetes client,
resolve workload selectors, list Nodes, or infer a cause.

The normalized `domain.SchedulingEvidence` record retains:

- Pod UID, namespace, name, phase, node assignment, and `PodScheduled`
  condition details;
- scheduling gates, resource requests including init-container requests,
  node selector/affinity constraints, and tolerations;
- UID-correlated `FailedScheduling` and related scheduler Events from both
  `core/v1` and `events.k8s.io/v1` when available;
- incident window, collection time, event time quality, and explicit coverage.

Coverage is one of `Complete`, `Partial`, `Unavailable`, `Truncated`, `Stale`,
`Unauthorized`, or `CollectionFailed`. An empty Event result remains distinct
from an Event API error. A Pod name or Event name without a matching UID is not
accepted as evidence for the current target.

Collection bounds cover Pod count, Event records, paginated list pages, and
API requests. A disappearing Pod, stale UID, denied Event API, timeout, or
exhausted budget is retained as an explicit degradation record. The collector
returns facts and normalized state only; root-cause wording remains the
responsibility of the pure diagnostic and verifier.

Phase 2B qualification is fake/replay based. Phase 2C now wires this collector
through the Investigation runtime: one shared bounded collection is adapted into
the existing `EvidenceSet`, `SchedulingDiagnostic` is registered beside
`ImagePullDiagnostic`, and Deployment and Pod targets reuse the same UID-scoped
pipeline. A source without the optional collector remains unchanged. A target
without an authoritative Pod UID is not collected by this path, preventing
name-only evidence from entering a report. The implicit collection consumes the
existing investigation query budget before it runs.

Phase 2C runtime qualification is replay/component based in this slice. Live
Kubernetes qualification and Node evidence remain separate gates.

## Replay-first qualification

Replay fixtures are a required merge gate before live composition claims.

Required fixture cases:

1. Pending Pod with no scheduling evidence → `Inconclusive` or
   `PodUnassigned` only;
2. `PodScheduled=False` or correlated `FailedScheduling` → supported
   `SchedulingUnschedulable` mechanism;
3. explicit insufficient CPU or memory predicate → unverified candidate with
   the matching resource kind;
4. explicit untolerated taint → `TaintMismatch` candidate;
5. explicit selector or affinity predicate → `NodeAffinityMismatch` candidate;
6. non-empty scheduling gates without Unschedulable evidence →
   `SchedulingGated`, without a false scheduling failure;
7. stale Event, mismatched Pod UID, truncated evidence, permission failure, or
   exhausted budget → explicit incomplete result;
8. one workload scope containing ImagePull, Scheduling, and healthy Pod
   records → isolated claims and evidence references for each Pod;
9. Pod already assigned to a Node with `ErrImagePull` → SchedulingDiagnostic
   not applicable, ImagePullDiagnostic remains responsible.

The fixture expected output records claim type, statement, verification,
evidence references, and whether a RootCause candidate remains unverified.
`False Confirmation Rate` is calculated from these known-truth fixtures; a
live run that happens not to reveal an error is not treated as a zero false
confirmation rate.

## Live qualification

Live tests validate collector and API behavior independently from replay
composition:

- a real unschedulable Pod with a bounded resource or scheduling constraint;
- a separate scheduling-gates Pod, when the cluster permits the feature;
- an independently created image-pull failure workload for cross-diagnostic
  non-interference;
- no forced three-state condition in one ReplicaSet template.

Each live case must verify target UID correlation, required evidence coverage,
report semantics, no unauthorized reads, no RiskSignal, no remediation, and
namespace cleanup.

## Measurement baseline

Phase 2 records baseline values without claiming an optimization:

- Diagnostic Reuse: existing collector, planner, verifier, and report paths
  reused by the new diagnostic;
- Kubernetes API Calls per Investigation;
- collection and diagnostic duration;
- evidence completeness;
- budget exhaustion rate;
- replay Claim Accuracy;
- replay False Confirmation Rate.

The baseline is diagnostic metadata and test output first. Any runtime metric
addition must preserve existing metric labels and contracts.

## Phase 2E local live qualification result

The final run is retained under
`reports/runtime/v0.5-alpha1-proxmox-k8s134/phase2e-live-20261020-r2/` and
used Kubernetes `v1.34.1` on the dedicated `fluxseer-k8s134` cluster. It ran
three independent read-only Pod scenarios plus an investigation-budget
exhaustion case:

- an affinity/taint scheduling failure retained Pod and UID-correlated
  `FailedScheduling` evidence and emitted scheduler, affinity, and taint
  candidates as `DiagnosticCandidateUnverified`;
- a Pod with an active scheduling gate emitted a gate candidate without an
  `Unschedulable` candidate;
- an assigned Pod with `ErrImagePull` retained ImagePull evidence and did not
  emit a scheduling failure candidate;
- `maxQueriesTotal: 1` preserved the declared event evidence and added an
  explicit `truncated=true` scheduling reference with reason
  `InvestigationQueryBudgetExhausted` without invoking the extra collector or
  fabricating Pod state.

All four requests completed `Inconclusive`, with no `RiskSignal`, remediation,
or workload mutation. The first run exposed a deployment RBAC gap for
`events.k8s.io/events`; the chart now grants only read-only get/list/watch for
that API. The test ClusterRole was patched before the final run and then
formally converged through Helm with `--force-conflicts`; the final release is
revision 21 using the qualification image. The public reports, raw requests,
raw Pod/Event objects, final status, metrics snapshot, and effective RBAC are
retained beside the qualification summary.

This is local live acceptance only. Phase 2F now treats explicit evidence
truncation, including a query-budget skip, as partial degradation and exposes
that state through the existing `Degradation` status plus the `Degraded=True`
condition. No new CRD field is required. The final live regression verified
this contract on the deployed image.

## Phase 2F observability and report contract

The report contract keeps investigation completion separate from evidence
completeness:

- `Phase=Completed` means the investigation workflow reached a terminal state.
- `Outcome=Inconclusive` means the collected evidence did not support a root
  cause conclusion.
- `Degradation.Partial=true` and `Degraded=True` expose incomplete evidence
  coverage, including `InvestigationQueryBudgetExhausted`.
- `EvidenceRef.Truncated` and `TruncationReason` retain the exact bounded
  evidence gap and remain visible to report consumers.

Scheduling claims now use the same separation in status output:

- `SchedulingUnschedulable` is a confirmed failure mechanism only after
  target-scoped, UID-correlated condition or Event evidence is verified.
- Resource, taint, and affinity explanations remain
  `DiagnosticCandidateUnverified` unless a later evidence contract verifies
  the specific root cause.
- A confirmed mechanism is not counted as a verified root cause and therefore
  cannot by itself produce a `Confirmed` investigation outcome or remediation.

Phase 2F adds low-cardinality baseline metrics:

```text
fluxseer_diagnostic_collection_requests_total{diagnostic,outcome}
fluxseer_diagnostic_collection_duration_seconds{diagnostic,outcome}
fluxseer_diagnostic_evidence_coverage_total{diagnostic,outcome,reason}
fluxseer_investigation_budget_exhausted_total{diagnostic,reason}
fluxseer_diagnostic_claims_total{diagnostic,claim_type,verification}
```

Pod identity, InvestigationRequest identity, evidence IDs, and query text are
not metric labels. Fixture-based known-truth cases remain the source for false
confirmation measurement; live absence of a false claim is not a zero-rate
production estimate.

## Phase 2F local live regression result

The targeted post-change run is retained under
`reports/runtime/v0.5-alpha1-proxmox-k8s134/phase2f-live-20261020-r4/` on
Kubernetes `v1.34.1`:

- Budget exhaustion completed `Inconclusive` with `Degraded=True`, reason
  `InvestigationQueryBudgetExhausted`, and the scheduling truncation reference
  retained in status. An uncorrelated Event query did not independently
  promote a scheduling mechanism claim.
- A real unschedulable Pod produced a `SchedulingUnschedulable` claim with
  `Supported` verification. Selector/affinity and taint explanations remained
  `DiagnosticCandidateUnverified` alternatives, and the overall investigation
  stayed `Inconclusive` without a verified root cause.
- The endpoint exposed all five Phase 2F metrics with bounded labels.
- No RiskSignal, remediation request, or workload mutation was created. The
  test namespace was deleted after evidence capture.

This verifies the changed semantics on the local test cluster. It does not
replace remote CI, pull-request review, or a broader release qualification.

## Definition of Done

- `SchedulingDiagnostic` is pure and deterministic for the same normalized
  evidence and version.
- Existing scope, planner, verifier, provider policy, and report paths are
  reused.
- Phase 2C component/runtime replay tests prove one shared scheduling
  collection can feed the existing Investigation pipeline for Deployment and
  Pod targets.
- ImagePull and Scheduling claims remain isolated by evidence and target UID.
- Replay-first positive, negative, missing-evidence, stale, UID-mismatch, and
  multi-Pod composition fixtures pass.
- `SchedulingGated` is not treated as `SchedulingUnschedulable` without
  supporting evidence.
- No new public CRD or report schema field is required.
- No unauthorized Node reads, RiskSignal creation, remediation, or workload
  mutation occurs.
- Local live qualification of real scheduling failure, scheduling gate,
  ImagePull isolation, and budget exhaustion is complete. Phase 2F semantic
  and metrics regression is implemented and verified locally; the overall
  phase is locally accepted, while CI/PR verification and review are still
  required for release.
- Existing Phase 1/1.5, CrashLoopBackOff, hardening, and schema checks remain
  green.
- Baseline measurement dimensions are defined; live values and performance
  claims remain unverified.

## Risks and explicit non-goals

- Scheduler predicates are historical observations; later Node snapshots may
  not reproduce the exact decision.
- A Pending Pod is not a root cause.
- Multiple candidates can remain simultaneously; the report must preserve
  uncertainty.
- Node, PVC, DNS, trace, and broad dependency correlation remain outside this
  phase.
