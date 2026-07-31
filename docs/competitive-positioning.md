# Competitive Positioning

FluxSeer RCA is the forward-looking product name for the project currently
published as FluxAgent. It should not try to become the broadest AI SRE agent.

Its strongest lane is:

```text
Kubernetes-native control plane for evidence-verifiable RCA.
```

Sharper positioning:

```text
FluxSeer RCA makes Kubernetes RCA governable, verifiable, and replay-oriented.
```

FluxSeer RCA does not merely generate RCA answers. It provides the control, evidence contract, policy boundary, and execution record required to trust and operationalize RCA at scale.

## Strategic Boundary

FluxAgent is not:

- a replacement observability platform
- a replacement Alertmanager
- a Kubernetes analyzer rule catalog
- a free-form autonomous cluster agent
- a production self-healing platform by default

FluxAgent should own:

- RCA workflow state in Kubernetes
- explicit datasource and provider configuration
- bounded evidence collection
- compact evidence provenance
- evidence-linked claims
- degradation status when evidence or providers fail
- auditable RCA results and replay-oriented artifacts

## Positioning Map

| Category | Projects That Tend To Own It | FluxAgent Boundary |
| --- | --- | --- |
| Agentic investigation runtime | HolmesGPT-style systems | Do not compete on tool breadth or unrestricted agent execution. |
| Kubernetes analyzer catalog | K8sGPT-style systems | Do not compete on the largest built-in rule set. |
| Full observability and APM | Coroot-style systems | Do not own telemetry storage, eBPF, tracing, profiling, or dashboards as core dependencies. |
| Automated remediation loop | Kubernaut-style systems | Keep remediation downstream, guarded, and optional. |
| RCA evaluation framework | OpenSRE-style systems | Build deterministic local evaluation gates for FluxAgent's own RCA contract. |

Long-term integrations with external investigation runtimes or evaluation systems can be provider implementations, not product identity.

## Differentiation

FluxAgent should make these questions first-class:

```text
Which evidence supported this RCA?
Which claims are inferred or unsupported?
Which datasources failed?
What evidence was missing?
Was confidence justified?
Can this same investigation be replayed?
Did a later version produce a better RCA?
```

That is the difference between an RCA control plane and a chatbot wrapper.

## Alert Assistant Versus RCA Control Plane

Alert assistants optimize the human response experience after an alert fires:

```text
alert
-> incident context
-> summary
-> operator workflow
```

FluxAgent should optimize governance and reproducibility of the RCA itself:

```text
request
-> bounded evidence
-> policy-governed reasoning
-> verified claims
-> Kubernetes status
-> replay and evaluation
```

This is a difference in system of record and control ownership. FluxAgent should be judged less by how quickly it posts a narrative summary and more by whether another controller, GitOps workflow, security reviewer, or future model-evaluation run can trust the recorded RCA state.

## Practical Wedge

The most credible wedge is:

```text
Evidence-verifiable RCA for Kubernetes alerts and investigations.
```

Concrete promise:

- every important RCA claim traces to evidence
- every hosted AI transmission has policy and audit context
- every investigation can become a replay or evaluation input

The minimum valuable scenario is:

```text
Deployment unavailable / CrashLoopBackOff / OOMKilled
-> Kubernetes Events + Pod state + Prometheus + Loki
-> heuristic or hosted AI RCA
-> verifier checks evidence linkage
-> InvestigationRequest.status records the result
-> replay fixture protects future changes from regression
```

## Deferred Work

The following work should remain deferred until the trustworthy RCA contract is strong:

- more hosted model providers
- more notification destinations
- large baseline rule catalog
- complete web UI
- multi-agent collaboration
- autonomous production remediation
- incident management suite
- self-managed observability stack

The project should prefer one production-like RCA path over many shallow integrations.
