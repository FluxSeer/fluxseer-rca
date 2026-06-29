# Model Providers

FluxAgent treats model providers as interchangeable backends, not as core platform assumptions.

## Available Provider Packages

- `internal/model/heuristic`
- `internal/model/openai`
- `internal/model/claude`
- `internal/model/gemini`
- `internal/model/bedrock`
- `internal/model/local`

## Current Truth

- The runnable repo defaults to the heuristic provider.
- A local HTTP model endpoint is supported as the first non-heuristic runtime path.
- Hosted `openai`, `gemini`, and `claude` adapters are wired into the runtime path through `ModelProvider`.
- No CRD depends on a specific model vendor.

## Multi-provider Usage

FluxAgent expects users to select the AI backend through `ModelProvider`, not by embedding vendor-specific details into `RiskRule`.

Typical pattern:

1. create one `Secret` per provider token
2. create one `ModelProvider` per hosted model choice
3. point `RiskRule.spec.ai.providerRef.name` at the desired provider

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
```

Then `RiskRule` chooses one:

```yaml
ai:
  rcaEnabled: true
  providerRef:
    name: gemini-provider
```

## Hosted Provider Contract

Hosted providers use a shared response contract. FluxAgent expects the upstream model output to normalize into these fields:

- `riskTitle`
- `riskSummary`
- `severity`
- `confidenceScore`
- `rationale`
- `rcaHypothesis`
- `rcaCauses`
- `actionType`

If a hosted provider response cannot be normalized to this schema, FluxAgent marks `RCAReady=False` with reason `InvalidProviderResponse`.

## Local Endpoint Contract

`ModelProvider.spec.provider: local` is the supported `v0.2 alpha` non-heuristic path.

Minimal example:

```yaml
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: local-provider
spec:
  provider: local
  model: llama3.1:8b
  endpoint: http://local-model.default.svc.cluster.local:8080/v1/reason
  timeout: 10s
```

FluxAgent sends an HTTP `POST` with:

- `model`
- `request.providerHint`
- `request.systemPrompt`
- `request.messages`
- `request.context`

The local endpoint should return a JSON `domain.ModelResponse` shape with a structured `output` map.

Provider-bound request context now includes redacted evidence, not raw secrets or tokens.

## Why This Matters

This design keeps FluxAgent:

- open-source friendly
- runnable without secrets
- vendor-neutral at the API layer
- easy to extend for hosted or self-hosted models later

For the detailed architecture, see [architecture/model-gateway.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/model-gateway.md:1).
