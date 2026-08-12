# FluxSeer RCA Glossary

This glossary defines the product and API terms used across FluxSeer RCA
documentation. Product language should preserve the distinction between
detection, evidence sufficiency, claim verification, and the final RCA result.

## Detection Pattern

A maintained detector included in an official Helm rule pack. FluxSeer RCA
currently includes 21 built-in detection patterns:

- 6 Kubernetes-native patterns enabled by default without an additional
  observability backend;
- 8 Prometheus patterns that require a configured Prometheus `DataSource` and
  explicit enablement;
- 7 Loki patterns that require a configured Loki `DataSource` and explicit
  enablement.

The 21 patterns are maintained defaults, not the product capability ceiling.
Declarative `RiskRule` resources can define additional Kubernetes Event,
Deployment condition, PromQL, and LogQL detectors.

## Signal Template

A parameterized configuration primitive that accepts an application-specific
query and threshold. The Application Profile request-rate, error-rate, p95
latency, and queue-depth entries are signal templates, not four additional
built-in detection patterns.

## Evidence Profile

A named contract that defines the minimum evidence categories needed to assess
a class of investigation. An evidence profile does not assert that the
incident exists or that a particular root cause has been confirmed.

## Evidence Sufficiency

Whether the evidence collected for one `InvestigationRequest` satisfies its
evidence profile. The API exposes this through `status.evidenceCoverage`,
`status.missingEvidence`, and conditions such as
`EvidenceCollectionReady=False/RequiredEvidenceMissing`.

## Verification

The evaluation of whether provider- or heuristic-generated root-cause claims
are supported, inferred, unsupported, contradicted, or unverified by the
collected evidence. Verification is separate from both detection and evidence
collection.

## Verdict And Outcome

Verdict is the product-level term for the bounded RCA conclusion. The API
exposes the machine-readable result in both the terminal workflow field
`InvestigationRequest.status.outcome` and the structured RCA field
`status.verdict.outcome`; they must carry the same result semantics. Current
outcome values include `Confirmed`, `Inconclusive`, `NoIssueFound`, and
`Unknown`.

Condition and failure reasons are not outcomes. For example,
`RequiredEvidenceMissing` describes why evidence was insufficient, while the
corresponding outcome is `Inconclusive`.

## User-facing Report

The product output returned by `fluxseer report riskrule`. It uses
`fluxseer-riskrule-report/v1` and contains the selected `RiskRule`, matching
`InvestigationRequest` objects, and direct or linked `RiskSignal` objects. It
answers what FluxSeer observed and concluded. It does not contain test
expectations, assertions, differences, or PASS/FAIL.

## Internal Validation Report

Maintainer and CI evidence that tests whether FluxSeer behaved according to a
defined runtime contract. It uses `fluxseer-test-report/v1` and contains suite
and run identity, expected and actual values, named assertions, differences,
artifacts, and side-effect checks. It is not a user incident report.

The number of User-facing Report catalog examples is not the number of
Detection Patterns, and an Internal Validation Report result such as P0 15/15
is not the size or PASS status of the user-facing catalog.

## Product Invariants

> Detection success does not imply RCA confirmation.

> The RCA verdict MUST NOT be more specific than the strongest
> evidence-supported causal claim.

For example, an `ErrImagePull` event supports an `ImagePullFailure` finding. By
itself, it does not distinguish an absent image from registry authentication,
DNS, or registry availability failures. Similarly, an `OOMKilled` event can
trigger detection while the `OOMKilled` evidence profile remains incomplete
when memory metrics are unavailable.

## Workflow Vocabulary

```text
RiskRule
-> Detection
-> InvestigationRequest
-> Evidence Collection
-> Evidence Sufficiency
-> RCA Hypothesis
-> Claim Verification
-> Verdict (API outcome)
```
