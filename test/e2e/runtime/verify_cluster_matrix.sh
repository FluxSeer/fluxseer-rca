#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
namespace="${FLUXSEER_RCA_RUNTIME_NAMESPACE:-fluxseer-rca-test}"
timeout="${FLUXSEER_RCA_RUNTIME_TIMEOUT:-180s}"
report_root="${FLUXSEER_RCA_RUNTIME_REPORT_ROOT:-${repo_root}/reports/runtime}"
run_id="${FLUXSEER_RCA_RUNTIME_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
report_dir="${report_root}/fluxseer-rca-runtime-access-log-${run_id}"
target_name="runtime-matrix-target"
mock_name="runtime-provider-mock"
denied_provider="runtime-openai-policy-denied"
rejected_provider="runtime-openai-policy-rejected"
denied_request="runtime-provider-policy-denied"
rejected_request="runtime-provider-policy-rejected"

if [[ -z "${KUBECONFIG:-}" ]]; then
  echo "KUBECONFIG must point to the explicitly authorized test cluster" >&2
  exit 1
fi

for command_name in kubectl jq git; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "required command not found: ${command_name}" >&2
    exit 1
  fi
done

mkdir -p "${report_dir}"

cleanup() {
  kubectl delete investigationrequest "${denied_request}" "${rejected_request}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete modelprovider "${denied_provider}" "${rejected_provider}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete secret runtime-matrix-openai-secret -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete event runtime-matrix-backoff -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete deployment "${target_name}" "${mock_name}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete service "${mock_name}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete configmap runtime-provider-mock-nginx -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

wait_for_phase() {
  local request_name="$1"
  local expected="$2"
  kubectl wait --for="jsonpath={.status.phase}=${expected}" --timeout="${timeout}" "investigationrequest/${request_name}" -n "${namespace}"
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

assert_terminal_contract() {
  local request_json="$1"
  local expected_reason="$2"
  jq -e --arg reason "${expected_reason}" '
    .metadata.generation as $generation |
    .status.phase == "Failed" and
    .status.outcome == "Unknown" and
    .status.failure.code == $reason and
    (.status.conditions | length > 0) and
    all(.status.conditions[]; .observedGeneration == $generation) and
    ((.status.linkedRiskSignalRef.name // "") == "") and
    (.status.execution.egressAudit.decision == "Rejected")
  ' <<<"${request_json}" >/dev/null
  assert_condition "${request_json}" "RCAReady" "False" "${expected_reason}"
  assert_condition "${request_json}" "Degraded" "False" "${expected_reason}"
}

cleanup

kubectl get namespace "${namespace}" >/dev/null
kubectl get deployment fluxseer-rca-controller-manager -n "${namespace}" -o yaml >"${report_dir}/controller-deployment.yaml"
kubectl get pods -n "${namespace}" -l app.kubernetes.io/component=controller-manager -o yaml >"${report_dir}/controller-pods.yaml"

kubectl apply -n "${namespace}" -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: runtime-provider-mock-nginx
data:
  default.conf: |
    server {
      listen 8080;
      access_log /dev/stdout combined;
      error_log /dev/stderr warn;
      location / {
        default_type application/json;
        return 200 '{"id":"runtime-mock","choices":[{"message":{"content":"{\"riskTitle\":\"runtime mock\",\"riskSummary\":\"unexpected provider request\",\"severity\":\"low\",\"confidenceScore\":1,\"rationale\":\"unexpected\",\"rcaHypothesis\":\"unexpected\",\"rcaCauses\":[\"unexpected\"],\"actionType\":\"notification.sendSlack\"}"}}]}';
      }
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: runtime-provider-mock
spec:
  replicas: 1
  selector:
    matchLabels:
      app: runtime-provider-mock
  template:
    metadata:
      labels:
        app: runtime-provider-mock
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
            name: runtime-provider-mock-nginx
---
apiVersion: v1
kind: Service
metadata:
  name: runtime-provider-mock
spec:
  selector:
    app: runtime-provider-mock
  ports:
    - name: http
      port: 8080
      targetPort: http
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: runtime-matrix-target
  labels:
    app: runtime-matrix-target
spec:
  replicas: 1
  selector:
    matchLabels:
      app: runtime-matrix-target
  template:
    metadata:
      labels:
        app: runtime-matrix-target
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
  name: runtime-matrix-openai-secret
type: Opaque
stringData:
  api-key: runtime-test-token
EOF

kubectl rollout status "deployment/${mock_name}" -n "${namespace}" --timeout="${timeout}"
kubectl rollout status "deployment/${target_name}" -n "${namespace}" --timeout="${timeout}"

# Prove that the access log is observable without touching either policy path.
kubectl exec -n "${namespace}" "deployment/${mock_name}" -- wget -qO- http://127.0.0.1:8080/control >/dev/null

target_uid="$(kubectl get deployment "${target_name}" -n "${namespace}" -o jsonpath='{.metadata.uid}')"
kubectl apply -n "${namespace}" -f - <<EOF
apiVersion: v1
kind: Event
metadata:
  name: runtime-matrix-backoff
involvedObject:
  apiVersion: apps/v1
  kind: Deployment
  name: ${target_name}
  namespace: ${namespace}
  uid: ${target_uid}
reason: BackOff
message: runtime matrix synthetic backoff evidence
source:
  component: runtime-matrix
type: Warning
firstTimestamp: "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
lastTimestamp: "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
count: 1
EOF

kubectl apply -n "${namespace}" -f - <<EOF
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: ${denied_provider}
spec:
  provider: openai
  model: runtime-mock
  endpoint: http://${mock_name}:8080/v1/policy-denied
  dataPolicy:
    allowExternalTransmission: false
    maximumClassification: Confidential
  apiKeySecretRef:
    name: runtime-matrix-openai-secret
    key: api-key
---
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: ${rejected_provider}
spec:
  provider: openai
  model: runtime-mock
  endpoint: http://${mock_name}:8080/v1/policy-rejected
  dataPolicy:
    allowExternalTransmission: true
    maximumClassification: Public
  apiKeySecretRef:
    name: runtime-matrix-openai-secret
    key: api-key
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: ${denied_request}
spec:
  target:
    namespace: ${namespace}
    apiVersion: apps/v1
    kind: Deployment
    name: ${target_name}
  mode: readOnly
  modelProviderRef:
    name: ${denied_provider}
  queries:
    - name: synthetic-backoff
      datasourceRef:
        name: kubernetes-events
      queryType: event
      reasons: [BackOff]
      queryTemplate: recent-events
  question: Verify hosted provider egress denial before request
  createRiskSignal: true
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: ${rejected_request}
spec:
  target:
    namespace: ${namespace}
    apiVersion: apps/v1
    kind: Deployment
    name: ${target_name}
  mode: readOnly
  modelProviderRef:
    name: ${rejected_provider}
  queries:
    - name: synthetic-backoff
      datasourceRef:
        name: kubernetes-events
      queryType: event
      reasons: [BackOff]
      queryTemplate: recent-events
  question: Verify classification rejection before provider request
  createRiskSignal: true
EOF

wait_for_phase "${denied_request}" Failed
wait_for_phase "${rejected_request}" Failed

denied_json="$(kubectl get investigationrequest "${denied_request}" -n "${namespace}" -o json)"
rejected_json="$(kubectl get investigationrequest "${rejected_request}" -n "${namespace}" -o json)"
jq . <<<"${denied_json}" >"${report_dir}/provider-data-policy-denied.json"
jq . <<<"${rejected_json}" >"${report_dir}/provider-data-policy-rejected.json"

if ! assert_terminal_contract "${denied_json}" ProviderDataPolicyDenied; then
  echo "ProviderDataPolicyDenied terminal contract mismatch" >&2
  jq '{generation: .metadata.generation, status: .status}' <<<"${denied_json}" >&2
  exit 1
fi
if ! assert_terminal_contract "${rejected_json}" ProviderDataPolicyRejected; then
  echo "ProviderDataPolicyRejected terminal contract mismatch" >&2
  jq '{generation: .metadata.generation, status: .status}' <<<"${rejected_json}" >&2
  exit 1
fi
jq -e '.status.execution.egressAudit.reason == "ExternalTransmissionDisabled"' <<<"${denied_json}" >/dev/null
jq -e '.status.execution.egressAudit.reason == "ClassificationExceeded"' <<<"${rejected_json}" >/dev/null

kubectl logs -n "${namespace}" "deployment/${mock_name}" >"${report_dir}/provider-access.log"
grep -q 'GET /control' "${report_dir}/provider-access.log"
if grep -q '/v1/policy-denied' "${report_dir}/provider-access.log"; then
  echo "provider policy denied path unexpectedly reached the mock provider" >&2
  exit 1
fi
if grep -q '/v1/policy-rejected' "${report_dir}/provider-access.log"; then
  echo "provider policy rejected path unexpectedly reached the mock provider" >&2
  exit 1
fi

kubectl get investigationrequest "${denied_request}" -n "${namespace}" -o yaml >"${report_dir}/provider-data-policy-denied.yaml"
kubectl get investigationrequest "${rejected_request}" -n "${namespace}" -o yaml >"${report_dir}/provider-data-policy-rejected.yaml"
kubectl get event runtime-matrix-backoff -n "${namespace}" -o yaml >"${report_dir}/synthetic-evidence-event.yaml"

controller_image="$(kubectl get deployment fluxseer-rca-controller-manager -n "${namespace}" -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].image}')"
controller_pod="$(kubectl get pods -n "${namespace}" -l app.kubernetes.io/component=controller-manager -o jsonpath='{.items[0].metadata.name}')"
controller_uid="$(kubectl get pod "${controller_pod}" -n "${namespace}" -o jsonpath='{.metadata.uid}')"
cat >"${report_dir}/runtime-access-log-report.md" <<EOF
# Runtime Provider Policy Access-Log Report

- Run ID: ${run_id}
- Source commit: $(git -C "${repo_root}" rev-parse HEAD)
- Source dirty: $(if [[ -n "$(git -C "${repo_root}" status --porcelain)" ]]; then echo true; else echo false; fi)
- Kubernetes context: $(kubectl config current-context)
- Namespace: ${namespace}
- Controller image: ${controller_image}
- Controller pod UID: ${controller_uid}

## Results

| Scenario | Phase | Outcome | Reason | Provider access-log matches | Result |
| --- | --- | --- | --- | --- | --- |
| ProviderDataPolicyDenied | Failed | Unknown | ExternalTransmissionDisabled | 0 | PASS |
| ProviderDataPolicyRejected | Failed | Unknown | ClassificationExceeded | 0 | PASS |

Both scenarios verified that every terminal condition uses the current
\`metadata.generation\`, no \`RiskSignal\` was linked, the egress audit decision
was \`Rejected\`, and the provider endpoint was not reached. The \`/control\`
request in \`provider-access.log\` proves that access logging was operational.
EOF

echo "runtime access-log matrix passed"
echo "artifacts: ${report_dir}"
