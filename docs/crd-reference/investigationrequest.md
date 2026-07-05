# `InvestigationRequest`

`InvestigationRequest` is the planned read-only ad-hoc investigation contract for `v0.3` alpha.

## Purpose

Use `InvestigationRequest` to ask FluxAgent to investigate one target now, collect evidence from selected datasources, and generate RCA without moving directly into remediation.

It is intended to complement `RiskRule`, not replace it.

- `RiskRule`: recurring background checks
- `InvestigationRequest`: one-off or externally triggered investigation

## Proposed Spec Fields

Suggested first fields:

- `target`
- `timeRange.lookback`
- `question`
- `dataSources[]`
- `modelProviderRef`
- `mode`
- `createRiskSignal`

Example:

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
```

## Proposed Status Fields

Suggested first status:

- `phase`
- `summary`
- `hypothesis`
- `confidence`
- `provider`
- `startedAt`
- `completedAt`
- `evidenceRefs`
- `linkedRiskSignalRef`
- `conditions`

## Proposed Conditions

Condition types:

- `Ready`
- `TargetResolved`
- `EvidenceCollectionReady`
- `RCAReady`
- `Degraded`

Common failure reasons should align with the rest of the platform:

- `DataSourceNotFound`
- `CapabilityMismatch`
- `ProviderNotFound`
- `ProviderUnavailable`
- `InvalidProviderResponse`

## Execution Semantics

The initial contract should remain strictly read-only.

That means:

- evidence collection only
- RCA generation only
- no executor routing
- no `AgentAction`
- optional promotion into `RiskSignal`

## Design Notes

- The CRD should be the common entrypoint for future CLI and UI experiences.
- Investigation results should be persisted as Kubernetes workflow state.
- The first alpha should prefer status summaries and evidence references over large raw payload storage.

See also:

- [../architecture/v0.3-investigation-experience.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/v0.3-investigation-experience.md:1)
- [../architecture/investigation-flow.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/investigation-flow.md:1)
