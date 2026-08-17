# EscalationChain

`EscalationChain` selects an escalation reference for approval timeout
notifications. It is used by the guarded remediation path when Policy Pack is
enabled.

**Runtime support:** Experimental and opt-in, limited to chain selection,
notification metadata, and the `Escalated` audit transition. Stage-by-stage
delays, conditions, assignees, templates, reassignment, auto-rejection, and
force-execution are Reserved and are not executed by the current runtime.

```yaml
apiVersion: aiops.platform/v1alpha1
kind: EscalationChain
metadata:
  name: standard-remediation-escalation
  namespace: fluxseer-rca-system
spec:
  enabled: true
  priority: 100
  stages:
    - name: platform-on-call
```

## Implemented behavior

- `spec.enabled`: enables or disables chain selection.
- `spec.resourceSelector`: selects chains by target resource labels.
- `spec.priority`: resolves multiple matching chains.
- `spec.stages`: must contain at least one stage; the selected chain is copied
  into the escalation route reference for auditability.
- An explicit chain reference can be supplied by
  `ApprovalPolicy.spec.escalation.escalationChainRef`.
- When an approval timeout is reached, the current beta sends a notification,
  records the chain name/version in the notification fields, and marks the
  `AgentAction` `Escalated`.

The CRD schema also contains stage delays, conditions, actions, assignees,
notification templates, and action types such as `reassign`, `auto_reject`,
and `force_execute`. These are not executed by the current beta controller.
The current path does not automatically reject, reassign, or force-execute an
action after escalation.

The CRD includes status fields for future validation reporting, but the
current beta does not run a separate `EscalationChain` reconciler. Invalid or
explicitly disabled chains are ignored by resolution.

## Enabling the policy pack

```yaml
features:
  remediation:
    enabled: true
  policyPack:
    enabled: true
```
