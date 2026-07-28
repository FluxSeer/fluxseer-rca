# Archived Decisions

This file records major paths that were explored during FluxAgent development but are not part of the current open-source product direction.

The goal is to keep historical context without leaving superseded implementation branches or discussion-only decisions ambiguous.

## Current Open-Source Direction

FluxAgent's formal open-source direction is:

```text
provider-neutral
API/workload credentials
heuristic default
optional OpenAI API / Claude API / Gemini API
Kubernetes-native read-only RCA control plane
```

This means:

- hosted model integrations use explicit API credentials through `ModelProvider`
- heuristic analysis remains the no-secret default
- reasoning providers receive bounded, normalized, redacted evidence
- providers do not receive Kubernetes credentials or direct cluster access
- CLI subscription auth caches are not mounted into controller pods

## Superseded: Subscription Codex CLI As In-Cluster Provider

Decision:

```text
Do not formalize subscription Codex CLI as an in-cluster provider.
```

Context:

- A Codex CLI executor and wrapper path was explored.
- The intended personal homelab path was to trigger Codex CLI analysis from RCA output.
- This depended on subscription-style interactive/user-scoped authentication and persistent runner assumptions.

Why it was superseded:

- subscription CLI auth is not a clean Kubernetes workload credential model
- auth cache mounting into pods creates operational and security ambiguity
- pod replacement and token cache lifecycle are brittle
- it does not fit the provider-neutral API contract expected by open-source users
- it risks confusing personal homelab automation with the formal project architecture

Current stance:

- Codex CLI may remain a personal homelab experiment outside the formal FluxAgent provider contract.
- It should not be documented as a supported open-source deployment path.
- Formal providers are API-based OpenAI, Claude, Gemini, plus heuristic.

Related historical commits:

```text
7fe5237 feat: add CLI agent analysis scaffold
23f8e17 feat: add Codex agent executor wrapper
db5bb4c ci: publish Codex agent executor image
40a25f8 ci: publish agent executor artifacts
8a94727 build: slim Codex executor image
90bf687 ci: add subscription Codex RCA workflow
```

## Superseded: PVC Or Secret Mounted Personal Auth Cache

Decision:

```text
Do not recommend mounting personal Codex, Claude, or Gemini CLI auth caches into FluxAgent pods.
```

Why:

- auth cache semantics are user-scoped, not workload-scoped
- token renewal is unclear
- pod replacement can invalidate or duplicate state
- secrets may be exposed to controller runtime, logs, backups, or unrelated workloads
- it is hard to document safely for open-source users

Preferred alternative:

```text
Use workload-scoped API keys or provider credentials through ModelProvider.
```

## Superseded: Persistent Homelab Runner As Formal Provider Runtime

Decision:

```text
Do not make a persistent personal runner the official FluxAgent RCA runtime.
```

Context:

- A persistent VM/runner can be useful in a homelab for subscription CLI tools or private automation.
- The project also uses self-hosted GitHub Actions runners for CI/CD.

Why it is not formalized:

- it is environment-specific
- it does not map cleanly to Kubernetes-native CRD semantics
- it is hard to make portable for open-source users
- it creates unclear boundaries between CI, RCA execution, and personal automation

Current stance:

- self-hosted runners are valid for CI/CD.
- persistent personal runners are optional homelab infrastructure.
- FluxAgent's open-source RCA provider model should not depend on them.

## Superseded: Broad AI Agent Platform Positioning

Decision:

```text
Do not position FluxAgent as a general-purpose AI SRE agent platform.
```

Current positioning:

```text
FluxAgent is a Kubernetes-native RCA control plane.
```

Why:

- broad agent positioning makes product scope too wide
- remediation, GitOps PRs, and action execution increase safety requirements
- the current strongest value is read-only, evidence-verifiable RCA
- users should be able to opt into model providers and rule packs without granting mutation privileges

Related docs:

- [../product-requirements.md](../product-requirements.md)
- [../open-source-positioning.md](../open-source-positioning.md)
- [../competitive-positioning.md](../competitive-positioning.md)

## Superseded: Default Hosted Provider Requirement

Decision:

```text
Do not require OpenAI, Claude, or Gemini credentials for a useful default install.
```

Current stance:

- heuristic provider is the default no-secret path
- hosted APIs are optional
- baseline rule packs are opt-in
- hosted providers must respect data policy and evidence redaction constraints

Why:

- API calls incur user cost
- some users cannot send evidence outside the cluster
- forcing hosted AI at install time harms open-source adoption
- read-only local RCA should remain a complete mode

## Superseded: RiskSignal As Canonical RCA Owner

Decision:

```text
Do not make RiskSignal the canonical RCA result surface.
```

Current stance:

```text
InvestigationRequest.status owns canonical RCA.
RiskSignal stores materialized finding state and compatibility RCA fields.
```

Why:

- `RiskSignal` is useful for downstream notification and materialized findings
- `InvestigationRequest` is the better orchestration and audit surface
- putting full RCA in both resources creates dual truth and drift risk

Follow-up:

- future work should make `RiskSignal` RCA fields a projection from `InvestigationRequest.status`
- compatibility fields should be documented as transitional before `v1beta1`

## Superseded: Unbounded Built-In Detection

Decision:

```text
Do not ship FluxAgent as an always-on scanner that discovers every possible incident by default.
```

Current stance:

- users can write custom `RiskRule` resources
- FluxAgent should provide baseline rule packs so first install has value
- rule packs remain explicit and bounded
- Prometheus and Loki assumptions must be configurable

Why:

- metric names and labels are not portable
- broad scanning can consume cluster and observability resources
- a clear Kubernetes-native RCA control plane is preferable to a black-box monitoring replacement

Related docs:

- [../helm-rulepacks.md](../helm-rulepacks.md)
- [v0.3-foundation-issues.md](v0.3-foundation-issues.md)

## Active Follow-Up

The current production-readiness backlog is tracked in:

- [v0.3-production-readiness.md](v0.3-production-readiness.md)
