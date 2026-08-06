#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/common.sh"

log_section "Verify RiskSignal"

wait_for_resolved_risk_signal "${FLUXSEER_RCA_DEMO_NAMESPACE}" "${FLUXSEER_RCA_RULE_NAME}" "${FLUXSEER_RCA_TARGET_NAME}"
kubectl get risksignal "${FLUXSEER_RCA_SIGNAL_NAME}" -n "${FLUXSEER_RCA_DEMO_NAMESPACE}"

wait_for_nonempty_jsonpath \
  "risksignal/${FLUXSEER_RCA_SIGNAL_NAME}" \
  "${FLUXSEER_RCA_DEMO_NAMESPACE}" \
  '{.status.rcaSummary}'

wait_for_condition \
  "riskrule/${FLUXSEER_RCA_RULE_NAME}" \
  "${FLUXSEER_RCA_DEMO_NAMESPACE}" \
  "Ready" \
  "True"

wait_for_condition \
  "risksignal/${FLUXSEER_RCA_SIGNAL_NAME}" \
  "${FLUXSEER_RCA_DEMO_NAMESPACE}" \
  "EvidenceCollectionReady" \
  "True"

wait_for_condition \
  "risksignal/${FLUXSEER_RCA_SIGNAL_NAME}" \
  "${FLUXSEER_RCA_DEMO_NAMESPACE}" \
  "RCAReady" \
  "True"

rca_summary="$(kubectl get risksignal "${FLUXSEER_RCA_SIGNAL_NAME}" -n "${FLUXSEER_RCA_DEMO_NAMESPACE}" -o jsonpath='{.status.rcaSummary}')"
echo "verified RiskSignal RCA summary: ${rca_summary}"
