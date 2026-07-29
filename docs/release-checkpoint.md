# FluxAgent Release Checkpoint

Last verified: 2026-07-29

This document records what the repository can actually do today, what remains incomplete, and which local checks were used to verify the current state.

## Current Version Read

FluxAgent is past the original `v0.1` demo-only stage.

Product positioning:

```text
Kubernetes-native, evidence-first SRE investigation and risk analysis control plane.
```

Published release scope:

```text
v0.2 focuses on read-only RCA workflows and is published as v0.2.0-beta.1.
```

Current v0.3 candidate scope:

```text
v0.3.0-beta.1 freezes the RCA status contract and has passed the aggregate release candidate gate. Artifact publication is pending.
```

The repository currently represents the published `v0.2.0-beta.1` read-only RCA beta:

- runnable read-only `RiskSignal` operator
- `RiskRule`-driven evidence collection and RCA flow
- adapter-neutral datasource contract with first-class `DataSource`
- degraded status conditions for missing or incompatible optional dependencies
- kind demo path that includes happy-path and degraded-path walkthroughs
- evidence redaction before provider-bound reasoning
- hosted OpenAI, Gemini, and Claude model-provider runtime support
- hosted provider fallback support through `ModelProvider.spec.fallbackProviderRef`
- operator-first `InvestigationRequest` workflow for ad-hoc read-only RCA
- `fluxagent investigate` CLI wrapper around `InvestigationRequest`
- optional `InvestigationRequest` to `RiskSignal` promotion
- `RiskSignal` and `InvestigationRequest` TTL cleanup behavior

It is past the original `v0.2` alpha checkpoint, passed the local and kind beta gates, and was published as a prerelease. The v0.3 RCA contract is frozen and approved for beta publication, but v0.3 artifacts have not yet been published.

Release identity:

```text
Status: Published prerelease
Tag: v0.2.0-beta.1
Tag commit: e37841baa7f4313577cc8942a77345856b709020
GitHub Release URL: https://github.com/FluxSeer/FluxAgent/releases/tag/v0.2.0-beta.1
```

Published artifacts:

```text
operator image:
  test-harbor.fluxseer.com/fluxseer/fluxagent/operator:v0.2.0-beta.1
  digest: sha256:dc363ad07be5e4ec345c2d6f7aa369761ba138e157551c968445b65c7602901b

demo-observability image:
  test-harbor.fluxseer.com/fluxseer/fluxagent/demo-observability:v0.2.0-beta.1
  digest: sha256:5e7e46e73b0efb0925431ae137b07b6830540eb54439ee2d4524873db7a57c81

Helm OCI chart:
  test-harbor.fluxseer.com/fluxseer/fluxagent/charts/kube-ai-sre:0.2.0-beta.1
  digest: sha256:0924e95465d146d0473594bb96fa00d67d4fd7115ff6cca6d99183a816470828
```

## v0.3.0-beta.1 Publication Candidate

Status:

```text
v0.3 schema frozen:                    YES
v0.3 release candidate gate:           IMPLEMENTED
v0.3 release candidate verification:   PASSED
candidate version:                     v0.3.0-beta.1
release approved for publication:      READY
release published:                     NO
```

Identity:

```text
Frozen API identity: aiops.platform/v1alpha1
Schema freeze baseline: 2821e254b65fc54bf6fa521aec89bf7c48240667
Release candidate gate commit: ab4f2bbbde95b99fe2336535e3c16c71a64ef1b9
```

Verified command:

```sh
make verify-release-v0.3-beta
```

Observed result on 2026-07-29:

```text
PASS
```

Verified coverage:

- frozen RCA contract verification
- Go tests and rendered manifests
- rule pack rendering and kind validation
- kind E2E `RiskRule -> RiskSignal -> RCA`
- `InvestigationRequest` E2E and degraded-provider scenarios
- artifact identity
- packaging consistency
- reproducible image build
- Helm install, upgrade, uninstall, and CRD retention
- release cleanup hygiene

Publication remains pending. Published image digests, chart digests, and GitHub Release URL must be recorded after tag-based artifact publication and provenance verification.

## Post `v0.2.0-alpha.2` Mainline Work

The `v0.2.0-alpha.2` tag points at `8208049`.

Current `main` has additional post-tag hardening:

- `131c7aa` `feat: harden provider response validation`
  - rejects structured provider responses missing `confidenceScore`
  - rejects empty or missing `rcaCauses`
  - validates all provider `ModelResponse` values in the reasoning engine before deriving RCA fields
  - adds tests for incomplete hosted OpenAI content and invalid custom provider responses
- `cea4ee6` `feat: harden datasource http retries`
  - standardizes Prometheus and Loki retry and HTTP status classification
  - maps auth, rate-limit, invalid-payload, unavailable, and invalid-request failures to stable datasource reasons
  - preserves typed datasource query failures in `InvestigationRequest` status conditions
- `bf673c1` `feat: surface datasource query degradation`
  - keeps `RiskRule` reconciliation alive when optional datasource queries fail
  - surfaces typed datasource query failures through `RiskRule` `Ready`/`Degraded` conditions
  - carries evidence collection failure reasons into generated `RiskSignal` status
  - allows partial-evidence `RiskSignal` creation when another optional datasource query fails

## Verified Working Scope

### Control Plane

- `RiskRule`, `DataSource`, `ModelProvider`, and `InvestigationRequest` CRDs exist and are wired into controllers
- `RiskSignal` status includes evidence readiness and RCA-related fields
- `RemediationPlan` and `AgentAction` remain optional expansion paths rather than the default success path

### Read-only Detection and RCA

- default operator mode remains read-only
- `RiskRule` can evaluate signals through `datasourceRef`, `queryType`, and `queryTemplate`
- legacy signal-type routing still exists for compatibility during migration
- heuristic reasoning is runnable without external credentials
- RCA output is persisted into `RiskSignal` status
- webhook notifications remain part of the runnable path

### Ad-hoc Investigation

- `InvestigationRequest` resolves Deployment targets
- investigation requests support simple `dataSources[]` mode and explicit `queries[]` mode
- query templates are rendered against target metadata
- datasource capability mismatches are reported through status conditions
- evidence is collected and normalized into `status.evidenceRefs`
- RCA output is persisted into `InvestigationRequest.status`
- `createRiskSignal: true` promotes completed RCA into a linked `RiskSignal`
- terminal requests are retained until `status.completedAt + spec.ttlSeconds` when TTL is set
- `fluxagent investigate` creates requests and can wait for terminal status

### Datasource Model

- Kubernetes Events is still the default datasource path
- Prometheus and Loki are optional adapters
- datasource capability declaration is implemented
- rule evaluation validates requested query type against datasource capabilities
- datasource resolution failures and capability mismatches do not crash reconciliation

### Model Providers

- heuristic provider remains the no-secret default path
- hosted OpenAI, Gemini, and Claude adapters are wired through `ModelProvider`
- hosted providers share timeout, HTTP status mapping, and transient retry behavior
- secret and hosted-provider failures surface as RCA readiness/degraded reasons
- `fallbackProviderRef` can fail over between provider objects for supported provider and secret failures
- Prometheus and Loki datasource adapters share retry, timeout, HTTP status, and payload error classification

### Demo and Validation Path

- `examples/kind` renders successfully
- fake observability demo remains the first-run path
- degraded demo flow includes:
  - missing datasource
  - reset to baseline
  - capability mismatch
- hosted provider auth failure is covered by kind validation scripts
- investigation kind validation creates an `InvestigationRequest`, waits for RCA, and verifies optional `RiskSignal` promotion
- `make demo-degrade-all` is structured for recording-friendly playback

## Not Complete Yet

The following items are still incomplete or only partially complete:

- production-grade auth, retry, and backoff behavior is not complete across all possible adapters
- production-grade vendor response governance is partially hardened after `v0.2.0-alpha.2`, but broader provider response-shape coverage is still pending
- GitOps PR backends and richer approval UX are not complete
- broader multi-cluster and policy hardening work is still pending
- remediation remains guarded and secondary, not the main product truth
- OpenTelemetry and CloudWatch remain scaffold-level integrations

## Practical Version Judgment

If this repository needs a concise status label today, the defensible description is:

`v0.2.0-beta.1 read-only RCA beta`: runnable read-only RCA platform with datasource contracts, evidence redaction, degraded-state visibility, hosted provider validation, and CRD-first ad-hoc investigation.

The project should not yet be presented as:

- fully complete `v0.3`
- production-hardened remediation platform
- provider-complete multi-backend RCA system

## Verification Commands

The following local checks were run during this checkpoint:

Initial checkpoint commands:

```sh
git log --oneline -3
GOWORK=off go test ./...
kubectl kustomize config/default >/tmp/fluxagent-config-default.yaml
kubectl kustomize examples/kind >/tmp/fluxagent-kind-example.yaml
make verify-e2e-kind
make verify-investigation-kind
git status --short
```

Initial checkpoint observed results:

- recent commits:
  - `9d3ea96` `test(kind): cover hosted provider auth-failed degradation`
  - `bdbef55` `test: cover hosted provider degradation in kind gate`
  - `1fbd391` `feat: unify hosted provider http policy`
- `GOWORK=off go test ./...` passed
- `kubectl kustomize config/default` passed
- `kubectl kustomize examples/kind` passed
- `make verify-e2e-kind` passed
- `make verify-investigation-kind` passed
- `git status --short` returned no pending changes before this checkpoint update

Latest beta candidate verification:

```sh
make verify-v0.2-alpha
make verify-e2e-kind
make verify-investigation-kind
kind get clusters
git status --short --branch
```

Latest observed results on 2026-07-23:

- `make verify-v0.2-alpha` passed
- `make verify-e2e-kind` passed
- `make verify-investigation-kind` passed
- `kind get clusters` returned no remaining kind clusters after cleanup
- `git status --short --branch` returned a clean branch synchronized with `origin/main`

The kind gates verified:

- read-only `RiskRule -> RiskSignal` flow
- webhook notification
- missing datasource degradation
- datasource capability mismatch degradation
- hosted provider `ProviderAuthFailed` degradation
- `InvestigationRequest -> RCA` completion
- optional `InvestigationRequest -> RiskSignal` promotion
- investigation degradation for missing datasource, capability mismatch, missing provider, provider auth failure, and provider rate limiting
- kind cleanup for `fluxagent-demo`

## Recommended Next Milestone

The beta candidate gate defined in `docs/backlog/v0.2-beta.md` passed before publication.

The historical release freeze pass is recorded in `docs/releases/v0.2.0-beta.1-freeze.md`. Its blocking findings were resolved before `v0.2.0-beta.1` was tagged and published.

Verified validation commands:

```sh
make verify-v0.2-alpha
make verify-e2e-kind
make verify-investigation-kind
kind get clusters
git status --short --branch
```

Post-release documentation guardrails:

1. keep beta claims limited to a read-only RCA beta with guarded remediation as a secondary path
2. keep OpenTelemetry and CloudWatch documented as not production-ready paths
3. keep provider and datasource coverage claims limited to the currently wired and tested behavior
4. keep historical `v0.2.0-beta.1` artifact references pointed at the actual Harbor release paths
5. describe GHCR as the planned canonical public registry until those artifacts are published there
