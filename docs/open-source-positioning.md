# Open Source Positioning

FluxAgent is positioned as a Kubernetes-native control plane for evidence-verifiable RCA, not as a bot tied to one monitoring stack and not as a general-purpose agent shell first.

FluxAgent favors explicit Kubernetes-native configuration over automatic discovery. It is for teams that want to define investigation scope, datasource boundaries, and model-provider choices themselves, accepting some CRD learning cost in exchange for customizability, low default resource usage, and auditability.

Security is part of the product positioning. FluxAgent is read-only by default, secret-minimizing by design, and usable in heuristic-only mode without sending evidence to an external model provider.

## What Is Open Source Here

- CRD contracts: `InvestigationRequest`, `DataSource`, `RiskRule`, `ModelProvider`, `RiskSignal`, `RemediationPlan`, `AgentAction`
- controller-runtime reconciliation loop
- read-only investigation workflow contracts
- datasource adapter interfaces
- model provider interfaces
- policy and guardrail engine
- executor routing and audit model

## Dependency Principles

- Kubernetes is the control-plane substrate, not a reason to leak Kubernetes API types through the whole core.
- Prometheus, Loki, OpenTelemetry, CloudWatch, and future backends are adapters, not hard product dependencies.
- model vendors are replaceable providers, not the system boundary.
- hosted model providers require explicit workload-scoped credentials.
- heuristic mode must remain useful for users that do not want external model calls.
- remediation is an optional guarded path, not the default truth of the project.
- install manifests should not pull in a full observability stack by default.
- install manifests should not run autonomous CLI agents or package developer-local auth caches.
- investigation entrypoints should collapse into Kubernetes workflow resources rather than bypass the control plane.

## What Stays Neutral

- Prometheus, Loki, CloudWatch, and OpenTelemetry are adapters, not hard dependencies.
- OpenAI API, Claude API, and Gemini API are provider choices, not platform assumptions.
- GitOps and notifications are preferred integration points for higher-risk actions.
- fake observability endpoints in the demo are for validation convenience, not a claim that FluxAgent owns the user's monitoring stack.
- CLI and future UI surfaces should wrap `InvestigationRequest` rather than define a separate execution truth.
- `RiskRule` remains an optional bootstrap signal source rather than the center of product identity.
- raw secrets, authorization headers, unredacted evidence, provider prompts, and provider raw responses should not be stored as default CRD status.

## Product Direction

The intended near-term distinction is:

```text
general agent platform: not the first goal
evidence-verifiable RCA control plane: current product goal
```

That means the current early investigation layer is:

- ad-hoc investigation requests
- reusable investigation orchestration
- auditable RCA results
- evidence-linked claims and verification status

It does not mean:

- direct model-to-tool execution
- large chat-first UX work
- remediation-first expansion
- competing on the largest rule catalog
