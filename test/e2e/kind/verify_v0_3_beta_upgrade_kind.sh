#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../../.." && pwd)"
source "${script_dir}/common.sh"

VERSION="${VERSION:?VERSION is required}"
PREVIOUS_VERSION="${PREVIOUS_VERSION:-v0.3.0-beta.1}"
PUBLISHED_CHART_OCI="${PUBLISHED_CHART_OCI:-oci://ghcr.io/fluxseer/fluxseer-rca/charts/fluxseer-rca}"
PUBLISHED_IMAGE_REPOSITORY="${PUBLISHED_IMAGE_REPOSITORY:-ghcr.io/fluxseer/fluxseer-rca/operator}"
IMAGE_TAG="${IMAGE_TAG:-v0.3-beta-upgrade-test}"
TARGET_PLATFORM="${TARGET_PLATFORM:-linux/amd64}"
IMAGE_REPOSITORY="${IMAGE_REPOSITORY:-fluxseer/fluxseer-rca/operator}"
DEMO_IMAGE_REPOSITORY="${DEMO_IMAGE_REPOSITORY:-fluxseer/fluxseer-rca/demo-observability}"
CLUSTER_NAME="${FLUXSEER_RCA_BETA_UPGRADE_CLUSTER_NAME:-fluxseer-rca-v03-upgrade}"
RELEASE_NAME="${FLUXSEER_RCA_BETA_UPGRADE_RELEASE_NAME:-fluxseer-rca}"
RELEASE_NAMESPACE="${FLUXSEER_RCA_BETA_UPGRADE_RELEASE_NAMESPACE:-fluxseer-rca-system}"
DEMO_NAMESPACE="${FLUXSEER_RCA_DEMO_NAMESPACE:-fluxseer-rca-demo}"
SIGNAL_NAME="fluxseer-rca-sample-observed-risk"

OPERATOR_IMAGE_REF="${IMAGE_REPOSITORY}:${IMAGE_TAG}"
DEMO_IMAGE_REF="${DEMO_IMAGE_REPOSITORY}:${IMAGE_TAG}"
PUBLISHED_OPERATOR_IMAGE_REF="${PUBLISHED_IMAGE_REPOSITORY}:${PREVIOUS_VERSION}"

preflight() {
  log_section "Preflight Checks"
  require_command "kind" "install kind and ensure it is on PATH"
  require_command "kubectl" "install kubectl and ensure it is on PATH"
  require_command "docker" "install Docker and ensure the CLI is on PATH"
  require_command "helm" "install Helm and ensure it is on PATH"
  require_command "curl" "install curl and ensure it is on PATH"
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

install_previous_release() {
  log_section "Install ${PREVIOUS_VERSION}"
  helm upgrade --install "${RELEASE_NAME}" "${PUBLISHED_CHART_OCI}" \
    --version "${PREVIOUS_VERSION#v}" \
    --namespace "${RELEASE_NAMESPACE}" \
    --create-namespace \
    --wait \
    --timeout 180s \
    --set image.repository="${PUBLISHED_IMAGE_REPOSITORY}" \
    --set image.tag="${PREVIOUS_VERSION}" \
    --set image.pullPolicy=IfNotPresent \
    --set rulePacks.kubernetesBaseline.enabled=false \
    --set controller.extraEnv[0].name=FLUXSEER_RCA_WEBHOOK_URL \
    --set controller.extraEnv[0].value=http://fluxseer-rca-observability.${DEMO_NAMESPACE}.svc.cluster.local:8080/demo/webhook \
    --set controller.extraEnv[1].name=FLUXSEER_RCA_SCAN_INTERVAL \
    --set controller.extraEnv[1].value=10s
}

upgrade_candidate_release() {
  local legacy_enabled="$1"

  helm upgrade "${RELEASE_NAME}" "${repo_root}/charts/fluxseer-rca" \
    --namespace "${RELEASE_NAMESPACE}" \
    --wait \
    --timeout 180s \
    --set image.repository="${IMAGE_REPOSITORY}" \
    --set image.tag="${IMAGE_TAG}" \
    --set image.pullPolicy=IfNotPresent \
    --set rulePacks.kubernetesBaseline.enabled=false \
    --set features.legacyDeploymentRisk.enabled="${legacy_enabled}" \
    --set controller.extraEnv[0].name=FLUXSEER_RCA_WEBHOOK_URL \
    --set controller.extraEnv[0].value=http://fluxseer-rca-observability.${DEMO_NAMESPACE}.svc.cluster.local:8080/demo/webhook \
    --set controller.extraEnv[1].name=FLUXSEER_RCA_SCAN_INTERVAL \
    --set controller.extraEnv[1].value=10s
}

apply_demo_fixtures() {
  kubectl create namespace "${DEMO_NAMESPACE}" >/dev/null 2>&1 || true
  IMAGE_REPOSITORY="${IMAGE_REPOSITORY}" DEMO_IMAGE_REPOSITORY="${DEMO_IMAGE_REPOSITORY}" IMAGE_TAG="${IMAGE_TAG}" \
    bash "${repo_root}/hack/render-release-kustomize.sh" examples/fake-observability | kubectl apply -n "${DEMO_NAMESPACE}" -f -
  kubectl apply -k "${repo_root}/examples/sample-app" -n "${DEMO_NAMESPACE}"
  kubectl apply -k "${repo_root}/examples/datasources" -n "${DEMO_NAMESPACE}"
  kubectl patch datasource prometheus -n "${DEMO_NAMESPACE}" --type merge \
    -p "{\"spec\":{\"endpoint\":\"http://fluxseer-rca-observability.${DEMO_NAMESPACE}.svc.cluster.local:8080\"}}"
  kubectl patch datasource loki -n "${DEMO_NAMESPACE}" --type merge \
    -p "{\"spec\":{\"endpoint\":\"http://fluxseer-rca-observability.${DEMO_NAMESPACE}.svc.cluster.local:8080\"}}"
  kubectl rollout status deployment/fluxseer-rca-observability -n "${DEMO_NAMESPACE}" --timeout=180s
  kubectl rollout status deployment/fluxseer-rca-sample -n "${DEMO_NAMESPACE}" --timeout=180s
}

inject_demo_fault() {
  kubectl run lifecycle-fault -n "${DEMO_NAMESPACE}" --restart=Never --rm -i --image=curlimages/curl:8.8.0 \
    -- curl -fsS -XPOST "http://fluxseer-rca-observability.${DEMO_NAMESPACE}.svc.cluster.local:8080/demo/fault/fluxseer-rca-sample"
}

touch_sample_deployment() {
  kubectl annotate deployment fluxseer-rca-sample -n "${DEMO_NAMESPACE}" \
    "fluxseer-rca.aiops.platform/upgrade-check=$(date -u +%Y%m%d%H%M%S)" --overwrite
}

wait_for_signal_confirmed() {
  wait_for_command "legacy RiskSignal ${SIGNAL_NAME} exists" \
    kubectl get risksignal "${SIGNAL_NAME}" -n "${DEMO_NAMESPACE}"
  wait_for_jsonpath_equals "risksignal/${SIGNAL_NAME}" "${DEMO_NAMESPACE}" '{.status.phase}' 'Confirmed'
}

assert_signal_absent_for_window() {
  local deadline=$((SECONDS + 35))
  while (( SECONDS < deadline )); do
    if kubectl get risksignal "${SIGNAL_NAME}" -n "${DEMO_NAMESPACE}" >/dev/null 2>&1; then
      echo "legacy RiskSignal ${SIGNAL_NAME} was recreated while legacy watcher is disabled" >&2
      exit 1
    fi
    sleep 5
  done
}

verify_candidate_version() {
  local version_json
  version_json="$(kubectl exec -n "${RELEASE_NAMESPACE}" deployment/fluxseer-rca-controller-manager -- /fluxseer-rca-operator version --output=json)"
  if [[ "$(jq -r .version <<<"${version_json}")" != "${VERSION}" ]]; then
    echo "candidate controller binary version mismatch: ${version_json}" >&2
    exit 1
  fi
}

preflight
log_section "Prepare Upgrade Cluster"
kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
kind create cluster --name "${CLUSTER_NAME}" --config "${repo_root}/examples/kind/kind-config.yaml"

log_section "Build And Load Candidate Images"
docker pull --platform "${TARGET_PLATFORM}" "${PUBLISHED_OPERATOR_IMAGE_REF}"
(
  cd "${repo_root}"
  make build-images build-demo-images VERSION="${VERSION}" IMAGE_TAG="${IMAGE_TAG}" TARGET_PLATFORM="${TARGET_PLATFORM}" IMAGE_REPOSITORY="${IMAGE_REPOSITORY}" DEMO_IMAGE_REPOSITORY="${DEMO_IMAGE_REPOSITORY}"
)
kind load docker-image "${PUBLISHED_OPERATOR_IMAGE_REF}" --name "${CLUSTER_NAME}"
kind load docker-image "${OPERATOR_IMAGE_REF}" --name "${CLUSTER_NAME}"
kind load docker-image "${DEMO_IMAGE_REF}" --name "${CLUSTER_NAME}"

install_previous_release
wait_for_crds
apply_demo_fixtures
inject_demo_fault
wait_for_signal_confirmed

log_section "Upgrade To Candidate With Legacy Watcher Disabled"
upgrade_candidate_release false
kubectl rollout status deployment/fluxseer-rca-controller-manager -n "${RELEASE_NAMESPACE}" --timeout=180s
verify_candidate_version
kubectl get risksignal "${SIGNAL_NAME}" -n "${DEMO_NAMESPACE}"
kubectl delete risksignal "${SIGNAL_NAME}" -n "${DEMO_NAMESPACE}"
touch_sample_deployment
assert_signal_absent_for_window

log_section "Enable Legacy Watcher Compatibility"
upgrade_candidate_release true
kubectl rollout status deployment/fluxseer-rca-controller-manager -n "${RELEASE_NAMESPACE}" --timeout=180s
touch_sample_deployment
wait_for_signal_confirmed

log_section "Verify Uninstall And CRD Retention"
helm uninstall "${RELEASE_NAME}" -n "${RELEASE_NAMESPACE}" --wait --timeout 120s
wait_for_command "controller deployment removed" \
  bash -c "! kubectl get deployment fluxseer-rca-controller-manager -n ${RELEASE_NAMESPACE} >/dev/null 2>&1"
kubectl get crd risksignals.aiops.platform
kubectl get risksignal "${SIGNAL_NAME}" -n "${DEMO_NAMESPACE}"

log_section "v0.3 Beta Upgrade Verification Complete"
echo "verify-v0.3-beta-upgrade-kind passed"
