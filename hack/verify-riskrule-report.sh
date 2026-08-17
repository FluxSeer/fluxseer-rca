#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 1 ]]; then
  echo "usage: $0 <riskrule-report.json>" >&2
  exit 2
fi

report="$1"
jq -e '
  . as $report |
  .schemaVersion == "fluxseer-riskrule-report/v1" and
  (.selection.namespace | type == "string" and length > 0) and
  (.selection.riskRule | type == "string" and length > 0) and
  .riskRule.apiVersion == "aiops.platform/v1alpha1" and
  .riskRule.kind == "RiskRule" and
  .riskRule.metadata.namespace == .selection.namespace and
  .riskRule.metadata.name == .selection.riskRule and
  (.investigationRequests | type == "array") and
  (.riskSignals | type == "array") and
  all(.investigationRequests[];
    .apiVersion == "aiops.platform/v1alpha1" and
    .kind == "InvestigationRequest" and
    .metadata.namespace == $report.selection.namespace and
    .metadata.labels["fluxseer-rca.aiops.platform/risk-rule"] == $report.selection.riskRule
  ) and
  all($report.riskSignals[];
    . as $signal |
    $signal.apiVersion == "aiops.platform/v1alpha1" and
    $signal.kind == "RiskSignal" and
    (
      $signal.metadata.labels["fluxseer-rca.aiops.platform/risk-rule"] == $report.selection.riskRule or
      any($report.investigationRequests[];
        .status.linkedRiskSignalRef.namespace == $signal.metadata.namespace and
        .status.linkedRiskSignalRef.name == $signal.metadata.name
      )
    )
  )
' "${report}" >/dev/null

echo "RiskRule report contract passed: ${report}"
