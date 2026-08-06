# Model Providers

FluxSeer RCA treats model providers as interchangeable backends, not as core platform assumptions.

## Available Provider Packages

- `internal/model/heuristic`
- `internal/model/openai`
- `internal/model/claude`
- `internal/model/gemini`

## Current Truth

- The runnable repo defaults to the heuristic provider.
- Hosted `openai`, `gemini`, and `claude` adapters are the only supported external model paths.
- No CRD depends on a specific model vendor.

## Multi-provider Usage

FluxSeer RCA expects users to select the AI backend through `ModelProvider`, not by embedding vendor-specific details into `RiskRule`.

Typical pattern:

1. create one `Secret` per provider token
2. create one `ModelProvider` per hosted model choice
3. point `RiskRule.spec.ai.providerRef.name` at the desired provider

Provider token `Secret` objects are namespace-local to the `ModelProvider`. The default Helm RBAC grants Secret read permission only in the FluxSeer RCA controller namespace, so cross-namespace provider credentials require explicit additional RoleBinding configuration.

Example provider choices:

```yaml
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: openai-provider
spec:
  provider: openai
  model: gpt-5.1
  apiKeySecretRef:
    name: openai-secret
    key: api-key
  dataPolicy:
    allowExternalTransmission: true
    maximumClassification: Internal
    allowedEvidenceKinds:
      - MetricObservation
      - KubernetesEventObservation
      - DeploymentConditionObservation
    deniedSensitivityTags:
      - CredentialLike
      - PersonalData
    allowLogSamples: false
    requireRedaction: true
  fallbackProviderRef:
    name: heuristic-provider
```

```yaml
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: gemini-provider
spec:
  provider: gemini
  model: gemini-2.5-pro
  apiKeySecretRef:
    name: gemini-secret
    key: api-key
  dataPolicy:
    allowExternalTransmission: true
    maximumClassification: Internal
    allowLogSamples: false
```

```yaml
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: claude-provider
spec:
  provider: claude
  model: claude-sonnet-4
  apiKeySecretRef:
    name: claude-secret
    key: api-key
  dataPolicy:
    allowExternalTransmission: true
    maximumClassification: Internal
    allowLogSamples: false
```

Then `RiskRule` chooses one:

```yaml
ai:
  rcaEnabled: true
  providerRef:
    name: gemini-provider
```

## Hosted Provider Contract

Hosted providers use a shared response contract. FluxSeer RCA expects the upstream model output to normalize into these fields:

- `riskTitle`
- `riskSummary`
- `severity`
- `confidenceScore`
- `rationale`
- `rcaHypothesis`
- `rcaCauses`
- `actionType`

If a hosted provider response cannot be normalized to this schema, FluxSeer RCA marks `RCAReady=False` with reason `InvalidProviderResponse`.

Hosted provider runtime behavior is now unified across `openai`, `gemini`, and `claude`:

- default request timeout: `15s` when `spec.timeout` is omitted
- transient retry budget: 3 attempts total
- retryable failures: transport timeout, `429`, and `5xx`
- auth failures: `ProviderAuthFailed`
- provider throttling after retry exhaustion: `ProviderRateLimited`
- request or model configuration rejection: `ProviderRequestInvalid`
- explicit egress opt-in is required through `spec.dataPolicy.allowExternalTransmission`
- evidence above `spec.dataPolicy.maximumClassification`, denied sensitivity tags, or missing required redaction metadata is rejected before any hosted provider call

Classification level and sensitivity tags are separate. Levels are ordered as `Public < Internal < Confidential < Restricted`; tags include `CredentialLike`, `PersonalData`, `CustomerData`, `SourceCode`, `InfrastructureMetadata`, and `SecuritySensitive`. Redaction does not automatically lower classification.

## Fallback Behavior

`spec.fallbackProviderRef.name` is optional.

When it is set, FluxSeer RCA attempts the primary `ModelProvider` first. If the primary path fails because of provider availability, unsupported provider type, secret resolution, or invalid structured response, the model gateway resolves the fallback provider and retries RCA there.

Typical pattern:

- primary: hosted provider such as `openai`, `gemini`, or `claude`
- fallback: `heuristic-provider` for a no-secret fail-closed path

If the fallback resolution fails, FluxSeer RCA surfaces the fallback failure reason such as:

- `ProviderNotFound`
- `ResolverUnavailable`
- `ProviderFallbackLoop`

## Fallback Policy Requirement

The fallback contract should remain explicit and narrow as the provider gateway hardens. This is a contract hardening target and may require narrowing currently supported fallback cases before production claims.

Recommended fallback-eligible failures:

- timeout
- HTTP `429`
- HTTP `5xx`
- connection failure
- provider unavailable

Recommended non-fallback failures:

- invalid credentials
- malformed provider spec
- unsupported model
- invalid provider response schema
- evidence exceeds configured limits and cannot be truncated safely

Invalid provider responses should remain visible because they may indicate schema drift or an adapter bug. Fallback chains must not loop; `v0.2` should either allow only one fallback level or use runtime visited-provider cycle detection.

## Why This Matters

This design keeps FluxSeer RCA:

- open-source friendly
- runnable without secrets
- vendor-neutral at the API layer
- explicit about which hosted API providers are supported

For the detailed architecture, see [architecture/model-gateway.md](../architecture/model-gateway.md).
