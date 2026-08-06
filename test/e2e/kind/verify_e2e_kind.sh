#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/common.sh"

preflight_verify_e2e_kind

cleanup() {
  local exit_code="$1"
  trap - EXIT
  cleanup_demo
  if command -v kind >/dev/null 2>&1; then
    bash "${script_dir}/verify_cleanup.sh"
  fi
  exit "${exit_code}"
}

on_exit() {
  local exit_code="$1"
  cleanup "${exit_code}"
}

trap 'on_exit $?' EXIT

log_section "Prepare Demo Cluster"
make demo-down >/dev/null 2>&1 || true
make demo-up

log_section "Wait For Deployments"
kubectl rollout status deployment/fluxseer-rca-controller-manager -n "${FLUXSEER_RCA_DEMO_NAMESPACE}" --timeout=180s
kubectl rollout status deployment/fluxseer-rca-observability -n "${FLUXSEER_RCA_DEMO_NAMESPACE}" --timeout=180s
kubectl rollout status deployment/fluxseer-rca-sample -n "${FLUXSEER_RCA_DEMO_NAMESPACE}" --timeout=180s

log_section "Inject Fault"
make inject-fault

bash "${script_dir}/verify_risksignal.sh"
bash "${script_dir}/verify_notification.sh"
bash "${script_dir}/verify_degraded_conditions.sh"
FLUXSEER_RCA_E2E_REUSE_CLUSTER=true bash "${script_dir}/verify_investigation_kind.sh"

log_section "E2E Verification Complete"
echo "verify-e2e-kind passed"
