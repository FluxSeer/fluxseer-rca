# Runtime P0 Cluster Matrix Evidence

- Run ID: `20260811T034237Z`
- Source commit: `4c64b5e86521d13a2f52e6d478d71bf35c38651f`
- Source dirty: `false`
- Kubernetes context: `synthetic-validation-context` (private context redacted)
- Namespace: `fluxseer-rca-test`
- Controller image: `registry.example.com/fluxseer/fluxseer-rca/operator:runtime-p0-4c64b5e` (private registry redacted)
- Image digest: `sha256:5a8d69b2c7896b56aba8678fea339bcbf4de2ad1264cf2f0f73fd7f6584cb68f`
- Result: `PASS` (`15/15`)

| Scenario | Phase | Outcome | Failure/terminal reason | Conditions | Provider attempts | RiskSignal |
| --- | --- | --- | --- | ---: | ---: | --- |
| `ProviderDataPolicyDenied` | Failed | Unknown | `ProviderDataPolicyDenied` / `ExternalTransmissionDisabled` | current generation | 0 | none |
| `ProviderDataPolicyRejected` | Failed | Unknown | `ProviderDataPolicyRejected` / `ClassificationExceeded` | current generation | 0 | none |
| `QueryPolicyRejected` | Failed | Unknown | `QueryPolicyRejected` | 10 | 0 | none |
| `QueryBudgetExceeded` | Failed | Unknown | `QueryBudgetExceeded` | 9 | 0 | none |
| `ProviderNotFound` | Failed | Unknown | `ProviderNotFound` | 10 | 0 | none |
| `InvalidProviderResponse` | Failed | Unknown | `InvalidProviderResponse` | 9 | 1 expected | none |
| `NoSupportedRootCauseClaims` / `RCAUnverified` | Completed | Inconclusive | `Verified=False/NoSupportedRootCauseClaims` | 10 | 1 heuristic | one bounded, `RCAReady=False/RCAUnverified` |
| `NoIssueFound` | Completed | NoIssueFound | `RCAReady=True/NoIssueFound` | 10 | 0 | none |
| `RequiredEvidenceMissing` | Completed | Inconclusive | `EvidenceCollectionReady=False/RequiredEvidenceMissing` | 10 | 0 | none |
| `CrashLoopEvidenceCoverageMissing` | Completed | Inconclusive | `RequiredEvidenceMissing` with incomplete CrashLoop check | 10 | 0 | none |
| `UnsupportedRetentionMode` | Failed | Unknown | `UnsupportedRetentionMode` | 10 | 0 | none |
| `EvidenceRetentionStoreUnavailable` | Failed | Unknown | `EvidenceRetentionStoreUnavailable` | 9 | 0 | none |
| `RiskSignalSourceBlocked` | Failed | Unknown | `RiskSignalSourceBlocked` | 10 | 0 | none |
| `InvestigationDepthLimitExceeded` | Failed | Unknown | `InvestigationDepthLimitExceeded` | 10 | 0 | none |
| TTL cleanup | object deleted | n/a | terminal request deleted after TTL | n/a | 0 | none |

Every retained request had `status.observedGeneration` equal to
`metadata.generation`, and every condition used that same generation. Provider
access logging was proven operational with one `/control` request in each mock.
The denied and rejected policy endpoints had zero access-log matches. The
malformed-response endpoint had exactly one expected request.

No new `RemediationPlan` or `AgentAction` UID appeared during the matrix. Test
resources were deleted, both Argo CD applications had `skip-reconcile` removed,
and the controller Deployment was restored to its original image after the run.

Raw YAML, JSON, pod snapshots, and access logs remain in the ignored local
artifact directory:
`reports/runtime/internal/cluster/p0-matrix/fluxseer-rca-runtime-p0-matrix-20260811T034237Z/`.
