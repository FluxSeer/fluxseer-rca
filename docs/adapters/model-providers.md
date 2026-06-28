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
- Remote hosted providers still exist mainly as integration seams and scaffolds.
- No CRD depends on a specific model vendor.

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
