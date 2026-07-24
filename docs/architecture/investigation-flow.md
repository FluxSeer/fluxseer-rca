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
InvestigationRequest
→ InvestigationRequestReconciler
→ investigation.Service
→ Datasource Adapters
→ Evidence Redaction
→ Model Gateway
→ InvestigationRequest.status
→ optional RiskSignal
```

This path is the current bounded read-only RCA flow. It calls `ModelProvider` for structured reasoning over collected evidence, but it does not run a long-lived agent runtime.

Future deep investigation should add an explicit executor boundary:

```text
InvestigationRequest
→ controller-owned execution key
→ Investigation Worker / Kubernetes Job
→ Codex SDK/CLI, Claude Agent SDK, or static executor
→ InvestigationResult
→ InvestigationRequest.status
```

That executor path is separate from `ModelProvider` because it may perform multi-turn tool use, repository checkout, additional read-only cluster inspection, command execution, or test execution.

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
5. redact sensitive evidence
6. call the selected `ModelProvider`
7. persist summary, hypothesis, confidence, provider, and conditions to status
8. optionally create a linked `RiskSignal`
9. delete the completed request after `ttlSeconds`, when retention is enabled

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

When `createRiskSignal: true`, FluxAgent can promote the result into the standard risk workflow.

## Safety Boundary

This path should not:

- execute remediation
- create `AgentAction`
- call executors
- mutate workloads
- require chat or UI infrastructure
- depend on a developer's local interactive Codex, Claude, or ChatGPT session

It should produce investigation output only.

Pod or runner based investigation executors must use independent workload-scoped credentials. Acceptable patterns include project-scoped API keys, service-account API keys, enterprise access tokens, or future supported workload identity mechanisms. Local OAuth caches, interactive CLI auth files, ChatGPT sessions, and Codex Remote sessions should not become Kubernetes secrets.

## Future Executor Controls

Future `InvestigationExecutor` requests should carry explicit scope and budget controls.

Example shape:

```yaml
spec:
  evidenceDigest: sha256:...
  executor:
    type: codex
    model: codex-capable-model
  scope:
    clusterAccess: read-only
    namespaces:
      - production
    repository:
      url: github.com/example/payment-api
      revision: 7d21c3f
      writeAccess: false
  budget:
    maxInputTokens: 500000
    maxOutputTokens: 50000
    maxToolCalls: 40
    maxDurationSeconds: 900
  output:
    mode: rca-and-patch-proposal
    createPullRequest: false
```

Execution idempotency should include:

```text
InvestigationRequest UID
+ metadata.generation
+ evidenceDigest
+ executorConfigDigest
+ repositoryCommitSHA
```

For the same execution key, the controller should observe the existing result instead of starting another paid model or agent run. If evidence, spec, executor configuration, or repository revision changes, FluxAgent can create a new execution key and attempt.

## Future Entry Points

The CRD should be the source of truth.

Future entrypoints should be wrappers around it:

- `fluxagent investigate ...`
- a thin UI form
- a webhook bridge
- a chat bridge

All of them should end at `InvestigationRequest`, not bypass it.

For lifecycle consistency with `RiskSignal`, terminal `InvestigationRequest` objects should be retained until `status.completedAt + spec.ttlSeconds` and should not re-run unless the spec changes and Kubernetes issues a new generation.
