# Read-only Investigation Flow

This document describes the early ad-hoc investigation path for `v0.3` alpha.

Unlike the default background path, this workflow starts from an explicit investigation request instead of a standing rule or workload annotation.

## Goal

Let an operator or another system ask:

```text
Investigate this workload now, collect read-only evidence, and produce RCA.
```

The path should remain:

- read-only
- auditable
- provider-neutral
- datasource-neutral
- compatible with future CLI or UI entrypoints

## Runtime Shape

Target runtime path:

```text
RiskRule / Alert / Manual Request
→ InvestigationRequest
→ InvestigationRequestReconciler
→ investigation.Service
→ Datasource Adapters
→ Bounded Evidence Bundle
→ Evidence Redaction
→ Model Gateway
→ Claim Verification
→ InvestigationRequest.status
→ optional discovered RiskSignal
```

This path is the current bounded read-only RCA flow. It calls `ModelProvider` for structured reasoning over collected evidence. FluxAgent's supported open-source path does not run long-lived CLI agent runtimes or reuse developer-local interactive sessions.

In `v0.2`, external alerting systems integrate by creating `InvestigationRequest` resources through the Kubernetes API. Built-in alert receivers, webhook ingress, and Kubernetes Event to `InvestigationRequest` adapters are future producer adapters.

## Why This Path Exists

`RiskRule` answers recurring policy questions:

```text
What should FluxAgent keep checking automatically?
```

`InvestigationRequest` should answer immediate operator questions:

```text
What should FluxAgent investigate now?
```

That split keeps the system declarative without forcing every user interaction into a future chat product.

`RiskRule`-driven RCA can remain as a compatibility flow, but the target architecture should route recurring detections, external alerts, and manual requests through `InvestigationRequest` before calling hosted reasoning providers. That keeps RCA orchestration, evidence provenance, provider retries, verification, and optional discovered-signal emission in one workflow contract.

## Proposed Spec

Suggested first request:

```yaml
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: investigate-open-api
  namespace: fluxagent-system
spec:
  target:
    kind: Deployment
    namespace: prod
    name: open-api
    apiVersion: apps/v1
  timeRange:
    lookback: 15m
  question: |
    Why did open-api latency increase after the latest rollout?
  dataSources:
    - name: kubernetes-events
    - name: prometheus-main
    - name: loki-main
  modelProviderRef:
    name: heuristic-provider
  mode: readOnly
  createRiskSignal: false
  ttlSeconds: 3600
```

## Control Flow

Suggested sequence:

1. resolve the target resource
2. validate datasource references
3. validate datasource capabilities against requested query intent
4. collect evidence within the requested lookback window
5. normalize raw query output into compact observations
6. redact sensitive evidence before provider-bound reasoning
7. assemble a bounded evidence bundle
8. call the selected `ModelProvider`
9. verify provider claims against collected evidence
10. persist verdict, summary, hypothesis, confidence, provider, claims, evidence references, and conditions to status
11. optionally emit a linked discovered `RiskSignal`
12. delete the completed request after `ttlSeconds`, when retention is enabled

## Evidence Bundle Contract

Reasoning providers should receive normalized observations instead of unrestricted Kubernetes API access, raw Prometheus responses, or large Loki excerpts.

Minimum evidence reference shape:

```yaml
evidence:
  - id: prom-request-rate
    datasourceRef:
      name: prometheus
    capability: MetricsQuery
    queryDigest: sha256:...
    timeRange:
      start: "2026-07-28T08:00:00Z"
      end: "2026-07-28T08:10:00Z"
    observation:
      currentValue: 480
      baselineValue: 100
      changeRatio: 4.8
    collectedAt: "2026-07-28T08:10:05Z"
    contentDigest: sha256:...
    redactionProfile: default-v1
    truncated: false
```

Provider-bound context should prefer statements such as:

```text
request rate increased 4.8x
CPU throttling increased from 2% to 36%
p95 latency increased from 180ms to 1.9s
17 upstream timeout logs occurred
```

This improves cost, privacy, and RCA quality while preserving auditability.

## Status Model

Recommended condition types:

- `Ready`
- `TargetResolved`
- `EvidenceCollectionReady`
- `RCAReady`
- `Degraded`

Recommended failure and degraded reasons should align with existing controller vocabulary:

- `DataSourceNotFound`
- `CapabilityMismatch`
- `ProviderNotFound`
- `ProviderUnavailable`
- `InvalidProviderResponse`

## Relationship With `RiskSignal`

`InvestigationRequest` should be allowed to complete without creating a `RiskSignal`.

That default is important because:

- not every investigation is a risk event
- users may want RCA without opening another workflow object
- read-only investigation should stay cheap and low-side-effect

When `createRiskSignal: true`, FluxAgent can emit a discovered `RiskSignal` for downstream workflows. The RCA itself remains on `InvestigationRequest.status`; the linked `RiskSignal` is a materialized finding, not a replacement for the investigation result.

Discovered `RiskSignal` emission must preserve lineage and avoid loops:

- emitted signals should reference the source investigation through annotation or status metadata
- discovered signals should not automatically trigger another `InvestigationRequest` by default
- future reinvestigation policies must enforce fingerprint deduplication, maximum investigation depth, and cooldowns

## Safety Boundary

This path should not:

- execute remediation
- create `AgentAction`
- call executors
- mutate workloads
- require chat or UI infrastructure
- depend on a developer's local interactive model or CLI session

It should produce investigation output only.

Investigation workflows use `ModelProvider` with heuristic output by default or workload-scoped OpenAI, Claude, and Gemini API credentials when hosted RCA is enabled. Local OAuth caches, interactive CLI auth files, and personal chat sessions are not supported Kubernetes credentials.

## Provider Controls

Hosted `ModelProvider` requests should carry explicit provider and budget controls.

Example shape:

```yaml
spec:
  evidenceDigest: sha256:...
  providerRef:
    name: openai-provider
  budget:
    maxInputTokens: 20000
    maxOutputTokens: 2000
    maxDurationSeconds: 30
  output:
    mode: rca
```

Execution idempotency should include:

```text
InvestigationRequest UID
+ metadata.generation
+ evidenceDigest
+ providerConfigDigest
```

For the same execution key, the controller should observe the existing result instead of starting another paid model call. If evidence, spec, or provider configuration changes, FluxAgent can create a new execution key and attempt.

## Future Entry Points

The CRD should be the source of truth.

Future entrypoints should be wrappers around it:

- `fluxagent investigate ...`
- a thin UI form
- a webhook bridge
- a chat bridge

All of them should end at `InvestigationRequest`, not bypass it.

For lifecycle consistency with `RiskSignal`, terminal `InvestigationRequest` objects should be retained until `status.completedAt + spec.ttlSeconds` and should not re-run unless the spec changes and Kubernetes issues a new generation.
