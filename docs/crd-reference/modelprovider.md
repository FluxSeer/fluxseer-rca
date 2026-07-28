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
- `spec.dataPolicy.allowLogSamples`: controls whether log sample text may be sent independently from metric and event metadata
- `spec.dataPolicy.maximumClassification`: maximum classification label recorded for provider egress audit

## Data Boundary

The built-in `heuristic` provider remains local and does not require external transmission. Hosted providers such as `openai`, `claude`, and `gemini` are blocked unless `spec.dataPolicy.allowExternalTransmission` is explicitly set to `true`.

When hosted transmission is allowed, FluxAgent sends normalized, redacted, and policy-filtered evidence. Log samples are omitted unless `allowLogSamples` is true. Execution metadata records an egress audit with provider type, evidence bundle digest, evidence kinds, whether log samples were included, and maximum classification sent. The audit does not include full evidence payloads.

Read-only means FluxAgent does not mutate user workloads. It does not mean zero data egress when a hosted provider is explicitly enabled.
