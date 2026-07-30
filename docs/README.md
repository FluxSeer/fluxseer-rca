# FluxAgent Docs

FluxAgent is documented as a Kubernetes-native RCA control plane with optional AI-assisted reasoning, a safe default path, and an optional guarded execution path.

Current published release: `v0.3.0-beta.2`

Frozen API identity: `aiops.platform/v1alpha1`

## Current Product Truth

- Product positioning: Kubernetes-native, evidence-verifiable RCA control plane.
- Current release scope: `v0.3.0-beta.2` is a published prerelease with frozen RCA contract, runtime default hardening, least-privilege RBAC defaults, published GHCR images, published Helm OCI chart, and verified provenance.
- Current v0.3 engineering state: post-`v0.3.0-beta.2` stabilization and experimental optimization.
- `InvestigationRequest` is the primary operator-first entrypoint for ad-hoc or externally triggered RCA.
- `RiskRule` is a valid bootstrap detection and rule-pack entrypoint, not the canonical RCA ownership surface.
- `RiskSignal` is a materialized finding, notification target, and compatibility projection.
- `RemediationPlan` and `AgentAction` remain guarded experimental expansion paths.
- Hosted model integrations are limited to OpenAI API, Claude API, and Gemini API; heuristic remains the no-secret default.
- Prometheus, Loki, and Kubernetes Events are supported adapters. Prometheus and Loki remain optional integrations.

## Start Here

- [Product requirements](product-requirements.md)
- [Architecture overview](architecture/overview.md)
- [Mermaid architecture diagrams](architecture/mermaid-diagrams.md)
- [Capability maturity](capability-maturity.md)
- [Runtime modes](runtime-modes.md)
- [Release checkpoint](release-checkpoint.md)
- [Quickstart with kind](tutorials/quickstart-kind.md)
- [Investigate a workload](tutorials/investigate-workload.md)

## Architecture

Current maintained architecture:

- [Architecture overview](architecture/overview.md)
- [Mermaid diagrams](architecture/mermaid-diagrams.md)
- [Dependency neutrality](architecture/dependency-neutrality.md)
- [Read-only flow](architecture/read-only-flow.md)
- [Investigation flow](architecture/investigation-flow.md)
- [Model gateway](architecture/model-gateway.md)
- [Action executor](architecture/action-executor.md)
- [Remediation flow](architecture/remediation-flow.md)
- [v0.3 investigation experience](architecture/v0.3-investigation-experience.md)

Historical architecture records:

- [v0.2 read-only RCA plan](architecture/v0.2-read-only-rca.md)
- [v0.2 adapter-neutral backlog](architecture/v0.2-adapter-neutral-backlog.md)

Compatibility entrypoint:

- [Architecture index](architecture.md)

## Reference

CRD references:

- [DataSource](crd-reference/datasource.md)
- [InvestigationRequest](crd-reference/investigationrequest.md)
- [ModelProvider](crd-reference/modelprovider.md)
- [RiskRule](crd-reference/riskrule.md)
- [RiskSignal](crd-reference/risksignal.md)
- [RemediationPlan](crd-reference/remediationplan.md)
- [AgentAction](crd-reference/agentaction.md)

Adapter references:

- [Prometheus](adapters/prometheus.md)
- [Loki](adapters/loki.md)
- [Kubernetes Events](adapters/kubernetes-events.md)
- [Model providers](adapters/model-providers.md)
- [Datasource adapter overview](datasource-adapters.md)
- [Model gateway overview](model-gateway.md)

Operations and packaging:

- [Runtime modes](runtime-modes.md)
- [Helm rule packs](helm-rulepacks.md)
- [Metrics](metrics.md)
- [GitHub repository setup](github-repo.md)

Product positioning:

- [Open source positioning](open-source-positioning.md)
- [Competitive positioning](competitive-positioning.md)

## Tutorials

- [Quickstart with kind](tutorials/quickstart-kind.md)
- [Investigate a workload](tutorials/investigate-workload.md)
- [Enable hosted model providers](tutorials/enable-hosted-model-providers.md)
- [Enable Prometheus](tutorials/enable-prometheus.md)
- [Enable Loki](tutorials/enable-loki.md)
- [Enable remediation](tutorials/enable-remediation.md)

## Releases

Current release:

- [v0.3.0-beta.2](releases/v0.3.0-beta.2.md)

Historical releases:

- [v0.3.0-beta.1](releases/v0.3.0-beta.1.md)
- [v0.2.0-beta.1](releases/v0.2.0-beta.1.md)
- [v0.2.0-beta.1 freeze report](releases/v0.2.0-beta.1-freeze.md)
- [v0.2.0-alpha.2](releases/v0.2.0-alpha.2.md)

## Backlog And Audit Records

Current planning:

- [v0.3 beta stabilization](backlog/v0.3-beta-stabilization.md)
- [v0.3 naming and API review](backlog/v0.3-naming-api-review.md)

Frozen-contract and production-readiness records:

- [v0.3 trustworthy RCA contract](backlog/v0.3-trustworthy-rca-contract.md)
- [v0.3 foundation issues](backlog/v0.3-foundation-issues.md)
- [v0.3 production readiness](backlog/v0.3-production-readiness.md)
- [v0.3 schema freeze audit](backlog/v0.3-schema-freeze-audit.md)

Historical records:

- [v0.2 beta backlog](backlog/v0.2-beta.md)
- [v0.2 release reproducibility](backlog/v0.2-release-reproducibility.md)
- [Archived decisions](backlog/archived-decisions.md)

## Diagram Sources

- [architecture/mermaid-diagrams.md](architecture/mermaid-diagrams.md)
  Maintained text diagrams for the current implementation.

Historical Draw.io drafts were removed from the maintained docs tree after the Mermaid diagrams became the source of truth.
