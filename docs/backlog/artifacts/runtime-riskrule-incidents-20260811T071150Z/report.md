# RiskRule User-Report Runtime Evidence

- Run ID: `20260811T071150Z`
- Result: `PASS` (5/5)
- Source commit: `c4edc5e245c7a1e06db19004328abe6583102d40`
- Source dirty: `false`
- Kubernetes context: `admin@homelab-test`
- Control namespace: `fluxseer-rca-test`
- Target namespace: `database-test`
- Controller image: `test-harbor.fluxseer.com/fluxseer/fluxseer-rca/operator:ci-20260808081237-5a432ee`
- User-report contract: `fluxseer-riskrule-report/v1`
- Test-report contract: `fluxseer-test-report/v1`

| Scenario | Route | Public output | Terminal state | Required observed reason |
| --- | --- | --- | --- | --- |
| Kubernetes BackOff event | `DirectRiskSignal` | 1 `RiskSignal`; 0 `InvestigationRequest` | `Confirmed` | `EventBackOffObserved` |
| Deployment unavailable condition | `DirectRiskSignal` | 1 `RiskSignal`; 0 `InvestigationRequest` | `Confirmed` | `DeploymentconditionMinimumreplicasunavailableObserved` |
| Prometheus unavailable replicas | `DirectRiskSignal` | 1 `RiskSignal`; 0 `InvestigationRequest` | `Confirmed` | `MetricObserved` |
| Loki error logs | `DirectRiskSignal` | 2 `RiskSignal`; 0 `InvestigationRequest` | both `Confirmed` | `LogObserved` |
| Canonical BackOff investigation | `CreateRequest` with projection | 1 `InvestigationRequest`; 1 linked `RiskSignal` | request `Completed/Inconclusive`; signal `Inconclusive` | public linked projection present |

Every incident artifact was produced by the user-facing command
`fluxseer report riskrule <name> -n fluxseer-rca-test -o json`. The command
exports the complete public `RiskRule`, matching `InvestigationRequest`
objects, and direct or linked `RiskSignal` objects; the runtime runner does not
substitute a test-only incident shape. All five outputs passed the
`fluxseer-riskrule-report/v1` validator.

The machine-readable summary records explicit expected and actual objects,
named assertions, and an empty differences list for every passing scenario.
The rendered Markdown comparison shows the suite-specific JSON fields instead
of blank generic columns.

The test RiskRules were stopped before generated objects were removed to avoid
reconciliation races. No test RiskRule, workload, Event,
`InvestigationRequest`, or `RiskSignal` remained after cleanup, and the five
pre-existing baseline RiskRules remained present.

The complete user-visible JSON reports, test summary, rendered comparison,
datasource snapshot, controller snapshot, rule definitions, targets, and
synthetic Event remain in the ignored local directory
`reports/runtime/internal/fluxseer-rca-riskrule-incidents-20260811T071150Z/` for
the test envelope and
`reports/runtime/user-facing/fluxseer-rca-riskrule-incidents-20260811T071150Z/`
for the user-facing JSON exports.
