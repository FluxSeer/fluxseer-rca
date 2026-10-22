# ADR 0004: Shadow-mode diagnostic candidate reconciliation

- Status: Accepted for Phase 3A-2
- Date: 2026-10-21
- Scope: Internal investigation lineage; no public API change
- Related: [ADR 0003](0003-diagnostic-claim-lineage.md)

## Context

The Phase 3A-1 accounting engine accepts normalized `CandidateRecord` values,
but the runtime still produces provider-neutral `diagnostic.Result` values.
The next boundary must preserve candidate identity and evidence provenance
without making the accounting result a second RCA verdict.

Candidate-provided evidence references are not authorization. The collection
layer is the trusted source of the evidence inventory. A candidate reference
can select an inventory entry, but it cannot create one or grant access to
one.

## Decision

Add a pure adapter in `internal/investigation`:

```text
diagnostic.Result[] + trusted EvidenceIdentity[]
  -> CandidateRecord[]
  -> diagnostic.AccountCandidates
  -> internal reconciliation result
```

The adapter runs in Shadow Mode from `Service.CollectEvidence`. Its result is
stored only on the internal `EvidenceCollectionResult` and is not consumed by
`buildRCAClaims`, the existing verifier, public CRD/report fields, RiskSignal,
or remediation logic.

The adapter exposes three internal outcomes:

- `ReconciliationCompleted`: every runtime candidate was accounted for;
- `NoCandidates`: no diagnostic candidate was produced, with coverage unknown;
- `ReconciliationFailed`: mapping preconditions failed, such as missing
  diagnostic identity or unavailable trusted evidence inventory.

The runtime inventory is marked authorized and valid only after collection has
completed through the existing bounded collectors. Scheduling evidence carries
Pod UID provenance. Generic evidence can receive a target UID only when the
validated scope contains exactly one Pod; ambiguous multi-Pod scope remains
unresolved and is rejected by the accounting layer rather than guessed.

The adapter performs no Kubernetes/API calls, metrics mutations, provider
lookups, or remediation. Metrics remain an outer runtime concern for a later
phase.

## Alternatives considered

### Let candidates authorize their own evidence references

Rejected because an untrusted candidate could claim an evidence ID that was
not collected, authorized, or scoped to the target investigation.

### Replace canonical RCA claims with reconciliation output immediately

Deferred. Shadow mode isolates mapping and lineage failures from existing RCA
verdicts and provides a reversible qualification step before verifier-input
integration.

### Treat adapter failure as an empty candidate set

Rejected. An adapter failure is distinct from a valid investigation with no
candidates; collapsing the two would falsely report successful accounting and
hide coverage loss.

## Verification

Replay tests cover ImagePull, Scheduling, multi-diagnostic mapping,
unauthorized evidence, UID and incident-window mismatch, logical-claim
deduplication, duplicate evidence references, adapter precondition failures,
empty candidates, and deterministic input ordering.

Runtime tests confirm that each collected candidate receives one lineage
resolution and that shadow reconciliation does not change canonical RCA
claims or verification results.

Phase 3A-2 does not expose lineage in the public report and does not yet feed
`ProposedClaim` values into the existing verifier. That remains Phase 3A-3.
