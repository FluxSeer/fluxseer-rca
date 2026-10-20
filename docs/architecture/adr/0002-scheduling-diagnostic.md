# ADR 0002: Scheduling Diagnostic and Replay-first Composition

- **Status:** Accepted for Phase 2 implementation
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

Phase 2B adds a read-only collector contract without wiring Scheduling into
Planner reconciliation yet. The collector accepts only already authorized,
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

Phase 2B qualification is fake/replay based in this slice. Planner registration,
InvestigationRequest wiring, live Kubernetes qualification, and Node evidence
remain Phase 2C+ work.

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

## Definition of Done

- `SchedulingDiagnostic` is pure and deterministic for the same normalized
  evidence and version.
- Existing scope, planner, verifier, provider policy, and report paths are
  reused.
- ImagePull and Scheduling claims remain isolated by evidence and target UID.
- Replay-first positive, negative, missing-evidence, stale, UID-mismatch, and
  multi-Pod composition fixtures pass.
- `SchedulingGated` is not treated as `SchedulingUnschedulable` without
  supporting evidence.
- No new public CRD or report schema field is required.
- No unauthorized Node reads, RiskSignal creation, remediation, or workload
  mutation occurs.
- Live qualification covers at least one real scheduling failure and cleanup.
- Existing Phase 1/1.5, CrashLoopBackOff, hardening, and schema checks remain
  green.
- Baseline measurements are recorded with their verification limits.

## Risks and explicit non-goals

- Scheduler predicates are historical observations; later Node snapshots may
  not reproduce the exact decision.
- A Pending Pod is not a root cause.
- Multiple candidates can remain simultaneously; the report must preserve
  uncertainty.
- Node, PVC, DNS, trace, and broad dependency correlation remain outside this
  phase.
