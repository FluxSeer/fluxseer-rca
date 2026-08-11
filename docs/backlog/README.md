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
  `reports/runtime/fluxseer-rca-runtime-access-log-20260811T031721Z/`.

The remaining cluster validation work is to rerun the complete current P0
matrix and record terminal condition sets plus `observedGeneration` for every
scenario. The provider-policy access-log portion is complete.

Every validation ticket must record both expected state and forbidden side
effects. At minimum, reports should cover provider requests, datasource
queries, evidence, projected `RiskSignal` objects, remediation objects, and
execution identity where applicable.

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
