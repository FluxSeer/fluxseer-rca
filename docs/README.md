# FluxAgent Docs

FluxAgent is documented as a Kubernetes-native operator with a safe default path and an optional guarded execution path.

## Structure

```text
docs/
├─ architecture/
│  ├─ overview.md
│  ├─ read-only-flow.md
│  ├─ remediation-flow.md
│  ├─ model-gateway.md
│  └─ action-executor.md
├─ crd-reference/
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
   └─ enable-remediation.md
```

## Start Here

- [architecture/overview.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/overview.md:1)
- [architecture/read-only-flow.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/read-only-flow.md:1)
- [tutorials/quickstart-kind.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/tutorials/quickstart-kind.md:1)
- [github-repo.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/github-repo.md:1)

## Current Product Truth

- `v0.1` is a read-only `RiskSignal` operator by default.
- `RemediationPlan` and `AgentAction` are available as guarded expansion paths.
- Prometheus, Loki, and Kubernetes Events adapters are wired into the runnable demo path.
