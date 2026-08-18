#!/usr/bin/env bash
set -euo pipefail

live_harness_script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
live_harness_repo_root="$(cd "${live_harness_script_dir}/../../.." && pwd)"
source "${live_harness_script_dir}/common.sh"

LIVE_HARNESS_RUN_ID="${LIVE_HARNESS_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
LIVE_HARNESS_CLUSTER_NAME="${LIVE_HARNESS_CLUSTER_NAME:-fluxseer-rca-v05-live}"
LIVE_HARNESS_RELEASE_NAME="${LIVE_HARNESS_RELEASE_NAME:-fluxseer-rca}"
LIVE_HARNESS_RELEASE_NAMESPACE="${LIVE_HARNESS_RELEASE_NAMESPACE:-fluxseer-rca-system}"
LIVE_HARNESS_VERSION="${LIVE_HARNESS_VERSION:-${VERSION:-live-kind-harness}}"
LIVE_HARNESS_IMAGE_TAG="${LIVE_HARNESS_IMAGE_TAG:-${IMAGE_TAG:-live-kind-harness}}"
LIVE_HARNESS_TARGET_PLATFORM="${LIVE_HARNESS_TARGET_PLATFORM:-${TARGET_PLATFORM:-linux/amd64}}"
LIVE_HARNESS_IMAGE_REPOSITORY="${LIVE_HARNESS_IMAGE_REPOSITORY:-${IMAGE_REPOSITORY:-fluxseer/fluxseer-rca/operator}}"
LIVE_HARNESS_CLUSTER_CONFIG="${LIVE_HARNESS_CLUSTER_CONFIG:-${live_harness_repo_root}/examples/kind/kind-config.yaml}"
LIVE_HARNESS_TIMEOUT_SECONDS="${LIVE_HARNESS_TIMEOUT_SECONDS:-${FLUXSEER_RCA_E2E_TIMEOUT_SECONDS:-300}}"
LIVE_HARNESS_POLL_SECONDS="${LIVE_HARNESS_POLL_SECONDS:-${FLUXSEER_RCA_E2E_POLL_SECONDS:-3}}"
LIVE_HARNESS_SNAPSHOT_REQUEST_TIMEOUT="${LIVE_HARNESS_SNAPSHOT_REQUEST_TIMEOUT:-10s}"
LIVE_HARNESS_KEEP_CLUSTER="${LIVE_HARNESS_KEEP_CLUSTER:-false}"
LIVE_HARNESS_ARTIFACT_ROOT="${LIVE_HARNESS_ARTIFACT_ROOT:-${live_harness_repo_root}/reports/runtime/v0.5-alpha1-kind-live/${LIVE_HARNESS_RUN_ID}}"
LIVE_HARNESS_CONTEXT="kind-${LIVE_HARNESS_CLUSTER_NAME}"
LIVE_HARNESS_KUBECONFIG="${LIVE_HARNESS_KUBECONFIG:-${LIVE_HARNESS_ARTIFACT_ROOT}/kind.kubeconfig}"
LIVE_HARNESS_OPERATOR_IMAGE_REF="${LIVE_HARNESS_IMAGE_REPOSITORY}:${LIVE_HARNESS_IMAGE_TAG}"
LIVE_HARNESS_CLUSTER_CREATED=false

live_harness_log() {
  printf '[kind-live] %s\n' "$*"
}

live_harness_kubectl() {
  kubectl --context "${LIVE_HARNESS_CONTEXT}" "$@"
}

live_harness_helm() {
  helm --kube-context "${LIVE_HARNESS_CONTEXT}" "$@"
}

live_harness_cluster_exists() {
  kind get clusters 2>/dev/null | grep -qx "${LIVE_HARNESS_CLUSTER_NAME}"
}

live_harness_require_tools() {
  log_section "Kind Live Harness Preflight"
  require_command "kind" "install kind and ensure it is on PATH"
  require_command "kubectl" "install kubectl and ensure it is on PATH"
  require_command "docker" "install Docker and ensure the CLI is on PATH"
  require_command "helm" "install Helm and ensure it is on PATH"
  require_command "jq" "install jq and ensure it is on PATH"
  require_command "make" "install make and ensure it is on PATH"
  require_docker_daemon

  if [[ ! -f "${LIVE_HARNESS_CLUSTER_CONFIG}" ]]; then
    echo "kind config does not exist: ${LIVE_HARNESS_CLUSTER_CONFIG}" >&2
    return 1
  fi
}

live_harness_prepare_artifacts() {
  mkdir -p \
    "${LIVE_HARNESS_ARTIFACT_ROOT}/cluster" \
    "${LIVE_HARNESS_ARTIFACT_ROOT}/fluxseer" \
    "${LIVE_HARNESS_ARTIFACT_ROOT}/reports"

  jq -n \
    --arg schema "fluxseer-kind-live-harness-run/v1" \
    --arg runId "${LIVE_HARNESS_RUN_ID}" \
    --arg clusterName "${LIVE_HARNESS_CLUSTER_NAME}" \
    --arg context "${LIVE_HARNESS_CONTEXT}" \
    --arg version "${LIVE_HARNESS_VERSION}" \
    --arg image "${LIVE_HARNESS_OPERATOR_IMAGE_REF}" \
    --arg artifactRoot "${LIVE_HARNESS_ARTIFACT_ROOT}" \
    '{schemaVersion:$schema,runId:$runId,clusterName:$clusterName,kubernetesContext:$context,version:$version,operatorImage:$image,artifactRoot:$artifactRoot}' \
    >"${LIVE_HARNESS_ARTIFACT_ROOT}/run.json"
}

live_harness_create_cluster() {
  log_section "Create Fresh Kind Cluster"
  kind delete cluster --name "${LIVE_HARNESS_CLUSTER_NAME}" >/dev/null 2>&1 || true
  kind create cluster \
    --name "${LIVE_HARNESS_CLUSTER_NAME}" \
    --config "${LIVE_HARNESS_CLUSTER_CONFIG}" \
    --wait 120s
  LIVE_HARNESS_CLUSTER_CREATED=true
  kind get kubeconfig --name "${LIVE_HARNESS_CLUSTER_NAME}" >"${LIVE_HARNESS_KUBECONFIG}"
  export KUBECONFIG="${LIVE_HARNESS_KUBECONFIG}"
  live_harness_kubectl cluster-info
}

live_harness_build_and_load_image() {
  log_section "Build And Load Local Operator Image"
  make -C "${live_harness_repo_root}" build-images \
    VERSION="${LIVE_HARNESS_VERSION}" \
    IMAGE_TAG="${LIVE_HARNESS_IMAGE_TAG}" \
    TARGET_PLATFORM="${LIVE_HARNESS_TARGET_PLATFORM}" \
    IMAGE_REPOSITORY="${LIVE_HARNESS_IMAGE_REPOSITORY}"
  kind load docker-image "${LIVE_HARNESS_OPERATOR_IMAGE_REF}" --name "${LIVE_HARNESS_CLUSTER_NAME}"
}

live_harness_wait_for_command() {
  local description="$1"
  shift
  local deadline=$((SECONDS + LIVE_HARNESS_TIMEOUT_SECONDS))

  until "$@"; do
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for: ${description}" >&2
      return 1
    fi
    sleep "${LIVE_HARNESS_POLL_SECONDS}"
  done
}

live_harness_assert_jsonpath_equals() {
  local resource="$1"
  local namespace="$2"
  local jsonpath="$3"
  local expected="$4"
  local actual

  actual="$(live_harness_kubectl get "${resource}" -n "${namespace}" -o "jsonpath=${jsonpath}" 2>/dev/null)"
  [[ "${actual}" == "${expected}" ]]
}

live_harness_wait_for_jsonpath_equals() {
  local resource="$1"
  local namespace="$2"
  local jsonpath="$3"
  local expected="$4"

  live_harness_wait_for_command \
    "${resource} ${jsonpath}=${expected}" \
    live_harness_assert_jsonpath_equals "${resource}" "${namespace}" "${jsonpath}" "${expected}"
}

live_harness_wait_for_crds() {
  local crd
  local crds=(
    agentactions.aiops.platform
    approvalpolicies.aiops.platform
    datasources.aiops.platform
    escalationchains.aiops.platform
    investigationrequests.aiops.platform
    modelproviders.aiops.platform
    namespacethresholds.aiops.platform
    remediationplans.aiops.platform
    riskrules.aiops.platform
    risksignals.aiops.platform
  )

  for crd in "${crds[@]}"; do
    live_harness_kubectl wait --for=condition=Established --timeout=180s "crd/${crd}"
  done
}

live_harness_install() {
  log_section "Install Read-Only FluxSeer"
  live_harness_helm upgrade --install "${LIVE_HARNESS_RELEASE_NAME}" "${live_harness_repo_root}/charts/fluxseer-rca" \
    --namespace "${LIVE_HARNESS_RELEASE_NAMESPACE}" \
    --create-namespace \
    --wait \
    --timeout 180s \
    --set image.repository="${LIVE_HARNESS_IMAGE_REPOSITORY}" \
    --set image.tag="${LIVE_HARNESS_IMAGE_TAG}" \
    --set image.pullPolicy=IfNotPresent \
    --set rbac.profile=readOnlyRCA \
    --set controller.enableRemediation=false \
    --set controller.enablePolicyPack=false \
    --set features.remediation.enabled=false \
    --set features.experimentalExecutor.enabled=false \
    --set features.policyPack.enabled=false \
    --set rulePacks.kubernetesBaseline.enabled=false \
    --set rulePacks.prometheusBaseline.enabled=false \
    --set rulePacks.lokiBaseline.enabled=false \
    --set rulePacks.applicationProfiles.enabled=false

  live_harness_wait_for_crds
  live_harness_kubectl rollout status \
    "deployment/${LIVE_HARNESS_RELEASE_NAME}-controller-manager" \
    -n "${LIVE_HARNESS_RELEASE_NAMESPACE}" \
    --timeout=180s
  live_harness_kubectl wait \
    --for=condition=Available=True \
    "deployment/${LIVE_HARNESS_RELEASE_NAME}-controller-manager" \
    -n "${LIVE_HARNESS_RELEASE_NAMESPACE}" \
    --timeout=180s
}

live_harness_verify_read_only_profile() {
  local service_account="system:serviceaccount:${LIVE_HARNESS_RELEASE_NAMESPACE}:fluxseer-rca-controller-manager"
  local can_patch
  local can_watch

  can_patch="$(live_harness_kubectl auth can-i patch deployments --as="${service_account}" || true)"
  can_watch="$(live_harness_kubectl auth can-i watch pods --as="${service_account}" || true)"
  live_harness_log "read-only RBAC: patch deployments=${can_patch}; watch pods=${can_watch}"
  if [[ "${can_patch}" != "no" ]]; then
    echo "read-only profile unexpectedly allows Deployment patch: ${can_patch}" >&2
    return 1
  fi
  if [[ "${can_watch}" != "yes" ]]; then
    echo "read-only profile cannot watch Pods: ${can_watch}" >&2
    return 1
  fi
}

live_harness_collect_json() {
  local output_path="$1"
  shift

  if ! live_harness_kubectl --request-timeout="${LIVE_HARNESS_SNAPSHOT_REQUEST_TIMEOUT}" "$@" -o json >"${output_path}"; then
    echo "failed to collect Kubernetes object snapshot: kubectl $*" >&2
    return 1
  fi
}

live_harness_collect_snapshots() {
  log_section "Collect Live Harness Artifacts"

  live_harness_collect_json "${LIVE_HARNESS_ARTIFACT_ROOT}/cluster/events.json" get events -A
  live_harness_collect_json "${LIVE_HARNESS_ARTIFACT_ROOT}/cluster/pods.json" get pods -A
  live_harness_collect_json "${LIVE_HARNESS_ARTIFACT_ROOT}/cluster/deployments.json" get deployments -A
  live_harness_collect_json "${LIVE_HARNESS_ARTIFACT_ROOT}/cluster/services.json" get services -A

  live_harness_collect_json "${LIVE_HARNESS_ARTIFACT_ROOT}/fluxseer/riskrule.json" get riskrules -A
  live_harness_collect_json "${LIVE_HARNESS_ARTIFACT_ROOT}/fluxseer/investigationrequest.json" get investigationrequests -A
  live_harness_collect_json "${LIVE_HARNESS_ARTIFACT_ROOT}/fluxseer/risksignal.json" get risksignals -A
  live_harness_collect_json "${LIVE_HARNESS_ARTIFACT_ROOT}/fluxseer/remediationplan.json" get remediationplans -A
  live_harness_collect_json "${LIVE_HARNESS_ARTIFACT_ROOT}/fluxseer/agentaction.json" get agentactions -A

  jq -n \
    --arg schema "fluxseer-kind-live-internal-snapshot/v1" \
    --arg runId "${LIVE_HARNESS_RUN_ID}" \
    --arg context "${LIVE_HARNESS_CONTEXT}" \
    --slurpfile events "${LIVE_HARNESS_ARTIFACT_ROOT}/cluster/events.json" \
    --slurpfile pods "${LIVE_HARNESS_ARTIFACT_ROOT}/cluster/pods.json" \
    --slurpfile deployments "${LIVE_HARNESS_ARTIFACT_ROOT}/cluster/deployments.json" \
    --slurpfile services "${LIVE_HARNESS_ARTIFACT_ROOT}/cluster/services.json" \
    --slurpfile riskRules "${LIVE_HARNESS_ARTIFACT_ROOT}/fluxseer/riskrule.json" \
    --slurpfile investigationRequests "${LIVE_HARNESS_ARTIFACT_ROOT}/fluxseer/investigationrequest.json" \
    --slurpfile riskSignals "${LIVE_HARNESS_ARTIFACT_ROOT}/fluxseer/risksignal.json" \
    --slurpfile remediationPlans "${LIVE_HARNESS_ARTIFACT_ROOT}/fluxseer/remediationplan.json" \
    --slurpfile agentActions "${LIVE_HARNESS_ARTIFACT_ROOT}/fluxseer/agentaction.json" \
    '{schemaVersion:$schema,runId:$runId,kubernetesContext:$context,cluster:{events:$events[0],pods:$pods[0],deployments:$deployments[0],services:$services[0]},fluxseer:{riskRules:$riskRules[0],investigationRequests:$investigationRequests[0],riskSignals:$riskSignals[0],remediationPlans:$remediationPlans[0],agentActions:$agentActions[0]}}' \
    >"${LIVE_HARNESS_ARTIFACT_ROOT}/reports/internal-snapshot.json"

  jq -n \
    --arg schema "fluxseer-kind-live-public-reports/v1" \
    --arg runId "${LIVE_HARNESS_RUN_ID}" \
    '{schemaVersion:$schema,runId:$runId,reports:[]}' \
    >"${LIVE_HARNESS_ARTIFACT_ROOT}/reports/public-report.json"
}

live_harness_collect_public_report() {
  local resource="$1"
  local name="$2"
  local namespace="$3"
  local output_path="${4:-${LIVE_HARNESS_ARTIFACT_ROOT}/reports/public-report.json}"
  local report_json

  report_json="$(
    cd "${live_harness_repo_root}"
    GOWORK=off go run ./cmd/fluxseer report "${resource}" "${name}" --namespace "${namespace}" --output json
  )"
  jq -n \
    --arg schema "fluxseer-kind-live-public-reports/v1" \
    --arg runId "${LIVE_HARNESS_RUN_ID}" \
    --argjson report "${report_json}" \
    '{schemaVersion:$schema,runId:$runId,reports:[$report]}' \
    >"${output_path}"
}

live_harness_cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

  if live_harness_cluster_exists; then
    live_harness_collect_snapshots || true
    if [[ "${LIVE_HARNESS_KEEP_CLUSTER}" == "true" ]]; then
      live_harness_log "keeping cluster ${LIVE_HARNESS_CLUSTER_NAME}"
    else
      log_section "Delete Kind Cluster"
      kind delete cluster --name "${LIVE_HARNESS_CLUSTER_NAME}" >/dev/null 2>&1 || true
    fi
  fi

  if [[ "${LIVE_HARNESS_KEEP_CLUSTER}" != "true" ]] && live_harness_cluster_exists; then
    echo "kind cluster ${LIVE_HARNESS_CLUSTER_NAME} still exists after cleanup" >&2
    exit_code=1
  fi
  exit "${exit_code}"
}

live_harness_run() {
  local scenario_function="${1:-}"

  live_harness_require_tools
  live_harness_prepare_artifacts
  trap live_harness_cleanup EXIT
  trap 'exit 130' INT TERM
  live_harness_create_cluster
  live_harness_build_and_load_image
  live_harness_install
  live_harness_verify_read_only_profile

  if [[ -n "${scenario_function}" ]]; then
    if ! declare -F "${scenario_function}" >/dev/null; then
      echo "scenario function is not defined: ${scenario_function}" >&2
      return 1
    fi
    "${scenario_function}"
  fi

  live_harness_collect_snapshots
  live_harness_log "generic live harness completed"
}
