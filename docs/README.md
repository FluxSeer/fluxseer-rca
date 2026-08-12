# FluxSeer RCA Docs

FluxSeer RCA is the product name for this Kubernetes-native RCA control
plane. Source code, Helm charts, and build artifacts use matching `fluxseer` /
`fluxseer-rca` naming; the most recently published release, `v0.4.0-beta.3`,
includes approval lifecycle, terminal-state TTL cleanup, and guardrails for production remediation governance (see
[architecture/rename-migration-plan.md](architecture/rename-migration-plan.md)).

Current published release: `v0.4.0-beta.3`

Current API identity: `aiops.platform/v1alpha1`

The API group/version identity is fixed for the current v0.4 line. This does
not mean all future v1alpha1 schema fields are GA-stable.

FluxSeer RCA's highest-value promise is turning every Kubernetes incident
investigation into verifiable, auditable, replayable, and reusable
organizational knowledge instead of letting it disappear into Slack threads,
dashboard screenshots, terminal history, or individual responder memory.

## Current Product Truth

- Product positioning: Kubernetes-native, evidence-verifiable RCA control plane.
- Highest-value promise: make Kubernetes incident investigations durable,
  evidence-linked organizational knowledge.
- Current release scope: `v0.4.0-beta.3` adds terminal-state TTL cleanup for `AgentAction` and `RemediationPlan` on top of approval lifecycle, escalation handling, approval audit timestamps, notification retry tracking, and production governance while maintaining read-only RCA as the default.
- Current v0.4 engineering state: guardrails and approval lifecycle consolidation, preparing for v0.5 low-risk action execution.
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
- [FluxSeer RCA → FluxSeer RCA rename migration plan](architecture/rename-migration-plan.md)

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
- [Report contracts and migration](reporting.md)
- [RiskRule anomaly reports](riskrule-reports.md)
- [Helm rule packs](helm-rulepacks.md)
- [Metrics](metrics.md)
- [Kubernetes ecosystem integration contract](ecosystem-integration-contract.md)
- [GitHub repository setup](github-repo.md)

Product positioning:

- [FluxSeer RCA product rename](backlog/v0.3-product-rename.md)
- [Open source positioning](open-source-positioning.md)
- [Competitive positioning](competitive-positioning.md)
- [v0.3 product direction backlog](backlog/v0.3-product-direction.md)
- [v0.3 architecture hardening backlog](backlog/v0.3-architecture-hardening.md)

## Tutorials

- [Quickstart with kind](tutorials/quickstart-kind.md)
- [Investigate a workload](tutorials/investigate-workload.md)
- [Enable hosted model providers](tutorials/enable-hosted-model-providers.md)
- [Enable Prometheus](tutorials/enable-prometheus.md)
- [Enable Loki](tutorials/enable-loki.md)
- [Enable remediation](tutorials/enable-remediation.md)

## Backlog And Audit Records

Current planning:

- [Backlog execution ledger](backlog/README.md)
- [v0.4 workload target coverage gate](backlog/v0.4-workload-target-coverage.md)
- [FluxSeer RCA product rename](backlog/v0.3-product-rename.md)
- [v0.3 product direction](backlog/v0.3-product-direction.md)
- [v0.3 architecture hardening](backlog/v0.3-architecture-hardening.md)
- [v0.3 beta stabilization](backlog/v0.3-beta-stabilization.md)
- [v0.3 naming and API review](backlog/v0.3-naming-api-review.md)

Frozen-contract and production-readiness records:

- [v0.3 trustworthy RCA contract](backlog/v0.3-trustworthy-rca-contract.md)
- [v0.3 foundation issues](backlog/v0.3-foundation-issues.md)
- [v0.3 production readiness](backlog/v0.3-production-readiness.md)
- [v0.3 schema freeze audit](backlog/v0.3-schema-freeze-audit.md)
- [v0.3 runtime error matrix](backlog/v0.3-runtime-error-matrix.md)

Historical records:

- [v0.2 beta backlog](backlog/v0.2-beta.md)
- [v0.2 release reproducibility](backlog/v0.2-release-reproducibility.md)
- [Archived decisions](backlog/archived-decisions.md)

## Diagram Sources

- [architecture/mermaid-diagrams.md](architecture/mermaid-diagrams.md)
  Maintained text diagrams for the current implementation.

Historical Draw.io drafts were removed from the maintained docs tree after the Mermaid diagrams became the source of truth.
