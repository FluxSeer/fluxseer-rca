# Kubernetes Events Adapter

Kubernetes Events are the only datasource wired by default, because they rely on the controller-runtime client already present inside the operator.

They can also be represented as a `DataSource` resource for consistency with the adapter-neutral config path, but the in-cluster adapter remains the built-in baseline.

## Runtime Wiring

The adapter is always registered by the manager.

Implementation source: [internal/datasource/kubernetes/adapter.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/datasource/kubernetes/adapter.go:1)

## Query Behavior

Current event behavior:

- list `Event` objects in the target namespace
- match by involved object name or kind
- return `reason`, `message`, `type`, and `object`

Deployment condition behavior:

- read the selected `Deployment` status directly
- return condition `type`, `status`, `reason`, and `message`
- support `queryType: deploymentCondition`

The Kubernetes adapter stays read-only and does not require a separate in-cluster service.

## Event Keywords

Per-workload override annotation:

- `fluxagent.aiops.platform/event-keywords`

If not provided, FluxAgent looks for these keywords:

- `backoff`
- `oomkilled`
- `unhealthy`
- `failed`

## Detection Behavior

When a matching event is found, FluxAgent creates a high-severity finding:

- signal type: `workload.kubernetes_event`
- confidence: `90`
- evidence source: `kubernetes-events`

That high confidence is why Kubernetes Events often dominate the merged read-only signal.
