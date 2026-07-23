# FluxAgent Product Requirements Baseline

Last updated: 2026-07-23

This document consolidates the product requirements that should guide README, architecture, CRD, and release-scope wording.

## Product Positioning

FluxAgent is a Kubernetes-native SRE investigation control plane with optional AI-assisted reasoning.

Long-term positioning:

```text
Kubernetes-native, evidence-first SRE investigation and risk analysis control plane.
```

Current release scope:

```text
v0.2 focuses on read-only RCA workflows and is currently a beta candidate.
```

The long-term product positioning is intentionally broader than the current release scope. Future remediation, multi-cluster, and policy workflows should extend the product without redefining it.

## Product Principles

- read-only by default
- evidence-first investigation and RCA
- Kubernetes-native workflow state through CRDs
- optional AI-assisted reasoning, with heuristic and local providers remaining valid paths
- datasource and model-provider neutrality through adapter contracts
- graceful degradation for optional integrations
- guarded remediation as an opt-in secondary path
- stable status and condition reasons for CLI, dashboard, alerting, and GitOps consumers

## Current Runtime Scope

The current verified beta candidate includes:

- `RiskRule`-driven recurring detection
- `DataSource`-backed evidence collection
- `ModelProvider`-backed RCA generation
- `RiskSignal` output with evidence and RCA status
- `InvestigationRequest` ad-hoc read-only investigation
- `fluxagent investigate` as a CLI wrapper around `InvestigationRequest`
- optional `InvestigationRequest` promotion into `RiskSignal`
- webhook notification
- TTL cleanup for `RiskSignal` and `InvestigationRequest`

The current scope does not include production-grade autonomous remediation.

## Workflow Ownership

Controllers own Kubernetes workflow state transitions. Shared orchestration should live in internal services instead of controller-to-controller calls.

Recommended ownership:

```text
RiskRule Controller
  -> resolve target
  -> validate datasource capability
  -> execute detection query
  -> create/update RiskSignal

InvestigationRequest Controller
  -> resolve target
  -> execute evidence queries
  -> redact and normalize evidence
  -> invoke ModelProvider
  -> update terminal status
  -> optionally create RiskSignal

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

Graceful degradation means more than avoiding reconcile crashes. Each workflow should distinguish evidence collection, RCA generation, notification, and promotion as separate result dimensions.

Expected matrix:

| Scenario | Expected result |
| --- | --- |
| One optional datasource is missing | Continue when other evidence is available; mark partial evidence |
| All datasources fail | Investigation or detection should fail evidence collection |
| Primary provider fails with fallback available | Use fallback for retryable failures; mark degraded |
| Primary and fallback provider both fail | Mark provider unavailable or specific provider failure |
| Provider response has invalid schema | Mark `InvalidProviderResponse`; do not persist incorrect RCA |
| Notification fails | Preserve successful RCA; mark notification degraded |
| RiskSignal promotion fails | Preserve successful investigation; mark promotion failed |

Workflow success, RCA success, notification success, and promotion success must remain separate dimensions.

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

## Release Tag Freeze Criteria

Before tagging `v0.2.0-beta.1`, freeze and confirm:

- the tag points at a commit that passed the release gate
- CRD YAML and generated deepcopy/client code are consistent
- release notes correspond to the same commit
- the kind verification uses the image intended for release, not uncommitted local code
- Kustomize manifests use the intended version or image reference
- upgrade and uninstall paths have at least smoke-test coverage or are explicitly documented as pending

The release must remain framed as a read-only RCA beta, not as a production remediation platform.
