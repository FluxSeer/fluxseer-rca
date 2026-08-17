# ApprovalPolicy

`ApprovalPolicy` defines approval decisions for guarded remediation. It is
used only when the remediation controller and Policy Pack are enabled.

**Runtime support:** Experimental and opt-in. The current beta supports policy
selection, action/severity decisions, timeout defaults, and escalation-chain
references. Installing the CRD alone does not start a policy reconciler or
enable mutation.

```yaml
apiVersion: aiops.platform/v1alpha1
kind: ApprovalPolicy
metadata:
  name: standard-remediation
  namespace: fluxseer-rca-system
spec:
  enabled: true
  actionTypeRules:
    - actionType: Restart
      action: manual
      reason: "Restart actions require human review"
  severityRules:
    - minSeverity: High
      maxSeverity: Critical
      action: manual
      timeoutSec: 3600
  defaultApprovalTimeout: 3600
  escalation:
    escalationChainRef: standard-remediation-escalation
```

## Implemented fields

- `spec.enabled`: enables or disables policy selection.
- `spec.resourceSelector`: matches remediation resources by labels.
- `spec.namespaceSelector`: matches target namespace labels.
- `spec.actionTypeRules`: selects `auto`, `manual`, or `reject` by action type.
- `spec.severityRules`: selects a decision by severity range and can override
  the approval timeout with `timeoutSec`.
- `spec.defaultApprovalTimeout`: default timeout in seconds.
- `spec.escalation.escalationChainRef`: records the selected escalation chain
  on the generated `AgentAction`.
- `spec.priority`: resolves multiple matching policies.

Policy evaluation records the selected policy reference and decision in the
remediation audit status. A policy is ignored when `spec.enabled` is false or
when its status phase is explicitly `Invalid` or `Disabled`.

The CRD includes status fields for future policy validation reporting. The
current beta does not run a separate `ApprovalPolicy` reconciler, so users
should not expect the operator to populate `Valid` or `Invalid` automatically.

## Enabling the policy pack

```yaml
features:
  remediation:
    enabled: true
  policyPack:
    enabled: true
```

Policy Pack is not a read-only mode switch and is rejected by Helm when
remediation is disabled.
