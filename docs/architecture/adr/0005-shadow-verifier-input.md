# ADR 0005: Shadow verification for reconciled diagnostic candidates

- Status: Accepted for Phase 3A-3
- Date: 2026-10-21
- Scope: Internal candidate verification; canonical RCA remains unchanged
- Related: [ADR 0003](0003-diagnostic-claim-lineage.md), [ADR 0004](0004-diagnostic-reconciliation-adapter.md)

## Context

Phase 3A-2 accounts runtime diagnostic candidates, but accounting alone does
not establish that a candidate is supported by the evidence contract used by
the existing RCA verifier. Phase 3A-3 must compare the two paths without
letting a new candidate source change the RC3 public verdict.

Reconciliation authorization is necessary but not sufficient for verification.
The verifier input adapter must again bind each candidate reference to trusted
evidence, target UID, incident window, and complete evidence state.

## Decision

Add an internal shadow verifier adapter:

```text
CandidateReconciliationResult
  -> ProposedClaim only
  -> trusted EvidenceRef lookup and scope/time validation
  -> existing verifier.VerifyClaims
  -> ShadowClaimVerification with lineage
```

`RetainedHypothesis`, `Rejected`, and `Deduplicated` resolutions are preserved
in the shadow result but are not submitted to the verifier. A deduplicated
candidate points to its canonical candidate and is not verified a second time.

Scheduling mechanism candidates use the canonical verifier vocabulary only for
the internal verifier input; the original diagnostic statement remains in the
lineage record. This permits parity with the existing `SchedulingUnschedulable`
bridge without changing the diagnostic claim text or public report.

The shadow result is attached to the internal `EvidenceCollectionResult` and
is not consumed by `buildRCAClaims`, canonical verdict evaluation, RiskSignal,
or remediation. Missing, stale, conflicting, unauthorized, UID-mismatched, or
out-of-window evidence fails closed and cannot become `Supported`.

## Alternatives considered

### Send all reconciled candidates to the verifier

Rejected. Retained, rejected, and deduplicated candidates must not gain
verification strength merely by reaching a shared verifier.

### Trust CandidateRecord evidence identities without joining EvidenceRefs

Rejected. Candidate accounting proves identity and authorization metadata, but
the existing verifier requires the evidence content. The adapter must join
those identities to the trusted collected references and fail closed on drift.

### Replace canonical RCA claims with shadow results

Deferred. Canonical cutover requires a separate parity and non-interference
decision after this shadow path is qualified.

## Verification

Replay tests cover Scheduling mechanism parity, ImagePull mechanism support,
retained and rejected candidates, deduplicated candidates, UID isolation,
conflicting evidence IDs, and deterministic ordering.

Runtime tests confirm ImagePull and Scheduling candidates receive shadow
verification while existing canonical RCA behavior remains unchanged.

Phase 3A-3 does not perform canonical cutover, add public schema fields, or
introduce new Prometheus metrics.
