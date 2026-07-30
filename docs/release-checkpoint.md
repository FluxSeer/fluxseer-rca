# FluxAgent Release Checkpoint

Last verified: 2026-07-30

This document records what the repository can actually do today, what remains incomplete, and which local checks were used to verify the current state.

## Current Version Read

FluxAgent is past the original `v0.1` demo-only stage.

Product positioning:

```text
Kubernetes-native, evidence-first SRE investigation and risk analysis control plane.
```

Published release scope:

```text
v0.3.0-beta.2 is the current published prerelease. v0.3.0-beta.1 remains the frozen-contract baseline beta. v0.2.0-beta.1 remains the earlier read-only RCA beta.
```

Current v0.3 release scope:

```text
v0.3.0-beta.2 keeps the frozen RCA status contract unchanged, hardens runtime defaults and RBAC, passed the aggregate release candidate gate, and has been published as a prerelease with provenance verified.
```

The repository currently represents the published `v0.3.0-beta.2` RCA contract beta:

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
- frozen structured RCA status contract on `InvestigationRequest.status`
- evidence-linked claim verification and compatibility projections
- durable provider execution checkpoint semantics
- deterministic execution identity, finding identity, incident occurrence, and lineage
- provider data egress policy with explicit hosted-provider opt-in
- low-cardinality RCA metrics and replay-oriented fixtures
- read-only Helm default with legacy deployment watcher disabled
- least-privilege default RBAC without remediation or executor mutation permissions

It is past the original `v0.2` alpha checkpoint, passed the local and kind beta gates, and was published as a prerelease. The v0.3 RCA contract is frozen and `v0.3.0-beta.2` has been published as a schema-compatible stabilization prerelease.

Historical v0.2 release identity:

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

## v0.3.0-beta.2 Published Prerelease

Status:

```text
v0.3 schema contract:          FROZEN / UNCHANGED
default runtime surface:       HARDENED
default RBAC profile:          READ-ONLY RCA
legacy detection path:         OPT-IN
experimental remediation:      OPT-IN
release published:             YES
provenance verification:       PASSED
```

Identity:

```text
API group/version identity: aiops.platform/v1alpha1
Release commit: d0f39e677d1e9aa3693e589aba3b23a80d5c8ef7
Tag: v0.3.0-beta.2
GitHub Release URL: https://github.com/FluxSeer/FluxAgent/releases/tag/v0.3.0-beta.2
```

Verified command:

```sh
make verify-release-v0.3-beta V0_3_RELEASE_VERSION=v0.3.0-beta.2 TARGET_PLATFORM=linux/amd64
```

Observed result on 2026-07-29:

```text
PASS
```

Additional publication checks:

- `main` and `test` fast-forwarded to the same release SHA
- main CI passed on the release SHA
- annotated tag was created and pushed
- release workflow built from the tag checkout
- published image metadata and OCI labels matched the tag commit
- GitHub prerelease and checksum assets were published

Published artifacts:

```text
operator image:
  ghcr.io/fluxseer/fluxagent/operator:v0.3.0-beta.2
  digest: sha256:f2a0d732796f28916eada7b8e6649f126ea5aa99b179063d21d5c09bdbf44cc3

demo-observability image:
  ghcr.io/fluxseer/fluxagent/demo-observability:v0.3.0-beta.2
  digest: sha256:135270a422b6d9c2c4b9105786f84398c2802a6230bdff77a50fdc60d0e653b1

Helm OCI chart:
  oci://ghcr.io/fluxseer/fluxagent/charts/fluxagent
  version: 0.3.0-beta.2
  digest: sha256:a04354c5877000b44309b66c346947d40daeb90d5b45e970facea5419940b3b8

GitHub Release asset:
  fluxagent-0.3.0-beta.2.tgz
  sha256:e2905b5a64684a3f6e9cd9df2cd92fa92770d6e6bed2ae1e855d2ba217771091
```

## v0.3.0-beta.1 Published Prerelease

Status:

```text
v0.3 schema frozen:                    YES
v0.3 release candidate gate:           IMPLEMENTED
v0.3 release candidate verification:   PASSED
candidate version:                     v0.3.0-beta.1
release approved for publication:      READY
release published:                     YES
provenance verification:               PASSED
```

Identity:

```text
API group/version identity: aiops.platform/v1alpha1
Schema freeze baseline: 2821e254b65fc54bf6fa521aec89bf7c48240667
Release candidate gate commit: ab4f2bbbde95b99fe2336535e3c16c71a64ef1b9
Release commit: b55f57920eb4ebcef2e454c18ba9437081362287
GitHub Release URL: https://github.com/FluxSeer/FluxAgent/releases/tag/v0.3.0-beta.1
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

Published artifacts:

```text
operator image:
  ghcr.io/fluxseer/fluxagent/operator:v0.3.0-beta.1
  digest: sha256:2573a6180957a2aaad0db10ab43d0b828b5381854a7426af8b6c66c20b30bafe

demo-observability image:
  ghcr.io/fluxseer/fluxagent/demo-observability:v0.3.0-beta.1
  digest: sha256:10357ad614d691804673b188bec93f3b5dc2e88873aa52985400f1a1922e804a

Helm OCI chart:
  oci://ghcr.io/fluxseer/fluxagent/charts/fluxagent
  version: 0.3.0-beta.1
  digest: sha256:c8228c93ca7fd59ff7f99eac5adbbda125dfe0dfe7bb733b0e873a8f01b38b9d
```

Published image metadata was verified from GHCR. Binary `version`, binary `gitCommit`, OCI version labels, OCI revision labels, Helm chart version, and Helm appVersion all match the release identity.

The active post-publication phase is `v0.3 Beta Stabilization and Evidence-Driven Optimization`.

## v0.3.0-beta.2 Stabilization Release

Status:

```text
release version:                       v0.3.0-beta.2
schema changes:                        NONE
runtime default hardening:             COMPLETE
default RBAC profile:                  READ-ONLY RCA
legacy Deployment watcher:             OPT-IN
experimental remediation/action path:  OPT-IN
publication status:                    PUBLISHED
provenance verification:               PASSED
```

Release scope:

- default Helm runtime disables the legacy annotation-driven `DeploymentRiskReconciler`
- default ClusterRole no longer grants cluster-wide Secret read
- default ClusterRole no longer grants Job or ConfigMap mutation permissions
- remediation/action controller and RBAC remain explicit opt-in
- provider credential Secrets are namespace-local to the `ModelProvider`
- capability maturity documentation distinguishes supported, beta, experimental, legacy, and scaffold paths

Release notes and artifact digests are tracked in `docs/releases/v0.3.0-beta.2.md`.

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

`v0.3.0-beta.2 RCA contract beta`: published schema-compatible stabilization prerelease with frozen RCA status contract, hardened read-only defaults, least-privilege default RBAC, release gate coverage, and verified GHCR/Helm artifact provenance.

The project should not yet be presented as:

- production-stable `v0.3`
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

Latest beta release verification:

```sh
make verify-release-v0.3-beta V0_3_RELEASE_VERSION=v0.3.0-beta.2 TARGET_PLATFORM=linux/amd64
kind get clusters
git status --short --branch
```

Latest observed results on 2026-07-29:

- `make verify-release-v0.3-beta V0_3_RELEASE_VERSION=v0.3.0-beta.2 TARGET_PLATFORM=linux/amd64` passed
- `kind get clusters` returned no remaining kind clusters after cleanup
- `git status --short --branch` returned a clean release branch before artifact publication

The release gate verified:

- frozen v0.3 RCA contract
- Go tests and rendered manifests
- RBAC profile checks
- packaging consistency
- reproducible image build
- beta.1 to beta.2 upgrade lifecycle
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

The current post-publication milestone is `v0.3 Beta Stabilization and Evidence-Driven Optimization`, tracked in `docs/backlog/v0.3-beta-stabilization.md`.

Historical release freeze records remain available in `docs/releases/v0.2.0-beta.1-freeze.md` and `docs/backlog/v0.2-release-reproducibility.md`.

Recommended stabilization validation commands:

```sh
make verify-v0.3-schema-freeze
make verify-release-v0.3-beta V0_3_RELEASE_VERSION=v0.3.0-beta.2 TARGET_PLATFORM=linux/amd64
kind get clusters
git status --short --branch
```

Post-release documentation guardrails:

1. keep beta claims limited to a frozen-contract RCA beta with guarded remediation as a secondary path
2. keep OpenTelemetry and CloudWatch documented as not production-ready paths
3. keep provider and datasource coverage claims limited to the currently wired and tested behavior
4. keep historical `v0.2.0-beta.1` artifact references pointed at the actual Harbor release paths
5. describe GHCR as the planned canonical public registry until those artifacts are published there
