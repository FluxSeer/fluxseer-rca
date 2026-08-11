#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 2 ]]; then
  echo "usage: $0 <report.json> <report.md>" >&2
  exit 2
fi

input="$1"
output="$2"
bash "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/verify-test-report.sh" "${input}" >/dev/null
mkdir -p "$(dirname "${output}")"

jq -r '
  def md: tostring | gsub("\\|"; "\\\\|") | gsub("`"; "\\`");
  def compact: tojson | md;
  def state:
    [
      (if .terminal.phase != null then "phase=" + (.terminal.phase | tostring) else empty end),
      (if .terminal.outcome != null then "outcome=" + (.terminal.outcome | tostring) else empty end),
      (if .terminal.failureReason != null then "failure=" + (.terminal.failureReason | tostring) else empty end),
      (if .diagnosis.rootCauseType != null then "rootCause=" + (.diagnosis.rootCauseType | tostring) else empty end),
      (if .diagnosis.requiredClaimVerification != null then "claim=" + (.diagnosis.requiredClaimVerification | tostring) else empty end),
      (if .diagnosis.claimVerification != null then "claims=" + (.diagnosis.claimVerification | tojson) else empty end),
      (if .evidence.retainedCount != null then "evidence=" + (.evidence.retainedCount | tostring) else empty end),
      (if .efficiency.queryCount != null then "queries=" + (.efficiency.queryCount | tostring) else empty end),
      (if .efficiency.providerRequestCount != null then "provider=" + (.efficiency.providerRequestCount | tostring) elif .sideEffects.providerRequests != null then "provider=" + (.sideEffects.providerRequests | tostring) else empty end),
      (if .execution.durationSeconds != null then "duration=" + (.execution.durationSeconds | tostring) + "s" else empty end),
      (if .execution.inputTokens != null then "tokens=" + (.execution.inputTokens | tostring) + "/" + ((.execution.outputTokens // 0) | tostring) else empty end)
    ] | join("; ") | md;
  [
    "# \(.suite.name) Test Report",
    "",
    "- Contract: `\(.schemaVersion)`",
    "- Suite schema: `\(.suiteSchemaVersion // "unspecified")`",
    "- Run ID: `\(.run.id)`",
    "- Source commit: `\(.run.sourceCommit)`",
    "- Source dirty: `\(.run.sourceDirty)`",
    "- Tier: `\(.suite.tier)`",
    "- Result: `\(.summary.result)` (\(.summary.passed)/\(.summary.total))",
    "",
    "| Scenario | Result | Expected | Actual | Differences | Artifacts |",
    "| --- | --- | --- | --- | --- | --- |",
    (.scenarios[] |
      "| `\(.id | md)` | \(.result) | `\(.expected | state)` | `\(.actual | state)` | " +
      (if (.differences | length) == 0 then "none" else (.differences | map(.path) | join(", ") | md) end) + " | " +
      (.artifacts | map("`" + (. | md) + "`") | join("<br>")) + " |"
    ),
    "",
    "## Differences",
    "",
    (if ([.scenarios[].differences[]] | length) == 0 then
      "None. Every scenario matched its expected contract."
    else
      (.scenarios[] | select((.differences | length) > 0) |
        "### \(.id)\n\n" +
        (.differences | map("- `\(.path)`: expected `\(.expected | compact)`, actual `\(.actual | compact)`") | join("\n"))
      )
    end),
    "",
    "## Aggregate Metrics",
    "",
    "```json",
    ((.metrics // {}) | tojson),
    "```"
  ] | flatten | .[]
' "${input}" >"${output}"

echo "rendered test report: ${output}"
