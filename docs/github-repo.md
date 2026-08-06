# GitHub Repo Metadata

This file contains suggested GitHub repository settings for the project as it
moves from the historical `FluxAgent` name to the public `FluxSeer RCA` product
name.

## Repository Name

`fluxseer-rca`

Historical name:

`FluxAgent`

## Repository Description

Short description:

`Kubernetes-native RCA control plane with governed, evidence-verifiable investigations and replay-oriented audit artifacts.`

Alternative shorter version:

`CRD-first RCA workflow, evidence, and audit contract for Kubernetes platforms.`

Highest-value promise:

`Turns Kubernetes incident investigations into verifiable, auditable, replayable, and reusable organizational knowledge.`

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
# FluxSeer RCA

Kubernetes-native control plane for evidence-verifiable RCA.

FluxSeer RCA turns Kubernetes incident investigations into verifiable,
auditable, replayable, and reusable organizational knowledge instead of
letting them disappear into Slack threads, dashboard screenshots, terminal
history, or individual responder memory.
```

Compatibility note:

```markdown
Source code, Helm charts, images, metrics, and CRDs now use `fluxseer` /
`fluxseer-rca` naming. The most recently published release, `v0.3.0-beta.3`,
was built under the earlier `fluxagent` name; existing installs from that
release remain a compatibility reference until a dedicated rename release is
published.
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
