# Engineering Baseline Behavioral Qualification

`engineering-baseline-evals.yaml` is the semantic case corpus for the
repository's `engineering-baseline` Skill. It contains:

- five violation cases;
- five safe counterexamples for restraint;
- three unrelated controls that should not activate the Skill.

The corpus is not itself an Agent qualification result. A local Codex session
or an external Agent harness must capture structured semantic tokens and pass
them to the runner.

## CI gate

`.github/workflows/ci.yml` runs the repository regression suite, validates the
Skill metadata, checks that the golden corpus produces a `PREPARED` report with
13 cases and 4 critical cases, and deterministically re-aggregates the
committed v1 captures. The deterministic checks are a hard CI gate; behavioral
enforcement remains report-only. CI does not call an Agent API or invent new
captures.

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
  Agent Execution          PASS (3 runs, 39 executions)

L4 CI Integration
  Workflow Definition      PASS
  Local Workflow Validation PASS
  Hosted GitHub Execution  PASS
  Behavioral Enforcement   REPORT_ONLY

Overall                    QUALIFIED
```

The runner report itself intentionally remains `executionStatus: PREPARED`
and `summary.overall: PREPARED` when invoked without `--results`. This is not
equivalent to `PASS` and must not be inferred from a successful CI job. The
versioned v1 evidence report is separate and is `EXECUTED`/`PASS` only after
the committed captures are supplied.

## Validate the corpus and produce a pending report

```bash
GOWORK=off go run ./cmd/engineering-baseline-eval \
  --cases test/skill/engineering-baseline-evals.yaml \
  --output /tmp/engineering-baseline-prepared.json
```

Without `--results`, the runner reports `executionStatus: PREPARED` and
`summary.overall: PREPARED`. This is intentionally not a qualification pass.

## Local Codex evidence collection

Agent API execution is not a CI prerequisite. To qualify locally, use a fixed
Codex Skill/model/configuration to evaluate all 13 cases three times, preserve
the raw session transcripts for audit, and write one normalized result file per
run. Then pass those files to the existing runner below. Normalization and
aggregation remain deterministic repository checks; the local Codex session is
the only behavioral evidence collection step.

## Captured result contract

A local Codex session or external harness should write JSON or YAML with this
shape:

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
