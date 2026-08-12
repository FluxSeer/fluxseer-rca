# Backlog Execution Ledger

This page is the status index for files in this directory. Individual backlog
documents preserve design context and historical acceptance criteria; this
ledger identifies which work is still actionable.

Status meanings:

| Status | Meaning |
| --- | --- |
| `Active` | Work is approved and currently actionable. |
| `Validation` | Implementation exists, but the required runtime or evaluation evidence is incomplete. |
| `Deferred` | Intentionally outside the current release scope. |
| `Complete` | Acceptance criteria and the relevant verification gate are satisfied. |
| `Historical` | Retained for decisions or release history; not an active work queue. |

## Current Execution Order

| Priority | Workstream | Status | Next completion signal |
| --- | --- | --- | --- |
| P0 | [Runtime error matrix](v0.3-runtime-error-matrix.md) | `Validation` | High-impact failure paths have durable runtime artifacts and negative side-effect assertions. |
| P0 | [Beta stabilization and RCA quality baseline](v0.3-beta-stabilization.md) | `Active` | The first five canonical scenarios have deterministic replay results and recorded quality/cost metrics. |
| P1 | [Production-readiness expansion](v0.3-production-readiness.md) | `Active` | Scenario and target coverage extends beyond the first E2E matrix. |
| P1 | [Architecture hardening follow-ups](v0.3-architecture-hardening.md) | `Active` | Post-action effectiveness verification and the selected durable evidence backend are complete. |
| P2 | Raw snapshot retention | `Deferred` | Security, encryption, retention, access-control, and audit contracts are approved before adapter implementation. |

The first runtime-validation batch is:

1. rerun the cluster matrix with the current operator image;
2. assert zero provider requests for provider-policy denial and rejection;
3. record terminal conditions and `observedGeneration` in reports;
4. validate `DatasourceQueryFailed`, `EvidenceRetentionWriteFailed`,
   `DatasourceNetworkPolicyDenied`, `ProviderRequestFailed`, and secret-reader
   dependency failures;
5. keep internal dependency-injection reasons at unit or envtest tier unless a
   public runtime path exists.

Local and mock-runtime completion checkpoint:

- `ProviderRequestFailed` now persists stable `Failed/Unknown` status and a
  bounded execution attempt instead of returning an unclassified reconcile
  error.
- `SecretReaderUnavailable` and `SecretReadFailed` persist terminal status and
  prove that the hosted provider request count remains zero.
- resource-backed datasource loading rejects a network-policy-denied endpoint,
  leaves it out of the active registry, and continues registering valid
  resources.
- `DatasourceQueryFailed` and `EvidenceRetentionWriteFailed` have direct
  regression coverage.
- `ProviderDataPolicyDenied` and `ProviderDataPolicyRejected` now have cluster
  runtime evidence that records terminal conditions, `observedGeneration`,
  rejected egress audits, and zero mock-provider access-log matches. The
  artifact is
  `reports/runtime/internal/cluster/access-log/fluxseer-rca-runtime-access-log-20260811T031721Z/`.

The complete current P0 cluster matrix was rerun against immutable image
`runtime-p0-4c64b5e` and passed 15/15 scenarios. The durable summary is
[`artifacts/runtime-p0-matrix-20260811T034237Z/report.md`](artifacts/runtime-p0-matrix-20260811T034237Z/report.md);
raw YAML, JSON, pod snapshots, and access logs remain under the ignored
`reports/runtime/internal/cluster/p0-matrix/fluxseer-rca-runtime-p0-matrix-20260811T034237Z/` directory.
The canonical OOMKilled event-only and ImagePullBackOff structured-evidence
cases also passed 2/2. Their durable summary is
[`artifacts/runtime-canonical-workloads-20260811T-final-a77adfb/report.md`](artifacts/runtime-canonical-workloads-20260811T-final-a77adfb/report.md),
with full raw output retained under the ignored `reports/runtime/` path. The
RiskRule user-report anomaly matrix passed 5/5 across Kubernetes events,
deployment conditions, Prometheus, Loki, and the canonical investigation
projection path. Its durable summary is
[`artifacts/runtime-riskrule-incidents-20260811T071150Z/report.md`](artifacts/runtime-riskrule-incidents-20260811T071150Z/report.md),
with exact user-retrievable `fluxseer-riskrule-report/v1` objects retained
under `reports/runtime/user-facing/`; the test envelope remains under
`reports/runtime/internal/`. The
Phase 3c Policy Pack, including the 3c-2 Policy Engine and subsequent
controller integration, is already implemented on this branch and its focused
Policy, Threshold, Escalation, and controller tests pass.

The first deterministic dogfooding baseline now covers five canonical cases
and records diagnosis, evidence, claim verification, query/provider cost, and
checkpoint reuse. Durable summary:
[`artifacts/rca-quality-baseline-20260811-959fec2/report.md`](artifacts/rca-quality-baseline-20260811-959fec2/report.md).
The second corpus batch expands this to nine cases with readiness mismatch,
dependency unavailable, partial datasource failure, and contradictory
evidence. Durable summary:
[`artifacts/rca-quality-baseline-boundary-nine-20260811-4ab75da/report.md`](artifacts/rca-quality-baseline-boundary-nine-20260811-4ab75da/report.md).
The third corpus batch expands this to twelve cases with Service port mismatch
and complete Prometheus/Loki outages. Durable summary:
[`artifacts/rca-quality-baseline-operations-twelve-20260811-831103f/report.md`](artifacts/rca-quality-baseline-operations-twelve-20260811-831103f/report.md).
Provider usage now flows through all three hosted adapters into execution
status. A test-cluster rerun measured 3 seconds and 321 input / 87 output
provider-reported tokens on the successful path while preserving the OOM
evidence gate's zero-call contract. Durable summary:
[`artifacts/runtime-rca-efficiency-20260811-65a166d/report.md`](artifacts/runtime-rca-efficiency-20260811-65a166d/report.md).

Every validation ticket must record both expected state and forbidden side
effects. At minimum, reports should cover provider requests, datasource
queries, evidence, projected `RiskSignal` objects, remediation objects, and
execution identity where applicable.

Generated reports now use the shared `fluxseer-test-report/v1` contract. Every
scenario must expose expected output, actual output, named assertions, exact
field-path differences, and raw artifact links; aggregate-only summaries are
not accepted for new runs. The schema, JSON/Markdown templates, validator, and
adoption instructions are under [`test/reporting/`](../../test/reporting/README.md).
The quality baseline, canonical workload runtime, and P0 cluster matrix
entrypoints validate this contract before reporting success.

The local regression entrypoint for the first failure-path batch is:

```sh
make verify-runtime-failure-paths
```

## Completed Or Historical Records

- [v0.4 guardrails and approval lifecycle](v0.4-guardrails-approval-lifecycle.md): `Complete`; guarded by `make verify-v0.4-approval-lifecycle`.
- [v0.4 workload target coverage](v0.4-workload-target-coverage.md): `Complete` for Kubernetes workload RCA positioning.
- [v0.3 schema freeze audit](v0.3-schema-freeze-audit.md): `Complete` for the frozen v0.3 contract.
- [v0.3 foundation issues](v0.3-foundation-issues.md): `Complete` for the v0.3 foundation scope.
- [v0.3 product direction](v0.3-product-direction.md): implementation checkpoints recorded; remaining future work is tracked by the active workstreams above.
- [v0.3 product rename](v0.3-product-rename.md): `Historical` and superseded by the executed rename migration plan.
- [v0.2 beta backlog](v0.2-beta.md): `Historical`; any still-relevant gap must be refiled against the current runtime matrix or stabilization workstream.
- [v0.2 release reproducibility](v0.2-release-reproducibility.md): `Historical` release record.
- [Archived decisions](archived-decisions.md): `Historical` by definition.
