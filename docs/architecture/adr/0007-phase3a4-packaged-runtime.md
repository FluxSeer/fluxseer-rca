# ADR 0007: Phase 3A packaged runtime qualification

- Status: Accepted for Phase 3A Release Candidate
- Date: 2026-10-21
- Scope: OCI image, Helm chart, CRD schema, and Kubernetes live qualification
- Related: [ADR 0005](0005-shadow-verifier-input.md), [ADR 0006](0006-risksignal-evidence-related-targets.md)

## Context

Phase 3A had passed source and local runtime qualification, but the final
artifact boundary still needed verification. A successful binary build does not
prove that the published controller image, Helm chart, installed CRD, and
Kubernetes API preserve the same behavior.

The qualification used source commit `fa7542a4011d35dd205d545767a3a80cace12f72`
and the candidate artifact version `v0.5.0-alpha.1-rc.fa7542a`.

## Decision

Treat the OCI image, Helm chart, CRD schema, and live Kubernetes qualification
as one release candidate gate. The gate does not perform canonical cutover and
does not claim a formal release.

The candidate artifacts are:

- Operator image:
  `ghcr.io/fluxseer/fluxseer-rca/operator:v0.5.0-alpha.1-rc.fa7542a`
- Operator digest:
  `sha256:c3357e76026e5697751cc1200db76c63402dc0ffe5063673f6380399ff9d12af`
- Helm chart version: `0.5.0-alpha.1-rc.fa7542a`
- Helm chart digest:
  `sha256:5e152c940bd46a9e944e9dc6749cc26cd9cb79d37be97b5478049dc9a86bc57f`

The existing test release was upgraded to the candidate, qualified, and then
rolled back. The additive `RiskSignal.spec.evidence[].relatedTargets` schema
was retained because Helm does not roll back CRDs stored in `crds/`.

## Verification

On Kubernetes v1.34.1 (`admin@fluxseer-k8s134`):

- ServicePortMismatch remained `Confirmed`.
- FailedScheduling remained `Inconclusive`; only the scheduling mechanism was
  `Supported`, while concrete root causes remained unverified.
- ImagePull isolation produced `ImagePullFailure` without a Scheduling claim.
- CrashLoopBackOffV2 missing termination evidence remained `Inconclusive`.
- The candidate Pod imageID matched the published operator digest.
- Four `relatedTargets` references survived RiskSignal API round-trip.
- No related-target schema warning, remediation resource, or workload mutation
  was observed.
- The old controller image with the new CRD completed a read-only
  InvestigationRequest and preserved a RiskSignal evidence `relatedTargets`
  field. This is a mixed-version compatibility smoke test, not a full upgrade
  matrix.

The test release was restored to Helm revision 29 with the previous controller
image. Temporary namespaces and remediation resources were removed. Evidence
is retained under:

`reports/runtime/v0.5-alpha1-proxmox-k8s134/phase3a4-schema-20261021-r1/`

and

`reports/runtime/v0.5-alpha1-proxmox-k8s134/phase3a4-packaged-20261021-r1/`.

Source and packaging CI passed in
[run 39556197872](https://github.com/FluxSeer/fluxseer-rca/actions/runs/39556197872).
Candidate artifact publication passed in
[run 39556530626](https://github.com/FluxSeer/fluxseer-rca/actions/runs/39556530626).

## Residual risks

- Remote PR review, merge, tag, and formal release remain pending.
- Qualification covers Kubernetes v1.34.1; other distributions and versions
  are not established by this gate.
- The deployment emitted PodSecurity restricted warnings. SecurityContext
  hardening is tracked separately and is not silently included in this phase.
