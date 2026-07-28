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
- `spec.networkPolicy.allowedHosts[]`: optional exact or wildcard host allowlist
- `spec.networkPolicy.allowedCIDRs[]`: CIDRs that may be used for private datasource IP endpoints
- `spec.networkPolicy.deniedCIDRs[]`: additional CIDRs denied before any request is made
- `spec.queryPolicy.mode`: query safety mode. Empty or `LegacyUnrestricted` preserves the current trusted-query behavior; `TemplatesOnly` only allows named query templates listed in `allowedTemplates[]`.
- `spec.queryPolicy.allowedTemplates[]`: template names allowed when `mode: TemplatesOnly`
- `spec.queryPolicy.maxRange`: maximum allowed query lookback before a datasource request is made
- `spec.queryPolicy.allowRegexMatchers`: whether rendered metric/log queries may contain regex matchers such as `=~`, `!~`, or `|~`
- `spec.queryPolicy.requireTargetScope`: whether rendered metric/log queries must include the target namespace and workload scope
- `spec.auth`: optional auth config
- `spec.tls`: optional TLS overrides

Datasource HTTP clients apply a network safety guard before registration, for every redirect target, and when dialing resolved hostnames. Metadata endpoints, loopback, link-local, unspecified IPs, and private IP endpoints without `allowedCIDRs` are denied. Cluster service hostnames ending in `.svc` or `.svc.cluster.local` are allowed by default and may resolve to private ClusterIP addresses. Environment proxy settings are disabled for datasource clients.

For HTTP datasources, FluxAgent resolves hostnames through a policy-aware dialer, validates all resolved A/AAAA addresses, and pins the TCP connection to a verified IP. This reduces DNS rebinding risk while keeping the original request hostname available to the HTTP transport.

When `queryPolicy.mode: TemplatesOnly` is configured, FluxAgent validates rendered queries before any datasource network request. It accepts named `queryTemplate` entries and controller-owned default templates; raw `query` expressions are rejected even when the query name matches an allowed template. Rejections use bounded diagnostic reasons such as `template_required`, `template_not_allowed`, `range_exceeded`, `regex_denied`, or `target_scope_required`; full query text is not needed for policy metrics.

## Current Behavior

- Kubernetes Events remain available by default through the in-cluster client.
- Prometheus and Loki remain optional.
- manager env vars still work as a migration fallback.
- `RiskRule` can now reference datasource objects through `datasourceRef`.
- query policy is opt-in for compatibility; new production installations should prefer `TemplatesOnly` after rule packs are migrated to named templates.

## Status Conditions

Current condition types:

- `Ready`
- `Unsupported`

Typical false or degraded reasons:

- `AdapterNotRegistered`
- `EndpointMissing`
- `SecretNotFound`
- `SecretKeyMissing`
