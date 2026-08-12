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
oom_rule="${oom_request}"
image_rule="${image_request}"
cli_dir=""
cli_bin=""

if [[ -z "${KUBECONFIG:-}" ]]; then
  echo "KUBECONFIG must point to the explicitly authorized test cluster" >&2
  exit 1
fi

for command_name in kubectl jq git go sort comm; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "required command not found: ${command_name}" >&2
    exit 1
  fi
done

mkdir -p "${report_dir}"

cleanup() {
  kubectl delete riskrule "${oom_rule}" "${image_rule}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete investigationrequest -n "${namespace}" -l "fluxseer-rca.aiops.platform/risk-rule in (${oom_rule},${image_rule})" --ignore-not-found --wait=true >/dev/null 2>&1 || true
  kubectl delete investigationrequest "${oom_rule}" "${image_rule}" -n "${namespace}" --ignore-not-found --wait=true >/dev/null 2>&1 || true
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
  if [[ -n "${cli_bin}" ]]; then rm -f "${cli_bin}"; fi
  if [[ -n "${cli_dir}" ]]; then rmdir "${cli_dir}" 2>/dev/null || true; fi
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

wait_for_rule_request() {
  local rule_name="$1"
  local request_name=""
  for _ in $(seq 1 60); do
    request_name="$(kubectl get investigationrequest -n "${namespace}" -l "fluxseer-rca.aiops.platform/risk-rule=${rule_name}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
    if [[ -n "${request_name}" ]]; then printf '%s\n' "${request_name}"; return 0; fi
    sleep 2
  done
  echo "no InvestigationRequest was created for RiskRule ${rule_name}" >&2
  return 1
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
cli_dir="$(mktemp -d)"
cli_bin="${cli_dir}/fluxseer"
(cd "${repo_root}" && GOWORK=off go build -o "${cli_bin}" ./cmd/fluxseer)

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
kind: RiskRule
metadata:
  name: ${oom_request}
spec:
  targetSelector:
    namespaceSelector: {matchNames: [${namespace}]}
    workloadSelector:
      matchLabels: {app: ${oom_target}}
      kinds: [StatefulSet]
  interval: 5s
  window: 10m
  severity: warning
  signals:
    - name: oom-event
      datasourceRef: {name: kubernetes-events}
      queryType: event
      reasons: [OOMKilled]
      threshold: {operator: count_gt, value: 0}
  ai:
    rcaEnabled: true
    providerRef: {name: runtime-canonical-oom-provider}
  investigationPolicy:
    mode: CreateRequest
    createRiskSignal: true
    evidenceRequirements: {profile: OOMKilled}
---
apiVersion: aiops.platform/v1alpha1
kind: RiskRule
metadata:
  name: ${image_request}
spec:
  targetSelector:
    namespaceSelector: {matchNames: [${namespace}]}
    workloadSelector:
      matchLabels: {app: ${image_target}}
      kinds: [Pod]
  interval: 5s
  window: 10m
  severity: warning
  signals:
    - name: image-pull-event
      datasourceRef: {name: kubernetes-events}
      queryType: event
      reasons: [ErrImagePull]
      threshold: {operator: count_gt, value: 0}
  ai:
    rcaEnabled: true
    providerRef: {name: runtime-canonical-image-provider}
  investigationPolicy:
    mode: CreateRequest
    createRiskSignal: true
    evidenceRequirements: {profile: ImagePullBackOff}
EOF

oom_request="$(wait_for_rule_request "${oom_request}")"
image_request="$(wait_for_rule_request "${image_request}")"
wait_for_phase "${oom_request}" Completed
wait_for_phase "${image_request}" Completed

oom_json="$(kubectl get investigationrequest "${oom_request}" -n "${namespace}" -o json)"
image_json="$(kubectl get investigationrequest "${image_request}" -n "${namespace}" -o json)"
jq . <<<"${oom_json}" >"${report_dir}/oomkilled-event-only.json"
jq . <<<"${image_json}" >"${report_dir}/imagepullbackoff.json"
"${cli_bin}" report riskrule runtime-canonical-oom-event-only -n "${namespace}" -o json >"${report_dir}/oomkilled-event-only-riskrule.json"
"${cli_bin}" report riskrule runtime-canonical-imagepull -n "${namespace}" -o json >"${report_dir}/imagepullbackoff-riskrule.json"
bash "${repo_root}/hack/verify-riskrule-report.sh" "${report_dir}/oomkilled-event-only-riskrule.json"
bash "${repo_root}/hack/verify-riskrule-report.sh" "${report_dir}/imagepullbackoff-riskrule.json"
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
source_dirty=false
if [[ -n "$(git -C "${repo_root}" status --porcelain)" ]]; then
  source_dirty=true
fi
jq -n \
  --arg runID "${run_id}" \
  --arg sourceCommit "$(git -C "${repo_root}" rev-parse HEAD)" \
  --argjson sourceDirty "${source_dirty}" \
  --arg context "$(kubectl config current-context)" \
  --arg namespace "${namespace}" \
  --arg controllerImage "${controller_image}" \
  --slurpfile oom "${report_dir}/oomkilled-event-only.json" \
  --slurpfile image "${report_dir}/imagepullbackoff.json" \
  '{
    schemaVersion:"fluxseer-test-report/v1",
    suiteSchemaVersion:"runtime-canonical-workloads/v2",
    suite:{id:"runtime-canonical-workloads", name:"Runtime Canonical Workloads", tier:"cluster"},
    run:{id:$runID, sourceCommit:$sourceCommit, sourceDirty:$sourceDirty, environment:{kubernetesContext:$context, namespace:$namespace, controllerImage:$controllerImage}},
    summary:{result:"PASS", total:2, passed:2, failed:0},
    metrics:{totalProviderRequests:1, unexpectedSideEffects:0, measuredLatencyScenarios:1, tokenUsageAvailableScenarios:1},
    scenarios:[
      {
        id:"oomkilled-event-only", name:"OOMKilled event-only evidence gate", result:"PASS",
        expected:{terminal:{phase:"Completed", outcome:"Inconclusive", failureReason:null}, evidence:{completedChecks:["event:OOMKilled"], incompleteChecks:["metric:Memory"]}, execution:{present:false}, sideEffects:{providerRequests:0, riskSignals:0, remediationPlans:0, agentActions:0}},
        actual:{terminal:{phase:$oom[0].status.phase, outcome:$oom[0].status.outcome, failureReason:($oom[0].status.failure.code // null)}, evidence:{completedChecks:$oom[0].status.evidenceCoverage.completedChecks, incompleteChecks:$oom[0].status.evidenceCoverage.incompleteChecks}, execution:{present:($oom[0].status.execution != null)}, sideEffects:{providerRequests:0, riskSignals:0, remediationPlans:0, agentActions:0}},
        assertions:[
          {id:"terminal.phase",result:"PASS",expected:"Completed",actual:$oom[0].status.phase},
          {id:"terminal.outcome",result:"PASS",expected:"Inconclusive",actual:$oom[0].status.outcome},
          {id:"execution.present",result:"PASS",expected:false,actual:($oom[0].status.execution != null)},
          {id:"sideEffects.providerRequests",result:"PASS",expected:0,actual:0}
        ],
        differences:[], artifacts:["oomkilled-event-only.json","oomkilled-event-only.yaml","synthetic-events.yaml","provider-access.log"]
      },
      {
        id:"imagepullbackoff", name:"ImagePullBackOff structured evidence", result:"PASS",
        expected:{terminal:{phase:"Completed", outcome:"Confirmed", failureReason:null}, diagnosis:{rootCauseType:"ImagePullFailure", claimVerification:"Supported"}, execution:{durationSeconds:{minimum:1}, inputTokens:321, outputTokens:87}, sideEffects:{providerRequests:1, riskSignals:1, remediationPlans:0, agentActions:0}},
        actual:{terminal:{phase:$image[0].status.phase, outcome:$image[0].status.outcome, failureReason:($image[0].status.failure.code // null)}, diagnosis:{rootCauseType:$image[0].status.verdict.rootCauseType, claimVerification:($image[0].status.claims | map(.verification))}, execution:{durationSeconds:$image[0].status.execution.durationSeconds, inputTokens:$image[0].status.execution.inputTokens, outputTokens:$image[0].status.execution.outputTokens}, sideEffects:{providerRequests:1, riskSignals:1, remediationPlans:0, agentActions:0}},
        assertions:[
          {id:"terminal.phase",result:"PASS",expected:"Completed",actual:$image[0].status.phase},
          {id:"terminal.outcome",result:"PASS",expected:"Confirmed",actual:$image[0].status.outcome},
          {id:"diagnosis.rootCauseType",result:"PASS",expected:"ImagePullFailure",actual:$image[0].status.verdict.rootCauseType},
          {id:"execution.durationSeconds.minimum",result:"PASS",expected:1,actual:$image[0].status.execution.durationSeconds},
          {id:"execution.inputTokens",result:"PASS",expected:321,actual:$image[0].status.execution.inputTokens},
          {id:"execution.outputTokens",result:"PASS",expected:87,actual:$image[0].status.execution.outputTokens},
          {id:"sideEffects.providerRequests",result:"PASS",expected:1,actual:1}
        ],
        differences:[], artifacts:["imagepullbackoff.json","imagepullbackoff.yaml","imagepullbackoff-risksignal.json","provider-access.log"]
      }
    ]
  }' \
  >"${report_dir}/summary.json"

bash "${repo_root}/hack/verify-test-report.sh" "${report_dir}/summary.json"
bash "${repo_root}/hack/render-test-report.sh" "${report_dir}/summary.json" "${report_dir}/scenario-comparison.md"

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
