# Capability Maturity

FluxSeer RCA exposes several Kubernetes resources and adapters, but they are not all part of the same maturity tier.

The default product path is intentionally narrow:

```text
InvestigationRequest
-> bounded evidence collection
-> heuristic or explicitly configured hosted ModelProvider
-> structured RCA status
-> optional RiskSignal materialization
```

## Resource Tiers

| Tier | Resources | Positioning |
| --- | --- | --- |
| Canonical core | `InvestigationRequest`, `DataSource`, `ModelProvider` | Required contracts for canonical RCA execution. |
| Bootstrap detection | `RiskRule` | Optional recurring detection and baseline rule-pack entrypoint. |
| Materialization / compatibility | `RiskSignal` | External finding, notification target, and v0.2-compatible output projection. |
| Guarded experimental | `RemediationPlan`, `AgentAction` | CRDs are installed for compatibility, but controllers and RBAC are disabled by default. |
| Legacy bootstrap | `DeploymentRiskReconciler` | Annotation-driven Deployment detection path; disabled by default and retained only as explicit opt-in. |
| Scaffold | OpenTelemetry, CloudWatch datasource adapters | Development skeletons, not supported v0.4 beta adapters. |
| Guarded policy pack | `ApprovalPolicy`, `NamespaceThreshold`, `EscalationChain` | Opt-in remediation governance; limits and TTL/approval defaults are implemented, while protection levels and multi-stage actions remain reserved. |

`RiskRule` and `RiskSignal` are valid public APIs, but they are not required for every RCA. New integrations should treat `InvestigationRequest.status` as the canonical RCA truth.

## Capability Matrix

| Capability | Maturity | Default |
| --- | --- | --- |
| Kubernetes Events evidence | Supported | Enabled through the built-in adapter and default Kubernetes baseline rule pack. |
| Kubernetes workload status | Supported | `deploymentCondition` remains a compatibility query type and returns workload status for supported workload targets. |
| Workload target discovery | Supported | `RiskRule` supports `Deployment`, `StatefulSet`, `DaemonSet`, `Job`, `CronJob`, and Pod owner-chain canonicalization. |
| Prometheus datasource | Supported | Opt-in `DataSource` or environment configuration. |
| Loki datasource | Supported | Opt-in `DataSource` or environment configuration. |
| Heuristic provider | Supported | Default no-secret reasoning provider. |
| OpenAI API provider | Beta / opt-in | Requires `ModelProvider`, Secret, and hosted-provider data egress opt-in. |
| Claude API provider | Beta / opt-in | Requires `ModelProvider`, Secret, and hosted-provider data egress opt-in. |
| Gemini API provider | Beta / opt-in | Requires `ModelProvider`, Secret, and hosted-provider data egress opt-in. |
| Normalized snapshot retention | Beta / opt-in | Requires `FLUXSEER_RCA_EVIDENCE_STORE_DIR` and `storageRef.name: local-filesystem`. |
| Raw snapshot retention | Reserved / unsupported | Contract is present, runtime rejects it in the current v0.4 beta. |
| Replay artifacts and comparison | Foundation / library | Terminal CRD export and deterministic bundle comparison exist; no runtime replay runner or controller entrypoint is shipped. |
| OpenTelemetry adapter | Scaffold | Not part of the supported v0.4 beta adapter set. |
| CloudWatch adapter | Scaffold | Not part of the supported v0.4 beta adapter set. |
| Remediation | Experimental | Requires explicit controller and RBAC opt-in. |
| Legacy Deployment annotation detection | Legacy / opt-in | Disabled by default. |

## Default Installation Boundary

The default Helm installation should provide only the trustworthy RCA path:

- no hosted provider resources
- no external provider calls
- no remediation/action controllers
- no legacy Deployment watcher
- no Job mutation permission
- no ConfigMap mutation permission
- no cluster-wide Secret read permission

Hosted provider credentials are read through a namespaced Role in the controller namespace. Cross-namespace provider credentials require explicit additional RBAC outside the default chart profile.

## RBAC Profiles

Default:

```yaml
rbac:
  profile: ""
features:
  remediation:
    enabled: false
  experimentalExecutor:
    enabled: false
  legacyDeploymentRisk:
    enabled: false
```

An empty `rbac.profile` lets Helm derive the profile from feature flags. The default derived profile is `readOnlyRCA`, which keeps workload access read-only and grants write access only to FluxSeer RCA-owned RCA resources and statuses.

Experimental remediation requires explicit opt-in:

```yaml
features:
  remediation:
    enabled: true
```

Executor-like permissions such as Job or ConfigMap mutation require the broader experimental profile:

```yaml
features:
  remediation:
    enabled: true
  experimentalExecutor:
    enabled: true
```

Set `rbac.profile` explicitly only as an advanced override.

These profiles do not change CRD installation. Helm CRDs remain installed for API compatibility; runtime controllers and mutation permissions are what remain disabled by default.
