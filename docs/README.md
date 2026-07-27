# FluxAgent Docs

FluxAgent is documented as a Kubernetes-native SRE investigation control plane with optional AI-assisted reasoning, a safe default path, and an optional guarded execution path.

## Structure

```text
docs/
├─ architecture/
│  ├─ overview.md
│  ├─ dependency-neutrality.md
│  ├─ read-only-flow.md
│  ├─ investigation-flow.md
│  ├─ remediation-flow.md
│  ├─ v0.2-read-only-rca.md
│  ├─ v0.3-investigation-experience.md
│  ├─ v0.2-adapter-neutral-backlog.md
│  ├─ model-gateway.md
│  └─ action-executor.md
├─ crd-reference/
│  ├─ datasource.md
│  ├─ investigationrequest.md
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
   ├─ investigate-workload.md
   ├─ enable-prometheus.md
   ├─ enable-loki.md
   ├─ enable-remediation.md
   └─ enable-hosted-model-providers.md
```

## Start Here

- [architecture/overview.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/overview.md:1)
- [architecture/dependency-neutrality.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/dependency-neutrality.md:1)
- [architecture/read-only-flow.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/read-only-flow.md:1)
- [architecture/investigation-flow.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/investigation-flow.md:1)
- [architecture/v0.2-read-only-rca.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/v0.2-read-only-rca.md:1)
- [architecture/v0.3-investigation-experience.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/v0.3-investigation-experience.md:1)
- [architecture/v0.2-adapter-neutral-backlog.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/v0.2-adapter-neutral-backlog.md:1)
- [product-requirements.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/product-requirements.md:1)
- [competitive-positioning.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/competitive-positioning.md:1)
- [helm-rulepacks.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/helm-rulepacks.md:1)
- [release-checkpoint.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/release-checkpoint.md:1)
- [backlog/v0.2-beta.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/backlog/v0.2-beta.md:1)
- [backlog/v0.2-release-reproducibility.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/backlog/v0.2-release-reproducibility.md:1)
- [backlog/v0.3-trustworthy-rca-contract.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/backlog/v0.3-trustworthy-rca-contract.md:1)
- [releases/v0.2.0-beta.1.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/releases/v0.2.0-beta.1.md:1)
- [releases/v0.2.0-beta.1-freeze.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/releases/v0.2.0-beta.1-freeze.md:1)
- [releases/v0.2.0-alpha.2.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/releases/v0.2.0-alpha.2.md:1)
- [tutorials/quickstart-kind.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/tutorials/quickstart-kind.md:1)
- [tutorials/investigate-workload.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/tutorials/investigate-workload.md:1)
- [tutorials/enable-hosted-model-providers.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/tutorials/enable-hosted-model-providers.md:1)
- [github-repo.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/github-repo.md:1)

## Diagram Sources

- `drawio/Kubernetes-native AI Agent Platform Architecture v1.drawio`
  Historical draft source.
- `drawio/Kubernetes-native AI Agent Platform Architecture v2.drawio`
  Current maintained source.
  Layer 2 detail pages: `Layer 2-1` through `Layer 2-5`

## Current Product Truth

- Product positioning: Kubernetes-native, evidence-verifiable RCA control plane.
- Current release scope: `v0.2` focuses on read-only RCA workflows and is a verified beta candidate.
- `RemediationPlan` and `AgentAction` remain guarded experimental expansion paths.
- Hosted model integrations are limited to OpenAI API, Claude API, and Gemini API; heuristic remains the no-secret default.
- Prometheus, Loki, and Kubernetes Events adapters are wired into the runnable demo path.
- Prometheus and Loki remain optional adapters even when the demo wires them through a fake observability service.
- `InvestigationRequest` is the primary operator-first entrypoint for ad-hoc or externally triggered RCA.
