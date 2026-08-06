#!/usr/bin/env bash
set -euo pipefail

FLUXSEER_RCA_DEMO_NAMESPACE="${FLUXSEER_RCA_DEMO_NAMESPACE:-fluxseer-rca-demo}"
FLUXSEER_RCA_RULE_NAME="${FLUXSEER_RCA_RULE_NAME:-fluxseer-rca-sample-latency}"
FLUXSEER_RCA_TARGET_NAME="${FLUXSEER_RCA_TARGET_NAME:-fluxseer-rca-sample}"
FLUXSEER_RCA_SIGNAL_NAME="${FLUXSEER_RCA_SIGNAL_NAME:-}"
FLUXSEER_RCA_CLUSTER_NAME="${FLUXSEER_RCA_CLUSTER_NAME:-fluxseer-rca-demo}"
FLUXSEER_RCA_E2E_TIMEOUT_SECONDS="${FLUXSEER_RCA_E2E_TIMEOUT_SECONDS:-240}"
FLUXSEER_RCA_E2E_POLL_SECONDS="${FLUXSEER_RCA_E2E_POLL_SECONDS:-5}"

log_section() {
  printf '\n%s\n' "============================================================"
  printf '%s\n' "$1"
  printf '%s\n' "============================================================"
}

require_command() {
  local command_name="$1"
  local install_hint="${2:-}"

  if command -v "${command_name}" >/dev/null 2>&1; then
    return 0
  fi

  echo "missing required command: ${command_name}" >&2
  if [[ -n "${install_hint}" ]]; then
    echo "${install_hint}" >&2
  fi
  exit 1
}

require_docker_daemon() {
  if docker info >/dev/null 2>&1; then
    return 0
  fi

  echo "docker daemon is not reachable" >&2
  echo "start Docker Desktop or another local Docker daemon, then retry" >&2
  exit 1
}

preflight_verify_e2e_kind() {
  log_section "Preflight Checks"
  require_command "kind" "install kind and ensure it is on PATH"
  require_command "kubectl" "install kubectl and ensure it is on PATH"
  require_command "docker" "install Docker and ensure the CLI is on PATH"
  require_docker_daemon
}

wait_for_command() {
  local description="$1"
  shift

  local deadline=$((SECONDS + FLUXSEER_RCA_E2E_TIMEOUT_SECONDS))
  until "$@"; do
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for: ${description}" >&2
      return 1
    fi
    sleep "${FLUXSEER_RCA_E2E_POLL_SECONDS}"
  done
}

wait_for_nonempty_jsonpath() {
  local resource="$1"
  local namespace="$2"
  local jsonpath="$3"

  wait_for_command "non-empty jsonpath ${jsonpath} on ${resource}" \
    bash -c "[[ -n \"\$(kubectl get ${resource} -n ${namespace} -o jsonpath='${jsonpath}' 2>/dev/null)\" ]]"
}

wait_for_jsonpath_equals() {
  local resource="$1"
  local namespace="$2"
  local jsonpath="$3"
  local expected="$4"

  wait_for_command "jsonpath ${jsonpath} equals ${expected} on ${resource}" \
    bash -c "[[ \"\$(kubectl get ${resource} -n ${namespace} -o jsonpath='${jsonpath}' 2>/dev/null)\" == \"${expected}\" ]]"
}

wait_for_condition() {
  local resource="$1"
  local namespace="$2"
  local cond_type="$3"
  local expected_status="$4"

  wait_for_command "condition ${cond_type}=${expected_status} on ${resource}" \
    bash -c "[[ \"\$(kubectl get ${resource} -n ${namespace} -o jsonpath='{.status.conditions[?(@.type==\"${cond_type}\")].status}' 2>/dev/null)\" == \"${expected_status}\" ]]"
}

wait_for_condition_reason() {
  local resource="$1"
  local namespace="$2"
  local cond_type="$3"
  local expected_reason="$4"

  wait_for_command "condition ${cond_type} reason ${expected_reason} on ${resource}" \
    bash -c "[[ \"\$(kubectl get ${resource} -n ${namespace} -o jsonpath='{.status.conditions[?(@.type==\"${cond_type}\")].reason}' 2>/dev/null)\" == \"${expected_reason}\" ]]"
}

resolve_risk_signal_name() {
  local namespace="$1"
  local rule_name="$2"
  local target_name="$3"

  kubectl get risksignal -n "${namespace}" \
    -l "fluxseer-rca.aiops.platform/risk-rule=${rule_name}" \
    --sort-by=.metadata.creationTimestamp \
    -o "jsonpath={range .items[?(@.spec.target.name==\"${target_name}\")]}{.metadata.name}{\"\\n\"}{end}" 2>/dev/null |
    tail -n1
}

wait_for_resolved_risk_signal() {
  local namespace="$1"
  local rule_name="$2"
  local target_name="$3"
  local deadline=$((SECONDS + FLUXSEER_RCA_E2E_TIMEOUT_SECONDS))
  local resolved=""

  until [[ -n "${resolved}" ]]; do
    resolved="$(resolve_risk_signal_name "${namespace}" "${rule_name}" "${target_name}")"
    if [[ -n "${resolved}" ]]; then
      break
    fi
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for RiskSignal for ${rule_name}/${target_name}" >&2
      return 1
    fi
    sleep "${FLUXSEER_RCA_E2E_POLL_SECONDS}"
  done

  FLUXSEER_RCA_SIGNAL_NAME="${resolved}"
  export FLUXSEER_RCA_SIGNAL_NAME
}

assert_contains() {
  local haystack="$1"
  local needle="$2"
  local description="$3"

  if [[ "${haystack}" != *"${needle}"* ]]; then
    echo "assertion failed: ${description}" >&2
    echo "expected to find: ${needle}" >&2
    exit 1
  fi
}

demo_state() {
  local port="${FLUXSEER_RCA_DEMO_PORT_FORWARD_PORT:-18080}"
  local pf_log
  local pf_pid

  pf_log="$(mktemp)"
  kubectl port-forward -n "${FLUXSEER_RCA_DEMO_NAMESPACE}" service/fluxseer-rca-observability "${port}:8080" >"${pf_log}" 2>&1 &
  pf_pid=$!

  cleanup_port_forward() {
    kill "${pf_pid}" >/dev/null 2>&1 || true
    wait "${pf_pid}" >/dev/null 2>&1 || true
    rm -f "${pf_log}"
  }

  trap cleanup_port_forward RETURN

  for _ in $(seq 1 20); do
    if curl -fsS "http://127.0.0.1:${port}/demo/state" >/dev/null 2>&1; then
      curl -fsS "http://127.0.0.1:${port}/demo/state"
      return 0
    fi
    sleep 1
  done

  cat "${pf_log}" >&2 || true
  return 1
}

demo_state_has_webhook() {
  local state
  state="$(demo_state 2>/dev/null || true)"
  [[ -n "${state}" && "${state}" == *"webhookEvents"* && "${state}" == *"RiskSignal detected"* ]]
}

demo_state_has_event_list() {
  local state
  state="$(demo_state 2>/dev/null || true)"
  [[ -n "${state}" && "${state}" == *"\"webhookEvents\""* ]]
}

demo_state_has_notification_title() {
  local state
  state="$(demo_state 2>/dev/null || true)"
  [[ -n "${state}" && "${state}" == *"RiskSignal detected"* ]]
}

demo_state_has_rca_summary() {
  local state
  state="$(demo_state 2>/dev/null || true)"
  [[ -n "${state}" && "${state}" == *"RCA Summary:"* ]]
}

kind_cluster_exists() {
  command -v kind >/dev/null 2>&1 && kind get clusters | grep -qx "${FLUXSEER_RCA_CLUSTER_NAME}"
}

cleanup_demo() {
  if ! command -v kind >/dev/null 2>&1; then
    echo "skipping cleanup: kind is not installed"
    return 0
  fi
  if ! kind_cluster_exists; then
    echo "skipping cleanup: cluster ${FLUXSEER_RCA_CLUSTER_NAME} does not exist"
    return 0
  fi
  log_section "Cleanup"
  make demo-down
}
