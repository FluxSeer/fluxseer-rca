# `DataSource`

`DataSource` is the runtime configuration contract for optional observability adapters.

## Purpose

Use `DataSource` to describe how FluxAgent should connect to a datasource without hard-coding every connection through manager environment variables.

Current `v0.2` batch support:

- `prometheus`
- `loki`
- `kubernetesEvents`

## Example

```yaml
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata:
  name: prometheus
  namespace: fluxagent-system
spec:
  type: prometheus
  endpoint: http://prometheus-server.monitoring.svc:9090
  timeout: 10s
```

## Spec

- `spec.type`: datasource type identifier
- `spec.endpoint`: HTTP endpoint for remote datasources such as Prometheus or Loki
- `spec.timeout`: request timeout
- `spec.auth`: optional auth config
- `spec.tls`: optional TLS overrides

## Current Behavior

- Kubernetes Events remain available by default through the in-cluster client.
- Prometheus and Loki remain optional.
- manager env vars still work as a migration fallback.
- `RiskRule` can now reference datasource objects through `datasourceRef`.

## Status Conditions

Current condition types:

- `Ready`
- `Unsupported`

Typical false or degraded reasons:

- `AdapterNotRegistered`
- `EndpointMissing`
- `SecretNotFound`
- `SecretKeyMissing`
