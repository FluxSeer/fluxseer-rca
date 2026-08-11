#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
namespace="${FLUXSEER_RCA_RUNTIME_NAMESPACE:-fluxseer-rca-test}"
timeout="${FLUXSEER_RCA_RUNTIME_TIMEOUT:-180s}"
report_root="${FLUXSEER_RCA_RUNTIME_REPORT_ROOT:-${repo_root}/reports/runtime}"
run_id="${FLUXSEER_RCA_RUNTIME_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
report_dir="${report_root}/fluxseer-rca-runtime-p0-matrix-${run_id}"
target_name="runtime-p0-target"
mock_name="runtime-p0-mock"

requests=(
  runtime-p0-query-policy-rejected
  runtime-p0-query-budget-exceeded
  runtime-p0-provider-not-found
  runtime-p0-invalid-provider-response
  runtime-p0-no-supported-claims
  runtime-p0-no-issue-found
  runtime-p0-required-evidence-missing
  runtime-p0-crashloop-coverage-missing
  runtime-p0-unsupported-retention
  runtime-p0-retention-store-unavailable
  runtime-p0-risk-signal-source-blocked
  runtime-p0-depth-limit-exceeded
  runtime-p0-ttl-cleanup
)

if [[ -z "${KUBECONFIG:-}" ]]; then
  echo "KUBECONFIG must point to the explicitly authorized test cluster" >&2
  exit 1
fi

for command_name in kubectl jq git comm sort; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "required command not found: ${command_name}" >&2
    exit 1
  fi
done

mkdir -p "${report_dir}"

cleanup() {
  kubectl delete investigationrequest "${requests[@]}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  while IFS= read -r signal_name; do
    [[ -z "${signal_name}" ]] || kubectl delete risksignal "${signal_name}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  done < <(kubectl get risksignal -n "${namespace}" -o json 2>/dev/null | jq -r '.items[] | select((.spec.investigationRef.name // "") | startswith("runtime-p0-") or startswith("runtime-provider-")) | .metadata.name' 2>/dev/null || true)
  kubectl delete modelprovider runtime-p0-invalid-provider -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete datasource runtime-p0-prometheus runtime-p0-policy-events -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete secret runtime-p0-provider-secret -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete event runtime-p0-backoff runtime-p0-scheduled -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete deployment "${target_name}" "${mock_name}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete service "${mock_name}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete configmap runtime-p0-mock-nginx -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

wait_for_status() {
  local request_name="$1"
  local phase="$2"
  kubectl wait --for="jsonpath={.status.phase}=${phase}" --timeout="${timeout}" "investigationrequest/${request_name}" -n "${namespace}"
}

assert_condition() {
  local request_json="$1"
  local condition_type="$2"
  local expected_status="$3"
  local expected_reason="$4"
  jq -e --arg type "${condition_type}" --arg status "${expected_status}" --arg reason "${expected_reason}" '
    any(.status.conditions[]; .type == $type and .status == $status and .reason == $reason)
  ' <<<"${request_json}" >/dev/null
}

assert_request() {
  local request_name="$1"
  local expected_phase="$2"
  local expected_outcome="$3"
  local expected_failure="$4"
  local request_json
  request_json="$(kubectl get investigationrequest "${request_name}" -n "${namespace}" -o json)"
  jq . <<<"${request_json}" >"${report_dir}/${request_name}.json"
  kubectl get investigationrequest "${request_name}" -n "${namespace}" -o yaml >"${report_dir}/${request_name}.yaml"

  if ! jq -e --arg phase "${expected_phase}" --arg outcome "${expected_outcome}" --arg failure "${expected_failure}" '
    .metadata.generation as $generation |
    .status.phase == $phase and
    .status.outcome == $outcome and
    .status.observedGeneration == $generation and
    (.status.conditions | length > 0) and
    all(.status.conditions[]; .observedGeneration == $generation) and
    (if $failure == "" then (.status.failure == null) else .status.failure.code == $failure end)
  ' <<<"${request_json}" >/dev/null; then
    echo "${request_name} terminal contract mismatch" >&2
    jq '{generation: .metadata.generation, status: .status}' <<<"${request_json}" >&2
    exit 1
  fi
}

assert_no_projected_signal() {
  local request_name="$1"
  local count
  count="$(kubectl get risksignal -n "${namespace}" -o json | jq --arg name "${request_name}" '[.items[] | select((.spec.investigationRef.name // "") == $name)] | length')"
  [[ "${count}" == "0" ]] || {
    echo "${request_name} unexpectedly projected ${count} RiskSignal objects" >&2
    return 1
  }
}

cleanup

kubectl get namespace "${namespace}" >/dev/null
kubectl get deployment fluxseer-rca-controller-manager -n "${namespace}" -o yaml >"${report_dir}/controller-deployment.yaml"
kubectl get pods -n "${namespace}" -l app.kubernetes.io/component=controller-manager -o yaml >"${report_dir}/controller-pods.yaml"
kubectl get remediationplan,agentaction -n "${namespace}" -o json | jq -r '.items[].metadata.uid' | sort >"${report_dir}/side-effect-uids-before.txt"

# Reuse the focused provider-policy runner so the P0 entrypoint preserves the
# access-log proof for both hosted-provider egress rejection paths.
KUBECONFIG="${KUBECONFIG}" \
  FLUXSEER_RCA_RUNTIME_NAMESPACE="${namespace}" \
  FLUXSEER_RCA_RUNTIME_TIMEOUT="${timeout}" \
  FLUXSEER_RCA_RUNTIME_REPORT_ROOT="${report_dir}" \
  FLUXSEER_RCA_RUNTIME_RUN_ID="provider-policy" \
  bash "${repo_root}/test/e2e/runtime/verify_cluster_matrix.sh"

kubectl apply -n "${namespace}" -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: runtime-p0-mock-nginx
data:
  default.conf: |
    server {
      listen 8080;
      access_log /dev/stdout combined;
      error_log /dev/stderr warn;
      location = /api/v1/query_range {
        default_type application/json;
        return 200 '{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"http_request_latency_regression_ratio"},"value":[1786420800,"0"]}]}}';
      }
      location = /v1/invalid-provider-response {
        default_type application/json;
        add_header x-request-id runtime-invalid-001;
        return 200 '{"id":"runtime-invalid-001","choices":[]}';
      }
      location = /control {
        default_type application/json;
        return 200 '{"ok":true}';
      }
      location / {
        default_type application/json;
        return 404 '{"error":"unexpected runtime matrix request"}';
      }
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: runtime-p0-mock
spec:
  replicas: 1
  selector:
    matchLabels:
      app: runtime-p0-mock
  template:
    metadata:
      labels:
        app: runtime-p0-mock
    spec:
      securityContext:
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: nginx
          image: nginxinc/nginx-unprivileged:1.27-alpine
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            runAsNonRoot: true
            runAsUser: 101
          ports:
            - name: http
              containerPort: 8080
          volumeMounts:
            - name: config
              mountPath: /etc/nginx/conf.d/default.conf
              subPath: default.conf
      volumes:
        - name: config
          configMap:
            name: runtime-p0-mock-nginx
---
apiVersion: v1
kind: Service
metadata:
  name: runtime-p0-mock
spec:
  selector:
    app: runtime-p0-mock
  ports:
    - name: http
      port: 8080
      targetPort: http
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: runtime-p0-target
  labels:
    app: runtime-p0-target
spec:
  replicas: 1
  selector:
    matchLabels:
      app: runtime-p0-target
  template:
    metadata:
      labels:
        app: runtime-p0-target
    spec:
      securityContext:
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: app
          image: busybox:1.36
          command: ["sh", "-c", "sleep 3600"]
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            runAsNonRoot: true
            runAsUser: 65532
---
apiVersion: v1
kind: Secret
metadata:
  name: runtime-p0-provider-secret
type: Opaque
stringData:
  api-key: runtime-test-token
EOF

kubectl rollout status "deployment/${mock_name}" -n "${namespace}" --timeout="${timeout}"
kubectl rollout status "deployment/${target_name}" -n "${namespace}" --timeout="${timeout}"
kubectl exec -n "${namespace}" "deployment/${mock_name}" -- wget -qO- http://127.0.0.1:8080/control >/dev/null

target_uid="$(kubectl get deployment "${target_name}" -n "${namespace}" -o jsonpath='{.metadata.uid}')"
mock_service_ip="$(kubectl get service "${mock_name}" -n "${namespace}" -o jsonpath='{.spec.clusterIP}')"
now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
kubectl apply -n "${namespace}" -f - <<EOF
apiVersion: v1
kind: Event
metadata:
  name: runtime-p0-backoff
involvedObject:
  apiVersion: apps/v1
  kind: Deployment
  name: ${target_name}
  namespace: ${namespace}
  uid: ${target_uid}
reason: BackOff
message: runtime P0 synthetic backoff evidence
source:
  component: runtime-p0-matrix
type: Warning
firstTimestamp: "${now}"
lastTimestamp: "${now}"
count: 1
---
apiVersion: v1
kind: Event
metadata:
  name: runtime-p0-scheduled
involvedObject:
  apiVersion: apps/v1
  kind: Deployment
  name: ${target_name}
  namespace: ${namespace}
  uid: ${target_uid}
reason: Scheduled
message: runtime P0 synthetic benign scheduling evidence
source:
  component: runtime-p0-matrix
type: Normal
firstTimestamp: "${now}"
lastTimestamp: "${now}"
count: 1
---
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata:
  name: runtime-p0-prometheus
spec:
  type: prometheus
  endpoint: http://${mock_name}:8080
  networkPolicy:
    allowedCIDRs: [${mock_service_ip}/32]
  queryPolicy:
    mode: LegacyUnrestricted
---
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata:
  name: runtime-p0-policy-events
spec:
  type: kubernetesEvents
  queryPolicy:
    mode: TemplatesOnly
    allowedTemplates: [allowed-template]
---
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: runtime-p0-invalid-provider
spec:
  provider: openai
  model: runtime-mock
  endpoint: http://${mock_name}:8080/v1/invalid-provider-response
  dataPolicy:
    allowExternalTransmission: true
    maximumClassification: Confidential
  apiKeySecretRef:
    name: runtime-p0-provider-secret
    key: api-key
EOF

kubectl wait --for=jsonpath='{.status.phase}'=Observed --timeout="${timeout}" datasource/runtime-p0-prometheus datasource/runtime-p0-policy-events -n "${namespace}"
sleep 2

kubectl apply -n "${namespace}" -f - <<EOF
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-query-policy-rejected
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  modelProviderRef: {name: heuristic-provider}
  queries:
    - name: blocked-template
      datasourceRef: {name: runtime-p0-policy-events}
      queryType: event
      queryTemplate: blocked-template
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-query-budget-exceeded
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  modelProviderRef: {name: heuristic-provider}
  queryBudget:
    maxCumulativeResponseBytes: 1
  queries:
    - name: normal-latency
      datasourceRef: {name: runtime-p0-prometheus}
      queryType: metric
      queryTemplate: runtime_latency_normal
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-provider-not-found
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  modelProviderRef: {name: runtime-p0-missing-provider}
  dataSources: [{name: kubernetes-events}]
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-invalid-provider-response
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  createRiskSignal: true
  modelProviderRef: {name: runtime-p0-invalid-provider}
  queries:
    - name: normal-latency
      datasourceRef: {name: runtime-p0-prometheus}
      queryType: metric
      queryTemplate: runtime_latency_normal
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-no-supported-claims
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  createRiskSignal: true
  modelProviderRef: {name: heuristic-provider}
  queries:
    - name: backoff
      datasourceRef: {name: kubernetes-events}
      queryType: event
      reasons: [BackOff]
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-no-issue-found
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  createRiskSignal: true
  modelProviderRef: {name: heuristic-provider}
  evidenceRequirements: {profile: LatencyRegression}
  queries:
    - name: normal-latency
      datasourceRef: {name: runtime-p0-prometheus}
      queryType: metric
      queryTemplate: runtime_latency_normal
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-required-evidence-missing
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  createRiskSignal: true
  modelProviderRef: {name: heuristic-provider}
  evidenceRequirements: {profile: LatencyRegression}
  queries:
    - name: backoff
      datasourceRef: {name: kubernetes-events}
      queryType: event
      reasons: [BackOff]
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-crashloop-coverage-missing
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  createRiskSignal: true
  modelProviderRef: {name: heuristic-provider}
  evidenceRequirements: {profile: CrashLoopBackOff}
  queries:
    - name: scheduled-only
      datasourceRef: {name: kubernetes-events}
      queryType: event
      reasons: [Scheduled]
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-unsupported-retention
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  modelProviderRef: {name: heuristic-provider}
  evidenceRetention: {mode: RawSnapshot}
  dataSources: [{name: kubernetes-events}]
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-retention-store-unavailable
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  modelProviderRef: {name: heuristic-provider}
  evidenceRetention:
    mode: NormalizedSnapshot
    storageRef: {name: local-filesystem}
  queries:
    - name: normal-latency
      datasourceRef: {name: runtime-p0-prometheus}
      queryType: metric
      queryTemplate: runtime_latency_normal
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-risk-signal-source-blocked
  annotations:
    fluxseer-rca.aiops.platform/lineage-source: ${namespace}/runtime-source-signal
    fluxseer-rca.aiops.platform/lineage-source-kind: RiskSignal
    fluxseer-rca.aiops.platform/lineage-source-api-version: aiops.platform/v1alpha1
    fluxseer-rca.aiops.platform/lineage-source-uid: runtime-source-signal-uid
    fluxseer-rca.aiops.platform/target-uid: ${target_uid}
    fluxseer-rca.aiops.platform/investigation-depth: "0"
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  createRiskSignal: true
  modelProviderRef: {name: heuristic-provider}
  dataSources: [{name: kubernetes-events}]
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-depth-limit-exceeded
  annotations:
    fluxseer-rca.aiops.platform/lineage-source: ${namespace}/runtime-source-rule
    fluxseer-rca.aiops.platform/lineage-source-kind: RiskRule
    fluxseer-rca.aiops.platform/lineage-source-api-version: aiops.platform/v1alpha1
    fluxseer-rca.aiops.platform/lineage-source-uid: runtime-source-rule-uid
    fluxseer-rca.aiops.platform/target-uid: ${target_uid}
    fluxseer-rca.aiops.platform/investigation-depth: "1"
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  createRiskSignal: true
  loopPolicy: {maxDepth: 1}
  modelProviderRef: {name: heuristic-provider}
  dataSources: [{name: kubernetes-events}]
EOF

wait_for_status runtime-p0-query-policy-rejected Failed
wait_for_status runtime-p0-query-budget-exceeded Failed
wait_for_status runtime-p0-provider-not-found Failed
wait_for_status runtime-p0-invalid-provider-response Failed
wait_for_status runtime-p0-no-supported-claims Completed
wait_for_status runtime-p0-no-issue-found Completed
wait_for_status runtime-p0-required-evidence-missing Completed
wait_for_status runtime-p0-crashloop-coverage-missing Completed
wait_for_status runtime-p0-unsupported-retention Failed
wait_for_status runtime-p0-retention-store-unavailable Failed
wait_for_status runtime-p0-risk-signal-source-blocked Failed
wait_for_status runtime-p0-depth-limit-exceeded Failed

assert_request runtime-p0-query-policy-rejected Failed Unknown QueryPolicyRejected
assert_request runtime-p0-query-budget-exceeded Failed Unknown QueryBudgetExceeded
assert_request runtime-p0-provider-not-found Failed Unknown ProviderNotFound
assert_request runtime-p0-invalid-provider-response Failed Unknown InvalidProviderResponse
assert_request runtime-p0-no-supported-claims Completed Inconclusive ""
assert_request runtime-p0-no-issue-found Completed NoIssueFound ""
assert_request runtime-p0-required-evidence-missing Completed Inconclusive ""
assert_request runtime-p0-crashloop-coverage-missing Completed Inconclusive ""
assert_request runtime-p0-unsupported-retention Failed Unknown UnsupportedRetentionMode
assert_request runtime-p0-retention-store-unavailable Failed Unknown EvidenceRetentionStoreUnavailable
assert_request runtime-p0-risk-signal-source-blocked Failed Unknown RiskSignalSourceBlocked
assert_request runtime-p0-depth-limit-exceeded Failed Unknown InvestigationDepthLimitExceeded

assert_condition "$(kubectl get investigationrequest runtime-p0-query-policy-rejected -n "${namespace}" -o json)" QueryPolicyReady False QueryPolicyRejected
assert_condition "$(kubectl get investigationrequest runtime-p0-query-budget-exceeded -n "${namespace}" -o json)" EvidenceCollectionReady False QueryBudgetExceeded
assert_condition "$(kubectl get investigationrequest runtime-p0-provider-not-found -n "${namespace}" -o json)" RCAReady False ProviderNotFound
assert_condition "$(kubectl get investigationrequest runtime-p0-invalid-provider-response -n "${namespace}" -o json)" Degraded True InvalidProviderResponse
assert_condition "$(kubectl get investigationrequest runtime-p0-no-supported-claims -n "${namespace}" -o json)" Verified False NoSupportedRootCauseClaims
assert_condition "$(kubectl get investigationrequest runtime-p0-no-supported-claims -n "${namespace}" -o json)" RCAReady False RCAUnverified
assert_condition "$(kubectl get investigationrequest runtime-p0-no-issue-found -n "${namespace}" -o json)" RCAReady True NoIssueFound
assert_condition "$(kubectl get investigationrequest runtime-p0-required-evidence-missing -n "${namespace}" -o json)" EvidenceCollectionReady False RequiredEvidenceMissing
assert_condition "$(kubectl get investigationrequest runtime-p0-crashloop-coverage-missing -n "${namespace}" -o json)" EvidenceCollectionReady False RequiredEvidenceMissing
assert_condition "$(kubectl get investigationrequest runtime-p0-retention-store-unavailable -n "${namespace}" -o json)" RCAReady False EvidenceRetentionStoreUnavailable

for request_name in \
  runtime-p0-query-policy-rejected \
  runtime-p0-query-budget-exceeded \
  runtime-p0-provider-not-found \
  runtime-p0-invalid-provider-response \
  runtime-p0-no-issue-found \
  runtime-p0-required-evidence-missing \
  runtime-p0-crashloop-coverage-missing \
  runtime-p0-unsupported-retention \
  runtime-p0-retention-store-unavailable \
  runtime-p0-risk-signal-source-blocked \
  runtime-p0-depth-limit-exceeded; do
  assert_no_projected_signal "${request_name}"
done

unverified_signal="$(kubectl get investigationrequest runtime-p0-no-supported-claims -n "${namespace}" -o jsonpath='{.status.linkedRiskSignalRef.name}')"
[[ -n "${unverified_signal}" ]]
for _ in {1..30}; do
  unverified_signal_json="$(kubectl get risksignal "${unverified_signal}" -n "${namespace}" -o json)"
  if assert_condition "${unverified_signal_json}" RCAReady False RCAUnverified; then
    break
  fi
  sleep 1
done
assert_condition "${unverified_signal_json}" RCAReady False RCAUnverified

kubectl logs -n "${namespace}" "deployment/${mock_name}" >"${report_dir}/mock-access.log"
grep -q 'GET /control' "${report_dir}/mock-access.log"
[[ "$(grep -c 'POST /v1/invalid-provider-response' "${report_dir}/mock-access.log" || true)" == "1" ]]

kubectl apply -n "${namespace}" -f - <<EOF
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: runtime-p0-ttl-cleanup
spec:
  target: {namespace: ${namespace}, apiVersion: apps/v1, kind: Deployment, name: ${target_name}}
  mode: readOnly
  ttlSeconds: 2
  modelProviderRef: {name: heuristic-provider}
  evidenceRetention: {mode: RawSnapshot}
  dataSources: [{name: kubernetes-events}]
EOF
wait_for_status runtime-p0-ttl-cleanup Failed
kubectl wait --for=delete investigationrequest/runtime-p0-ttl-cleanup -n "${namespace}" --timeout="${timeout}"

kubectl get remediationplan,agentaction -n "${namespace}" -o json | jq -r '.items[].metadata.uid' | sort >"${report_dir}/side-effect-uids-after.txt"
comm -13 "${report_dir}/side-effect-uids-before.txt" "${report_dir}/side-effect-uids-after.txt" >"${report_dir}/unexpected-side-effect-uids.txt"
if [[ -s "${report_dir}/unexpected-side-effect-uids.txt" ]]; then
  echo "unexpected RemediationPlan or AgentAction objects were created" >&2
  cat "${report_dir}/unexpected-side-effect-uids.txt" >&2
  exit 1
fi

controller_image="$(kubectl get deployment fluxseer-rca-controller-manager -n "${namespace}" -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].image}')"
jq -n \
  --arg runID "${run_id}" \
  --arg sourceCommit "$(git -C "${repo_root}" rev-parse HEAD)" \
  --arg context "$(kubectl config current-context)" \
  --arg namespace "${namespace}" \
  --arg controllerImage "${controller_image}" \
  '{runID:$runID, sourceCommit:$sourceCommit, kubernetesContext:$context, namespace:$namespace, controllerImage:$controllerImage, result:"PASS", scenarioCount:15, providerPolicyScenarios:2, directScenarios:12, ttlCleanupScenarios:1, blockedOrAbsentRiskSignalScenarios:14, boundedUnverifiedRiskSignals:1, unexpectedSideEffects:0}' \
  >"${report_dir}/summary.json"

cat >"${report_dir}/runtime-p0-matrix-report.md" <<EOF
# Runtime P0 Cluster Matrix Report

- Run ID: ${run_id}
- Source commit: $(git -C "${repo_root}" rev-parse HEAD)
- Source dirty: $(if [[ -n "$(git -C "${repo_root}" status --porcelain)" ]]; then echo true; else echo false; fi)
- Kubernetes context: $(kubectl config current-context)
- Namespace: ${namespace}
- Controller image: ${controller_image}
- Result: PASS

The matrix passed 15 P0 scenarios: two provider-policy access-log cases,
twelve direct public-CRD terminal cases, and TTL cleanup. Every retained
terminal request used the current metadata generation for status and all
conditions. Fourteen scenarios produced no RiskSignal; the unverified-RCA case
linked one bounded signal with \`RCAReady=False/RCAUnverified\`. No new
RemediationPlan or AgentAction objects were observed. The only hosted-provider
request was the single expected malformed-response call.
EOF

echo "runtime P0 cluster matrix passed"
echo "artifacts: ${report_dir}"
