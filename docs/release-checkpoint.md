# FluxAgent Release Checkpoint

Last verified: 2026-06-28

This document records what the repository can actually do today, what remains incomplete, and which local checks were used to verify the current state.

## Current Version Read

FluxAgent is past the original `v0.1` demo-only stage.

The repository currently represents a runnable `v0.2` alpha candidate:

- runnable read-only `RiskSignal` operator
- `RiskRule`-driven evidence collection and RCA flow
- adapter-neutral datasource contract with first-class `DataSource`
- degraded status conditions for missing or incompatible optional dependencies
- kind demo path that includes happy-path and degraded-path walkthroughs
- evidence redaction before provider-bound reasoning
- local endpoint model-provider runtime support

It is not yet a fully complete `v0.2` release according to the roadmap definition of done.

## Verified Working Scope

### Control Plane

- `RiskRule`, `DataSource`, and `ModelProvider` CRDs exist and are wired into controllers
- `RiskSignal` status includes evidence readiness and RCA-related fields
- `RemediationPlan` and `AgentAction` remain optional expansion paths rather than the default success path

### Read-only Detection and RCA

- default operator mode remains read-only
- `RiskRule` can evaluate signals through `datasourceRef`, `queryType`, and `queryTemplate`
- legacy signal-type routing still exists for compatibility during migration
- heuristic reasoning is runnable without external credentials
- RCA output is persisted into `RiskSignal` status
- webhook notifications remain part of the runnable path

### Datasource Model

- Kubernetes Events is still the default datasource path
- Prometheus and Loki are optional adapters
- datasource capability declaration is implemented
- rule evaluation validates requested query type against datasource capabilities
- datasource resolution failures and capability mismatches do not crash reconciliation

### Demo and Validation Path

- `examples/kind` renders successfully
- fake observability demo remains the first-run path
- degraded demo flow includes:
  - missing datasource
  - reset to baseline
  - capability mismatch
- `make demo-degrade-all` is structured for recording-friendly playback

## Not Complete Yet

The following items are still incomplete or only partially complete:

- full `v0.2` definition of done is not closed
- production-grade auth, retry, and backoff behavior is not complete across adapters
- GitOps PR backends and richer approval UX are not complete
- broader multi-cluster and policy hardening work is still pending
- remediation remains guarded and secondary, not the main product truth

## Practical Version Judgment

If this repository needs a concise status label today, the defensible description is:

`v0.2` alpha: runnable read-only RCA platform with datasource contracts, evidence redaction, and degraded-state visibility.

The project should not yet be presented as:

- fully complete `v0.2`
- production-hardened remediation platform
- provider-complete multi-backend RCA system

## Verification Commands

The following local checks were run during this checkpoint:

```sh
git log --oneline -3
GOWORK=off go test ./...
kubectl kustomize config/default >/tmp/fluxagent-config-default.yaml
kubectl kustomize examples/kind >/tmp/fluxagent-kind-example.yaml
make verify-e2e-kind
git status --short
```

Observed results:

- recent commits:
  - `e25a94a` `docs: expand kind demo and degraded-case walkthrough`
  - `1726a7f` `feat: add rule-driven read-only RCA and datasource contracts`
  - `463c62d` `docs: define adapter-neutral v0.2 architecture`
- `GOWORK=off go test ./...` passed
- `kubectl kustomize config/default` passed
- `kubectl kustomize examples/kind` passed
- `git status --short` returned no pending changes at verification time

## Recommended Next Milestone Gate

Before calling `v0.2` complete, the next gate should be:

1. keep kind demo and degraded-case validation green end to end
2. harden local and hosted provider auth, retry, and timeout behavior
3. expand provider contract coverage beyond heuristic plus local endpoint
4. re-check `v0.2` alpha exit assumptions before broader beta claims
