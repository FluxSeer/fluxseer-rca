# NamespaceThreshold

`NamespaceThreshold` limits guarded remediation concurrency for one namespace
or a set of namespaces. It is used by the Policy Pack threshold enforcer.

**Runtime support:** Experimental and opt-in. The current beta applies matching
limits and TTL/approval defaults during guarded remediation. The
`spec.protectionLevel` field is Reserved: it is stored for schema compatibility
but is not applied by the runtime.

```yaml
apiVersion: aiops.platform/v1alpha1
kind: NamespaceThreshold
metadata:
  name: production-remediation-limits
  namespace: fluxseer-rca-system
spec:
  enabled: true
  namespaceSelector:
    matchLabels:
      environment: production
  activePlansLimit: 10
  pendingApprovalsLimit: 5
  priority: 100
  protectionLevel: standard
```

## Implemented fields

- `spec.namespaceSelector`: omitted means the resource's own namespace;
  an explicit selector can match target namespaces by labels.
- `spec.activePlansLimit`: maximum active `RemediationPlan` objects. `0` is
  unlimited.
- `spec.pendingApprovalsLimit`: maximum waiting or escalated `AgentAction`
  objects. `0` is unlimited.
- `spec.priority`: resolves multiple matching thresholds.
- `spec.enabled`: enables or disables the threshold.

The selected threshold reference and any violations are recorded in the
remediation decision path. When a `RemediationPlan` omits `ttlSeconds` or
`approvalTimeoutSeconds`, the threshold's `defaultTTLSeconds` and
`defaultApprovalTimeoutSeconds` are persisted onto the plan and generated
`AgentAction`. Explicit plan values take precedence. `protectionLevel` remains
a schema reservation and is not applied by the current beta controller.

The CRD includes status fields for future validation and enforcement reporting,
but the current beta does not run a separate `NamespaceThreshold` reconciler.
Invalid or explicitly disabled thresholds are ignored by resolution.

## Enabling the policy pack

```yaml
features:
  remediation:
    enabled: true
  policyPack:
    enabled: true
```
