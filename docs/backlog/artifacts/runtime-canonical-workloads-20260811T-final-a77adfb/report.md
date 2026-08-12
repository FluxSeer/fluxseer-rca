# Canonical Workload Runtime Evidence

- Run ID: `20260811T-final-a77adfb`
- Result: `PASS` (2/2)
- Source commit: `a77adfbc63a0777056c3b2de62e3e07aa8243fdf`
- Source dirty: `false`
- Kubernetes context: `admin@homelab-test`
- Namespace: `fluxseer-rca-test`
- Controller image: `test-harbor.fluxseer.com/fluxseer/fluxseer-rca/operator:runtime-canonical-a77adfb`
- Image digest: `sha256:dba5ff8f1f8dd95cd55b554da54a3fbd48dd33eafd3b121f8992e6fd247e46b3`

| Scenario | Terminal contract | Provider access | Projected objects | Forbidden side effects |
| --- | --- | ---: | --- | --- |
| OOMKilled event-only | `Completed/Inconclusive`; `event:OOMKilled` complete; `metric:Memory` incomplete; `RCAReady=False/RequiredEvidenceMissing`; all conditions observed generation 1 | 0 | no `RiskSignal` | no `RemediationPlan` or `AgentAction` |
| ImagePullBackOff structured evidence | `Completed/Confirmed`; `ImagePullFailure`; retained `ErrImagePull` evidence digest; supported claim; `Verified=True/RootCauseClaimsSupported`; all conditions observed generation 1 | 1 | one verified `RiskSignal` projection | no `RemediationPlan` or `AgentAction` |

The mock-provider access log contains exactly one `POST /v1/imagepull` and no
request to the OOM provider endpoint. Both status objects retained content and
query digests. The test controller deployment was restored to the original
image, the temporary Argo skip-reconcile annotations were removed, and no
runtime-canonical resources remained after cleanup.

Full raw JSON, YAML, controller snapshots, provider access log, and UID side
effect diff remain in the ignored directory
`reports/runtime/internal/cluster/canonical-workloads/fluxseer-rca-runtime-canonical-workloads-20260811T-final-a77adfb/`.
