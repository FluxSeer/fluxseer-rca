# RCA Operations Replay Baseline

- Corpus: `operations-twelve-v3`
- Source commit: `831103f7790773240f1adde04db0488ed90897cf`
- Result: `PASS` (12/12)
- Entrypoint: `make verify-rca-quality-baseline`
- Hosted credentials or paid provider calls: none

This batch adds Service port mismatch and complete Prometheus and Loki outage
cases to the previous nine-case boundary corpus.

| Scenario | Terminal contract | Claims / failure contract | Queries | Provider requests |
| --- | --- | --- | ---: | ---: |
| Service port mismatch | `Completed/Confirmed`, `WorkloadDegradation` | 2 Supported | 1 | 1 |
| Prometheus outage | `Failed/Unknown` | `DatasourceQueryFailed`; no claims | 1 | 0 |
| Loki outage | `Failed/Unknown` | `DatasourceQueryFailed`; no claims | 1 | 0 |

Across all twelve cases, root-cause type/entity accuracy, evidence
precision/recall, failure-contract accuracy, and terminal checkpoint reuse are
all `1.00`; unsafe `NoIssueFound` remains `0`. The corpus issued 15 datasource
queries and 8 fixture-provider requests. Verification distribution is 13
Supported, 1 Unsupported, and 2 Contradicted claims.

The Service port case explicitly exercises the new internal provider-usage
flow and records 286 input and 74 output fixture tokens in
`status.execution`. This is a deterministic plumbing assertion, not a runtime
cost measurement. Runtime latency and provider-reported token evidence are
recorded separately in the runtime efficiency artifact.

The full generated report remains under the ignored path
`reports/evaluation/rca-quality-baseline.json`.
