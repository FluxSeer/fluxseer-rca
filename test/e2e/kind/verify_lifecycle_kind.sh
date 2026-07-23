#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../../.." && pwd)"
source "${script_dir}/common.sh"

VERSION="${VERSION:?VERSION is required}"
IMAGE_TAG="${IMAGE_TAG:-lifecycle-release-test}"
TARGET_PLATFORM="${TARGET_PLATFORM:-linux/amd64}"
IMAGE_REPOSITORY="${IMAGE_REPOSITORY:-fluxagent/operator}"
DEMO_IMAGE_REPOSITORY="${DEMO_IMAGE_REPOSITORY:-fluxagent/demo-observability}"
CLUSTER_NAME="${FLUXAGENT_LIFECYCLE_CLUSTER_NAME:-fluxagent-lifecycle}"
RELEASE_NAME="${FLUXAGENT_LIFECYCLE_RELEASE_NAME:-fluxagent}"
RELEASE_NAMESPACE="${FLUXAGENT_LIFECYCLE_RELEASE_NAMESPACE:-fluxagent-system}"
DEMO_NAMESPACE="${FLUXAGENT_DEMO_NAMESPACE:-fluxagent-demo}"

OPERATOR_IMAGE_REF="${IMAGE_REPOSITORY}:${IMAGE_TAG}"
DEMO_IMAGE_REF="${DEMO_IMAGE_REPOSITORY}:${IMAGE_TAG}"

preflight() {
  log_section "Preflight Checks"
  require_command "kind" "install kind and ensure it is on PATH"
  require_command "kubectl" "install kubectl and ensure it is on PATH"
  require_command "docker" "install Docker and ensure the CLI is on PATH"
  require_command "helm" "install Helm and ensure it is on PATH"
  require_command "jq" "install jq and ensure it is on PATH"
  require_docker_daemon
}

cluster_exists() {
  kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"
}

cleanup() {
  local exit_code="$1"
  trap - EXIT INT TERM
  if cluster_exists; then
    log_section "Cleanup"
    kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
  fi
  if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
    echo "kind cluster ${CLUSTER_NAME} still exists after cleanup" >&2
    exit 1
  fi
  exit "${exit_code}"
}

trap 'cleanup $?' EXIT INT TERM

wait_for_crds() {
  local crds=(
    agentactions.aiops.platform
    datasources.aiops.platform
    investigationrequests.aiops.platform
    modelproviders.aiops.platform
    remediationplans.aiops.platform
    riskrules.aiops.platform
    risksignals.aiops.platform
  )
  for crd in "${crds[@]}"; do
    kubectl wait --for=condition=Established --timeout=120s "crd/${crd}"
  done
}

install_chart() {
  local phase="$1"

  helm upgrade --install "${RELEASE_NAME}" "${repo_root}/charts/kube-ai-sre" \
    --namespace "${RELEASE_NAMESPACE}" \
    --create-namespace \
    --wait \
    --timeout 180s \
    --set image.repository="${IMAGE_REPOSITORY}" \
    --set image.tag="${IMAGE_TAG}" \
    --set image.pullPolicy=IfNotPresent \
    --set controller.extraEnv[0].name=FLUXAGENT_WEBHOOK_URL \
    --set controller.extraEnv[0].value=http://fluxagent-observability.${DEMO_NAMESPACE}.svc.cluster.local:8080/demo/webhook \
    --set controller.extraEnv[1].name=FLUXAGENT_SCAN_INTERVAL \
    --set controller.extraEnv[1].value=15s \
    --set controller.extraEnv[2].name=FLUXAGENT_LIFECYCLE_PHASE \
    --set controller.extraEnv[2].value="${phase}"
}

apply_demo_fixtures() {
  kubectl create namespace "${DEMO_NAMESPACE}" >/dev/null 2>&1 || true
  IMAGE_REPOSITORY="${IMAGE_REPOSITORY}" DEMO_IMAGE_REPOSITORY="${DEMO_IMAGE_REPOSITORY}" IMAGE_TAG="${IMAGE_TAG}" \
    bash "${repo_root}/hack/render-release-kustomize.sh" examples/fake-observability | kubectl apply -n "${DEMO_NAMESPACE}" -f -
  kubectl apply -k "${repo_root}/examples/sample-app" -n "${DEMO_NAMESPACE}"
  kubectl apply -k "${repo_root}/examples/datasources" -n "${DEMO_NAMESPACE}"
  kubectl apply -k "${repo_root}/examples/modelproviders" -n "${DEMO_NAMESPACE}"
  kubectl apply -k "${repo_root}/examples/riskrules" -n "${DEMO_NAMESPACE}"
  kubectl patch datasource prometheus -n "${DEMO_NAMESPACE}" --type merge \
    -p "{\"spec\":{\"endpoint\":\"http://fluxagent-observability.${DEMO_NAMESPACE}.svc.cluster.local:8080\"}}"
  kubectl patch datasource loki -n "${DEMO_NAMESPACE}" --type merge \
    -p "{\"spec\":{\"endpoint\":\"http://fluxagent-observability.${DEMO_NAMESPACE}.svc.cluster.local:8080\"}}"
}

verify_controller_version() {
  local version_json
  version_json="$(kubectl exec -n "${RELEASE_NAMESPACE}" deployment/fluxagent-controller-manager -- /fluxagent-operator version --output=json)"
  if [[ "$(jq -r .version <<<"${version_json}")" != "${VERSION}" ]]; then
    echo "controller binary version mismatch: ${version_json}" >&2
    exit 1
  fi
  if [[ "$(jq -r .gitDirty <<<"${version_json}")" != "false" ]]; then
    echo "controller binary must be clean for release lifecycle: ${version_json}" >&2
    exit 1
  fi
}

verify_investigation_smoke() {
  local request_name="$1"

  (
    cd "${repo_root}"
    GOWORK=off go run ./cmd/fluxagent investigate deployment fluxagent-sample \
      --namespace "${DEMO_NAMESPACE}" \
      --request-namespace "${DEMO_NAMESPACE}" \
      --request-name "${request_name}" \
      --query-file config/samples/investigation-queries.yaml \
      --question "Lifecycle smoke investigation for ${request_name}" \
      --provider heuristic-provider \
      --create-risk-signal \
      --wait
  )

  wait_for_jsonpath_equals "investigationrequest/${request_name}" "${DEMO_NAMESPACE}" '{.status.phase}' 'Completed'
  wait_for_condition "investigationrequest/${request_name}" "${DEMO_NAMESPACE}" "Ready" "True"
  wait_for_condition "investigationrequest/${request_name}" "${DEMO_NAMESPACE}" "RCAReady" "True"
}

verify_install() {
  log_section "Verify Helm Install"
  wait_for_crds
  kubectl rollout status deployment/fluxagent-controller-manager -n "${RELEASE_NAMESPACE}" --timeout=180s
  kubectl rollout status deployment/fluxagent-observability -n "${DEMO_NAMESPACE}" --timeout=180s
  kubectl rollout status deployment/fluxagent-sample -n "${DEMO_NAMESPACE}" --timeout=180s
  kubectl wait deployment/fluxagent-controller-manager -n "${RELEASE_NAMESPACE}" --for=condition=Available=True --timeout=180s
  kubectl get serviceaccount fluxagent-controller-manager -n "${RELEASE_NAMESPACE}"
  kubectl get clusterrole fluxagent-manager-role
  kubectl get clusterrolebinding fluxagent-manager-rolebinding
  verify_controller_version
  verify_investigation_smoke lifecycle-install-success
}

verify_upgrade() {
  log_section "Verify Helm Upgrade"
  local generation_before
  generation_before="$(kubectl get deployment fluxagent-controller-manager -n "${RELEASE_NAMESPACE}" -o jsonpath='{.metadata.generation}')"

  install_chart upgrade

  local helm_revision
  helm_revision="$(helm status "${RELEASE_NAME}" -n "${RELEASE_NAMESPACE}" -o json | jq -r .version)"
  if [[ "${helm_revision}" != "2" ]]; then
    echo "expected Helm revision 2 after upgrade, got ${helm_revision}" >&2
    exit 1
  fi

  wait_for_command "deployment generation increased after upgrade" \
    bash -c "[[ \"\$(kubectl get deployment fluxagent-controller-manager -n ${RELEASE_NAMESPACE} -o jsonpath='{.metadata.generation}')\" -gt \"${generation_before}\" ]]"
  kubectl rollout status deployment/fluxagent-controller-manager -n "${RELEASE_NAMESPACE}" --timeout=180s
  wait_for_command "deployment observedGeneration catches up" \
    bash -c "[[ \"\$(kubectl get deployment fluxagent-controller-manager -n ${RELEASE_NAMESPACE} -o jsonpath='{.status.observedGeneration}')\" == \"\$(kubectl get deployment fluxagent-controller-manager -n ${RELEASE_NAMESPACE} -o jsonpath='{.metadata.generation}')\" ]]"
  kubectl get investigationrequest lifecycle-install-success -n "${DEMO_NAMESPACE}"
  verify_investigation_smoke lifecycle-upgrade-success
}

verify_uninstall() {
  log_section "Verify Helm Uninstall And CRD Retention"
  helm uninstall "${RELEASE_NAME}" -n "${RELEASE_NAMESPACE}" --wait --timeout 120s

  wait_for_command "controller deployment removed" \
    bash -c "! kubectl get deployment fluxagent-controller-manager -n ${RELEASE_NAMESPACE} >/dev/null 2>&1"
  wait_for_command "service account removed" \
    bash -c "! kubectl get serviceaccount fluxagent-controller-manager -n ${RELEASE_NAMESPACE} >/dev/null 2>&1"
  wait_for_command "cluster role removed" \
    bash -c "! kubectl get clusterrole fluxagent-manager-role >/dev/null 2>&1"
  wait_for_command "cluster role binding removed" \
    bash -c "! kubectl get clusterrolebinding fluxagent-manager-rolebinding >/dev/null 2>&1"

  kubectl get crd investigationrequests.aiops.platform
  kubectl get investigationrequest lifecycle-install-success -n "${DEMO_NAMESPACE}"
  kubectl get investigationrequest lifecycle-upgrade-success -n "${DEMO_NAMESPACE}"
}

preflight
log_section "Prepare Lifecycle Cluster"
kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
kind create cluster --name "${CLUSTER_NAME}" --config "${repo_root}/examples/kind/kind-config.yaml"

log_section "Build And Load Release Images"
(
  cd "${repo_root}"
  make build-images build-demo-images VERSION="${VERSION}" IMAGE_TAG="${IMAGE_TAG}" TARGET_PLATFORM="${TARGET_PLATFORM}" IMAGE_REPOSITORY="${IMAGE_REPOSITORY}" DEMO_IMAGE_REPOSITORY="${DEMO_IMAGE_REPOSITORY}"
)
kind load docker-image "${OPERATOR_IMAGE_REF}" --name "${CLUSTER_NAME}"
kind load docker-image "${DEMO_IMAGE_REF}" --name "${CLUSTER_NAME}"

log_section "Install Helm Release"
install_chart install
apply_demo_fixtures
verify_install
verify_upgrade
verify_uninstall

log_section "Lifecycle Verification Complete"
echo "verify-lifecycle-kind passed"
