#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart="${root}/charts/fluxseer-rca"

for command_name in go kubectl helm; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "required command not found: ${command_name}" >&2
    exit 1
  fi
done

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT INT TERM

echo "==> v0.4 approval lifecycle: focused controller tests"
(
  cd "${root}"
  GOWORK=off go test ./internal/controllers -run 'Test(AgentActionReconciler(AllowsApprovalAfterRepeatedPendingReconciles|EscalatesAfterApprovalTimeout|RetriesEscalationNotificationAfterFailure)|RemediationPlanReconcilerDoesNotOverwriteTerminalAgentActionStatus|RiskRuleReconcilerDefaultsUnsetInvestigationPolicyToCreateRequest)$'
)

echo "==> v0.4 approval lifecycle: CRD source/chart consistency"
for source_crd in "${root}"/config/crd/bases/*.yaml; do
  name="$(basename "${source_crd}")"
  chart_crd="${chart}/crds/${name}"
  if [[ ! -f "${chart_crd}" ]]; then
    echo "missing Helm chart CRD: ${chart_crd}" >&2
    exit 1
  fi
  diff -u "${source_crd}" "${chart_crd}" >/dev/null
done

echo "==> v0.4 approval lifecycle: API and CRD contract checks"
grep -q 'PhaseEscalated.*= "Escalated"' "${root}/api/v1alpha1/types.go"
grep -q 'ApprovalTimeoutSeconds.*json:"approvalTimeoutSeconds,omitempty"' "${root}/api/v1alpha1/types.go"
grep -q 'EscalatedAt.*json:"escalatedAt,omitempty"' "${root}/api/v1alpha1/types.go"
for crd in "${root}"/config/crd/bases/aiops.platform_{risksignals,remediationplans,agentactions}.yaml; do
  grep -q 'approvalTimeoutSeconds:' "${crd}"
done
grep -q 'escalatedAt:' "${root}/config/crd/bases/aiops.platform_agentactions.yaml"

echo "==> v0.4 approval lifecycle: Helm and example routing"
helm lint "${chart}"
helm template fluxseer-rca "${chart}" --namespace fluxseer-rca-system >"${tmpdir}/helm-default.yaml"

# Read version dynamically from Chart.yaml to avoid hardcoding version number
chart_version=$(awk '/^version:/ {print $2}' "${chart}/Chart.yaml")
app_version=$(awk '/^appVersion:/ {gsub(/"/, ""); print $2}' "${chart}/Chart.yaml")

if grep -q "^version: ${chart_version}" "${chart}/Chart.yaml"; then
  echo "✓ Chart version: ${chart_version}"
else
  echo "✗ Chart version mismatch" >&2
  exit 1
fi

if grep -q "^appVersion: \"${app_version}\"" "${chart}/Chart.yaml"; then
  echo "✓ App version: ${app_version}"
else
  echo "✗ App version mismatch" >&2
  exit 1
fi

grep -q 'mode: CreateRequest' "${root}/examples/riskrules/latency-regression.yaml"
grep -q 'mode: CreateRequest' "${tmpdir}/helm-default.yaml"

echo "v0.4 approval lifecycle gate passed"
