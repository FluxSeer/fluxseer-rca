# Open Source Positioning

FluxAgent is positioned as a Kubernetes-native AI SRE Agent Operator, not as a bot tied to one monitoring stack.

## What Is Open Source Here

- CRD contracts: `RiskSignal`, `RemediationPlan`, `AgentAction`
- controller-runtime reconciliation loop
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

## What Stays Neutral

- Prometheus, Loki, CloudWatch, and OpenTelemetry are adapters, not hard dependencies.
- OpenAI, Claude, Gemini, Bedrock, and local models are provider choices, not platform assumptions.
- GitOps and notifications are preferred integration points for higher-risk actions.
- fake observability endpoints in the demo are for validation convenience, not a claim that FluxAgent owns the user's monitoring stack.
