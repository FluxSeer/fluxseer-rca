# Phase 3B-1 Trust Boundary Audit: `relatedTargets`

## Status

- Phase: 3B-1A / 3B-1B / 3B-1C
- Decision: `PASS_WITH_RISKS / RUNTIME_INTEGRATION_PENDING`
- Baseline: FluxSeer RCA `v0.5.0-alpha.3`
- Baseline commit: `6ac8e92c333278de65f79e401db2fefc2c124a60`
- Scope: audit and reproducible baseline red-state only

This document records the trust-boundary audit before introducing an
authorization validator. It intentionally does not change the public CRD,
canonical RCA contract, remediation policy, or Shadow-to-Canonical cutover.

## Trust path under audit

```text
Datasource record labels
  -> relatedTargetsFromRecord()
  -> normalized EvidenceRef.RelatedTargets
  -> EvidenceCollectionResult
       ├─ causalDependencyLogEvidencePresent()
       │    -> causal evidence coverage / canonical decision gates
       └─ causalRootCauseEntity()
            -> supported claim -> RootCauseEntity / RiskSignal projection

EvidenceCollectionResult
  -> diagnosticEvidenceInventory()
  -> Candidate Reconciliation / Shadow Verification
```

### Observed baseline behavior

1. `internal/investigation/service.go:relatedTargetsFromRecord` derives a
   related target from datasource labels. The namespace can be supplied by the
   record, while the cluster is copied from the query target. No trusted
   Kubernetes object snapshot or UID lookup is performed.
2. `internal/controllers/investigationrequest_controller.go:causalDependencyLogEvidencePresent`
   treats a log EvidenceRef with any non-empty `RelatedTargets` as satisfying
   the causal dependency evidence gate when the text contains a dependency
   failure phrase. It does not validate namespace, cluster, UID, or an
   authorization decision.
3. `causalRootCauseEntity` promotes a single related target from a supported
   log claim when only `Kind` and `Name` are present. It does not require
   target authorization or identity verification. `TargetRef` currently has
   no UID field, so a missing UID cannot be treated as verified identity.
4. `internal/investigation/reconciliation.go:diagnosticEvidenceInventory`
   marks generic EvidenceRefs as `Authorized=true` and `Valid=true` when they
   are present in the collected result. Evidence authorization is therefore
   not evidence that every `RelatedTargets` entry is authorized for the
   investigation scope.

## Trust contracts to preserve

The Phase 3B-1 fix must keep these decisions separate:

| Decision | Meaning | Trusted input required |
| --- | --- | --- |
| `EvidenceAuthorized` | The investigation may use the evidence reference | Collection policy / evidence inventory |
| `TargetAuthorized` | The evidence may be linked to this target | Verified investigation scope and cluster/namespace policy |
| `TargetIdentityVerified` | The referenced object is the intended object | Trusted Kubernetes resource snapshot and UID |

`EvidenceAuthorized=true` must not imply either target decision. An absent
historical UID is `Unresolved`, not an implicit match. A rejected related
target must not increase coverage, claim verification strength, RootCauseEntity
confidence, RiskSignal provenance, or remediation eligibility. Independent
authorized evidence may still support a valid canonical conclusion.

## Phase 3B-1B red-state replay matrix

The replay fixture is
[`related-target-authorization-red-state-v1.json`](../../internal/controllers/testdata/related-target-authorization/red-state-v1.json).
The baseline probe deliberately records current acceptance so this first
commit remains executable; the security gate in 3B-1C must invert the
unauthorized expectations to `Rejected` or `Unresolved`.

| Case | Baseline probe | Required post-fix disposition |
| --- | --- | --- |
| Valid target in investigation scope | Accepted | Authorized |
| Cross-namespace target | Accepted (**red**) | `Rejected / CrossScopeTarget` |
| Cross-cluster target | Accepted (**red**) | `Rejected / UnauthorizedCluster` |
| Target without a verifiable UID | Accepted (**red**) | `Unresolved / TargetIdentityUnavailable` |
| Evidence ID absent from inventory | Rejected by existing evidence lookup | `Rejected / EvidenceUnauthorized` |

The probe checks the two currently exposed canonical paths: causal evidence
coverage and `RootCauseEntity` derivation. It does not claim that a full
runtime remediation path has been exercised; that belongs to 3B-1D/3B-1E.

## Explicit exclusions

- No new Diagnostic or Fault Domain Registry.
- No public CRD or `TargetRef` schema expansion in 3B-1A/1B.
- No change to canonical RCA verdicts or RiskSignal behavior yet.
- No remediation or workload mutation.
- No Kubernetes live qualification in this commit.

## Next gate

3B-1C must add a pure, deterministic, fail-closed authorization validator and
convert the red-state cases into security regression assertions before runtime
integration or Kubernetes qualification begins. The pure validator now lives
under `internal/authorization`; its result is not consumed by the existing
runtime yet.

## Pure validator contract (3B-1C)

`authorization.ValidateRelatedTargets` accepts only:

- an explicit evidence authorization decision from the collection layer;
- an explicit investigation cluster and allowed namespace set;
- related targets paired with a trusted evidence-observed UID; and
- trusted resource snapshots supplied by the runtime adapter.

The function performs no Kubernetes/API calls and returns one deterministic
decision per related target. It checks evidence authorization before scope,
scope before identity, and identity before `Authorized`:

| Result | Meaning |
| --- | --- |
| `Authorized / TargetAuthorized` | Evidence, scope, and UID identity all agree |
| `Rejected / EvidenceUnauthorized` | Evidence itself is not permitted |
| `Rejected / CrossScopeTarget` or `UnauthorizedCluster` | Target is outside the investigation boundary |
| `Rejected / TargetUIDMismatch` | Trusted snapshot and evidence UID conflict |
| `Unresolved / TargetIdentityUnavailable` | UID or trusted snapshot is missing |

The validator's unit matrix covers valid, cross-namespace, cross-cluster,
missing UID, UID mismatch, stale/deleted target, missing scope, unauthorized
evidence, and input-order determinism. Runtime adapters still need to construct
the trusted inputs and enforce the decisions in both Canonical and Shadow
paths.
