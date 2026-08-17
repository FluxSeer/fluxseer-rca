# FluxSeer RCA Product Requirements Baseline

Last updated: 2026-08-17

This document consolidates the product requirements that should guide README, architecture, CRD, and release-scope wording.

## Product Positioning

FluxSeer RCA is the product name for the Kubernetes-native control plane for
evidence-verifiable root cause analysis. Source code and build artifacts now
use matching `fluxseer` / `fluxseer-rca` naming.

Historical compatibility name (`v0.3.0-beta.3`):

```text
FluxAgent
```

Target product name:

```text
FluxSeer RCA
```

Long-term positioning:

```text
Kubernetes-native, evidence-verifiable RCA control plane.
```

Product philosophy:

```text
FluxSeer RCA turns RCA into a governed, evidence-verifiable,
replay-oriented Kubernetes-native workflow.
```

Highest-value promise:

```text
FluxSeer RCA turns every Kubernetes incident investigation into verifiable,
auditable, replayable, and reusable organizational knowledge instead of
letting it disappear into Slack threads, dashboard screenshots, terminal
history, or individual responder memory.
```

Current release scope:

```text
v0.4.0-beta.3 is the current beta release. It retains the frozen v0.3
canonical RCA contract and adds approval lifecycle governance, audit timestamps,
notification retry tracking, and terminal-state TTL cleanup for guarded
remediation resources. Policy Pack resources are implemented as opt-in
governance foundations; autonomous production remediation is not included.
```

The long-term product positioning is intentionally narrower than a general AI SRE agent. Future remediation, multi-cluster, and policy workflows should extend the product without redefining it. The product rename must not be used as a shortcut for breaking the current v1alpha1 API, metric, annotation, or release-artifact compatibility surfaces once real external installs depend on them. (The `fluxagent` -> `fluxseer` / `fluxseer-rca` metric, annotation, and schema/digest identifier rename was an exception made before any external cluster ran this product; see [architecture/rename-migration-plan.md](architecture/rename-migration-plan.md).)

## Product Principles

- read-only by default
- security-first and secret-minimizing by design
- evidence-first investigation and RCA
- evidence-linked claims and verification status
- Kubernetes-native workflow state through CRDs
- explicit configuration over black-box discovery
- optional AI-assisted reasoning, with heuristic default plus OpenAI, Claude, and Gemini API providers
- datasource and model-provider neutrality through adapter contracts
- low default footprint; optional integrations must remain opt-in
- graceful degradation for optional integrations
- guarded remediation as an opt-in secondary path
- stable status and condition reasons for CLI, dashboard, alerting, and GitOps consumers
- detection success does not imply RCA confirmation
- the RCA verdict must not be more specific than the strongest evidence-supported causal claim

The maintained terminology for detection patterns, signal templates, evidence
profiles, evidence sufficiency, verification, and verdict/outcome is defined in
the [product and API glossary](glossary.md).

## Product Boundaries

FluxSeer RCA should not compete on the breadth of built-in Kubernetes analyzers, autonomous agent tool use, full observability ownership, or production self-healing.

Primary product ownership:

```text
InvestigationRequest
-> bounded evidence collection
-> structured hypotheses
-> evidence-linked claims
-> independent verification
-> auditable RCA status
```

Canonical RCA ownership should remain `InvestigationRequest`-first:

```text
RiskRule / Alert / Manual Request
-> InvestigationRequest
-> bounded evidence bundle
-> provider-neutral structured RCA
-> claim verification
-> InvestigationRequest.status
-> optional discovered RiskSignal
```

`RiskRule` should detect recurring conditions. `InvestigationRequest` should orchestrate investigation and own canonical RCA output. `RiskSignal` should preserve confirmed or trackable risks, summaries, lineage, and compact evidence references, not become the primary RCA orchestration surface.

Secondary entrypoint:

```text
RiskRule
-> RiskSignal
-> optional InvestigationRequest
```

`RiskRule` is an optional bootstrap signal source. It exists to help users create initial signals from Kubernetes Events, Prometheus, or Loki, but it should not become the center of product identity or a replacement for Alertmanager.

Experimental extension:

```text
RCA Result
-> Remediation Proposal
-> Policy Evaluation
-> Human Approval
-> Deterministic Execution
-> Post-action Verification
```

Remediation must remain downstream from RCA and must not grant reasoning providers direct executor credentials.

## Existence And Value

FluxSeer RCA exists to give Kubernetes platform teams an auditable RCA workflow substrate without requiring them to adopt a black-box AI monitoring agent, a full replacement observability stack, or a specific model vendor.

The core advantage is not that FluxSeer RCA can also use AI to look at Kubernetes problems. The core advantage is that it turns temporary, opaque, and hard-to-repeat RCA into durable organizational knowledge with governed workflow state.

FluxSeer RCA should solve operational problems platform teams actually feel:

- nobody knows what was checked during an incident
- different responders reach different conclusions for the same symptoms
- AI answers cannot be tied back to evidence
- sensitive data transmission is invisible or uncontrolled
- model upgrades cannot be evaluated against previous investigations
- incident knowledge does not become reusable assets

The intended value is:

- teams define what matters through native Kubernetes resources
- FluxSeer RCA collects bounded evidence from declared datasources
- evidence is normalized and redacted before model reasoning
- heuristic mode remains usable without external API calls
- hosted OpenAI, Claude, and Gemini providers are opt-in through workload-scoped credentials
- RCA output is stored in CRD status for GitOps, dashboards, alerting, and automation
- important RCA claims should become linked to compact evidence references and explicit verification status
- completed investigations can become reusable incident knowledge instead of staying in Slack, screenshots, terminal history, or responder memory
- optional remediation remains guarded and secondary

This project intentionally accepts some YAML and CRD learning cost in exchange for high customizability, provider neutrality, lower default resource usage, and security-first data boundaries.

The minimum compelling promise is:

```text
Every important RCA claim can be traced to evidence.
Every hosted provider transmission can be governed and audited.
Every terminal investigation can become a reusable replay artifact or evaluation input.
```

FluxSeer RCA should become useful first for teams that already have Kubernetes, Prometheus, Loki, Alertmanager, and GitOps, but need AI-assisted RCA to satisfy platform governance instead of bypassing it.

## Product Moats

### RCA Contract

FluxSeer RCA should standardize:

- verdict
- claim
- evidence linkage
- missing evidence
- degradation semantics
- execution metadata

### Replay Corpus

FluxSeer RCA should make completed investigations reusable for:

- provider regression tests
- prompt and model upgrade comparison
- heuristic regression
- verifier regression
- rule-pack evaluation
- offline comparison of recorded RCA bundles; full runtime replay remains future work

### Policy Governance

FluxSeer RCA should keep these controls explicit and auditable:

- query policy
- data classification
- redaction
- hosted provider egress
- evidence retention
- RBAC profiles

### Kubernetes Ecosystem Integration

FluxSeer RCA should integrate through Kubernetes-native surfaces:

- `RiskRule`
- `InvestigationRequest`
- `RiskSignal`
- Prometheus and Alertmanager
- Argo CD and Argo Rollouts
- Kyverno or Gatekeeper
- Grafana
- GitHub and notification systems

### Community Assets

The long-term community asset should be high-quality, replayable, and verifiable Kubernetes incident knowledge:

- web API profiles
- worker profiles
- queue profiles
- database client profiles
- ingress profiles
- resource contention profiles
- traffic anomaly profiles

## Non-Goals And False Wedges

FluxSeer RCA should avoid product directions that dilute the control-plane value:

- building another AI Kubernetes chatbot
- claiming that CRDs alone make a control plane
- treating provider count as the main product value
- making automatic remediation the proof of completeness
- prioritizing dashboard or Slack UX before replay, policy, and contract quality

The project should first prove that it can produce trusted, traceable, and repeatable RCA. Autonomous action remains downstream and guarded.

## Security Principles

Read-only does not automatically prevent data exposure. Logs, events, pod specs, ConfigMaps, and error messages can still contain tokens or connection strings. FluxSeer RCA therefore treats evidence minimization and redaction as core product requirements, not implementation details.

Reasoning providers must not receive unrestricted Kubernetes access. Hosted providers should receive only bounded, normalized, and redacted evidence collected through declared `InvestigationRequest`, `RiskRule`, datasource, and policy configuration.

Required security posture:

- never send Kubernetes Secret values to model providers
- redact sensitive-looking evidence before hosted API calls
- do not persist raw model prompts, provider responses, large log bodies, or unredacted Kubernetes objects
- do not run autonomous CLI agents or shell-based model runtimes in the cluster
- do not package developer-local OAuth caches, subscription sessions, or interactive CLI auth files as workload credentials
- do not mutate workloads unless guarded remediation is explicitly enabled
- allow heuristic-only operation for zero external model data transfer
- keep provider credentials explicit, scoped, revocable, and referenced through `ModelProvider`

## Current Runtime Scope

The current published beta release includes:

- canonical `InvestigationRequest` ad-hoc read-only investigation
- Kubernetes workload RCA target coverage for `Deployment`, `StatefulSet`, `DaemonSet`, `Job`, `CronJob`, and Pod owner-chain canonicalization
- structured `InvestigationRequest.status` with verdict, claims, alternative hypotheses, missing evidence, degradation, execution, lineage, identity, and compatibility projections
- `RiskRule`-driven recurring detection and baseline rule packs as bootstrap entrypoints
- `DataSource`-backed evidence collection for Kubernetes Events, Prometheus, and Loki
- `ModelProvider`-backed RCA generation with heuristic default plus optional OpenAI, Claude, and Gemini API providers
- explicit hosted-provider data egress policy on the canonical `InvestigationRequest` path
- `RiskSignal` output with materialized findings, compact evidence references, and compatibility RCA fields for read-only rule evaluation
- `fluxseer investigate` as a CLI wrapper around `InvestigationRequest`
- optional discovered `RiskSignal` materialization from `InvestigationRequest`
- webhook notification
- approval lifecycle, audit timestamps, and notification retry tracking for
  guarded remediation
- TTL cleanup for `RiskSignal`, `InvestigationRequest`, `RemediationPlan`, and
  `AgentAction`
- opt-in `ApprovalPolicy`, `NamespaceThreshold`, and `EscalationChain`
  governance foundations
- read-only Helm default with the legacy Deployment watcher disabled
- least-privilege default RBAC without remediation or executor mutation permissions

The current scope does not include production-grade autonomous remediation.

The current scope includes the frozen v0.3 structured `InvestigationRequest.status` contract. Future stabilization should improve runtime coverage, fixtures, dashboards, provider accuracy, and compatibility tests without changing the frozen schema unless an explicit schema-freeze exception is accepted.

## Baseline Rule Pack Contract

The official Helm rule packs contain 21 built-in detection patterns:

- 6 Kubernetes-native workload patterns available without an additional
  observability backend;
- 8 Prometheus metric patterns requiring a configured Prometheus `DataSource`;
- 7 Loki log patterns requiring a configured Loki `DataSource`.

These counts describe maintained defaults, not the limit of the generic
`RiskRule` engine. Application Profile entries are parameterized signal
templates and are not counted as additional built-in detection patterns.
The maintained identities and capability boundaries live in the
[machine-readable detection pattern catalog](../config/rule-packs/detection-patterns.json).

FluxSeer RCA should not require users to hand-write every initial `RiskRule`, but built-in rules must remain explicit, bounded, and secondary to the RCA workflow.

Rule pack principles:

- provide immediate value after install without taking over the user's observability stack
- keep Kubernetes Events baseline enabled by default with a narrow namespace scope
- support explicit workload selectors for `Deployment`, `StatefulSet`, `DaemonSet`, `Job`, and `CronJob`
- keep Prometheus and Loki baselines disabled by default
- never install external datasource backends as a side effect of enabling a rule pack
- never install hosted `ModelProvider` resources or provider secrets as a side effect of enabling a rule pack
- allow users to override target namespace, workload labels, interval, window, severity, RCA enablement, datasource names, and provider reference
- keep portable Kubernetes signals separate from application-specific metric profiles
- parameterize application metric queries instead of hard-coding metric names into controllers
- use stable names, labels, and annotations so GitOps and dashboards can recognize generated rules

Initial Helm contract:

```yaml
rulePacks:
  defaultTargetSelector:
    namespaceSelector:
      matchNames:
        - <release namespace>
    workloadSelector:
      kinds:
        - Deployment
  kubernetesBaseline:
    enabled: true
  prometheusBaseline:
    enabled: false
  lokiBaseline:
    enabled: false
```

The Kubernetes baseline may rely on the built-in Kubernetes Events datasource adapter. Prometheus and Loki baselines may reference `DataSource` names but must not create those datasources or install those systems.

Rule pack categories:

```text
Portable baseline
- CrashLoopBackOff
- ImagePullBackOff
- readiness failure
- workload unavailable or degraded
- CPU throttling
- memory near limit
- restart increase

Application profile
- HTTP request rate
- HTTP latency
- queue depth
- worker saturation
- database connection pool
- external API rate limiting
```

Application profiles must expose query expressions and labels as values. FluxSeer RCA should not assume that every workload uses `http_requests_total`, the same histogram buckets, the same queue metric, or the same application label shape.

Verification contract:

- `make verify-rule-packs` validates Helm rendering, stable rule names, labels, disabled-by-default optional packs, and absence of implicit datasource or provider creation.
- `make verify-rule-packs-kind` installs the chart into kind, triggers Kubernetes Events, Prometheus, and Loki baseline evidence through fake observability, and verifies baseline `RiskSignal` plus heuristic RCA status.

## Workflow Ownership

Controllers own Kubernetes workflow state transitions. Shared orchestration should live in internal services instead of controller-to-controller calls.

Recommended ownership:

```text
RiskRule Controller
  -> resolve target
  -> validate datasource capability
  -> execute detection query
  -> create/update InvestigationRequest by default
  -> create/update RiskSignal through the DirectRiskSignal compatibility path

InvestigationRequest Controller
  -> resolve target
  -> execute evidence queries
  -> redact and normalize evidence
  -> invoke ModelProvider
  -> update terminal RCA status
  -> optionally materialize discovered RiskSignal when requested

RiskSignal Controller
  -> manage RiskSignal lifecycle and optional downstream guarded planning
  -> avoid re-running provider RCA for already materialized RCA output

RemediationPlan Controller
  -> evaluate guardrails and approval state
  -> create AgentAction

AgentAction Controller
  -> execute or simulate approved action
  -> record execution result
```

Recommended shared services:

- `TargetResolver`
- `EvidenceCollector`
- `EvidenceNormalizer`
- `Redactor`
- `ReasoningGateway`
- `RiskSignalBuilder`
- `NotificationDispatcher`

## Provider And Agent Runtime Boundary

FluxSeer RCA should keep synchronous model reasoning separate from multi-step agent execution.

`ModelProvider` is the abstraction for bounded, structured, relatively stateless RCA generation:

```text
EvidenceBundle
-> ModelProvider
-> structured RCA output
```

Expected implementations include:

- `HeuristicProvider`
- `OpenAIProvider`
- `ClaudeProvider`
- `GeminiProvider`

FluxSeer RCA's open-source product line intentionally does not include CLI agent runtimes, subscription-session runners, or Kubernetes pods that mount developer-local interactive auth caches. RCA reasoning is provided through `ModelProvider` using either the no-secret heuristic provider or workload-scoped API credentials for OpenAI, Claude, and Gemini.

`ModelProvider` is the current `v1alpha1` name. Before a breaking API cut, evaluate whether the abstraction should become `ReasoningProvider` to cover heuristic, hosted model APIs, and future RCA runtime providers without implying that every provider is a model.

The core permission rule is:

```text
Production Kubernetes write access
Git repository write access
LLM autonomous execution
```

These capabilities must not all be granted to the same pod or ServiceAccount.

## Trustworthy RCA Contract

FluxSeer RCA now includes the frozen v0.3 structured RCA status contract for `InvestigationRequest`. The next product hardening target is runtime validation, replay coverage, dashboards, provider accuracy, and real-cluster compatibility without changing the frozen schema.

The contract should make this relationship explicit:

```text
Detection or explicit trigger
-> Evidence collection
-> Evidence sufficiency
-> RCA hypothesis
-> Evidence-linked claims
-> Verification status
-> Bounded verdict/outcome
```

A detected incident must remain distinguishable from a confirmed RCA. Missing
required evidence must produce an abstaining outcome, and verification must
prevent provider specificity from exceeding the strongest supported causal
claim.

Compatibility status projections:

- summary
- hypothesis
- confidence
- provider
- evidence references with stable IDs
- linked discovered `RiskSignal` reference
- workflow and readiness conditions

Canonical v0.3 status fields:

- verdict summary and outcome
- root cause entity
- root cause type
- normalized confidence
- claims
- degradation and partial failure metadata
- provider execution metadata
- alternative hypotheses
- missing evidence

Verification values should distinguish at least:

- `Supported`
- `Inferred`
- `Unsupported`
- `Contradicted`
- `Unverified`

Important RCA statements without evidence references must not be represented as fully supported conclusions.

Verdict outcomes should support abstention instead of forcing a confident answer when evidence is insufficient. Target outcomes:

- `Confirmed`
- `Inconclusive`
- `NoIssueFound`
- `Unknown`

Workflow execution failures are not RCA outcomes. They should be represented as `status.phase=Failed`, `status.outcome=Unknown`, and a populated `status.failure`.

Evidence provenance should preserve compact metadata even when raw payloads are not stored:

- datasource
- capability
- query digest
- time range
- collected timestamp
- content digest
- digest algorithm and canonicalization version
- redaction profile
- truncation flag
- original size
- original and retained byte counts
- retention policy

The first verifier can be heuristic. It should check whether each claim cites evidence, whether the evidence type is relevant, whether contradictory evidence exists, and whether confidence is consistent with evidence coverage.

Confidence is a ranking signal from the provider or verifier. It must not be documented as a calibrated probability of correctness unless a future evaluator explicitly calibrates it.

Execution metadata should support audit and comparison:

- provider reference and generation
- provider type
- model name
- reasoning policy version
- controller version
- attempt count
- duration
- token usage when available

Discovered-signal emission must carry lineage and avoid investigation loops. By default, a `RiskSignal` emitted from an `InvestigationRequest` should not automatically trigger another investigation unless an explicit reinvestigation policy opts in and maximum depth, fingerprint deduplication, and cooldown constraints are satisfied.

## CRD Contract Requirements

### InvestigationRequest Modes

`InvestigationRequest.spec.dataSources[]` and `InvestigationRequest.spec.queries[]` are mutually exclusive planning modes.

Runtime behavior:

- exactly one mode should be specified
- if both are set, surface `InvalidSpec`
- if neither is set, surface `InvalidSpec`
- controller behavior should not silently prefer one mode over the other

Optional future CRD admission validation:

```yaml
x-kubernetes-validations:
  - rule: "has(self.dataSources) != has(self.queries)"
    message: "exactly one of dataSources or queries must be specified"
```

### Common Status Fields

Major workflow CRDs should converge on:

```yaml
status:
  observedGeneration:
  phase:
  conditions:
  startedAt:
  completedAt:
```

Rationale:

- `observedGeneration` prevents stale status from being mistaken for current spec handling
- `phase` gives humans and CLI tools a quick workflow summary
- `conditions` provide machine-readable state
- `startedAt` and `completedAt` support timing, retention, and TTL behavior

`InvestigationRequest` should define explicit terminal phases instead of requiring callers to infer terminal state from conditions alone.

Recommended terminal phases:

- `Succeeded`
- `Failed`
- `PartiallySucceeded`
- `Cancelled`
- `Expired`

### Stable Condition Reasons

Condition reasons are public API for automation. They should be treated as stable names rather than implementation log messages.

Recommended reason vocabulary:

- `DataSourceNotFound`
- `UnsupportedQueryType`
- `QueryTimeout`
- `QueryAuthenticationFailed`
- `QueryRateLimited`
- `ProviderUnavailable`
- `ProviderTimeout`
- `InvalidProviderResponse`
- `InsufficientEvidence`
- `PartialEvidenceAvailable`
- `NotificationFailed`
- `PromotionFailed`
- `InvalidSpec`

Existing reason names should be preserved where already published or tested. New names should be added intentionally and documented in CRD references.

## Graceful Degradation Semantics

Graceful degradation means more than avoiding reconcile crashes. Each workflow should distinguish evidence collection, RCA generation, notification, and discovered-signal emission as separate result dimensions.

Expected matrix:

| Scenario | Expected result |
| --- | --- |
| One optional datasource is missing | Continue when other evidence is available; mark partial evidence |
| All datasources fail | Investigation or detection should fail evidence collection |
| Primary provider fails with fallback available | Use fallback for retryable failures; mark degraded |
| Primary and fallback provider both fail | Mark provider unavailable or specific provider failure |
| Provider response has invalid schema | Mark `InvalidProviderResponse`; do not persist incorrect RCA |
| Notification fails | Preserve successful RCA; mark notification degraded |
| Discovered RiskSignal creation fails | Preserve successful investigation; mark signal creation failed |

Workflow success, RCA success, notification success, and discovered-signal emission success must remain separate dimensions.

## Evidence Storage Boundary

`RiskSignal` and `InvestigationRequest.status.evidenceRefs` should store compact evidence references and summaries, not raw observability or model payloads.

Allowed to persist:

- datasource type
- datasource name
- query time range
- query type
- evidence digest
- small evidence summary
- resource references
- RCA conclusion
- confidence
- evidence count
- redaction metadata

Should not persist:

- full Prometheus payloads
- large Loki log bodies
- full model prompts
- provider raw responses
- secrets, tokens, or authorization headers
- unredacted Kubernetes objects

Future optional external evidence references may use a shape such as:

```yaml
evidenceRefs:
  - kind: ObjectStorage
    uri: s3://example/evidence/...
    digest: sha256:...
    expiresAt: "2026-07-30T00:00:00Z"
```

Object storage must remain optional and must not become a `v0.2` core dependency.

## ModelProvider Fallback Policy

`ModelProvider.spec.fallbackProviderRef` should have an explicit policy boundary.

Recommended fallback-eligible failures:

- timeout
- HTTP `429`
- HTTP `5xx`
- connection failure
- provider unavailable

Recommended non-fallback failures:

- invalid credentials
- malformed provider spec
- unsupported model
- invalid provider response schema
- evidence exceeds configured limits and cannot be truncated safely

Invalid provider responses should remain visible because they may indicate schema drift or a provider adapter bug.

Fallback chains must not loop. `v0.2` should either allow only one fallback level or use runtime visited-provider cycle detection.

## Release Gate Criteria

For release tags, freeze and confirm:

- the tag points at a commit that passed the release gate
- CRD YAML and generated deepcopy/client code are consistent
- release notes correspond to the same commit
- the kind verification uses the image intended for release, not uncommitted local code
- Kustomize manifests use the intended version or image reference
- upgrade and uninstall paths have at least smoke-test coverage or are explicitly documented as pending
- `make verify-release-v0.2-beta` or its documented equivalent passes against the intended release image reference

Historical `v0.2.0-beta.1` passed this gate before it was tagged and published
as a prerelease. That historical release remained framed as a read-only RCA
beta, not as a production remediation platform.
