# FluxAgent Docs

FluxAgent is documented as a Kubernetes-native operator with a safe default path and an optional guarded execution path.

## Structure

```text
docs/
├─ architecture/
│  ├─ overview.md
│  ├─ dependency-neutrality.md
│  ├─ read-only-flow.md
│  ├─ remediation-flow.md
│  ├─ v0.2-read-only-rca.md
│  ├─ v0.2-adapter-neutral-backlog.md
│  ├─ model-gateway.md
│  └─ action-executor.md
├─ crd-reference/
│  ├─ datasource.md
│  ├─ riskrule.md
│  ├─ risksignal.md
│  ├─ remediationplan.md
│  └─ agentaction.md
├─ adapters/
│  ├─ prometheus.md
│  ├─ loki.md
│  ├─ kubernetes-events.md
│  └─ model-providers.md
└─ tutorials/
   ├─ quickstart-kind.md
   ├─ enable-prometheus.md
   ├─ enable-loki.md
   ├─ enable-remediation.md
   └─ enable-hosted-model-providers.md
```

## Start Here

- [architecture/overview.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/overview.md:1)
- [architecture/dependency-neutrality.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/dependency-neutrality.md:1)
- [architecture/read-only-flow.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/read-only-flow.md:1)
- [architecture/v0.2-read-only-rca.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/v0.2-read-only-rca.md:1)
- [architecture/v0.2-adapter-neutral-backlog.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/v0.2-adapter-neutral-backlog.md:1)
- [release-checkpoint.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/release-checkpoint.md:1)
- [tutorials/quickstart-kind.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/tutorials/quickstart-kind.md:1)
- [tutorials/enable-hosted-model-providers.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/tutorials/enable-hosted-model-providers.md:1)
- [github-repo.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/github-repo.md:1)

## Diagram Sources

- `drawio/Kubernetes-native AI Agent Platform Architecture v1.drawio`
  Historical draft source.
- `drawio/Kubernetes-native AI Agent Platform Architecture v2.drawio`
  Current maintained source.
  Layer 2 detail pages: `Layer 2-1` through `Layer 2-5`

## Current Product Truth

- `v0.1` is a read-only `RiskSignal` operator by default.
- `RemediationPlan` and `AgentAction` are available as guarded expansion paths.
- Prometheus, Loki, and Kubernetes Events adapters are wired into the runnable demo path.
- Prometheus and Loki remain optional adapters even when the demo wires them through a fake observability service.
- `v0.2` planning is centered on a configurable read-only RCA platform, not autonomous remediation first.
