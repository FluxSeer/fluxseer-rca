# ADR 0003: Candidate claim lineage accounting

- Status: Accepted for Phase 3A-1
- Date: 2026-10-21
- Scope: Internal diagnostic accounting; no public API change
- Related: [ADR 0001](0001-composable-diagnostics.md), [ADR 0002](0002-scheduling-diagnostic.md)

## Context

Composable diagnostics produce candidate claims, while the existing RCA
controller builds canonical claims and verifies them against evidence. Those
are separate decisions. A candidate can be proposed for verification, kept as
an unverified hypothesis, rejected for invalid or unauthorized provenance, or
deduplicated against an equivalent claim.

Phase 3A-1 establishes accounting before changing the canonical RCA claim
pipeline. The goal is to ensure that no candidate disappears silently, gains
verification strength merely because it came from a second source, or crosses
Pod and incident boundaries during deduplication.

## Decision

Add a pure internal accounting boundary in `internal/diagnostic`:

```text
CandidateRecord
  -> input, scope, authorization, and evidence validation
  -> CandidateFingerprint / ClaimFingerprint
  -> deterministic deduplication
  -> CandidateResolution
```

`CandidateDisposition`, claim verification, and the canonical RCA verdict are
separate layers.

The four dispositions are:

- `ProposedClaim`: complete, authorized candidate accepted as verifier input;
- `RetainedHypothesis`: candidate retained because evidence is absent or
  incomplete;
- `Rejected`: candidate or evidence violates identity, authorization, validity,
  or scope requirements;
- `Deduplicated`: equivalent logical claim linked to a deterministic canonical
  candidate.

`CandidateFingerprint` identifies a specific diagnostic candidate and includes
diagnostic identity, candidate identity, normalized claim content, target UID,
incident window, and evidence identity. `ClaimFingerprint` identifies a logical
claim for cross-source deduplication and excludes diagnostic identity and raw
evidence references. Target UID, claim type, normalized statement, and incident
window remain part of the logical identity.

Validation and authorization happen before deduplication. Every input receives
exactly one terminal resolution. Rejected candidates cannot be absorbed into a
valid candidate, and duplicate inputs retain `DuplicateOf` and canonical claim
links.

The accounting function is pure: it returns resolutions and a summary and does
not mutate Prometheus counters, call Kubernetes APIs, or alter public CRD,
report, verifier, or RiskSignal contracts. Runtime metrics are an adapter
responsibility.

## Alternatives considered

### Deduplicate by claim type or statement only

Rejected because equivalent statements from different Pods, incident windows,
or scopes could be incorrectly merged.

### Feed every diagnostic candidate directly into the existing verifier

Deferred. Candidate accounting must first prove identity, authorization,
provenance, and duplicate handling without changing the RC3 canonical claim
path.

### Store disposition in the public CRD

Rejected for Phase 3A-1. The first slice is an internal contract and does not
require a public schema migration.

## Verification

Replay contract tests cover valid, missing, incomplete, invalid, unauthorized,
UID-mismatched, out-of-window, duplicate, conflicting-identity, cross-
diagnostic, cross-Pod, deterministic-order, and empty-input cases.

The accounting summary reports `CoverageKnown=false` for an empty candidate set;
non-empty replay inputs must have disposition coverage of `1.0`.

Phase 3A-1 does not integrate candidates into the canonical RCA verdict. That
remains a later, separately qualified reconciliation and verifier-input slice.
