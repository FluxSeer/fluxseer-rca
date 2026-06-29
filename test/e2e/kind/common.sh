#!/usr/bin/env bash
set -euo pipefail

FLUXAGENT_DEMO_NAMESPACE="${FLUXAGENT_DEMO_NAMESPACE:-fluxagent-demo}"
FLUXAGENT_RULE_NAME="${FLUXAGENT_RULE_NAME:-fluxagent-sample-latency}"
FLUXAGENT_SIGNAL_NAME="${FLUXAGENT_SIGNAL_NAME:-fluxagent-sample-latency-fluxagent-sample-risk}"
FLUXAGENT_CLUSTER_NAME="${FLUXAGENT_CLUSTER_NAME:-fluxagent-demo}"
FLUXAGENT_E2E_TIMEOUT_SECONDS="${FLUXAGENT_E2E_TIMEOUT_SECONDS:-240}"
FLUXAGENT_E2E_POLL_SECONDS="${FLUXAGENT_E2E_POLL_SECONDS:-5}"

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

  local deadline=$((SECONDS + FLUXAGENT_E2E_TIMEOUT_SECONDS))
  until "$@"; do
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for: ${description}" >&2
      return 1
    fi
    sleep "${FLUXAGENT_E2E_POLL_SECONDS}"
  done
}

wait_for_nonempty_jsonpath() {
  local resource="$1"
  local namespace="$2"
  local jsonpath="$3"

  wait_for_command "non-empty jsonpath ${jsonpath} on ${resource}" \
    bash -c "[[ -n \"\$(kubectl get ${resource} -n ${namespace} -o jsonpath='${jsonpath}' 2>/dev/null)\" ]]"
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
  kubectl run curl-status \
    -n "${FLUXAGENT_DEMO_NAMESPACE}" \
    --restart=Never \
    --rm \
    -i \
    --image=curlimages/curl:8.8.0 \
    -- \
    curl -fsS http://fluxagent-observability:8080/demo/state
}

demo_state_has_webhook() {
  local state
  state="$(demo_state 2>/dev/null || true)"
  [[ -n "${state}" && "${state}" == *"webhookEvents"* && "${state}" == *"RiskSignal detected"* ]]
}

kind_cluster_exists() {
  command -v kind >/dev/null 2>&1 && kind get clusters | grep -qx "${FLUXAGENT_CLUSTER_NAME}"
}

cleanup_demo() {
  if ! command -v kind >/dev/null 2>&1; then
    echo "skipping cleanup: kind is not installed"
    return 0
  fi
  if ! kind_cluster_exists; then
    echo "skipping cleanup: cluster ${FLUXAGENT_CLUSTER_NAME} does not exist"
    return 0
  fi
  log_section "Cleanup"
  make demo-down
}
