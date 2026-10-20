# ADR 0001: Composable diagnostics for evidence-driven RCA

- Status: Accepted for first implementation slice
- Date: 2026-10-20
- Scope: Internal diagnostic architecture; no public API change

## Context

Kubernetes failure symptoms are not a one-to-one mapping to root causes. A
Deployment can be unavailable because a workload was not created, a Pod could
not be scheduled, a container could not start, or a running container failed
readiness or runtime checks. The same underlying mechanism can also be
observed through more than one signal.

Adding a separate RiskRule, Evidence Profile, and RCA flow for every symptom
would duplicate resource traversal, evidence collection, correlation, and
verification. It would also make a detected symptom too easy to confuse with
its root cause.

FluxSeer already has a bounded read-only investigation pipeline:

```text
Signal / manual request
-> InvestigationRequest
-> evidence collection
-> normalized evidence bundle
-> model reasoning
-> claim verification
-> existing RCA status and report
```

The first composable diagnostics slice must extend this pipeline without
changing the released CRD contracts, RiskSignal behavior, evidence report
schema, or safety boundary.

## Decision

Adopt the following internal flow for reusable diagnostic capabilities:

```text
Signal
-> Scope Resolver
-> Evidence Collectors
-> Diagnostic Planner
-> Diagnostic Evaluators
-> Candidate Claims
-> Claim Verification
-> Existing RCA Report Builder
```

The first vertical slice is `DeploymentUnavailable -> ImagePullDiagnostic`.
It is entered through the existing `InvestigationRequest` workflow only. This
slice does not add a new automatic trigger and does not change direct
RiskSignal detection or projection behavior.

### Scope identity and ownership

Resource identity is the tuple `namespace`, `kind`, `name`, and `uid`.

For the initial Deployment scope:

- a ReplicaSet is in scope only when it has a valid controller
  OwnerReference whose UID is the target Deployment UID;
- a Pod is in scope only when it has a valid controller OwnerReference whose
  UID is an in-scope ReplicaSet UID;
- the Deployment selector is used to constrain candidates and cross-check the
  result, not to prove ownership by itself;
- all resolved resources must remain in the target namespace;
- a reused name with a different UID is a different resource occurrence;
- multiple ReplicaSets remain in scope during rollout when ownership is
  valid.

The resolver returns a bounded result with warnings and a completeness flag.
Partial results may be used for diagnosis, but `complete=false` must remain
visible to planning and reporting.

### Collector and diagnostic boundaries

Collectors obtain facts. Diagnostics do not call Kubernetes or external
datasource APIs directly.

```text
Diagnostic.Requirements(evidence)
-> Planner
-> allowlisted Collector queries
-> Diagnostic.Evaluate(evidence)
-> candidate claims
```

Diagnostics are deterministic for the same evidence and diagnostic version.
The initial ImagePull diagnostic consumes normalized workload, Pod lifecycle,
container state, and Event evidence. It may classify `ErrImagePull`,
`ImagePullBackOff`, authorization, manifest, timeout, and DNS-related clues,
but it must not infer a specific root cause from a reason string alone.

### Claim semantics and verification

Diagnostics produce candidate claims; Claim Verification decides whether the
existing RCA contract may expose them as verified conclusions.

The first slice distinguishes:

| Claim type | Example | Contract meaning |
| --- | --- | --- |
| Observation | Container waiting with `ErrImagePull` | Directly observed fact |
| Failure mechanism | Image pull failed | Supported or confirmed only under its evidence rule |
| Root cause | Registry denied image access | Supported, confirmed, or inconclusive only after identity, container, image, and incident-window checks |

Missing, stale, partial, or failed evidence cannot be treated as negative
evidence. Existing `Confirmed`, `Inconclusive`, and `Unknown` outcomes remain
the public outcome vocabulary.

### Freshness and budgets

Evidence keeps its source, resource UID, collection time, and incident-window
context. Current snapshots must not silently replace incident evidence from a
different Pod occurrence.

Planning remains bounded by existing query policy and budget controls, with
explicit limits for API calls, resolved resources, traversal depth, and
duration. All first-slice operations are read-only.

### Compatibility

This ADR does not change:

- CRD schemas or API versions;
- `InvestigationRequest` entry semantics;
- `RiskSignal` detection or projection behavior;
- `EvidenceCoverage`, `EvidenceRef`, or `fluxseer-riskrule-report/v1`;
- RBAC mutation scope or remediation policy.

Internal diagnostic results map into existing status and report fields. A new
public status or user-configurable diagnostic DSL requires a separate ADR and
compatibility review.

## Alternatives considered

### One profile and workflow per symptom

Rejected because it duplicates scope traversal and evidence logic, and makes
coverage grow with rule count rather than reusable diagnostic capability.

### Diagnostics that query Kubernetes directly

Rejected because it couples evaluators to providers, makes offline replay and
unit testing harder, and weakens the Collector/Planner budget boundary.

### A general dynamic query or graph engine in the first slice

Rejected as unnecessary complexity. The first slice uses existing clients,
datasource adapters, normalized evidence, and in-memory bounded relations. A
larger evidence graph can be evaluated after cross-workload correlation needs
are demonstrated.

## Consequences

Positive consequences:

- Image pull analysis can be reused by Deployment, Pod, and container signals.
- Scope and evidence provenance become testable independently of reasoning
  providers.
- Candidate hypotheses remain separate from verified RCA conclusions.
- Future CrashLoop, Scheduling, and Readiness diagnostics can reuse the same
  boundaries.

Costs and limitations:

- The first slice adds internal interfaces and deterministic test fixtures.
- Partial scope and freshness metadata must be carried through reporting.
- The initial implementation does not establish broad Kubernetes distribution
  compatibility or cross-workload causal correlation.

## First-slice Definition of Done

- Deployment → ReplicaSet → Pod scope uses validated controller OwnerReference
  UIDs and bounded selector checks.
- ImagePullDiagnostic has no Kubernetes or external API calls.
- Evidence retains resource UID, source, collection time, and freshness context.
- `ErrImagePull` alone cannot produce a confirmed registry or image root cause.
- Missing, stale, partial, permission, and budget failures remain explicit.
- Existing InvestigationRequest and RiskSignal behavior is unchanged.
- Existing report schema remains compatible.
- Runtime qualification demonstrates no unauthorized writes or remediation.
- Existing CrashLoopBackOffV2 regression coverage remains green.

## Verification plan

The first implementation will be qualified at the lowest layer that proves
each behavior, then with a real Kubernetes vertical slice:

1. scope resolver unit cases, including UID replacement and multiple
   ReplicaSets;
2. deterministic ImagePull evaluator cases for positive, negative, stale, and
   ambiguous evidence;
3. controller integration coverage for existing InvestigationRequest status
   mapping;
4. v1.34.1 live report qualification using the designated external kubeconfig;
5. report contract, no-remediation, and namespace cleanup assertions.

No performance, managed-distribution, or broad scenario-coverage claim is
made by this ADR.

## Implementation status

The first internal seams are now present on the implementation branch:

- `internal/scope` provides bounded Deployment → ReplicaSet → Pod resolution
  with UID-authoritative ownership and partial-result warnings;
- `internal/diagnostic` provides the deterministic Diagnostic interface,
  bounded planner registry, and `ImagePullDiagnostic` candidate claims;
- `investigation.Service` evaluates registered diagnostics after evidence
  collection and passes explicitly unverified advisory candidates to the
  existing provider input path;
- hosted-provider evidence policy filtering removes diagnostic claim links to
  evidence that is not allowed for that provider.

The public CRD/report contracts and direct RiskSignal behavior remain
unchanged. The ImagePull vertical slice was qualified on the designated
Kubernetes v1.34.1 test cluster with controller image
`operator:k8s134-20261020-composable-37e14f2` (manifest digest
`sha256:f54bec0f1f908642d5e10340d0a2532cb4f23a48eb14c21c006e25d9b14a8b81`).
An invalid-image workload produced `ErrImagePull`; the InvestigationRequest
completed with `Confirmed` for the bounded `ImagePullBackOff` mechanism,
complete required-event coverage, and an explicitly unverified diagnostic
candidate. No RiskSignal or remediation was created, and the test namespace
was removed after qualification. The retained artifacts are under
`reports/runtime/composable-diagnostics-imagepull-20261020/`.

This qualifies the first vertical slice only. It does not establish a
confirmed registry/DNS root cause, broad Kubernetes distribution compatibility,
cross-workload causal correlation, or performance characteristics.

Phase 1.5 reuse qualification now covers the same normalized image-pull
evidence entering through Deployment and Pod targets. Both paths use the same
planner and evaluator and produce identical candidate claims. A replayable,
de-identified fixture derived from the live evidence is stored at
`internal/diagnostic/testdata/imagepull-failure-v1.json`. The existing
`ImagePullBackOff` profile name remains compatible, while an `ErrImagePull`
event is emitted as the more precise `ImagePullFailure` mechanism unless
explicit BackOff state evidence is present.

The same reuse path was qualified live on Kubernetes v1.34.1 with paired
Deployment-target and Pod-target InvestigationRequests. Both completed with
the same `ImagePullFailure` supported claim and the same explicitly unverified
diagnostic candidate; neither created a RiskSignal. The paired artifacts are
under `reports/runtime/composable-diagnostics-entrypoints-20261020/`.

## Phase 1 + 1.5 acceptance record

Status: **Accepted as an independent merge increment**.

The acceptance boundary is the ImagePull vertical slice plus cross-entrypoint
reuse. It includes deterministic scope and diagnostic tests, provider evidence
policy filtering, replayable evidence, full repository regression, hardening
checks, and v1.34.1 live qualification for both Deployment and Pod targets.
The next phase must start from the merged baseline on a separate branch and
must not add Scheduling, Node, PVC, DNS, or other new diagnostic domains to
this increment.

Phase 2 composition claims are intentionally not covered by this record. They
require a replay-first multi-diagnostic fixture gate before any live combined
scenario is treated as a compatibility or accuracy result.
