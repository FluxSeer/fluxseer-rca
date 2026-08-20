# Engineering Baseline Behavioral Qualification

`engineering-baseline-evals.yaml` is the semantic case corpus for the
repository's `engineering-baseline` Skill. It contains:

- five violation cases;
- five safe counterexamples for restraint;
- three unrelated controls that should not activate the Skill.

The corpus is not itself an Agent qualification result. An external Agent eval
harness must capture structured semantic tokens and pass them to the runner.

## CI gate

`.github/workflows/ci.yml` runs the repository regression suite, validates the
Skill metadata, and checks that the golden corpus produces a `PREPARED` report
with 13 cases and 4 critical cases. The deterministic checks are a hard CI
gate; behavioral evidence remains report-only. CI does not invent Agent
captures, so L3 Agent Execution remains `PENDING` until an external Agent
harness supplies real results.

## Qualification status

The CI workflow result and the Engineering Baseline qualification result are
separate status spaces. A successful hosted workflow can legitimately produce
an unqualified baseline because the workflow only validates the qualification
infrastructure and prepared corpus.

```text
Engineering Baseline v1

L0 Structure               PASS
L1 Repository Regression   PASS
L2 Behavioral Corpus       PASS (13 cases, 4 critical)

L3 Behavioral Qualification
  Runner Infrastructure    PASS
  Stability Contract       PASS
  Aggregation Contract     PASS
  Agent Execution          PENDING

L4 CI Integration
  Workflow Definition      PASS
  Local Workflow Validation PASS
  Hosted GitHub Execution  PENDING until the first hosted run
  Behavioral Enforcement   REPORT_ONLY

Overall                    NOT QUALIFIED
```

The runner report itself intentionally remains `executionStatus: PREPARED`
and `summary.overall: PREPARED` until real Agent results are supplied. This is
not equivalent to `PASS` and must not be inferred from a successful CI job.

## Validate the corpus and produce a pending report

```bash
GOWORK=off go run ./cmd/engineering-baseline-eval \
  --cases test/skill/engineering-baseline-evals.yaml \
  --output /tmp/engineering-baseline-prepared.json
```

Without `--results`, the runner reports `executionStatus: PREPARED` and
`summary.overall: PREPARED`. This is intentionally not a qualification pass.

## Captured result contract

The external harness should write JSON or YAML with this shape:

```yaml
schemaVersion: fluxseer-engineering-baseline-results/v1
runId: run-2026-08-20-01
cases:
  - case_id: public-crd-removal
    skill_activated: true
    decision: NEEDS_CHANGES
    identified:
      - public_contract
      - breaking_change
    recommended:
      - deprecation_migration
      - compatibility_verification
    flags: []
    trace_ref: run-123/public-crd-removal
```

`identified`, `recommended`, and `flags` are semantic tokens. The runner does
not compare prose or require exact wording.

## Execute captured results

```bash
GOWORK=off go run ./cmd/engineering-baseline-eval \
  --cases test/skill/engineering-baseline-evals.yaml \
  --results /path/to/captured-results.json \
  --output /tmp/engineering-baseline-qualification.json
```

The executed report has fixed checks for `activation`, `correctness`,
`restraint`, and `actionability`. With the current small corpus, all cases must
pass; any failed case makes the process exit non-zero.

For stability, repeat `--results` once per independent Agent run:

```bash
GOWORK=off go run ./cmd/engineering-baseline-eval \
  --cases test/skill/engineering-baseline-evals.yaml \
  --results /path/to/run-1.json \
  --results /path/to/run-2.json \
  --results /path/to/run-3.json \
  --output /tmp/engineering-baseline-stability.json
```

The aggregate status is `PASS` only when every case passes on every run. A
case that passes on some runs and fails on others produces `UNSTABLE`; any
critical correctness failure produces `FAIL`. This runner does not execute an
Agent or claim that captures exist.
