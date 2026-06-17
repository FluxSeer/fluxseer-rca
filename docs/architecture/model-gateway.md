# Model Gateway

FluxAgent keeps model providers behind a provider-neutral interface so the control plane does not depend on one vendor.

## Provider Contract

Source: [internal/model/interface.go](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/model/interface.go:1)

```go
type Provider interface {
    Name() string
    Complete(ctx context.Context, req domain.ModelRequest) (domain.ModelResponse, error)
}
```

## Current Providers

Implemented provider packages:

- `heuristic`
- `openai`
- `claude`
- `gemini`
- `bedrock`
- `local`

See [internal/model](/Users/czhuang/Chongzhe-workspace/HomeLab/FluxSeer/FluxAgent/internal/model:1).

## Runtime Posture

Today the runnable path defaults to the heuristic provider. That choice is deliberate:

- no external secrets required
- no vendor lock-in in the core operator
- repeatable local demos
- safer open-source first-run story

The non-heuristic providers are currently contract scaffolds, not fully wired runtime integrations.

## Intended Usage

The model gateway is the seam for:

- RCA drafting
- symptom correlation
- runbook selection
- remediation proposal formatting
- future evidence-linked reasoning

The provider should not directly own:

- Kubernetes writes
- approval decisions
- policy enforcement
- execution routing

Those responsibilities stay in CRD controllers, guardrails, and executors.

## Design Rules

- Provider output must be explainable and attachable to evidence.
- Guardrails must evaluate actions after reasoning, not inside the provider.
- Runtime should stay functional when no remote model is configured.
- Provider integrations should be swappable without CRD schema changes.
