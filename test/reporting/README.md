# Test Report Contract

All generated evaluation and runtime reports use `fluxseer-test-report/v1`.
The contract makes suite output comparable without knowing the implementation
of an individual runner.

Every `report.json` or `summary.json` must contain:

- suite and run identity;
- an aggregate result and passed/failed counts;
- one entry per scenario;
- explicit `expected` and `actual` objects for every scenario;
- named assertions with their expected and actual values;
- a `differences` array containing only mismatches;
- artifact paths and side-effect expectations where applicable.

An empty `differences` array means that actual output matched the declared
contract. A failed scenario must contain at least one difference. Values that
cannot be measured are represented as `null`; they must not be reported as a
measured zero.

Validate a generated report with:

```sh
bash hack/verify-test-report.sh path/to/report.json
```

Use [`report.template.json`](report.template.json) when adding a runner. The
JSON Schema in [`report.schema.json`](report.schema.json) documents the stable
machine-readable envelope. Suite-specific metrics may be added under
`metrics`, and suite-specific expected/actual fields are allowed.

Markdown reports should follow [`report.template.md`](report.template.md) and
must include a scenario comparison table. JSON remains the source of truth.
