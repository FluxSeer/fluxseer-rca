# Kubernetes Events Adapter

Kubernetes Events are the only datasource wired by default, because they rely on the controller-runtime client already present inside the operator.

They can also be represented as a `DataSource` resource for consistency with the adapter-neutral config path, but the in-cluster adapter remains the built-in baseline.

## Runtime Wiring

The adapter is always registered by the manager.

Implementation source: [internal/datasource/kubernetes/adapter.go](../../internal/datasource/kubernetes/adapter.go)

## Query Behavior

Current event behavior:

- list `Event` objects in the target namespace
- match by involved object name or kind
- when `reasons[]` is set on an event query, retain only events whose Kubernetes `reason` exactly matches one of the requested reasons, case-insensitively
- return `reason`, `message`, `type`, and `object`

Deployment condition behavior:

- read the selected `Deployment` status directly
- return condition `type`, `status`, `reason`, and `message`
- support `queryType: deploymentCondition`

Probe configuration behavior:

- read HTTP readiness/liveness probe configuration from the selected workload
- resolve numeric and named probe ports against declared container ports
- emit `queryType: probeConfiguration` evidence with path, port resolution,
  container port, and `mismatchConfirmed`
- unresolved named ports and unsupported probe handlers remain unconfirmed

Container termination behavior:

- support `queryType: containerTermination` for `CrashLoopBackOffV2`
- read only application `status.containerStatuses` for a Pod or same-namespace,
  owner-resolved Deployment, StatefulSet, DaemonSet, or ReplicaSet target
- retain waiting reason, restart count, and last termination reason, exit code,
  signal, and finish time; omit Pod specs, environment, and command arguments
- inspect at most 20 Pods and 10 application containers per Pod; truncated
  results cannot satisfy the required termination evidence check; workload
  owner scans use the uncached API reader, with a maximum of 10 Pod-list pages
- require the last termination finish time to fall within the investigation
  window before the profile marks that check complete

The Kubernetes adapter stays read-only and does not require a separate in-cluster service.

## Event Keywords

The supported path is to set event reasons on `RiskRule` or `InvestigationRequest` queries. These reasons are treated as Kubernetes event `reason` filters, not free-text message keywords.

The legacy Deployment annotation path also accepts this per-workload override when `--enable-legacy-deployment-risk=true` is explicitly enabled:

- `fluxseer-rca.aiops.platform/event-keywords`

If not provided, FluxSeer RCA looks for these keywords:

- `backoff`
- `oomkilled`
- `unhealthy`
- `failed`

## Detection Behavior

When a matching event is found through the legacy path, FluxSeer RCA creates a high-severity finding:

- signal type: `workload.kubernetes_event`
- confidence: `90`
- evidence source: `kubernetes-events`

That high confidence is why Kubernetes Events often dominate the merged read-only signal.
