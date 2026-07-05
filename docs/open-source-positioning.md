# Open Source Positioning

FluxAgent is positioned as a Kubernetes-native AI SRE control plane with read-only investigation workflows, not as a bot tied to one monitoring stack and not as a general-purpose agent shell first.

## What Is Open Source Here

- CRD contracts: `DataSource`, `RiskRule`, `ModelProvider`, `RiskSignal`, `RemediationPlan`, `AgentAction`
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
- remediation is an optional guarded path, not the default truth of the project.
- install manifests should not pull in a full observability stack by default.
- investigation entrypoints should collapse into Kubernetes workflow resources rather than bypass the control plane.

## What Stays Neutral

- Prometheus, Loki, CloudWatch, and OpenTelemetry are adapters, not hard dependencies.
- OpenAI, Claude, Gemini, Bedrock, and local models are provider choices, not platform assumptions.
- GitOps and notifications are preferred integration points for higher-risk actions.
- fake observability endpoints in the demo are for validation convenience, not a claim that FluxAgent owns the user's monitoring stack.
- future CLI or UI surfaces should wrap CRDs such as `RiskRule` and planned `InvestigationRequest` rather than define a separate execution truth.

## Product Direction

The intended near-term distinction is:

```text
general agent platform: not the first goal
operator-first investigation control plane: the next goal
```

That means the next missing layer is:

- ad-hoc investigation requests
- reusable investigation orchestration
- auditable RCA results

It does not mean:

- direct model-to-tool execution
- large chat-first UX work
- remediation-first expansion
