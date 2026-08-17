# Open Source Positioning

FluxSeer RCA is the project's product name, and source code, Helm charts, and
build artifacts now use matching `fluxseer` / `fluxseer-rca` naming (renamed
from the earlier `fluxagent` identity; see
[architecture/rename-migration-plan.md](architecture/rename-migration-plan.md)).
It is positioned as a Kubernetes-native control plane for evidence-verifiable
RCA, not as a bot tied to one monitoring stack and not as a general-purpose
agent shell first.

Its open-source value is:

```text
turn every Kubernetes incident investigation into verifiable, auditable,
replayable, and reusable organizational knowledge instead of letting it
disappear into Slack threads, dashboard screenshots, terminal history,
or individual responder memory
```

FluxSeer RCA favors explicit Kubernetes-native configuration over automatic discovery. It is for teams that want to define investigation scope, datasource boundaries, and model-provider choices themselves, accepting some CRD learning cost in exchange for customizability, low default resource usage, and auditability.

Its primary audience is platform teams and security/compliance-governance stakeholders who need to justify AI-assisted RCA to an internal or external auditor — not individual on-call responders looking for the fastest incident chat tool. The CRD learning cost and explicit-configuration model are deliberate tradeoffs for that audience: they buy auditability and control at the cost of first-run speed. Teams without an audit or governance requirement may find a lighter-weight, chat-first tool a better fit for the incident-response moment itself.

Security is part of the product positioning. FluxSeer RCA is read-only by default, secret-minimizing by design, and usable in heuristic-only mode without sending evidence to an external model provider.

## Public Support Boundary

Public documentation uses these labels consistently:

| Label | Meaning |
| --- | --- |
| Supported | Tested public runtime behavior in the default read-only RCA contract. |
| Experimental | Implemented but explicitly gated behavior; not a production-readiness claim. |
| Planned | Documented future direction without a supported implementation. |
| Reserved | Compatibility schema or extension point that is not applied at runtime. |

The supported path covers Kubernetes Events, workload status, Prometheus, Loki,
heuristic RCA, and the governed investigation/audit contracts. Hosted model
providers are opt-in beta integrations. The only official mutation backend in
the current development slice is the experimental, allowlisted Kubernetes
Deployment `kubernetes.rolloutRestart` path.

Real mutation requires both remediation and experimental-executor enablement;
the default Helm installation remains read-only. Its effectiveness result is
bounded to the configured observation window and is classified as `Effective`,
`Ineffective`, `Regressed`, or `Inconclusive`; it is not a permanent claim that
the incident root cause has been eliminated.

GitOps production execution is planned. Runbook execution, generic Kubernetes
mutation, shell/SSH actions, and autonomous agent behavior are not supported.
The public `Executor` interface is extensible, but custom implementations are
not official FluxSeer support commitments.

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
- model-provider output never directly invokes a Kubernetes API; it must pass
  through plan creation, policy/approval, executor validation, and audit.
- install manifests should not pull in a full observability stack by default.
- install manifests should not run autonomous CLI agents or package developer-local auth caches.
- investigation entrypoints should collapse into Kubernetes workflow resources rather than bypass the control plane.

## What Stays Neutral

- Prometheus, Loki, CloudWatch, and OpenTelemetry are adapters, not hard dependencies.
- OpenAI API, Claude API, and Gemini API are provider choices, not platform assumptions.
- GitOps and notifications are preferred integration points for higher-risk actions.
- fake observability endpoints in the demo are for validation convenience, not a claim that FluxSeer RCA owns the user's monitoring stack.
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
- policy-governed provider egress
- replay artifact and offline comparison foundations

It does not mean:

- direct model-to-tool execution
- large chat-first UX work
- remediation-first expansion
- competing on the largest rule catalog

## What Should Become Valuable

FluxSeer RCA should create open-source value around assets that platform teams can inspect, adapt, and test:

- stable RCA status contracts
- rule packs with explicit scope and thresholds
- replay fixtures
- provider comparison fixtures
- verifier rules
- policy examples for query, egress, classification, and retention
- Kubernetes-native integration examples

The long-term network effect should come from high-quality, reusable Kubernetes incident knowledge and rule packs, not from hiding diagnosis logic behind a hosted service.
