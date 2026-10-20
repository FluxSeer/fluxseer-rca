# Evidence-Driven RCA Actionability

Status: `Active`

## Objective

Improve how often a detected workload or service anomaly becomes a useful,
evidence-linked investigation. Success is not measured by Detection Pattern
count or by maximizing `Confirmed` outcomes. A useful investigation should tell
the user:

1. what FluxSeer observed;
2. what causal claims are supported, inferred, contradicted, or unknown;
3. what evidence is missing or unavailable; and
4. which bounded, read-only diagnostic step can close the most important gap.

The canonical `InvestigationRequest.status.outcome` contract remains unchanged:
`Confirmed` requires evidence-supported root-cause claims. A hypothesis or
recommended next query must never be presented as a confirmed cause or used to
authorize remediation.

## Current State And Gap

The repository already provides useful foundations:

- 21 built-in Detection Patterns; six Kubernetes patterns are enabled by
  default, while Prometheus and Loki rule packs require explicit setup.
- Controller-planned and user-planned evidence collection, named evidence
  profiles, query budgets, evidence coverage, and missing-evidence status.
- Read-only Kubernetes Event, Deployment condition, Service configuration,
  and probe configuration queries, plus Prometheus and Loki adapters.
- Claim verification statuses and public status fields for hypotheses,
  alternative hypotheses, degradation, and evidence references.

The remaining gap is execution and presentation, not the absence of all
planning primitives:

- `internal/adaptive` is a disabled-by-default allowlisted planner prototype;
  it is not connected to an iterative investigation runtime.
- The controller currently clears `status.alternativeHypotheses` instead of
  populating it.
- Missing evidence is recorded, but there is no general deterministic mapping
  from an evidence gap to an actionable next diagnostic query.
- Kubernetes container termination details, previous-container logs, and
  workload configuration-change history are not a general automatic evidence
  bundle today. Each requires an explicit supported source and contract.
- The built-in detector and public report catalogs cover only bounded workload
  and service scenarios; they do not imply cluster-wide RCA coverage.

## Product And Evidence Contract

### Report layers

Present separate sections or fields for:

| Layer | Meaning | Example |
| --- | --- | --- |
| Observed fact | Directly retained evidence, with source and time | Container status reports `terminated.reason=OOMKilled`. |
| Confirmed root cause | Bounded causal conclusion supported by linked evidence | Memory evidence and termination details support memory-limit termination. |
| Hypothesis | Plausible but unconfirmed explanation, explicitly marked `Inferred` or equivalent | Recent traffic increase may have contributed to memory pressure. |
| Unknown / evidence gap | Required evidence was unavailable, missing, stale, or insufficient | No memory metric was available for the incident window. |
| Next diagnostic step | A safe, bounded query tied to a named evidence gap | Query memory usage for the affected Pod over the incident window. |

The wording may be direct when a fact or root cause is confirmed. It must keep
the distinction between an observed symptom and its cause. Do not add a new
terminal `Probable` outcome in the first implementation slice. Prefer the
existing claim verification and hypothesis concepts; preserve `Inconclusive`
when no root cause is confirmed.

### Evidence acquisition

Evidence planning must be profile-driven and bounded. For each scenario, define
the trigger, required and optional checks, applicable datasource capabilities,
allowlisted query templates, target scope, freshness window, and stop
conditions. Additional collection must:

- remain read-only and scoped to the declared target and namespace;
- obey per-investigation query, time, and response-size budgets;
- preserve evidence provenance, timestamps, query digests, and redaction;
- distinguish `not configured`, `unsupported`, `query failed`, `no matching
  data`, `stale data`, and `insufficient evidence`;
- fail closed when the source, query template, or policy is unavailable; and
- avoid arbitrary model-generated queries or broad RBAC expansion.

For CrashLoopBackOff, a candidate profile can request Kubernetes termination
state and an explicitly configured log source. Image/configuration history and
memory metrics are optional checks only when a supported datasource can supply
them. Event detection alone must not be promoted to a specific application
root cause.

### Bounded CrashLoopBackOff evidence profile

Keep the current `CrashLoopBackOff` profile contract intact. The additive
`CrashLoopBackOffV2` profile is implemented without changing the current
required `event` plus `application log` contract. Its container status query
uses the uncached Kubernetes API reader for bounded owner-chain pagination;
the ordinary Kubernetes datasource queries keep their existing reader.

| Check | Role | Collection contract | Missing or failed result |
| --- | --- | --- | --- |
| Kubernetes `BackOff` / restart Event | Required symptom evidence | One namespace; exact target owner chain; default 10-minute lookback; retain at most 20 matching Events | `Inconclusive` with `CrashLoopEvidenceCoverageMissing`; API errors remain explicit `Failed` results |
| Container termination status | Required execution fact | Add a read-only `containerTermination` query to the existing Kubernetes datasource. For a Pod target, read that Pod by name and UID. For a workload target, use its declared selector plus owner references scoped to the same namespace. Inspect only `containerStatuses[].state.waiting.reason`, `restartCount`, and `lastState.terminated.{reason,exitCode,signal,finishedAt}`. Limit to 20 owned Pods and 10 application containers per Pod; do not return environment variables, command arguments, or full Pod specs. | `Inconclusive` with `ContainerTerminationEvidenceMissing` when no in-scope status is available; permission, timeout, or API errors remain explicit failures. |
| Application log context | Optional causal evidence | Collect only when an explicit, policy-allowed Loki query is included. Keep target labels and the same incident window; existing query budgets and log result limits apply. Do not read `pods/log` in this profile. | If no query is configured, report the log gap while retaining the termination fact. If a requested Loki query fails, preserve the existing terminal datasource failure semantics. |

Use only `status.containerStatuses`, not init or ephemeral container status, in
the first profile. Select only Pods whose owner chain resolves to the declared
target; do not trust a matching label alone. A `finishedAt` value outside the
investigation window is stale and cannot complete the termination check.

The evidence can affirm the immediate fact that a named container last
terminated with a reported reason and exit status. It cannot, by itself,
explain why the process exited or establish a code/configuration/dependency
defect. `CrashLoopBackOffV2` must remain `Inconclusive` unless the existing
claim verifier links the specific causal claim to supporting evidence. An
`OOMKilled` status may support the narrow statement that the container was
terminated for OOM; it must not imply whether the limit, workload size, or
traffic caused the memory pressure. No hypothesis may create a `RiskSignal` or
remediation action.

The Kubernetes adapter already has read-only access to `pods` in the
`readOnlyRCA` RBAC profile, so this query should not require new permissions.
Previous-container log access is deliberately excluded because `pods/log` is
not currently granted and would require a separate security and retention
decision.

## Delivery Plan

### Initial implementation checkpoint

The first report slice adds a CLI Markdown view for existing RiskRule report
data and deterministic, read-only CrashLoopBackOff guidance for four states:
application evidence confirms the cause, application evidence is missing,
the Loki datasource is not configured, and the Loki query is unavailable. Its
controller/runtime-harness tests exercise each state through the public JSON
report builder and Markdown renderer. This qualifies the report projection;
it does not establish live-cluster compatibility or automatically collect
additional evidence.

The existing v0.5 RCA Kind suite passed on 2026-10-19 using a newly created
cluster. It exercised the current `CrashLoopBackOff` positive and negative
cases and captured both public report formats. Artifacts are under
`reports/runtime/v0.5-alpha1-kind-live/crashloop-profile-preflight-20261019/`.
This is live evidence for the current profile and Markdown projection, not for
the proposed `CrashLoopBackOffV2` termination query.

### P0 — Report actionability and outcome measurement

- Define the user-facing report view for observed facts, confirmed cause,
  hypotheses, evidence gaps, source health, and next diagnostic steps.
- Prefer existing status fields. If a new persisted field or report schema is
  needed, document its compatibility classification and migration before
  implementation; do not silently change `fluxseer-riskrule-report/v1`.
- Generate next-step guidance only from deterministic mappings between an
  evidence profile and supported datasource capabilities. Every suggestion
  must name the gap it addresses and whether it is executable in the current
  installation.
- Ensure an `Inconclusive` report can still state confirmed observations and
  useful evidence gaps without implying a confirmed root cause.
- Add product metrics with documented denominators and bounded labels.

### P1 — Profile-guided evidence completion

- Define an evidence-plan contract per accepted scenario: trigger, required
  checks, optional checks, query templates, target binding, evidence freshness,
  and termination conditions.
- Connect the planner to the investigation runtime using controller-owned,
  allowlisted templates; do not expose unbounded adaptive query generation.
- Add only the source capabilities required by the initial profiles, such as
  Pod termination state and configured previous-container log retrieval where
  access and data-retention policy permit.
- Persist each planned and executed step, source result, budget decision, and
  resulting evidence reference for replay and audit.
- Test missing, stale, contradictory, malformed, unavailable, and partial
  datasource results, along with duplicate reconcile and restart behavior.

#### CrashLoopBackOff qualification gate

The existing `test/e2e/kind/scenarios/crashloopbackoff.sh` now includes a live
`CrashLoopBackOffV2` case. The live gate must cover:

| Case | Required live assertion |
| --- | --- |
| Restarting container with `lastState.terminated` and no causal log | `Completed/Inconclusive`; event and termination fact are present; per-check profile coverage is complete; no causal root cause or `RiskSignal`; public Markdown states the termination fact and missing log context. |
| Restarting container with matching bounded application log | Preserve the existing V1 positive case; `Completed/Confirmed` only if the current verifier accepts the linked log as causal evidence; both evidence refs appear in the public report. |
| Exit code `137` without `reason=OOMKilled` | Do not claim OOM; preserve the reported exit status and keep the cause inconclusive. |
| No recent termination status or stale `finishedAt` | Required termination check is incomplete; do not substitute Event or log evidence for the missing status fact. |

For each case, assert terminal request phase/outcome, coverage checks, evidence
source/kind/stable refs, root-cause entity, `RiskSignal` presence or absence,
zero remediation resources, public JSON, and public Markdown. Retain the
namespace-scoped Pod, Event, InvestigationRequest, RiskSignal, and report
artifacts. Also verify the controller's existing `get/list/watch pods` access;
the profile must not add `pods/log`, Secret, or mutation permissions.

The repository's live Kind harness deletes the named Kind cluster before
creating it. Run qualification only with a new, unique
`LIVE_HARNESS_CLUSTER_NAME` and a unique `LIVE_HARNESS_RUN_ID`; do not point it
at an existing developer cluster. The current branch's full v0.5-alpha1 Kind
live suite passed on 2026-10-19 using a fresh `kindest/node:v1.36.1` cluster.
It includes ServicePortMismatch, FailedScheduling, CrashLoopBackOff positive
and negative controls, the new V2 no-causal-log case, and HighHTTPErrorRate
positive and negative controls. The V2 report retained Event and termination
evidence, marked both required checks complete, remained
`Completed/Inconclusive`, emitted no `RiskSignal`, and recommended an
explicitly scoped Loki query for process-level investigation. Artifacts are
under `reports/runtime/v0.5-alpha1-kind-live/crashloop-profile-v2g-20261019/`.
The same representative suite also passed on 2026-10-19 with a fresh
`kindest/node:v1.35.8` cluster, pinned by image digest. The run artifacts are
under `reports/runtime/v0.5-alpha1-kind-live/crashloop-v2-scenarios-k8s-1.35.8-20261019/`.
The harness records the selected node image in `run.json`; set
`LIVE_HARNESS_NODE_IMAGE` to qualify another pinned Kind node image.

The controller now reports missing, stale, and truncated termination status
with distinct missing-evidence reasons, and the public report gives a tailored
read-only next step for each. Dedicated live cases for all four gaps passed on
Kind Kubernetes 1.35.8: missing status, stale `finishedAt`, 21 target Pods
against the 20-Pod bound, and denied Pod reads. The run artifacts are under
`reports/runtime/v0.5-alpha1-kind-live/crashloop-v2-gap-matrix-k8s-1.35.8-20261019/`.
The same four cases also passed on the Proxmox test cluster running Kubernetes
1.31.2. Missing, stale, and truncated termination evidence returned
`Completed/Inconclusive` with their specific evidence-gap reasons; denied Pod
reads returned `Failed/Unknown` with `DatasourceQueryFailed`. All cases emitted
zero `RiskSignal` objects and no remediation resources. Artifacts are under
`reports/runtime/v0.5-alpha1-kind-live/proxmox-test-20261019-crashloop-v2-r1/`.
This run is behavioral evidence only: the operator uses client-go v0.34.1,
three minor versions ahead of the test API server, and the local kubectl client
is v1.36. Kubernetes 1.31 compatibility is not qualified by this run.
The expanded matrix has not yet been verified on 1.36.1: etcd readiness failed
before scenario execution while the local filesystem had less than 200 MiB
free, and Docker containerd returned metadata/blob I/O errors. The previous
1.36.1 representative suite covers the earlier scenario set only. Rerun the
expanded matrix on 1.36.1 after the local Docker/filesystem issue is repaired.
Kind results do not establish compatibility with managed Kubernetes
distributions.

The same four evidence-gap cases passed on 2026-10-19 using a dedicated
Proxmox Talos cluster with Kubernetes v1.34.1. Missing, stale, and truncated
termination evidence returned `Completed/Inconclusive` with the corresponding
gap reason; denied Pod reads returned `Failed/Unknown` with
`DatasourceQueryFailed`. All four cases emitted zero `RiskSignal` objects and
no remediation resources. The retained scenario artifacts are under
`reports/runtime/v0.5-alpha1-proxmox-k8s134/proxmox-k8s134-crashloop-v2-20261019-r1/`.
This qualifies only those evidence-gap behaviors on that test cluster; it does
not establish broad Kubernetes distribution compatibility or qualify other
scenarios on Kubernetes v1.34.1.

### P2 — Cross-source workload and time correlation

- Normalize timestamps and target identity across Kubernetes, Prometheus, and
  Loki evidence.
- Preserve explicit workload ownership and related-target references; do not
  infer service dependencies from coincident timing alone.
- Qualify correlation rules on positive, negative, ambiguous, and conflicting
  evidence cases before exposing stronger causal language.

### P3 — Expand validated scenario coverage

- Select Node, PVC/storage, DNS, and Ingress scenarios from concrete user need
  and available evidence sources.
- Add a scenario to supported coverage only after its public evidence contract,
  missing-data behavior, failure behavior, and runtime report are validated.
- Keep built-in pattern count, validated scenario coverage, and report catalog
  size as separate measures.

### P4 — Trace and dependency-graph reasoning

- Consider trace input and broader dependency-graph analysis only after target
  identity, evidence provenance, and bounded cross-source correlation are
  established.
- Keep AI assistance advisory. Any causal conclusion must remain traceable to
  retained supporting evidence and must report uncertainty honestly.

## Product Metrics

Publish these metrics with stable definitions. Do not combine infrastructure
failures and evidence insufficiency into one unsuccessful-RCA bucket.

| Metric | Initial definition | Required breakdown |
| --- | --- | --- |
| Scenario Coverage | Count of named scenarios with validated runtime evidence, divided by the explicitly declared scenario portfolio. | Scenario ID, target kind, evidence profile, validation tier. |
| RCA Resolution Rate | Completed eligible investigations with `Confirmed` root cause divided by completed eligible investigations. Report separately from operational actionability. | Evidence profile, provider class, required-evidence availability. |
| Evidence Sufficiency Rate | Investigations whose required profile checks completed with usable evidence divided by investigations where those checks were required. | Check/source, unavailable, stale, empty, failed, or usable. |
| Actionable Inconclusive Rate | Inconclusive investigations that include a specific evidence gap and an available next diagnostic step divided by all Inconclusive investigations. | Evidence profile, bounded gap reason, and whether the step was executable. |

Exclude raw query text, resource names, namespace names, and other
high-cardinality or sensitive values from metric labels.

### Runtime measurement contract

The controller exports these additive Prometheus counter families:

| Metric | Unit counted | Rate denominator |
| --- | --- | --- |
| `fluxseer_rca_investigation_quality_total` | Persisted terminal `InvestigationRequest` outcomes, labeled by bounded profile, provider class, evidence availability, phase, and outcome. | RCA Resolution Rate includes `phase="completed"` with outcome `confirmed`, `inconclusive`, or `unknown`; it excludes failed executions and `NoIssueFound`. |
| `fluxseer_rca_evidence_sufficiency_total` | One terminal request with a recognized required evidence profile, labeled `sufficient=true/false`. Failed executions count as insufficient. | All terminal requests that declared one of the supported profiles. |
| `fluxseer_rca_required_evidence_checks_total` | One required check per terminal request, labeled by bounded profile, check, and result (`usable`, `empty`, `missing`, `stale`, `truncated`, `failed`, `unavailable`, or `other`). | Supports per-check/source sufficiency breakdowns; only `usable` is numerator evidence. |
| `fluxseer_rca_actionable_inconclusive_total` | One completed `Inconclusive` request, labeled by bounded profile, gap reason, and `actionable=true/false`. `true` requires a specific recorded evidence gap with a supported read-only next step. | All completed `Inconclusive` requests with a recognized required profile. |

`evidence_availability` describes the recorded required-check state: all checks
complete, partial/gapped, unavailable, or not applicable. It does not claim
health for every configured datasource. Profile and provider labels map
unknown values to `other`; no request, namespace, workload, datasource name, or
query text is exported. The actionability gap label is restricted to known
CrashLoop reason categories plus `none`, `multiple`, and `other`. Actionability
is currently `true` only for CrashLoop profiles with a gap that maps to a
specific report diagnostic. Other profiles remain `false` until equivalent
guidance exists.

`Scenario Coverage` is computed from the declared scenario portfolio and its
validated runtime/replay artifacts. It is not a controller runtime counter.
These in-memory counters are updated after a terminal status write succeeds;
they can undercount if the controller exits after the API write and before the
metric update, and reset on process restart as normal Prometheus counters do.
Adding these metric families is `NON_BREAKING`; no CRD, report schema, RBAC, or
Helm values contract changes.

## Acceptance Criteria

- A confirmed observation can be reported affirmatively even when root cause is
  inconclusive, with the distinction visible in the report.
- Every confirmed root-cause claim links to supporting evidence; contradictory
  evidence prevents confirmation or is explicitly resolved by the contract.
- Every supported evidence profile produces explicit per-check completion or
  gap status.
- When a supported, allowlisted next query can address a gap, the report names
  it; when it cannot run, the report explains the bounded reason.
- Any automatic follow-up query is read-only, target-scoped, policy-checked,
  budgeted, deterministic, and represented in the audit status.
- Hypotheses remain marked as hypotheses and cannot independently trigger
  `RiskSignal` confirmation or remediation.
- Scenario claims are backed by runtime or replay evidence at the stated
  qualification tier; controller unit tests alone do not establish live
  Kubernetes behavior.
- Public API, CRD, report, RBAC, and datasource compatibility changes are
  reviewed and documented.

## Engineering Baseline

- **Decision:** `PASS_WITH_RISKS` for planning; no implementation is approved
  by this document alone beyond the requested planning work.
- **Critical invariants:** evidence provenance; bounded causal claims; explicit
  insufficiency; read-only evidence gathering; least privilege; deterministic
  retries; compatible public contracts.
- **Invariants:** `APPLIED` — carried into the plan as hard requirements.
- **Challenge:** `APPLIED` — prioritize evidence-to-actionability over detector
  count.
- **Delete:** `NOT_APPLICABLE` — planning task; no runtime implementation
  changed.
- **Simplify:** `APPLIED` — build on existing profiles, status, datasource
  capabilities, and query budgets before adding abstractions.
- **Measure:** `APPLIED` — define outcome metrics before claiming improvement;
  no performance claims are made.
- **Automate:** `APPLIED` — require deterministic templates and scenario
  qualification before expanding runtime automation.
- **Govern:** `APPLIED` — treat additive status/report fields, datasource
  behavior, RBAC, and evidence retention as public/security contracts.

## Not In Scope

- Lowering the evidence bar for `Confirmed` to improve headline resolution
  rates.
- Treating every detected anomaly as a root cause.
- Arbitrary model-generated PromQL, LogQL, Kubernetes reads, or mutations.
- Enabling remediation from `Inferred` claims or hypotheses.
- Claiming full cluster-wide RCA before Node, network, storage, and dependency
  scenarios have supported evidence contracts and qualification results.
