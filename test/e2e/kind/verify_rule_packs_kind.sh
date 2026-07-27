#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../../.." && pwd)"
source "${script_dir}/common.sh"

VERSION="${VERSION:-dev}"
IMAGE_TAG="${IMAGE_TAG:-rulepack-kind-test}"
TARGET_PLATFORM="${TARGET_PLATFORM:-linux/amd64}"
IMAGE_REPOSITORY="${IMAGE_REPOSITORY:-fluxagent/operator}"
DEMO_IMAGE_REPOSITORY="${DEMO_IMAGE_REPOSITORY:-fluxagent/demo-observability}"
CLUSTER_NAME="${FLUXAGENT_RULE_PACK_CLUSTER_NAME:-fluxagent-rule-packs}"
RELEASE_NAME="${FLUXAGENT_RULE_PACK_RELEASE_NAME:-fluxagent}"
RELEASE_NAMESPACE="${FLUXAGENT_RULE_PACK_NAMESPACE:-fluxagent-rule-packs}"
TARGET_NAME="${FLUXAGENT_RULE_PACK_TARGET_NAME:-fluxagent-baseline-crash}"
RISK_RULE_NAME="fluxagent-kubernetes-baseline"
RISK_SIGNAL_NAME="${RISK_RULE_NAME}-${TARGET_NAME}-risk"
PROMETHEUS_RISK_RULE_NAME="fluxagent-prometheus-baseline"
PROMETHEUS_RISK_SIGNAL_NAME="${PROMETHEUS_RISK_RULE_NAME}-${TARGET_NAME}-risk"
LOKI_RISK_RULE_NAME="fluxagent-loki-baseline"
LOKI_RISK_SIGNAL_NAME="${LOKI_RISK_RULE_NAME}-${TARGET_NAME}-risk"
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
    riskrules.aiops.platform
    risksignals.aiops.platform
    datasources.aiops.platform
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
      matchLabels:
        app: ${TARGET_NAME}
  kubernetesBaseline:
    enabled: true
    interval: 10s
    window: 10m
    rcaEnabled: true
  prometheusBaseline:
    enabled: true
    datasourceRef:
      name: prometheus
    interval: 10s
    window: 10m
    rcaEnabled: true
  lokiBaseline:
    enabled: true
    datasourceRef:
      name: loki
    interval: 10s
    window: 10m
    rcaEnabled: true
EOF
}

apply_fake_observability() {
  IMAGE_REPOSITORY="${IMAGE_REPOSITORY}" DEMO_IMAGE_REPOSITORY="${DEMO_IMAGE_REPOSITORY}" IMAGE_TAG="${IMAGE_TAG}" \
    bash "${repo_root}/hack/render-release-kustomize.sh" examples/fake-observability | kubectl apply -n "${RELEASE_NAMESPACE}" -f -
  kubectl rollout status deployment/fluxagent-observability -n "${RELEASE_NAMESPACE}" --timeout=180s
}

apply_datasources() {
  kubectl apply -n "${RELEASE_NAMESPACE}" -f - <<EOF
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata:
  name: prometheus
spec:
  type: prometheus
  endpoint: http://fluxagent-observability.${RELEASE_NAMESPACE}.svc.cluster.local:8080
  timeout: 10s
---
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata:
  name: loki
spec:
  type: loki
  endpoint: http://fluxagent-observability.${RELEASE_NAMESPACE}.svc.cluster.local:8080
  timeout: 10s
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
  log_section "Verify Baseline RiskRules"
  kubectl get riskrule "${RISK_RULE_NAME}" -n "${RELEASE_NAMESPACE}"
  kubectl get riskrule "${PROMETHEUS_RISK_RULE_NAME}" -n "${RELEASE_NAMESPACE}"
  kubectl get riskrule "${LOKI_RISK_RULE_NAME}" -n "${RELEASE_NAMESPACE}"
  wait_for_condition "riskrule/${RISK_RULE_NAME}" "${RELEASE_NAMESPACE}" "DatasourceResolved" "True"
  wait_for_condition "riskrule/${RISK_RULE_NAME}" "${RELEASE_NAMESPACE}" "QueryTypeSupported" "True"
  wait_for_condition "riskrule/${PROMETHEUS_RISK_RULE_NAME}" "${RELEASE_NAMESPACE}" "DatasourceResolved" "True"
  wait_for_condition "riskrule/${PROMETHEUS_RISK_RULE_NAME}" "${RELEASE_NAMESPACE}" "QueryTypeSupported" "True"
  wait_for_condition "riskrule/${LOKI_RISK_RULE_NAME}" "${RELEASE_NAMESPACE}" "DatasourceResolved" "True"
  wait_for_condition "riskrule/${LOKI_RISK_RULE_NAME}" "${RELEASE_NAMESPACE}" "QueryTypeSupported" "True"
}

wait_for_crashloop_event() {
  log_section "Wait For Kubernetes Event"
  wait_for_command "CrashLoop BackOff event for ${TARGET_NAME}" \
    bash -c "kubectl get events -n ${RELEASE_NAMESPACE} --field-selector involvedObject.kind=Pod 2>/dev/null | grep '${TARGET_NAME}' | grep -E 'BackOff|CrashLoopBackOff'"
}

inject_observability_fault() {
  kubectl run fluxagent-rulepack-fault -n "${RELEASE_NAMESPACE}" --restart=Never --rm -i --image=curlimages/curl:8.8.0 -- \
    curl -fsS -XPOST "http://fluxagent-observability:8080/demo/fault/${TARGET_NAME}"
}

verify_risk_signal() {
  local rule_name="$1"
  local signal_name="$2"
  local expected_evidence_kind="$3"

  log_section "Verify ${rule_name} RiskSignal And RCA"
  wait_for_command "RiskSignal ${signal_name} exists" \
    kubectl get "risksignal/${signal_name}" -n "${RELEASE_NAMESPACE}"
  wait_for_jsonpath_equals "risksignal/${signal_name}" "${RELEASE_NAMESPACE}" '{.status.phase}' 'Confirmed'
  wait_for_condition "risksignal/${signal_name}" "${RELEASE_NAMESPACE}" "EvidenceCollectionReady" "True"
  wait_for_condition "risksignal/${signal_name}" "${RELEASE_NAMESPACE}" "RCAReady" "True"
  wait_for_jsonpath_equals "risksignal/${signal_name}" "${RELEASE_NAMESPACE}" '{.status.rcaProvider}' 'default-heuristic'
  wait_for_nonempty_jsonpath "risksignal/${signal_name}" "${RELEASE_NAMESPACE}" '{.status.rcaSummary}'

  local signal_json
  signal_json="$(kubectl get "risksignal/${signal_name}" -n "${RELEASE_NAMESPACE}" -o json)"
  if [[ "$(jq -r '.metadata.labels["fluxagent.aiops.platform/risk-rule"]' <<<"${signal_json}")" != "${rule_name}" ]]; then
    echo "RiskSignal is missing expected RiskRule label" >&2
    exit 1
  fi
  if [[ "$(jq -r '.spec.target.name' <<<"${signal_json}")" != "${TARGET_NAME}" ]]; then
    echo "RiskSignal target name mismatch" >&2
    exit 1
  fi
  if ! jq -e --arg kind "${expected_evidence_kind}" '.spec.evidence[] | select(.kind == $kind)' <<<"${signal_json}" >/dev/null; then
    echo "RiskSignal ${signal_name} is missing ${expected_evidence_kind} evidence" >&2
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
  make build-images build-demo-images VERSION="${VERSION}" IMAGE_TAG="${IMAGE_TAG}" TARGET_PLATFORM="${TARGET_PLATFORM}" IMAGE_REPOSITORY="${IMAGE_REPOSITORY}" DEMO_IMAGE_REPOSITORY="${DEMO_IMAGE_REPOSITORY}"
)
kind load docker-image "${OPERATOR_IMAGE_REF}" --name "${CLUSTER_NAME}"
kind load docker-image "${DEMO_IMAGE_REF}" --name "${CLUSTER_NAME}"

values_file="$(mktemp)"
write_values_file "${values_file}"

log_section "Install Helm Release With Baseline Rule Pack"
install_chart "${values_file}"
rm -f "${values_file}"
wait_for_crds
kubectl rollout status deployment/fluxagent-controller-manager -n "${RELEASE_NAMESPACE}" --timeout=180s
apply_fake_observability
apply_datasources
verify_baseline_rule

log_section "Apply CrashLoop Fixture"
apply_crashloop_fixture
inject_observability_fault
wait_for_crashloop_event
verify_risk_signal "${RISK_RULE_NAME}" "${RISK_SIGNAL_NAME}" "event"
verify_risk_signal "${PROMETHEUS_RISK_RULE_NAME}" "${PROMETHEUS_RISK_SIGNAL_NAME}" "metric"
verify_risk_signal "${LOKI_RISK_RULE_NAME}" "${LOKI_RISK_SIGNAL_NAME}" "log"

log_section "Rule Pack Kind Verification Complete"
echo "verify-rule-packs-kind passed"
