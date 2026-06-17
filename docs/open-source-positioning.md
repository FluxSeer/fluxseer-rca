# Open Source Positioning

FluxAgent is positioned as a Kubernetes-native AI SRE Agent Operator, not as a bot tied to one monitoring stack.

## What Is Open Source Here

- CRD contracts: `RiskSignal`, `RemediationPlan`, `AgentAction`
- controller-runtime reconciliation loop
- datasource adapter interfaces
- model provider interfaces
- policy and guardrail engine
- executor routing and audit model

## What Stays Neutral

- Prometheus, Loki, CloudWatch, and OpenTelemetry are adapters, not hard dependencies.
- OpenAI, Claude, Gemini, Bedrock, and local models are provider choices, not platform assumptions.
- GitOps and notifications are preferred integration points for higher-risk actions.
