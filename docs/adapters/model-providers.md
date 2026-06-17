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
- Remote providers exist as integration seams and scaffolds.
- No CRD depends on a specific model vendor.

## Why This Matters

This design keeps FluxAgent:

- open-source friendly
- runnable without secrets
- vendor-neutral at the API layer
- easy to extend for hosted or self-hosted models later

For the detailed architecture, see [architecture/model-gateway.md](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/docs/architecture/model-gateway.md:1).
