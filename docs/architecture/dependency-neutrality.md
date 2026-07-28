# Dependency Neutrality

FluxAgent can integrate with Kubernetes, Prometheus, Loki, and external model providers, but the project should not be structurally bound to any one of them.

The design rule is:

```text
Integrate through adapters.
Do not couple the core product truth to one stack.
```

## Three Dependency Types

### Runtime Dependency

FluxAgent should remain usable when optional systems are absent.

Expected baseline:

- Kubernetes is required for the operator control plane.
- Kubernetes Events are the only default datasource.
- Prometheus is optional.
- Loki is optional.
- external LLM credentials are optional.
- remediation is optional.

Examples:

- without Prometheus, `RiskRule` evaluation can still use Kubernetes Events
- without Loki, FluxAgent can still produce `RiskSignal`
- without external model credentials, FluxAgent can fall back to the heuristic provider
- without remediation enablement, FluxAgent must not create `RemediationPlan` or `AgentAction`

### Compile-time Dependency

Core logic should depend on FluxAgent-owned contracts, not vendor SDKs or Kubernetes API types everywhere.

Required direction:

- `internal/controllers` may depend on controller-runtime and Kubernetes clients
- `api/v1alpha1` may depend on Kubernetes CRD machinery
- datasource adapters may depend on Prometheus, Loki, or cloud-specific clients
- model adapters may depend on provider SDKs
- core evaluation, evidence, and reasoning flow should stay expressed in internal domain types

### Deployment Dependency

FluxAgent should not install a monitoring stack for the user by default.

Expected behavior:

- Helm and manifests install FluxAgent
- Prometheus and Loki remain bring-your-own integrations
- optional examples may show how to connect to those systems
- demos may ship fake observability endpoints for repeatable local validation

## Ports and Adapters

The intended shape is:

```text
Core logic
  ├─ rule evaluation
  ├─ evidence normalization
  ├─ RCA orchestration
  └─ workflow status generation

Ports
  ├─ DataSource
  ├─ ModelProvider
  ├─ Notifier
  ├─ Executor
  └─ AuditStore

Adapters
  ├─ Kubernetes Events
  ├─ Prometheus
  ├─ Loki
  ├─ OpenAI API / Claude API / Gemini API
  ├─ Webhook
  └─ Kubernetes / GitOps executors
```

The core path should know that evidence is metrics, logs, or events. It should not care whether those came from Prometheus, Loki, or another backend.

## Domain Model Boundary

FluxAgent already has internal domain types such as [internal/domain/types.go](../../internal/domain/types.go).

That boundary should remain the center of the design:

- adapters translate external payloads into FluxAgent domain types
- controllers translate Kubernetes resources into domain inputs
- reasoning and evaluation operate on FluxAgent-owned models

This is the main protection against scattering `appsv1.Deployment`, `corev1.Event`, or vendor response types across the whole codebase.

## Datasource Design Direction

Current repo status:

- FluxAgent already has a datasource registry and adapter interface
- Prometheus and Loki are optional registrations
- Kubernetes Events are available by default
- `RiskRule` first-batch schema still uses inline signal `type` and `query`

Target direction:

1. datasources become capability-oriented rather than hard-coded by backend name
2. query output is normalized before it reaches rule evaluation or reasoning
3. datasource configuration moves toward its own CRD
4. `RiskRule` references datasource objects instead of binding directly to Prometheus or Loki

Example future contract:

```go
type DataSource interface {
    Name() string
    Type() string
    Capabilities() Capabilities
    Query(ctx context.Context, req QueryRequest) (*QueryResult, error)
    HealthCheck(ctx context.Context) error
}
```

This is a design target, not the exact current interface.

## Query and Evidence Normalization

Adapter output should converge before it reaches the rest of the system.

Target properties:

- a standard query result model
- explicit source and query type metadata
- normalized metric, log, and event evidence
- redaction before provider-bound reasoning

This keeps rule evaluation, RCA generation, and notification formatting independent from one datasource implementation.

## Template-driven Queries

Prometheus and Loki queries should be reviewable configuration, not arbitrary LLM-generated execution.

Expected direction:

- queries come from `RiskRule` templates or future datasource-linked templates
- templates render against target metadata such as namespace, labels, or workload name
- expensive or unsafe query generation is constrained before runtime

This keeps observability access auditable and GitOps-friendly.

## Graceful Degradation

Missing optional integrations should produce status and conditions, not crashes.

Examples:

- unsupported datasource type: mark resource status as unsupported
- referenced datasource not found: mark rule evaluation degraded
- model provider not found: still create `RiskSignal`, but mark RCA condition as not ready
- optional adapter unhealthy: continue with remaining evidence sources when possible

FluxAgent should fail closed on mutation, but fail soft on optional evidence enrichment.

## Staged Delivery

The current `v0.2` first batch is intentionally smaller than the full dependency-neutral target.

Delivered now:

- `RiskRule` CRD
- `ModelProvider` CRD
- `RiskRuleReconciler`
- heuristic provider wiring
- read-only RCA persistence into `RiskSignal`

Still planned as follow-up work:

- richer datasource capability contracts
- normalized query result evolution
- dedicated `DataSource` CRD
- `RiskRule` `datasourceRef` support
- broader degraded-status reporting for missing adapters

That sequencing is deliberate: ship a working read-only RCA path first, then move datasource configuration from inline rule fields into a more adapter-neutral contract.
