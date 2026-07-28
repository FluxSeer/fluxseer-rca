# `RiskRule`

`RiskRule` is the read-only detection contract for configurable datasource-backed evaluation.

## Purpose

Use `RiskRule` to select workloads, query datasources, and turn matching evidence into a direct `RiskSignal` or an `InvestigationRequest`.

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

## Investigation Routing

`spec.investigationPolicy.mode` controls what FluxAgent creates after a rule match:

- `DirectRiskSignal`: default v0.2-compatible path. The controller materializes a `RiskSignal` directly.
- `CreateRequest`: opt-in v0.3 path. The controller creates or updates a deterministic `InvestigationRequest` and lets `InvestigationRequest.status` own the canonical RCA result.

Example:

```yaml
spec:
  investigationPolicy:
    mode: CreateRequest
    createRiskSignal: true
```

`CreateRequest` uses a deterministic identity derived from the `RiskRule`, target, normalized window bucket, and finding identity. Repeated reconciles for the same incident occurrence update the same request instead of creating unbounded objects. The created request carries lineage annotations that the `InvestigationRequest` controller writes to `status.lineage`.

FluxAgent records three finding identities:

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
```
