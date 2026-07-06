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

## Architectural Role

The model gateway is a reasoning seam inside the broader remediation architecture, not the owner of workflow state or execution rights.

Its intended role is:

```text
Risk evidence
→ provider-neutral reasoning interface
→ explainable model output
→ RemediationPlan enrichment or proposal formatting
```

Its non-role is equally important:

- it does not write to Kubernetes
- it does not decide approval policy
- it does not dispatch executors
- it does not bypass CRD status transitions

## Runtime Posture

Today the runnable path defaults to the heuristic provider. That choice is deliberate:

- no external secrets required
- no vendor lock-in in the core operator
- repeatable local demos
- safer open-source first-run story

The heuristic provider remains the default runnable path. The local HTTP endpoint is the simplest non-heuristic runtime path, and hosted `openai`, `claude`, and `gemini` adapters are also wired through `ModelProvider`.

## Architecture Diagram

```mermaid
flowchart LR
    EV[Risk Evidence]
    CRD[RiskSignal / RemediationPlan]
    MG[Model Gateway]
    PR[Provider Interface]
    H[heuristic]
    O[openai scaffold]
    C[claude scaffold]
    G[gemini scaffold]
    B[bedrock scaffold]
    L[local endpoint]
    GR[Guardrails]
    EX[Executors]

    EV --> MG
    CRD --> MG
    MG --> PR
    PR --> H
    PR --> O
    PR --> C
    PR --> G
    PR --> B
    PR --> L
    MG --> CRD
    CRD --> GR
    GR --> EX
```

## Intended Usage

The model gateway is the seam for:

- RCA drafting
- symptom correlation
- runbook selection
- remediation proposal formatting
- evidence-linked reasoning with redacted provider context

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

## Implementation Status

Implemented today:

- provider interface
- provider registry
- heuristic provider for runnable local behavior
- local endpoint provider wired into runtime configuration
- provider-bound evidence redaction before reasoning calls
- hosted `openai`, `claude`, and `gemini` adapters
- `fallbackProviderRef` failover between `ModelProvider` objects for provider and secret related failures
- shared hosted-provider timeout, HTTP status mapping, and transient retry policy

Scaffolded today:

- `bedrock`

This is why the model gateway should be described as an extensibility seam with a runnable heuristic default and guarded hosted-provider support, not as a fully integrated multi-provider production inference layer.
