#!/usr/bin/env bash
set -euo pipefail

catalog="${1:-test/e2e/runtime/public_report_scenarios.json}"
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 1; }

jq -e '
  .schemaVersion == "fluxseer-public-report-scenarios/v1" and
  (.targetKind | type == "string" and length > 0) and
  (.scenarios | length > 0) and
  ([.scenarios[].caseId] | length == (unique | length)) and
  ([.scenarios[].output] | length == (unique | length)) and
  all(.scenarios[];
    (.caseId | test("^[a-z0-9-]+$")) and
    (.riskRule | test("^[a-z0-9-]+$")) and
    (.source | type == "string" and length > 0) and
    (.output | test("^[a-z0-9-]+\\.json$"))
  )
' "${catalog}" >/dev/null

printf 'public report scenario catalog passed: %s (%s cases)\n' \
  "${catalog}" "$(jq '.scenarios | length' "${catalog}")"
