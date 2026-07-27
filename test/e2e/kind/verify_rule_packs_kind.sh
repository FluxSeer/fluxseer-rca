#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../../.." && pwd)"
source "${script_dir}/common.sh"

VERSION="${VERSION:-dev}"
IMAGE_TAG="${IMAGE_TAG:-rulepack-kind-test}"
TARGET_PLATFORM="${TARGET_PLATFORM:-linux/amd64}"
IMAGE_REPOSITORY="${IMAGE_REPOSITORY:-fluxagent/operator}"
CLUSTER_NAME="${FLUXAGENT_RULE_PACK_CLUSTER_NAME:-fluxagent-rule-packs}"
RELEASE_NAME="${FLUXAGENT_RULE_PACK_RELEASE_NAME:-fluxagent}"
RELEASE_NAMESPACE="${FLUXAGENT_RULE_PACK_NAMESPACE:-fluxagent-rule-packs}"
TARGET_NAME="${FLUXAGENT_RULE_PACK_TARGET_NAME:-fluxagent-baseline-crash}"
RISK_RULE_NAME="fluxagent-kubernetes-baseline"
RISK_SIGNAL_NAME="${RISK_RULE_NAME}-${TARGET_NAME}-risk"
OPERATOR_IMAGE_REF="${IMAGE_REPOSITORY}:${IMAGE_TAG}"

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
    riskrules.aiops.platform
    risksignals.aiops.platform
  )
  for crd in "${crds[@]}"; do
    kubectl wait --for=condition=Established --timeout=120s "crd/${crd}"
  done
}

install_chart() {
  local values_file="$1"

  helm upgrade --install "${RELEASE_NAME}" "${repo_root}/charts/kube-ai-sre" \
    --namespace "${RELEASE_NAMESPACE}" \
    --create-namespace \
    --wait \
    --timeout 180s \
    --values "${values_file}"
}

write_values_file() {
  local values_file="$1"

  cat >"${values_file}" <<EOF
image:
  repository: ${IMAGE_REPOSITORY}
  tag: ${IMAGE_TAG}
  pullPolicy: IfNotPresent
controller:
  extraEnv:
    - name: FLUXAGENT_SCAN_INTERVAL
      value: 10s
rulePacks:
  defaultTargetSelector:
    namespaceSelector:
      matchNames:
        - ${RELEASE_NAMESPACE}
    workloadSelector:
      kinds:
        - Deployment
  kubernetesBaseline:
    enabled: true
    interval: 10s
    window: 10m
    rcaEnabled: true
  prometheusBaseline:
    enabled: false
  lokiBaseline:
    enabled: false
EOF
}

apply_crashloop_fixture() {
  kubectl apply -n "${RELEASE_NAMESPACE}" -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${TARGET_NAME}
  labels:
    app: ${TARGET_NAME}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ${TARGET_NAME}
  template:
    metadata:
      labels:
        app: ${TARGET_NAME}
    spec:
      containers:
        - name: app
          image: busybox:1.36
          imagePullPolicy: IfNotPresent
          command:
            - sh
            - -c
            - echo baseline crashloop; exit 1
EOF
}

verify_baseline_rule() {
  log_section "Verify Baseline RiskRule"
  kubectl get riskrule "${RISK_RULE_NAME}" -n "${RELEASE_NAMESPACE}"
  wait_for_condition "riskrule/${RISK_RULE_NAME}" "${RELEASE_NAMESPACE}" "DatasourceResolved" "True"
  wait_for_condition "riskrule/${RISK_RULE_NAME}" "${RELEASE_NAMESPACE}" "QueryTypeSupported" "True"
}

wait_for_crashloop_event() {
  log_section "Wait For Kubernetes Event"
  wait_for_command "CrashLoop BackOff event for ${TARGET_NAME}" \
    bash -c "kubectl get events -n ${RELEASE_NAMESPACE} --field-selector involvedObject.kind=Pod 2>/dev/null | grep -E '${TARGET_NAME}.*(BackOff|CrashLoopBackOff)'"
}

verify_risk_signal() {
  log_section "Verify RiskSignal And RCA"
  wait_for_command "RiskSignal ${RISK_SIGNAL_NAME} exists" \
    kubectl get "risksignal/${RISK_SIGNAL_NAME}" -n "${RELEASE_NAMESPACE}"
  wait_for_jsonpath_equals "risksignal/${RISK_SIGNAL_NAME}" "${RELEASE_NAMESPACE}" '{.status.phase}' 'Confirmed'
  wait_for_condition "risksignal/${RISK_SIGNAL_NAME}" "${RELEASE_NAMESPACE}" "EvidenceCollectionReady" "True"
  wait_for_condition "risksignal/${RISK_SIGNAL_NAME}" "${RELEASE_NAMESPACE}" "RCAReady" "True"
  wait_for_jsonpath_equals "risksignal/${RISK_SIGNAL_NAME}" "${RELEASE_NAMESPACE}" '{.status.rcaProvider}' 'default-heuristic'
  wait_for_nonempty_jsonpath "risksignal/${RISK_SIGNAL_NAME}" "${RELEASE_NAMESPACE}" '{.status.rcaSummary}'

  local signal_json
  signal_json="$(kubectl get "risksignal/${RISK_SIGNAL_NAME}" -n "${RELEASE_NAMESPACE}" -o json)"
  if [[ "$(jq -r '.metadata.labels["fluxagent.aiops.platform/risk-rule"]' <<<"${signal_json}")" != "${RISK_RULE_NAME}" ]]; then
    echo "RiskSignal is missing expected RiskRule label" >&2
    exit 1
  fi
  if [[ "$(jq -r '.spec.target.name' <<<"${signal_json}")" != "${TARGET_NAME}" ]]; then
    echo "RiskSignal target name mismatch" >&2
    exit 1
  fi
}

preflight
log_section "Prepare Rule Pack Cluster"
kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
kind create cluster --name "${CLUSTER_NAME}" --config "${repo_root}/examples/kind/kind-config.yaml"

log_section "Build And Load Operator Image"
(
  cd "${repo_root}"
  make build-images VERSION="${VERSION}" IMAGE_TAG="${IMAGE_TAG}" TARGET_PLATFORM="${TARGET_PLATFORM}" IMAGE_REPOSITORY="${IMAGE_REPOSITORY}"
)
kind load docker-image "${OPERATOR_IMAGE_REF}" --name "${CLUSTER_NAME}"

values_file="$(mktemp)"
write_values_file "${values_file}"

log_section "Install Helm Release With Baseline Rule Pack"
install_chart "${values_file}"
rm -f "${values_file}"
wait_for_crds
kubectl rollout status deployment/fluxagent-controller-manager -n "${RELEASE_NAMESPACE}" --timeout=180s
verify_baseline_rule

log_section "Apply CrashLoop Fixture"
apply_crashloop_fixture
wait_for_crashloop_event
verify_risk_signal

log_section "Rule Pack Kind Verification Complete"
echo "verify-rule-packs-kind passed"
