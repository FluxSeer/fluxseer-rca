#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 1 ]]; then
  echo "usage: $0 <report.json>" >&2
  exit 2
fi

report="$1"
if [[ ! -f "${report}" ]]; then
  echo "test report not found: ${report}" >&2
  exit 1
fi

jq -e '
  .schemaVersion == "fluxseer-test-report/v1" and
  (.suite.id | type == "string" and length > 0) and
  (.suite.name | type == "string" and length > 0) and
  (.suite.tier | IN("unit", "integration", "envtest", "mock-runtime", "cluster", "replay")) and
  (.run.id | type == "string" and length > 0) and
  (.run.sourceCommit | type == "string" and length > 0) and
  (.run.sourceDirty | type == "boolean") and
  (.run.environment | type == "object") and
  (.summary.result | IN("PASS", "FAIL")) and
  (.summary.total == (.scenarios | length)) and
  (.summary.passed == ([.scenarios[] | select(.result == "PASS")] | length)) and
  (.summary.failed == ([.scenarios[] | select(.result == "FAIL")] | length)) and
  (.summary.total == (.summary.passed + .summary.failed)) and
  ((.summary.result == "PASS") == (.summary.failed == 0)) and
  all(.scenarios[];
    (.id | type == "string" and length > 0) and
    (.name | type == "string" and length > 0) and
    (.result | IN("PASS", "FAIL")) and
    (.expected | type == "object") and
    (.actual | type == "object") and
    (.assertions | type == "array") and
    (.differences | type == "array") and
    (.artifacts | type == "array") and
    all(.assertions[];
      (.id | type == "string" and length > 0) and
      (.result | IN("PASS", "FAIL")) and
      has("expected") and has("actual")
    ) and
    all(.differences[]; (.path | type == "string" and length > 0) and has("expected") and has("actual")) and
    ((.result == "PASS") == ((.differences | length) == 0)) and
    ((.result == "PASS") == (all(.assertions[]; .result == "PASS")))
  )
' "${report}" >/dev/null

echo "test report contract passed: ${report}"
