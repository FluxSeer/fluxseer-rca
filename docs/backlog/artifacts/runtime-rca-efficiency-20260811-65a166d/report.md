# Runtime RCA Efficiency Evidence

- Run ID: `efficiency-20260811-65a166d`
- Result: `PASS` (2/2)
- Source commit: `65a166dccb57a999f06d20e8a2bffadb51458d2b`
- Source dirty: `false`
- Kubernetes context: `admin@homelab-test`
- Namespace: `fluxseer-rca-test`
- Controller image: `test-harbor.fluxseer.com/fluxseer/fluxseer-rca/operator:runtime-efficiency-65a166d`
- Image digest: `sha256:47db4ecc0c40c1643f281372fb0e2e241f43f49df1d34cf95c45e5401c03df89`

| Scenario | Terminal contract | Provider requests | Runtime metrics | Forbidden side effects |
| --- | --- | ---: | --- | --- |
| OOMKilled event-only | `Completed/Inconclusive`; required memory metric absent | 0 | execution absent because the evidence gate stops before provider reasoning | no `RiskSignal`, `RemediationPlan`, or `AgentAction` |
| ImagePullBackOff structured evidence | `Completed/Confirmed`; `ImagePullFailure`; supported claim | 1 | duration 3 seconds; 321 input / 87 output tokens | no `RemediationPlan` or `AgentAction` |

The OpenAI-compatible cluster mock returned standard `usage.prompt_tokens` and
`usage.completion_tokens` fields. The deployed adapter parsed them, the
reasoning layer retained them, and the controller persisted them as
`status.execution.inputTokens=321` and `outputTokens=87`. A throttled mock
response made the successful provider path span three wall-clock seconds,
recorded in both the execution attempt timestamps and `durationSeconds=3`.

All status conditions used observed generation 1. The provider access log
contained exactly one request for the ImagePull case and none for the
evidence-gated OOM case. The test controller was restored to
`ci-20260808081237-5a432ee`, both Argo applications returned to
`Synced/Healthy`, all temporary resources were removed, and no unexpected
side-effect object was created.

Full status JSON/YAML, provider access log, controller snapshots, and side
effect UID diff remain under the ignored path
`reports/runtime/fluxseer-rca-runtime-canonical-workloads-efficiency-20260811-65a166d/`.
