# RCA Boundary Replay Baseline

- Corpus: `boundary-nine-v2`
- Source commit: `4ab75da67c9f1c0708b15e8f5bbbf8d59a86e9b1`
- Result: `PASS` (9/9)
- Entrypoint: `make verify-rca-quality-baseline`
- Hosted credentials or paid provider calls: none

The second corpus batch adds four boundary scenarios:

| Scenario | Terminal contract | Claim contract | Queries | Provider requests |
| --- | --- | --- | ---: | ---: |
| Readiness mismatch | `Completed/Confirmed`, `WorkloadDegradation` | 2 Supported | 1 | 1 |
| Dependency unavailable | `Completed/Confirmed`, `WorkloadDegradation` | 2 Supported | 1 | 1 |
| Partial datasource failure | `Failed/Unknown`, `DatasourceQueryFailed` | no claims or retained partial evidence | 2 | 0 |
| Contradictory evidence | `Completed/Inconclusive`, empty root-cause type | 2 Contradicted | 1 | 1 |

Across all nine cases, root-cause type/entity accuracy, evidence
precision/recall, failure-contract accuracy, and terminal checkpoint reuse are
all `1.00`; unsafe `NoIssueFound` remains `0`. The corpus issued 12 datasource
queries and 7 fixture-provider requests. Verification distribution is 11
Supported, 1 Unsupported, and 2 Contradicted claims.

The readiness fixture exposed and fixed a verifier defect where the token
`ready` inside the negative phrase `not ready` was interpreted as healthy
contradictory evidence. Negative health phrases now support matching failure
claims, while genuinely healthy markers such as `normal and below threshold`
continue to contradict an incident claim.

The partial datasource case documents the current conservative behavior: if
one required query fails, collection terminates as `DatasourceQueryFailed`,
does not retain the successful fragment, and makes zero provider requests.

Token use and meaningful wall-clock latency remain unavailable from fixture
providers. The full generated report remains under the ignored path
`reports/evaluation/rca-quality-baseline.json`.
