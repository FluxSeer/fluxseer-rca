# Generated Test Reports

`reports/` contains local, generated validation output. The generated files are
ignored by Git; durable summaries that need review history belong under
`docs/backlog/artifacts/`.

This directory is not a product-side archive of user incidents. A user obtains
the current public-API report with `fluxseer report riskrule <name> -n
<namespace> -o json`; see [RiskRule anomaly
reports](../docs/riskrule-reports.md). Runtime suites may save that exact output
under `incidents/`, then wrap it with test-only expected/actual comparisons.

New reports use the shared [`fluxseer-test-report/v1`](../test/reporting/README.md)
contract:

```text
reports/
├── evaluation/
│   ├── <suite>.json        machine-readable source of truth
│   └── <suite>.md          rendered per-scenario comparison
└── runtime/
    └── <suite>-<run-id>/
        ├── summary.json    same machine-readable contract
        ├── scenario-comparison.md
        └── <raw case artifacts>
```

Each scenario records:

- stable ID and PASS/FAIL result;
- expected output;
- actual output;
- named assertions;
- exact differences by field path;
- relevant raw artifacts;
- positive and forbidden side effects where applicable.

Run `make verify-report-contract` to validate the canonical template. Individual
test entrypoints validate their generated JSON before returning success.

Current adopted entrypoints:

- `make verify-rca-quality-baseline`
- `make verify-runtime-canonical-workloads-cluster`
- `make verify-runtime-matrix-cluster`
- `make verify-runtime-riskrule-incidents-cluster`

Directories created before `fluxseer-test-report/v1` are historical legacy
evidence and may not have per-scenario expected/actual comparisons. Do not use
their shapes as templates for new runners.
