# `ModelProvider`

`ModelProvider` configures the reasoning backend used by `InvestigationRequest`.

## API

- Group: `aiops.platform`
- Version: `v1alpha1`
- Kind: `ModelProvider`

Source schema: [api/v1alpha1/modelprovider_types.go](../../api/v1alpha1/modelprovider_types.go)

## Spec

- `spec.provider`: provider type such as `heuristic`, `openai`, `claude`, or `gemini`
- `spec.model`: provider model identifier
- `spec.endpoint`: optional provider endpoint override
- `spec.apiKeySecretRef`: optional API key secret reference for hosted providers
- `spec.timeout`: provider request timeout
- `spec.maxTokens`: provider output token limit
- `spec.fallbackProviderRef`: optional fallback provider
- `spec.dataPolicy.allowExternalTransmission`: must be `true` before FluxAgent sends evidence to hosted providers
- `spec.dataPolicy.allowedEvidenceKinds[]`: optional allowlist such as `MetricObservation`, `KubernetesEventObservation`, `LogObservation`, or `DeploymentConditionObservation`
- `spec.dataPolicy.deniedSensitivityTags[]`: optional denylist such as `CredentialLike`, `PersonalData`, `CustomerData`, `SourceCode`, `InfrastructureMetadata`, or `SecuritySensitive`
- `spec.dataPolicy.allowLogSamples`: controls whether log sample text may be sent independently from metric and event metadata
- `spec.dataPolicy.requireRedaction`: requires transmitted evidence to carry redaction metadata before hosted provider egress
- `spec.dataPolicy.maximumClassification`: highest classification level allowed for hosted provider egress. Unset defaults to `Internal`.

## Data Boundary

The built-in `heuristic` provider remains local and does not require external transmission. Hosted providers such as `openai`, `claude`, and `gemini` are blocked unless `spec.dataPolicy.allowExternalTransmission` is explicitly set to `true`.

When hosted transmission is allowed, FluxAgent sends normalized, redacted, and policy-filtered evidence. Log samples are omitted unless `allowLogSamples` is true.

Hosted provider egress uses this order:

1. normalize evidence
2. redact evidence
3. compute classification
4. filter allowed evidence kinds
5. reject denied sensitivity tags
6. reject evidence above `maximumClassification`
7. require redaction metadata when `requireRedaction` is true
8. compact the evidence bundle
9. compute the evidence bundle digest
10. call the provider

Classification level and sensitivity tags are separate dimensions:

| Field | Values |
| --- | --- |
| `classification.level` | `Public`, `Internal`, `Confidential`, `Restricted` |
| `classification.sensitivityTags[]` | `CredentialLike`, `PersonalData`, `CustomerData`, `SourceCode`, `InfrastructureMetadata`, `SecuritySensitive` |

The level order is `Public < Internal < Confidential < Restricted`. `CredentialLike` is a sensitivity tag, not a level. Unknown future or malformed levels are treated conservatively by provider policy enforcement.

Redaction does not automatically lower classification. For example, a log sample that looked credential-like before redaction remains at least `Restricted` unless a future explicit, versioned declassification policy exists.

Execution metadata records an egress audit with decision, reason, provider type, evidence bundle digest, evidence kinds, sensitivity tags sent, whether log samples were included, maximum classification observed, maximum classification allowed, maximum classification sent, and classification policy version. The audit does not include full evidence payloads, full prompts, raw provider responses, secrets, or complete queries.

Read-only means FluxAgent does not mutate user workloads. It does not mean zero data egress when a hosted provider is explicitly enabled.
