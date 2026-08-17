# `RiskRule`

`RiskRule` is the read-only detection contract for configurable datasource-backed evaluation.

## Purpose

Use `RiskRule` to select workloads, query datasources, and turn matching evidence into a direct `RiskSignal` or an `InvestigationRequest`.

A **detection pattern** is a maintained detector shipped in an official rule
pack. A **signal template** is a parameterized query and threshold used to
define application-specific detection. FluxSeer RCA includes 21 built-in
detection patterns, but declarative `RiskRule` resources can extend Kubernetes
Event, Deployment condition, PromQL, and LogQL detection beyond those defaults.

A RiskRule match answers whether a configured abnormal condition was observed.
It does not assert that the later RCA is confirmed:

> Detection success does not imply RCA confirmation.

Evidence sufficiency and root-cause claim verification belong to the routed
`InvestigationRequest`. See the [product and API glossary](../glossary.md).

## Signal Shape

Current preferred signal fields:

- `datasourceRef`
- `queryType`
- `queryTemplate`
- `threshold`
- `reasons` for event-oriented or deployment-condition rules

Legacy `type` and `query` fields are still accepted as a compatibility path.

Supported query types include:

- `metric`
- `log`
- `event`
- `deploymentCondition`

Target discovery supports `Deployment`, `StatefulSet`, `DaemonSet`, `Job`,
`CronJob`, and `Pod`. Pod owner chains are canonicalized to their supported
workload controller when possible. `deploymentCondition` evaluation remains
specific to Deployment status; other workload kinds can use event, metric, or
log signals.

## Investigation Routing

`spec.investigationPolicy.mode` controls what FluxSeer RCA creates after a rule match:

- `DirectRiskSignal`: default v0.2-compatible path. The controller materializes a `RiskSignal` directly.
- `CreateRequest`: opt-in v0.3 path. The controller creates or updates a deterministic `InvestigationRequest` and lets `InvestigationRequest.status` own the canonical RCA result.

For compatibility, an unset mode still behaves as `DirectRiskSignal`. Built-in
Helm rule packs prefer `CreateRequest` so new installs route RCA through the
canonical `InvestigationRequest` workflow by default while still allowing
optional `RiskSignal` projection through `createRiskSignal: true`.

Example:

```yaml
spec:
  investigationPolicy:
    mode: CreateRequest
    createRiskSignal: true
```

`CreateRequest` uses a deterministic identity derived from the `RiskRule`, target, normalized window bucket, and finding identity. Repeated reconciles for the same incident occurrence update the same request instead of creating unbounded objects. The created request carries lineage annotations that the `InvestigationRequest` controller writes to `status.lineage`.

FluxSeer RCA records three finding identities:

- `objectFindingIdentity`: source UID, target UID, finding type, and normalized evidence attributes. Use this for precise deduplication across reconciles.
- `logicalFindingIdentity`: source apiVersion/kind/namespace/name, target apiVersion/kind/namespace/name, finding type, and normalized evidence attributes. Use this for dashboards and long-term correlation when objects are recreated with new UIDs.
- `incidentOccurrence`: object finding identity plus source generation, target generation, and rounded evidence window bucket. Use this for per-incident `InvestigationRequest` object identity.

Example:

```text
objectFindingIdentity  = sha256(sourceUID, targetUID, findingType, normalizedAttributes)
logicalFindingIdentity = sha256(sourceRef, targetRef, findingType, normalizedAttributes)
incidentOccurrence     = sha256(objectFindingIdentity, sourceGeneration, targetGeneration, windowBucket)
```

Cooldown and notification policy are matching policies; they are not fingerprint inputs.

## Status Conditions

`RiskRule` now reports controller-visible evaluation readiness through conditions.

Current condition types:

- `Ready`
- `Degraded`
- `DatasourceResolved`
- `QueryTypeSupported`

Typical degraded reasons:

- `DataSourceNotFound`
- `CapabilityMismatch`

## Example Checks

```bash
kubectl get riskrule -A
kubectl describe riskrule <name> -n <namespace>
fluxseer report riskrule <name> -n <namespace> -o json > riskrule-report.json
```

The report command exports the selected `RiskRule` together with its public
`InvestigationRequest` and `RiskSignal` objects. See
[RiskRule anomaly reports](../riskrule-reports.md) for the stable report
contract, RBAC requirements, and the distinction between user reports and
maintainer-only runtime test artifacts.
