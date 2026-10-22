# ADR 0006: Preserve RiskSignal evidence related targets

- Status: Accepted for Phase 3A-4A
- Date: 2026-10-21
- Scope: `RiskSignal.spec.evidence[].relatedTargets`

## Context

`EvidenceRef` includes `relatedTargets` so an evidence record can retain the
explicit Kubernetes resources named by its provenance. The field was present
in the Go API type and in `InvestigationRequest.status.evidenceRefs`, but the
`RiskSignal.spec.evidence` schema did not declare it.

On Kubernetes v1.34.1, a server-side dry-run rejected a `RiskSignal` containing
the field. A regular write accepted the object but pruned `relatedTargets`
from the stored resource. This weakened provenance on the compatibility
projection and produced controller warnings during confirmed investigations.

## Decision

Declare `relatedTargets` as an optional array of `TargetRef` objects in the
`RiskSignal` v1alpha1 schema. Keep the field bounded and structural with the
same properties as the Go type and the InvestigationRequest evidence schema.

Update both CRD sources consumed by this repository:

- `config/crd/bases/aiops.platform_risksignals.yaml`
- `charts/fluxseer-rca/crds/aiops.platform_risksignals.yaml`

Do not use `x-kubernetes-preserve-unknown-fields`; the field is now explicitly
validated. The change is additive and optional, so existing RiskSignal
objects remain compatible.

Because Helm does not automatically upgrade CRDs stored in its `crds/`
directory, deployment qualification must apply and verify the CRD explicitly
before testing the packaged controller.

## Verification

The authorized `fluxseer-k8s134` cluster (Kubernetes v1.34.1) was checked
before and after the schema update. Before the update, server-side dry-run
failed and a normal write dropped `relatedTargets`. After the update,
server-side dry-run succeeded and an isolated object round-tripped with the
field preserved. The temporary namespace and object were deleted.

Artifacts are retained under:

`reports/runtime/v0.5-alpha1-proxmox-k8s134/phase3a4-schema-20261021-r1/`

Packaged controller image parity remains a separate Phase 3A-4B gate.

## Consequences

- RiskSignal consumers can rely on explicit evidence target provenance.
- Existing public schemas remain backward compatible through an optional field.
- CRD installation and upgrade checks must include the RiskSignal evidence
  schema to prevent future source/chart drift.
