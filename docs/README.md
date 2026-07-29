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
├─ backlog/
│  ├─ v0.2-beta.md
│  ├─ v0.2-release-reproducibility.md
│  ├─ v0.3-trustworthy-rca-contract.md
│  ├─ v0.3-foundation-issues.md
│  ├─ v0.3-production-readiness.md
│  ├─ v0.3-beta-stabilization.md
│  ├─ v0.3-schema-freeze-audit.md
│  ├─ v0.3-naming-api-review.md
│  └─ archived-decisions.md
└─ tutorials/
   ├─ quickstart-kind.md
   ├─ investigate-workload.md
   ├─ enable-prometheus.md
   ├─ enable-loki.md
   ├─ enable-remediation.md
   └─ enable-hosted-model-providers.md
```

## Start Here

- [architecture/overview.md](architecture/overview.md)
- [architecture/dependency-neutrality.md](architecture/dependency-neutrality.md)
- [architecture/read-only-flow.md](architecture/read-only-flow.md)
- [architecture/investigation-flow.md](architecture/investigation-flow.md)
- [architecture/v0.2-read-only-rca.md](architecture/v0.2-read-only-rca.md)
- [architecture/v0.3-investigation-experience.md](architecture/v0.3-investigation-experience.md)
- [architecture/v0.2-adapter-neutral-backlog.md](architecture/v0.2-adapter-neutral-backlog.md)
- [product-requirements.md](product-requirements.md)
- [competitive-positioning.md](competitive-positioning.md)
- [capability-maturity.md](capability-maturity.md)
- [helm-rulepacks.md](helm-rulepacks.md)
- [metrics.md](metrics.md)
- [release-checkpoint.md](release-checkpoint.md)
- [backlog/v0.2-beta.md](backlog/v0.2-beta.md)
- [backlog/v0.2-release-reproducibility.md](backlog/v0.2-release-reproducibility.md)
- [backlog/v0.3-trustworthy-rca-contract.md](backlog/v0.3-trustworthy-rca-contract.md)
- [backlog/v0.3-foundation-issues.md](backlog/v0.3-foundation-issues.md)
- [backlog/v0.3-production-readiness.md](backlog/v0.3-production-readiness.md)
- [backlog/v0.3-beta-stabilization.md](backlog/v0.3-beta-stabilization.md)
- [backlog/v0.3-schema-freeze-audit.md](backlog/v0.3-schema-freeze-audit.md)
- [backlog/v0.3-naming-api-review.md](backlog/v0.3-naming-api-review.md)
- [backlog/archived-decisions.md](backlog/archived-decisions.md)
- [releases/v0.3.0-beta.1.md](releases/v0.3.0-beta.1.md)
- [releases/v0.3.0-beta.2.md](releases/v0.3.0-beta.2.md)
- [releases/v0.2.0-beta.1.md](releases/v0.2.0-beta.1.md)
- [releases/v0.2.0-beta.1-freeze.md](releases/v0.2.0-beta.1-freeze.md)
- [releases/v0.2.0-alpha.2.md](releases/v0.2.0-alpha.2.md)
- [tutorials/quickstart-kind.md](tutorials/quickstart-kind.md)
- [tutorials/investigate-workload.md](tutorials/investigate-workload.md)
- [tutorials/enable-hosted-model-providers.md](tutorials/enable-hosted-model-providers.md)
- [github-repo.md](github-repo.md)

## Diagram Sources

- `drawio/Kubernetes-native AI Agent Platform Architecture v1.drawio`
  Historical draft source.
- `drawio/Kubernetes-native AI Agent Platform Architecture v2.drawio`
  Current maintained source.
  Layer 2 detail pages: `Layer 2-1` through `Layer 2-5`

## Current Product Truth

- Product positioning: Kubernetes-native, evidence-verifiable RCA control plane.
- Current release scope: `v0.3.0-beta.1` is a published prerelease with frozen RCA contract, published GHCR images, published Helm OCI chart, and verified provenance.
- Current v0.3 engineering state: `v0.3.0-beta.2` stabilization candidate for runtime default hardening and least-privilege RBAC.
- Frozen v0.3 API identity: `aiops.platform/v1alpha1`.
- Capability maturity is tracked in [capability-maturity.md](capability-maturity.md); not every CRD or adapter is part of the default supported path.
- `RemediationPlan` and `AgentAction` remain guarded experimental expansion paths.
- Hosted model integrations are limited to OpenAI API, Claude API, and Gemini API; heuristic remains the no-secret default.
- Prometheus, Loki, and Kubernetes Events adapters are wired into the runnable demo path.
- Prometheus and Loki remain optional adapters even when the demo wires them through a fake observability service.
- `InvestigationRequest` is the primary operator-first entrypoint for ad-hoc or externally triggered RCA.
