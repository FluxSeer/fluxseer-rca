# Kubernetes Ecosystem Integration Contract

FluxAgent exposes RCA workflow state through Kubernetes API objects. External tools should consume stable object metadata, top-level conditions, and bounded status fields before parsing provider-specific execution details.

## Recommended Consumption Model

```text
watch InvestigationRequest
-> inspect status.conditions
-> read status.outcome / status.failure / status.degradation
-> follow linked RiskSignal or evidence refs only when needed
```

Do not treat provider-specific response bodies, prompts, raw evidence, or full rendered queries as an integration surface.

## Canonical Object

The canonical RCA execution record is:

```text
InvestigationRequest.status
```

`RiskSignal` is a materialized finding or compatibility projection. It is useful for notifications and dashboards, but it is not the authoritative RCA execution record.

## Conditions

Current `InvestigationRequest` condition types:

| Type | Meaning |
| --- | --- |
| `Ready` | The investigation reached a consumable terminal state or failed before it could do so. |
| `TargetResolved` | Target reference resolution succeeded or failed. |
| `DatasourceResolved` | Datasource references and collection plan were resolved. |
| `QueryTypeSupported` | Query types are compatible with datasource capabilities. |
| `EvidenceCollectionReady` | Evidence collection succeeded or produced a bounded failure. |
| `RCAReady` | RCA output is available. |
| `RemediationReady` | Verified RCA evidence is available for remediation planning. |
| `Degraded` | The result is partial or degraded due to optional dependency or bounded failure semantics. |

Every condition should be read with:

```text
type
status
reason
message
observedGeneration
lastTransitionTime
```

`RCAReady=True` means an RCA result exists. It does not mean the target workload is healthy or remediated.

`Degraded=True` describes a partial or bounded execution, such as optional dependency or evidence loss, that still produced a consumable result. A hard workflow failure such as `TargetNotFound` is represented by `phase: Failed`, `outcome: Unknown`, and blocking conditions; it is not automatically degraded.

`QueryTypeSupported=Unknown` with reason `DataSourceUnavailable` means datasource resolution failed before query capability validation could run.

`Verified=Unknown` with reason `RCAUnavailable` means verification did not run because RCA execution was blocked. `Verified=False` means verification ran and did not support the proposed root-cause claims.

For `RiskSignal`, `status.phase` describes the finding lifecycle only. A `Confirmed` phase means the finding was materialized, not that root-cause evidence was verified. Consumers should use:

```text
FindingReady=True -> display or notify the finding
RCAReady=True -> display verified RCA compatibility fields
RemediationReady=True -> allow remediation planning or execution workflows
```

When `RCAReady=False`, `RemediationReady` should also be `False` with the same blocking reason.

`RiskSignal.spec.confidence` describes finding detection confidence. It is not RCA confidence and must not override `RCAReady`, `RemediationReady`, or canonical `InvestigationRequest.status.verdict` fields. A signal can have high detection confidence while its root cause remains unverified.

`RiskSignal.spec.actionType` is notification or planning metadata until a remediation resource is explicitly created. Existing `notification.sendSlack` values are a legacy notification alias and may route to a generic webhook sink depending on installation settings; consumers should not infer a Slack-specific delivery guarantee from that value.

## Stable Reason Examples

External integrations may use reason strings for routing, but should tolerate new reasons.

Common reasons include:

```text
InvestigationCompleted
TargetNotFound
DataSourceNotFound
CapabilityMismatch
DataSourceUnavailable
QueryBudgetExceeded
ProviderDataPolicyDenied
ProviderDataPolicyRejected
ProviderUnavailable
ProviderFallbackLoop
InvalidProviderResponse
RequiredEvidenceMissing
NoIssueFound
UnsupportedRetentionMode
```

## Labels And Annotations

FluxAgent-owned objects use low-cardinality metadata for ownership, lineage, and finding identity.

Examples:

```text
fluxagent.aiops.platform/managed-by
fluxagent.aiops.platform/risk-rule
fluxagent.aiops.platform/target-ref
fluxagent.aiops.platform/lineage-source
fluxagent.aiops.platform/lineage-source-kind
fluxagent.aiops.platform/target-uid
fluxagent.aiops.platform/finding-fingerprint
fluxagent.aiops.platform/object-finding-identity
fluxagent.aiops.platform/logical-finding-identity
fluxagent.aiops.platform/incident-occurrence
fluxagent.aiops.platform/investigation-depth
```

Do not add object UID, full query text, evidence bundle digest, prompt text, or target name as metric labels.

## Prometheus And kube-state-metrics

Use kube-state-metrics custom resource support to project bounded fields such as:

```text
InvestigationRequest status.phase
InvestigationRequest status.outcome
InvestigationRequest status.conditions[]
RiskSignal status.phase
RiskSignal status.conditions[]
```

Use FluxAgent native metrics for controller and RCA quality signals:

```text
fluxagent_investigation_total
fluxagent_provider_requests_total
fluxagent_provider_failures_total
fluxagent_datasource_query_duration_seconds
fluxagent_datasource_query_queue_depth
fluxagent_datasource_queries_in_flight
fluxagent_evidence_truncated_total
fluxagent_claim_verification_total
```

## Argo CD Health Example

An Argo CD health check should prefer conditions:

```lua
hs = {}
if obj.status ~= nil and obj.status.conditions ~= nil then
  for _, condition in ipairs(obj.status.conditions) do
    if condition.type == "Ready" and condition.status == "True" then
      hs.status = "Healthy"
      hs.message = condition.message
      return hs
    end
    if condition.type == "Ready" and condition.status == "False" then
      hs.status = "Degraded"
      hs.message = condition.reason .. ": " .. condition.message
      return hs
    end
  end
end
hs.status = "Progressing"
hs.message = "waiting for FluxAgent RCA status"
return hs
```

## Kyverno Policy Example

Example policy intent:

```text
deny hosted ModelProvider objects unless dataPolicy.allowExternalTransmission=true is explicitly reviewed by the platform team
```

FluxAgent still enforces hosted-provider egress at runtime. Admission policy is an additional organizational control, not the primary data boundary.

## External Alert Producer Boundary

External alert systems should create or update Kubernetes API inputs:

```text
Alertmanager / webhook / Argo Events
-> InvestigationRequest
```

or:

```text
Alertmanager / webhook / Argo Events
-> RiskRule-compatible custom producer
-> InvestigationRequest
```

External producers should preserve source lineage through labels, annotations, or `status.lineage.source` when a controller owns the object.

FluxAgent remains the RCA control plane. Alert producers should not bypass FluxAgent evidence policy by sending raw logs or arbitrary provider prompts directly to hosted AI.
