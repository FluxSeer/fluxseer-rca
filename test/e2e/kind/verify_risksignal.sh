#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/common.sh"

log_section "Verify RiskSignal"

wait_for_command "RiskSignal ${FLUXAGENT_SIGNAL_NAME} exists" \
  kubectl get risksignal "${FLUXAGENT_SIGNAL_NAME}" -n "${FLUXAGENT_DEMO_NAMESPACE}"

wait_for_nonempty_jsonpath \
  "risksignal/${FLUXAGENT_SIGNAL_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  '{.status.rcaSummary}'

wait_for_condition \
  "riskrule/${FLUXAGENT_RULE_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "Ready" \
  "True"

wait_for_condition \
  "risksignal/${FLUXAGENT_SIGNAL_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "EvidenceCollectionReady" \
  "True"

wait_for_condition \
  "risksignal/${FLUXAGENT_SIGNAL_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "RCAReady" \
  "True"

rca_summary="$(kubectl get risksignal "${FLUXAGENT_SIGNAL_NAME}" -n "${FLUXAGENT_DEMO_NAMESPACE}" -o jsonpath='{.status.rcaSummary}')"
echo "verified RiskSignal RCA summary: ${rca_summary}"
