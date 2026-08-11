#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
namespace="${FLUXSEER_RCA_RUNTIME_NAMESPACE:-fluxseer-rca-test}"
timeout="${FLUXSEER_RCA_RUNTIME_TIMEOUT:-180s}"
report_root="${FLUXSEER_RCA_RUNTIME_REPORT_ROOT:-${repo_root}/reports/runtime}"
run_id="${FLUXSEER_RCA_RUNTIME_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
report_dir="${report_root}/fluxseer-rca-runtime-canonical-workloads-${run_id}"
mock_name="runtime-canonical-provider-mock"
oom_target="runtime-canonical-oom"
image_target="runtime-canonical-imagepull"
oom_request="runtime-canonical-oom-event-only"
image_request="runtime-canonical-imagepull"

if [[ -z "${KUBECONFIG:-}" ]]; then
  echo "KUBECONFIG must point to the explicitly authorized test cluster" >&2
  exit 1
fi

for command_name in kubectl jq git sort comm; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "required command not found: ${command_name}" >&2
    exit 1
  fi
done

mkdir -p "${report_dir}"

cleanup() {
  kubectl delete investigationrequest "${oom_request}" "${image_request}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  while IFS= read -r signal_name; do
    [[ -z "${signal_name}" ]] || kubectl delete risksignal "${signal_name}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  done < <(kubectl get risksignal -n "${namespace}" -o json 2>/dev/null | jq -r '.items[] | select((.spec.investigationRef.name // "") | startswith("runtime-canonical-")) | .metadata.name' 2>/dev/null || true)
  kubectl delete modelprovider runtime-canonical-oom-provider runtime-canonical-image-provider -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete secret runtime-canonical-provider-secret -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete event runtime-canonical-oom-event runtime-canonical-image-event -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete statefulset "${oom_target}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete pod "${image_target}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete deployment "${mock_name}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete service "${mock_name}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete configmap runtime-canonical-provider-nginx -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl wait --for=delete "pod/${image_target}" "pod/${oom_target}-0" -n "${namespace}" --timeout=60s >/dev/null 2>&1 || true
  kubectl wait --for=delete pod -l app=runtime-canonical-provider-mock -n "${namespace}" --timeout=60s >/dev/null 2>&1 || true
  while IFS= read -r event_name; do
    [[ -z "${event_name}" ]] || kubectl delete event "${event_name}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  done < <(kubectl get events -n "${namespace}" -o json 2>/dev/null | jq -r '.items[] | select((.involvedObject.name // "") | startswith("runtime-canonical-")) | .metadata.name' 2>/dev/null || true)
}
trap cleanup EXIT INT TERM

wait_for_phase() {
  local request_name="$1"
  local phase="$2"
  kubectl wait --for="jsonpath={.status.phase}=${phase}" --timeout="${timeout}" "investigationrequest/${request_name}" -n "${namespace}"
}

assert_condition() {
  local object_json="$1"
  local condition_type="$2"
  local expected_status="$3"
  local expected_reason="$4"
  jq -e --arg type "${condition_type}" --arg status "${expected_status}" --arg reason "${expected_reason}" '
    any(.status.conditions[]; .type == $type and .status == $status and .reason == $reason)
  ' <<<"${object_json}" >/dev/null
}

assert_generation_contract() {
  local request_json="$1"
  jq -e '
    .metadata.generation as $generation |
    .status.observedGeneration == $generation and
    (.status.conditions | length > 0) and
    all(.status.conditions[]; .observedGeneration == $generation)
  ' <<<"${request_json}" >/dev/null
}

cleanup

kubectl get namespace "${namespace}" >/dev/null
kubectl get deployment fluxseer-rca-controller-manager -n "${namespace}" -o yaml >"${report_dir}/controller-deployment.yaml"
kubectl get pods -n "${namespace}" -l app.kubernetes.io/component=controller-manager -o yaml >"${report_dir}/controller-pods.yaml"
kubectl get remediationplan,agentaction -n "${namespace}" -o json | jq -r '.items[].metadata.uid' | sort >"${report_dir}/side-effect-uids-before.txt"

kubectl apply -n "${namespace}" -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: runtime-canonical-provider-nginx
data:
  imagepull.json: |
    {"id":"runtime-imagepull-001","choices":[{"message":{"content":"{\"riskTitle\":\"ImagePullBackOff on runtime pod\",\"riskSummary\":\"The pod cannot pull the configured image.\",\"severity\":\"medium\",\"confidenceScore\":80,\"rationale\":\"event evidence reports ErrImagePull\",\"rcaHypothesis\":\"The pod image pull is failing because the image reference is unavailable.\",\"rcaCauses\":[\"ErrImagePull events were observed\"],\"actionType\":\"notification.sendSlack\"}"}}],"usage":{"prompt_tokens":321,"completion_tokens":87}}
  default.conf: |
    server {
      listen 8080;
      access_log /dev/stdout combined;
      error_log /dev/stderr warn;
      location = /v1/imagepull {
        add_header x-request-id runtime-imagepull-001;
        proxy_method GET;
        proxy_pass http://127.0.0.1:8080/imagepull-static;
      }
      location = /imagepull-static {
        default_type application/json;
        root /usr/share/nginx/html;
        limit_rate 256;
      }
      location = /v1/oom-unexpected {
        default_type application/json;
        return 500 '{"error":"OOM event-only evidence gate unexpectedly called provider"}';
      }
      location = /control {
        default_type application/json;
        return 200 '{"ok":true}';
      }
      location / {
        default_type application/json;
        return 404 '{"error":"unexpected canonical workload request"}';
      }
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: runtime-canonical-provider-mock
spec:
  replicas: 1
  selector:
    matchLabels:
      app: runtime-canonical-provider-mock
  template:
    metadata:
      labels:
        app: runtime-canonical-provider-mock
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
            - name: config
              mountPath: /usr/share/nginx/html/imagepull-static
              subPath: imagepull.json
      volumes:
        - name: config
          configMap:
            name: runtime-canonical-provider-nginx
---
apiVersion: v1
kind: Service
metadata:
  name: runtime-canonical-provider-mock
spec:
  selector:
    app: runtime-canonical-provider-mock
  ports:
    - name: http
      port: 8080
      targetPort: http
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: runtime-canonical-oom
spec:
  serviceName: runtime-canonical-oom
  replicas: 1
  selector:
    matchLabels:
      app: runtime-canonical-oom
  template:
    metadata:
      labels:
        app: runtime-canonical-oom
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
kind: Pod
metadata:
  name: runtime-canonical-imagepull
  labels:
    app: runtime-canonical-imagepull
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
  name: runtime-canonical-provider-secret
type: Opaque
stringData:
  api-key: runtime-test-token
EOF

kubectl rollout status "deployment/${mock_name}" -n "${namespace}" --timeout="${timeout}"
kubectl rollout status "statefulset/${oom_target}" -n "${namespace}" --timeout="${timeout}"
kubectl wait --for=condition=Ready "pod/${image_target}" -n "${namespace}" --timeout="${timeout}"
kubectl exec -n "${namespace}" "deployment/${mock_name}" -- wget -qO- http://127.0.0.1:8080/control >/dev/null

oom_uid="$(kubectl get statefulset "${oom_target}" -n "${namespace}" -o jsonpath='{.metadata.uid}')"
image_uid="$(kubectl get pod "${image_target}" -n "${namespace}" -o jsonpath='{.metadata.uid}')"
now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
kubectl apply -n "${namespace}" -f - <<EOF
apiVersion: v1
kind: Event
metadata:
  name: runtime-canonical-oom-event
involvedObject:
  apiVersion: apps/v1
  kind: StatefulSet
  name: ${oom_target}
  namespace: ${namespace}
  uid: ${oom_uid}
reason: OOMKilled
message: runtime-canonical-oom-0 container was killed after exceeding memory limit
source:
  component: runtime-canonical-matrix
type: Warning
firstTimestamp: "${now}"
lastTimestamp: "${now}"
count: 1
---
apiVersion: v1
kind: Event
metadata:
  name: runtime-canonical-image-event
involvedObject:
  apiVersion: v1
  kind: Pod
  name: ${image_target}
  namespace: ${namespace}
  uid: ${image_uid}
reason: ErrImagePull
message: failed to pull image test-harbor.fluxseer.com/runtime/canonical:bad
source:
  component: runtime-canonical-matrix
type: Warning
firstTimestamp: "${now}"
lastTimestamp: "${now}"
count: 1
---
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: runtime-canonical-oom-provider
spec:
  provider: openai
  model: runtime-mock
  endpoint: http://${mock_name}:8080/v1/oom-unexpected
  dataPolicy:
    allowExternalTransmission: true
    maximumClassification: Confidential
  apiKeySecretRef:
    name: runtime-canonical-provider-secret
    key: api-key
---
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: runtime-canonical-image-provider
spec:
  provider: openai
  model: runtime-mock
  endpoint: http://${mock_name}:8080/v1/imagepull
  dataPolicy:
    allowExternalTransmission: true
    maximumClassification: Confidential
  apiKeySecretRef:
    name: runtime-canonical-provider-secret
    key: api-key
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: ${oom_request}
spec:
  target:
    namespace: ${namespace}
    apiVersion: apps/v1
    kind: StatefulSet
    name: ${oom_target}
  mode: readOnly
  createRiskSignal: true
  modelProviderRef: {name: runtime-canonical-oom-provider}
  evidenceRequirements: {profile: OOMKilled}
  queries:
    - name: oom-event
      datasourceRef: {name: kubernetes-events}
      queryType: event
      reasons: [OOMKilled]
---
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: ${image_request}
spec:
  target:
    namespace: ${namespace}
    apiVersion: v1
    kind: Pod
    name: ${image_target}
  mode: readOnly
  createRiskSignal: true
  modelProviderRef: {name: runtime-canonical-image-provider}
  evidenceRequirements: {profile: ImagePullBackOff}
  queries:
    - name: image-pull-event
      datasourceRef: {name: kubernetes-events}
      queryType: event
      reasons: [ErrImagePull]
EOF

wait_for_phase "${oom_request}" Completed
wait_for_phase "${image_request}" Completed

oom_json="$(kubectl get investigationrequest "${oom_request}" -n "${namespace}" -o json)"
image_json="$(kubectl get investigationrequest "${image_request}" -n "${namespace}" -o json)"
jq . <<<"${oom_json}" >"${report_dir}/oomkilled-event-only.json"
jq . <<<"${image_json}" >"${report_dir}/imagepullbackoff.json"
kubectl get investigationrequest "${oom_request}" -n "${namespace}" -o yaml >"${report_dir}/oomkilled-event-only.yaml"
kubectl get investigationrequest "${image_request}" -n "${namespace}" -o yaml >"${report_dir}/imagepullbackoff.yaml"
kubectl get event runtime-canonical-oom-event runtime-canonical-image-event -n "${namespace}" -o yaml >"${report_dir}/synthetic-events.yaml"

assert_generation_contract "${oom_json}"
assert_generation_contract "${image_json}"

jq -e '
  .status.phase == "Completed" and
  .status.outcome == "Inconclusive" and
  .status.failure == null and
  ((.status.linkedRiskSignalRef.name // "") == "") and
  .status.evidenceCoverage.profile == "OOMKilled" and
  (.status.evidenceCoverage.requiredChecks | contains(["event:OOMKilled", "metric:Memory"])) and
  (.status.evidenceCoverage.completedChecks | contains(["event:OOMKilled"])) and
  (.status.evidenceCoverage.incompleteChecks | contains(["metric:Memory"])) and
  .status.evidenceCoverage.issueMatches >= 1 and
  (.status.missingEvidence | any(.source == "metric")) and
  (.status.evidenceRefs | length == 1) and
  (.status.evidenceRefs[0].kind == "event") and
  (.status.evidenceRefs[0].reason == "OOMKilled") and
  (.status.evidenceRefs[0].contentDigest | startswith("sha256:")) and
  (.status.evidenceRefs[0].queryDigest | startswith("sha256:")) and
  (.status.execution == null)
' <<<"${oom_json}" >/dev/null
assert_condition "${oom_json}" EvidenceCollectionReady False RequiredEvidenceMissing
assert_condition "${oom_json}" RCAReady False RequiredEvidenceMissing
assert_condition "${oom_json}" Verified Unknown RCAUnavailable

jq -e '
  .status.phase == "Completed" and
  .status.outcome == "Confirmed" and
  .status.failure == null and
  .status.verdict.rootCauseType == "ImagePullFailure" and
  (.status.evidenceRefs | length == 1) and
  (.status.evidenceRefs[0].kind == "event") and
  (.status.evidenceRefs[0].reason == "ErrImagePull") and
  (.status.evidenceRefs[0].contentDigest | startswith("sha256:")) and
  (.status.claims | any(.verification == "Supported" and (.evidenceRefs | length > 0))) and
  .status.execution.attemptCount == 1 and
  .status.execution.durationSeconds >= 1 and
  .status.execution.inputTokens == 321 and
  .status.execution.outputTokens == 87 and
  (.status.execution.providerResult.digest.value | startswith("sha256:")) and
  ((.status.linkedRiskSignalRef.name // "") != "")
' <<<"${image_json}" >/dev/null
assert_condition "${image_json}" RCAReady True ProviderSucceeded
assert_condition "${image_json}" Verified True RootCauseClaimsSupported
assert_condition "${image_json}" RemediationReady True RootCauseVerified

image_signal="$(jq -r '.status.linkedRiskSignalRef.name' <<<"${image_json}")"
for _ in {1..30}; do
  image_signal_json="$(kubectl get risksignal "${image_signal}" -n "${namespace}" -o json)"
  if assert_condition "${image_signal_json}" RCAReady True RootCauseVerified; then
    break
  fi
  sleep 1
done
assert_condition "${image_signal_json}" RCAReady True RootCauseVerified
jq . <<<"${image_signal_json}" >"${report_dir}/imagepullbackoff-risksignal.json"
jq -e --arg request "${image_request}" '
  .spec.investigationRef.name == $request and
  .status.projection.mode == "InvestigationRequestProjection" and
  .status.projection.projectedFrom.name == $request
' <<<"${image_signal_json}" >/dev/null

kubectl logs -n "${namespace}" "deployment/${mock_name}" >"${report_dir}/provider-access.log"
grep -q 'GET /control' "${report_dir}/provider-access.log"
[[ "$(grep -c 'POST /v1/imagepull' "${report_dir}/provider-access.log" || true)" == "1" ]]
[[ "$(grep -c '/v1/oom-unexpected' "${report_dir}/provider-access.log" || true)" == "0" ]]

kubectl get remediationplan,agentaction -n "${namespace}" -o json | jq -r '.items[].metadata.uid' | sort >"${report_dir}/side-effect-uids-after.txt"
comm -13 "${report_dir}/side-effect-uids-before.txt" "${report_dir}/side-effect-uids-after.txt" >"${report_dir}/unexpected-side-effect-uids.txt"
if [[ -s "${report_dir}/unexpected-side-effect-uids.txt" ]]; then
  echo "unexpected RemediationPlan or AgentAction objects were created" >&2
  exit 1
fi

controller_image="$(kubectl get deployment fluxseer-rca-controller-manager -n "${namespace}" -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].image}')"
jq -n \
  --arg runID "${run_id}" \
  --arg sourceCommit "$(git -C "${repo_root}" rev-parse HEAD)" \
  --arg context "$(kubectl config current-context)" \
  --arg namespace "${namespace}" \
  --arg controllerImage "${controller_image}" \
  --argjson imagePullDurationSeconds "$(jq '.status.execution.durationSeconds' <<<"${image_json}")" \
  --argjson imagePullInputTokens "$(jq '.status.execution.inputTokens' <<<"${image_json}")" \
  --argjson imagePullOutputTokens "$(jq '.status.execution.outputTokens' <<<"${image_json}")" \
  '{runID:$runID, sourceCommit:$sourceCommit, kubernetesContext:$context, namespace:$namespace, controllerImage:$controllerImage, result:"PASS", scenarioCount:2, oomProviderRequests:0, imagePullProviderRequests:1, imagePullDurationSeconds:$imagePullDurationSeconds, imagePullInputTokens:$imagePullInputTokens, imagePullOutputTokens:$imagePullOutputTokens, unexpectedSideEffects:0}' \
  >"${report_dir}/summary.json"

cat >"${report_dir}/runtime-canonical-workloads-report.md" <<EOF
# Runtime Canonical Workload Evidence Report

- Run ID: ${run_id}
- Source commit: $(git -C "${repo_root}" rev-parse HEAD)
- Source dirty: $(if [[ -n "$(git -C "${repo_root}" status --porcelain)" ]]; then echo true; else echo false; fi)
- Kubernetes context: $(kubectl config current-context)
- Namespace: ${namespace}
- Controller image: ${controller_image}
- Result: PASS (2/2)

OOMKilled event-only completed Inconclusive with event coverage present,
\`metric:Memory\` incomplete, zero provider requests, and no RiskSignal.
ImagePullBackOff completed Confirmed with root cause \`ImagePullFailure\`, one
retained \`ErrImagePull\` evidence record, one supported structured claim, one
provider request, and a verified RiskSignal projection. Neither scenario
created a RemediationPlan or AgentAction. The successful provider path recorded
$(jq '.status.execution.durationSeconds' <<<"${image_json}") seconds of runtime
latency and provider-reported token usage of
$(jq '.status.execution.inputTokens' <<<"${image_json}") input /
$(jq '.status.execution.outputTokens' <<<"${image_json}") output tokens.
EOF

echo "runtime canonical workload evidence passed"
echo "artifacts: ${report_dir}"
