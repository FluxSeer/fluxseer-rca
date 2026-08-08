# FluxSeer RCA Runtime Modes

This document defines the supported mode switches in FluxSeer RCA and separates user-facing runtime configuration from maintainer-only deployment and release flows.

Current release baseline: `v0.4.0-beta.1`

Current API identity: `aiops.platform/v1alpha1`

The API group and version identity are fixed for the current v0.4 line. This does not mean that every v1alpha1 schema field is generally available or stable.

## Mode Ownership

FluxSeer RCA has several configuration surfaces. They should not be treated as one flat list.

```text
Helm values
  -> installation capabilities and default RBAC

CRD spec
  -> per-investigation behavior and policy

Data policy
  -> evidence egress and safety boundary

Status
  -> actual execution, degradation, and compatibility output

CI/CD workflows
  -> maintainer-only artifact publication channels
```

The long-term design goal is:

```text
Helm decides installed capability.
CRDs decide single-request behavior.
Policy decides data and safety boundaries.
Status reports what actually happened.
```

## User-Facing Switching Surfaces

These are the primary mode choices an installer or API user should understand.

| Surface | Modes | Preferred / Default | Scope |
| --- | --- | --- | --- |
| Runtime capability | read-only RCA, remediation, experimental executor | read-only RCA | Helm |
| RCA provider | heuristic, OpenAI, Claude, Gemini | heuristic | `ModelProvider` reference |
| RCA entry | `InvestigationRequest`, `RiskRule` | Canonical path: `InvestigationRequest` | CRD |
| Evidence planning | `dataSources[]`, `queries[]` | request-defined | `InvestigationRequest` |
| Evidence retention | `MetadataOnly`, `NormalizedSnapshot`, `RawSnapshot` | `MetadataOnly` | `InvestigationRequest` |
| Query security | `LegacyUnrestricted`, `TemplatesOnly` | compatibility-dependent | `DataSource` |
| Rule packs | Kubernetes, Prometheus, Loki baseline | Kubernetes baseline enabled | Helm |

## Runtime Capability

Helm exposes current runtime feature flags:

```yaml
features:
  legacyDeploymentRisk:
    enabled: false
  remediation:
    enabled: false
  experimentalExecutor:
    enabled: false
```

The default read-only RCA mode is not a single boolean. It is the installed state formed by:

```text
remediation=false
experimentalExecutor=false
legacyDeploymentRisk=false
```

Capability semantics:

| Capability | Default | Maturity | Meaning |
| --- | --- | --- | --- |
| read-only RCA | enabled | Supported | Collect bounded evidence and write FluxSeer RCA CRD status without mutating workloads. |
| `legacyDeploymentRisk` | disabled | Legacy / opt-in | Enables the annotation-driven Deployment watcher. |
| `remediation` | disabled | Experimental / opt-in | Enables `RemediationPlan` and `AgentAction` reconciliation. |
| `experimentalExecutor` | disabled | Experimental / opt-in | Adds executor-like permissions such as Job and ConfigMap mutation. |

Dependency rules:

```text
experimentalExecutor -> requires remediation
remediation          -> independent from legacyDeploymentRisk
legacy watcher       -> compatibility only
```

## RCA Entry

FluxSeer RCA has two public RCA entrypoints, but they are not equivalent long-term ownership surfaces.

| Resource | Role |
| --- | --- |
| `InvestigationRequest` | Canonical RCA execution API and status owner. |
| `RiskRule` | Detection and bootstrap API for recurring conditions and rule packs. |
| `RiskSignal` | Materialized finding, notification target, and compatibility projection. |

Preferred long-term flow:

```text
RiskRule
-> InvestigationRequest
-> InvestigationRequest.status
-> optional RiskSignal projection
```

`RiskRule.spec.investigationPolicy.mode: DirectRiskSignal` is a compatibility path. It should not be treated as the preferred canonical RCA path. Prefer:

```yaml
spec:
  investigationPolicy:
    mode: CreateRequest
```

`InvestigationRequest.status` is the authoritative execution record for canonical RCA.

`RiskSignal` may expose a materialized or compatibility-oriented view of the finding, but it must not become a second authoritative owner of investigation execution state.

## Investigation Execution

`InvestigationRequest.spec.mode` currently supports only:

```yaml
spec:
  mode: readOnly
```

`readOnly` means FluxSeer RCA may read declared evidence sources and write FluxSeer RCA-owned status or optional result resources. It does not grant workload mutation.

Other execution modes are not implemented in `v0.4.0-beta.1`. The field exists as a compatibility and future-extension point, not as a hidden remediation switch.

## RCA Provider

Provider selection and evidence transmission are separate decisions.

Provider types:

```text
heuristic
openai
claude
gemini
```

Provider classes:

| Class | Providers | Default | Data boundary |
| --- | --- | --- | --- |
| Local / no-secret | `heuristic` | yes | No hosted API call. |
| Hosted model API | `openai`, `claude`, `gemini` | no | Requires credentials and explicit egress policy. |

Configuring a hosted provider does not automatically permit evidence transmission. The canonical `InvestigationRequest` path must pass `ModelProvider.spec.dataPolicy`, including:

```yaml
spec:
  dataPolicy:
    allowExternalTransmission: true
    maximumClassification: Internal
    deniedSensitivityTags:
      - CredentialLike
```

The following are conceptually distinct states, although they may not yet exist as separate condition types:

```text
ProviderConfigured
TransmissionAllowed
ProviderReady
```

These states are not the same. A provider can be configured while transmission is blocked by policy.

## Evidence Planning

`InvestigationRequest` supports two evidence planning styles.

| Style | Field | Owner |
| --- | --- | --- |
| Controller-planned evidence | `spec.dataSources[]` | FluxSeer RCA chooses datasource-specific default queries. |
| User-planned evidence | `spec.queries[]` | User supplies explicit query type, datasource, and query or template. |

These fields are mutually exclusive at runtime. If both are set, the request is ambiguous because FluxSeer RCA cannot tell whether the controller or the user owns the evidence plan, so the controller marks the request `InvalidSpec`.

Optional future CRD admission validation direction:

```yaml
x-kubernetes-validations:
  - rule: "!has(self.dataSources) || size(self.dataSources) == 0 || !has(self.queries) || size(self.queries) == 0"
    message: "dataSources and queries are mutually exclusive"
```

Any future CEL implementation should choose the exact form based on the generated OpenAPI schema. If both arrays are defaulted to `[]`, this can be simplified to `size(self.dataSources) == 0 || size(self.queries) == 0`.

## Evidence Retention

Evidence retention controls what FluxSeer RCA keeps after collection and normalization.

| Mode | Support level | Runtime behavior |
| --- | --- | --- |
| `MetadataOnly` | Supported / default | Persist compact metadata, digests, summaries, and references. |
| `NormalizedSnapshot` | Beta / opt-in | Persist normalized evidence snapshots through the configured evidence store. |
| `RawSnapshot` | Reserved / unsupported | API contract exists, but v0.3 runtime fails the request explicitly. |

`NormalizedSnapshot` requires an evidence store, currently configured through:

```text
FLUXSEER_RCA_EVIDENCE_STORE_DIR
```

`RawSnapshot` must not be silently accepted or downgraded.

Required v0.4 runtime behavior:

```text
status.phase=Failed
condition Ready=False
reason=UnsupportedRetentionMode
```

This behavior is implemented at runtime. `RawSnapshot` remains an unsupported contract value and must not be advertised as a usable capability.

## Query Security

`DataSource.spec.queryPolicy.mode` controls query safety.

```yaml
spec:
  queryPolicy:
    mode: TemplatesOnly
```

Supported modes:

| Mode | Meaning |
| --- | --- |
| `LegacyUnrestricted` | Compatibility behavior for trusted raw queries. |
| `TemplatesOnly` | Only named query templates are allowed. |

This is a safety policy, not a datasource type.

Backend-specific limits are separate capabilities:

```text
PromQL function policy
LogQL pipeline policy
result limits
query timeout
query concurrency budget
cumulative byte and duration budget
```

Long-term direction:

```text
TemplatesOnly       default
LegacyUnrestricted  compatibility opt-in
```

## Rule Packs

Rule packs are detection bootstrap configuration, not an RCA execution mode.

```yaml
rulePacks:
  kubernetesBaseline:
    enabled: true
  prometheusBaseline:
    enabled: false
  lokiBaseline:
    enabled: false
```

| Rule pack | Default | Meaning |
| --- | --- | --- |
| Kubernetes baseline | enabled | Portable Kubernetes Events and workload condition checks. |
| Prometheus baseline | disabled | Metrics-based detection; requires a matching `DataSource`. |
| Loki baseline | disabled | Log-based detection; requires a matching `DataSource`. |

Rule packs must not install external observability systems, hosted model providers, or provider secrets as a side effect.

## Deployment And Permission Modes

RBAC currently has profiles:

```yaml
rbac:
  profile: readOnlyRCA
```

| Profile | Purpose |
| --- | --- |
| `readOnlyRCA` | Default read-only RCA permissions. |
| `remediation` | Adds remediation/action CRD permissions. |
| `experimentalExecutor` | Adds executor-like Job and ConfigMap permissions. |

The design risk is that feature flags and `rbac.profile` can become two sources of truth. For example:

```yaml
features:
  remediation:
    enabled: true
rbac:
  profile: readOnlyRCA
```

or:

```yaml
features:
  remediation:
    enabled: false
rbac:
  profile: experimentalExecutor
```

The preferred future direction is:

```text
features -> derive RBAC profile
```

`rbac.profile` is an advanced override. When it is empty, Helm derives the effective RBAC profile from the enabled feature flags:

```text
experimentalExecutor -> experimentalExecutor
remediation          -> remediation
otherwise            -> readOnlyRCA
```

## Controller Runtime Flags

The manager process exposes flags:

```text
--enable-remediation
--enable-legacy-deployment-risk
--leader-elect
--metrics-bind-address
--health-probe-bind-address
```

These split into two groups:

| Group | Flags |
| --- | --- |
| Capability flags | `--enable-remediation`, `--enable-legacy-deployment-risk` |
| Operational flags | `--leader-elect`, `--metrics-bind-address`, `--health-probe-bind-address` |

For normal users, Helm should be the public configuration surface. Container args are the Deployment rendering mechanism, not the preferred user interface.

## Maintainer Release Channels

CI/CD release channels are maintainer workflows, not FluxSeer RCA runtime modes.

| Channel | Trigger | Artifact target |
| --- | --- | --- |
| test dogfood | `test` branch push | Harbor test registry |
| main snapshot | `main` branch push | GHCR snapshot/main artifacts |
| tagged release | `v*` tag / release workflow | GHCR release images, Helm OCI chart, GitHub prerelease |

Do not document these as user-facing runtime modes. They belong in release engineering and maintainer documentation.

## Support Matrix

In this beta document, `Supported` means implemented, covered by the current runtime path, and intended for use in `v0.4.0-beta.1`. It does not imply general availability or compatibility guarantees beyond the documented API group/version identity.

| Capability | Support level |
| --- | --- |
| `readOnly` investigation mode | Supported |
| `MetadataOnly` evidence retention | Supported / default |
| heuristic provider | Supported / default |
| Kubernetes baseline rule pack | Supported / default |
| `NormalizedSnapshot` evidence retention | Beta / opt-in |
| OpenAI, Claude, Gemini hosted providers | Beta / opt-in |
| Prometheus and Loki rule packs | Beta / opt-in |
| remediation | Experimental / opt-in |
| experimental executor | Experimental / opt-in |
| legacy Deployment watcher | Legacy / opt-in |
| `DirectRiskSignal` RCA path | Deprecated compatibility path |
| `RawSnapshot` runtime retention | Reserved / unsupported |
| non-`readOnly` investigation modes | Reserved / unsupported |
| OpenTelemetry and CloudWatch datasources | Scaffold / unsupported for v0.4 production use |

## Priority Follow-ups

1. Add live-cluster RBAC smoke tests for feature-derived profiles.
2. Mark `DirectRiskSignal` and `legacyDeploymentRisk` as migration paths in user-facing docs.
3. Add CRD-level CEL admission validation for unsupported `RawSnapshot` if that can be done without breaking existing manifests.
4. Consider CRD-level CEL validation for the already-enforced `dataSources[]` and `queries[]` runtime mutual exclusion.
5. Move maintainer-only CI/CD channel details out of runtime-mode guidance.
