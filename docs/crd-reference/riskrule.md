# `RiskRule`

`RiskRule` is the read-only detection contract for configurable datasource-backed evaluation.

## Purpose

Use `RiskRule` to select workloads, query datasources, and turn matching evidence into `RiskSignal`.

## Signal Shape

Current preferred signal fields:

- `datasourceRef`
- `queryType`
- `queryTemplate`
- `threshold`
- `reasons` for event-oriented rules

Legacy `type` and `query` fields are still accepted as a compatibility path.

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
