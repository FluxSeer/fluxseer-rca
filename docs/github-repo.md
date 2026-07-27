# GitHub Repo Metadata

This file contains suggested GitHub repository settings for FluxAgent.

## Repository Name

`FluxAgent`

## Repository Description

Short description:

`Kubernetes-native control plane for evidence-verifiable root cause analysis.`

Alternative shorter version:

`CRD-first RCA workflow and audit contract for Kubernetes platforms.`

## Suggested Topics

- `kubernetes`
- `operator`
- `sre`
- `aiops`
- `platform-engineering`
- `observability`
- `prometheus`
- `loki`
- `incident-response`
- `rca`
- `gitops`
- `controller-runtime`
- `crd`
- `golang`

## Suggested README Opening

```markdown
# FluxAgent

Kubernetes-native control plane for evidence-verifiable RCA.
```

## Release Plan

### Pre-release Tags

- `v0.1.0-alpha.1`
  first public repo cut with read-only operator, CRDs, docs, and kind demo
- `v0.1.0-beta.1`
  stabilized quickstart, better tests, cleaner onboarding

### First Stable Release

- `v0.1.0`
  read-only `RiskSignal` operator with Prometheus, Loki, and Kubernetes Events adapters, webhook notifications, and documented kind demo

### Next Expansion Release

- `v0.2.0-alpha.1`
  guarded remediation refinement, stronger policy flow, and better executor contracts

## Suggested GitHub Labels

- `area/controllers`
- `area/crd`
- `area/adapters`
- `area/guardrails`
- `area/executor`
- `area/docs`
- `area/demo`
- `kind/feature`
- `kind/bug`
- `kind/refactor`
- `kind/question`
- `priority/p0`
- `priority/p1`
- `priority/p2`
- `good-first-issue`
- `help-wanted`

## Suggested Release Messaging

For the first public release, lead with:

- read-only RCA workflows
- `InvestigationRequest` as the primary operator-native entrypoint
- evidence collection through explicit datasource contracts
- compact evidence and RCA status in Kubernetes
- provider-neutral reasoning with heuristic default

Do not lead with:

- full autonomous remediation
- production-ready self-healing claims
- largest Kubernetes analyzer catalog
- all-in-one observability ownership
