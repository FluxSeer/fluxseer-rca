#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
catalog="${root}/config/rule-packs/detection-patterns.json"
public_reports="${root}/test/e2e/runtime/public_report_scenarios.json"

for command_name in jq diff sort sed; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "required command not found: ${command_name}" >&2
    exit 1
  }
done

jq -e '
  .schemaVersion == "fluxseer-detection-pattern-catalog/v1" and
  (.patterns | length == 21) and
  ([.patterns[].id] | length == (unique | length)) and
  all(.patterns[];
    (.id | type == "string" and length > 0) and
    (.displayName | type == "string" and length > 0) and
    (.rulePack | IN("kubernetes-baseline", "prometheus-baseline", "loki-baseline")) and
    (.datasource | type == "string" and length > 0) and
    (.queryType | IN("event", "deploymentCondition", "metric", "log")) and
    (.enabledByDefault | type == "boolean") and
    (.requiresExternalBackend | type == "boolean") and
    (.workloadKinds | type == "array" and length > 0) and
    (.detectionEvidence | type == "array" and length > 0) and
    (.maximumCausalClaim | type == "string" and length > 0) and
    (.evidenceProfile == null or (.evidenceProfile | IN("ImagePullBackOff", "CrashLoopBackOff", "OOMKilled", "LatencyRegression", "RolloutLatencyRegression"))) and
    (.runtimeReportCase == null or (.runtimeReportCase | type == "string" and length > 0))
  ) and
  ([.patterns[] | select(.rulePack == "kubernetes-baseline")] | length == 6) and
  ([.patterns[] | select(.rulePack == "prometheus-baseline")] | length == 8) and
  ([.patterns[] | select(.rulePack == "loki-baseline")] | length == 7) and
  all(.patterns[] | select(.rulePack == "kubernetes-baseline");
    .enabledByDefault == true and .requiresExternalBackend == false and .datasource == "kubernetes-events"
  ) and
  all(.patterns[] | select(.rulePack != "kubernetes-baseline");
    .enabledByDefault == false and .requiresExternalBackend == true
  )
' "${catalog}" >/dev/null

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT INT TERM

compare_pack() {
  local pack="$1"
  local template="$2"
  jq -r --arg pack "${pack}" '.patterns[] | select(.rulePack == $pack) | .id' "${catalog}" | sort >"${tmpdir}/${pack}-catalog.txt"
  sed -n 's/^    - name: *//p' "${template}" | tr -d '"' | sort >"${tmpdir}/${pack}-template.txt"
  if ! diff -u "${tmpdir}/${pack}-template.txt" "${tmpdir}/${pack}-catalog.txt"; then
    echo "detection pattern catalog does not match ${template}" >&2
    exit 1
  fi
}

compare_pack kubernetes-baseline "${root}/charts/fluxseer-rca/templates/rulepack-kubernetes-baseline.yaml"
compare_pack prometheus-baseline "${root}/charts/fluxseer-rca/templates/rulepack-prometheus-baseline.yaml"
compare_pack loki-baseline "${root}/charts/fluxseer-rca/templates/rulepack-loki-baseline.yaml"

while IFS= read -r case_id; do
  jq -e --arg case_id "${case_id}" 'any(.scenarios[]; .caseId == $case_id)' "${public_reports}" >/dev/null || {
    echo "catalog runtimeReportCase is absent from public report catalog: ${case_id}" >&2
    exit 1
  }
done < <(jq -r '.patterns[].runtimeReportCase // empty' "${catalog}")

echo "detection pattern catalog passed: 21 patterns (6 Kubernetes, 8 Prometheus, 7 Loki)"
