# RCA Quality Replay Baseline

- Corpus: `canonical-five-v1`
- Source commit: `959fec2d1a531e6a5cbf777bd7ef1a867b764244`
- Result: `PASS` (5/5)
- Entrypoint: `make verify-rca-quality-baseline`
- Hosted credentials or paid provider calls: none

| Scenario | Outcome / root cause | Evidence P/R | Claims | Queries | Provider requests | Checkpoint reuse |
| --- | --- | ---: | --- | ---: | ---: | --- |
| CrashLoopBackOff | `Confirmed / CrashLoop` | 1.00 / 1.00 | 2 Supported | 1 | 1 | yes |
| NormalLatencyNoIssue | `NoIssueFound` | 1.00 / 1.00 | none | 1 | 0 | yes |
| DeploymentRolloutLatencyRegression | `Confirmed / LatencyRegression` | 1.00 / 1.00 | 2 Supported | 2 | 1 | yes |
| ImagePullBackOff | `Confirmed / ImagePullFailure` | 1.00 / 1.00 | 1 Supported, 1 Unsupported | 1 | 1 | yes |
| OOMKilled | `Confirmed / ResourcePressure` | 1.00 / 1.00 | 2 Supported | 2 | 1 | yes |

Aggregate baseline:

- root-cause type accuracy: `1.00`
- root-cause entity accuracy: `1.00`
- evidence precision: `1.00`
- evidence recall: `1.00`
- unsupported claim rate: `0.125` (1 of 8 claims)
- unsafe `NoIssueFound` rate: `0.00`
- terminal checkpoint reuse rate: `1.00`
- datasource queries: `7`
- provider requests: `4`

The unsupported claim is the ImagePull fixture's broad provider summary, which
does not cite retained evidence. The verifier preserves it as `Unsupported`
while the evidence-linked `ErrImagePull` cause is `Supported`; this is recorded
as an optimization target instead of being treated as verified truth.

Fixture providers do not expose token usage, and the deterministic clock does
not provide meaningful wall-clock latency. Those fields remain explicitly
unavailable in this offline baseline and require runtime/provider telemetry in
a later batch. The full generated machine report remains under the ignored
path `reports/evaluation/rca-quality-baseline.json`.
