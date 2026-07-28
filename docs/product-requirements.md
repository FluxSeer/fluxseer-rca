# FluxAgent Product Requirements Baseline

Last updated: 2026-07-23

This document consolidates the product requirements that should guide README, architecture, CRD, and release-scope wording.

## Product Positioning

FluxAgent is a Kubernetes-native control plane for evidence-verifiable root cause analysis.

Long-term positioning:

```text
Kubernetes-native, evidence-verifiable RCA control plane.
```

Current release scope:

```text
v0.2.0-beta.1 is a published prerelease focused on read-only RCA workflows.
```

The long-term product positioning is intentionally narrower than a general AI SRE agent. Future remediation, multi-cluster, and policy workflows should extend the product without redefining it.

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

## Product Boundaries

FluxAgent should not compete on the breadth of built-in Kubernetes analyzers, autonomous agent tool use, full observability ownership, or production self-healing.

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

FluxAgent exists to give Kubernetes platform teams an auditable RCA workflow substrate without requiring them to adopt a black-box AI monitoring agent, a full replacement observability stack, or a specific model vendor.

The intended value is:

- teams define what matters through native Kubernetes resources
- FluxAgent collects bounded evidence from declared datasources
- evidence is normalized and redacted before model reasoning
- heuristic mode remains usable without external API calls
- hosted OpenAI, Claude, and Gemini providers are opt-in through workload-scoped credentials
- RCA output is stored in CRD status for GitOps, dashboards, alerting, and automation
- important RCA claims should become linked to compact evidence references and explicit verification status
- optional remediation remains guarded and secondary

This project intentionally accepts some YAML and CRD learning cost in exchange for high customizability, provider neutrality, lower default resource usage, and security-first data boundaries.

## Security Principles

Read-only does not automatically prevent data exposure. Logs, events, pod specs, ConfigMaps, and error messages can still contain tokens or connection strings. FluxAgent therefore treats evidence minimization and redaction as core product requirements, not implementation details.

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

The current verified beta candidate includes:

- `RiskRule`-driven recurring detection
- `DataSource`-backed evidence collection
- `ModelProvider`-backed RCA generation
- `RiskSignal` output with evidence and RCA status for read-only rule evaluation
- `InvestigationRequest` ad-hoc read-only investigation
- `fluxagent investigate` as a CLI wrapper around `InvestigationRequest`
- optional discovered `RiskSignal` materialization from `InvestigationRequest`
- webhook notification
- TTL cleanup for `RiskSignal` and `InvestigationRequest`

The current scope does not include production-grade autonomous remediation.

The current scope includes the first structured `InvestigationRequest.status` contract. `v0.3` should harden claim verification, richer evidence provenance, abstention semantics, alternative hypothesis disposition, missing-evidence semantics, and partial-failure behavior.

## Baseline Rule Pack Contract

FluxAgent should not require users to hand-write every initial `RiskRule`, but built-in rules must remain explicit, bounded, and secondary to the RCA workflow.

Rule pack principles:

- provide immediate value after install without taking over the user's observability stack
- keep Kubernetes Events baseline enabled by default with a narrow namespace scope
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
- Deployment unavailable
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

Application profiles must expose query expressions and labels as values. FluxAgent should not assume that every workload uses `http_requests_total`, the same histogram buckets, the same queue metric, or the same application label shape.

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
  -> create/update RiskSignal
  -> optionally trigger InvestigationRequest in future

InvestigationRequest Controller
  -> resolve target
  -> execute evidence queries
  -> redact and normalize evidence
  -> invoke ModelProvider
  -> update terminal RCA status
  -> optionally emit discovered RiskSignal in future

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

FluxAgent should keep synchronous model reasoning separate from multi-step agent execution.

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

FluxAgent's open-source product line intentionally does not include CLI agent runtimes, subscription-session runners, or Kubernetes pods that mount developer-local interactive auth caches. RCA reasoning is provided through `ModelProvider` using either the no-secret heuristic provider or workload-scoped API credentials for OpenAI, Claude, and Gemini.

`ModelProvider` is the current `v1alpha1` name. Before a breaking API cut, evaluate whether the abstraction should become `ReasoningProvider` to cover heuristic, hosted model APIs, and future RCA runtime providers without implying that every provider is a model.

The core permission rule is:

```text
Production Kubernetes write access
Git repository write access
LLM autonomous execution
```

These capabilities must not all be granted to the same pod or ServiceAccount.

## Trustworthy RCA Contract

FluxAgent now includes the first compatibility RCA status contract for `InvestigationRequest`. The next major product hardening target is to add stricter structured RCA fields and make the contract more evidence-verifiable across provider adapters and partial-failure cases.

The contract should make this relationship explicit:

```text
Claim
-> Evidence reference
-> Verification status
```

Implemented in `v0.2.0-beta.1`:

- summary
- hypothesis
- confidence
- provider
- evidence references with stable IDs
- linked discovered `RiskSignal` reference
- workflow and readiness conditions

Target for `v0.3`:

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
- `Probable`
- `Inconclusive`
- `NoIssueFound`
- `ExecutionFailed`

Evidence provenance should preserve compact metadata even when raw payloads are not stored:

- datasource
- capability
- query digest
- time range
- collected timestamp
- content digest
- redaction profile
- truncation flag
- original size
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

`InvestigationRequest.spec.dataSources[]` and `InvestigationRequest.spec.queries[]` should be mutually exclusive.

Required behavior:

- exactly one mode should be specified
- if both are set, reject the spec or surface `InvalidSpec`
- if neither is set, reject the spec or surface `InvalidSpec`
- controller behavior should not silently prefer one mode over the other

Preferred CRD validation:

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

`v0.2.0-beta.1` passed this gate before it was tagged and published as a prerelease. The release must remain framed as a read-only RCA beta, not as a production remediation platform.
