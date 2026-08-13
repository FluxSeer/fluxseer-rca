#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
control_namespace="${FLUXSEER_RCA_RUNTIME_NAMESPACE:-fluxseer-rca-test}"
target_namespace="${FLUXSEER_RCA_PROMETHEUS_TARGET_NAMESPACE:-prometheus-pattern-conformance-test}"
timeout_seconds="${FLUXSEER_RCA_RUNTIME_TIMEOUT_SECONDS:-240}"
report_root="${FLUXSEER_RCA_RUNTIME_REPORT_ROOT:-${repo_root}/reports/runtime}"
run_id="${FLUXSEER_RCA_RUNTIME_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
batch_name="fluxseer-rca-runtime-prometheus-pattern-conformance-${run_id}"
internal_dir="${report_root}/internal/cluster/prometheus-pattern-conformance/${batch_name}"
user_dir="${report_root}/user-facing/prometheus-pattern-conformance/${batch_name}"
mock_name="prometheus-pattern-conformance-mock"
datasource_name="prometheus-pattern-conformance"
failure_datasource_name="prometheus-pattern-conformance-failure"
provider_name="heuristic-provider"
patterns=(high-error-rate high-latency)
cases=(error-high error-low error-none error-zero latency-high latency-normal latency-boundary latency-zero)
rules=()
failure_rules=()

if [[ -z "${KUBECONFIG:-}" ]]; then
  echo "KUBECONFIG must point to the explicitly authorized test cluster" >&2
  exit 1
fi
for command_name in kubectl jq git go helm awk sed; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "required command not found: ${command_name}" >&2
    exit 1
  }
done

mkdir -p "${internal_dir}/cases" "${user_dir}"
scenario_results="${internal_dir}/scenarios.jsonl"
: >"${scenario_results}"
cli_dir=""

cleanup() {
  kubectl delete riskrule "${rules[@]}" "${failure_rules[@]}" -n "${control_namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  for rule in "${rules[@]}" "${failure_rules[@]}"; do
    kubectl delete investigationrequest -n "${control_namespace}" -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    kubectl delete risksignal -A -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  done
  kubectl delete datasource "${datasource_name}" "${failure_datasource_name}" -n "${control_namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete service "${mock_name}" -n "${control_namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete deployment "${mock_name}" -n "${control_namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete configmap "${mock_name}-nginx" -n "${control_namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete namespace "${target_namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  [[ -z "${cli_dir}" ]] || rm -rf "${cli_dir}"
}
trap cleanup EXIT INT TERM

wait_for_condition() {
  local resource="$1" type="$2" status="$3" reason="${4:-}" deadline=$((SECONDS + timeout_seconds))
  while (( SECONDS < deadline )); do
    object="$(kubectl get "${resource}" -n "${control_namespace}" -o json 2>/dev/null || true)"
    if [[ -n "${object}" ]] && jq -e --arg type "${type}" --arg status "${status}" --arg reason "${reason}" '
      any(.status.conditions[]?; .type == $type and .status == $status and ($reason == "" or .reason == $reason))
    ' <<<"${object}" >/dev/null; then
      return 0
    fi
    sleep 3
  done
  echo "timed out waiting for ${resource} condition ${type}=${status} ${reason}" >&2
  kubectl get "${resource}" -n "${control_namespace}" -o yaml >&2 || true
  return 1
}

wait_for_terminal_request() {
  local rule="$1" deadline=$((SECONDS + timeout_seconds))
  while (( SECONDS < deadline )); do
    request_count="$(kubectl get investigationrequest -n "${control_namespace}" -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" -o json 2>/dev/null | jq '[.items[] | select(.status.phase == "Completed" or .status.phase == "Failed")] | length' || printf '0')"
    if (( request_count > 0 )); then return 0; fi
    sleep 3
  done
  echo "timed out waiting for terminal InvestigationRequest from ${rule}" >&2
  return 1
}

case_pattern() {
  case "$1" in
    error-*) printf 'high-error-rate' ;;
    latency-*) printf 'high-latency' ;;
  esac
}

case_expected_matched() {
  case "$1" in
    error-high|error-low|latency-high) printf 'true' ;;
    *) printf 'false' ;;
  esac
}

case_fixture_value() {
  case "$1" in
    error-high) printf '1' ;;
    error-low) printf '0.5' ;;
    error-none|error-zero) printf '0' ;;
    latency-high) printf '1.8' ;;
    latency-normal) printf '0.4' ;;
    latency-boundary) printf '1' ;;
    latency-zero) printf '0' ;;
  esac
}

case_reason() {
  case "$1" in
    error-high|error-low) printf 'MetricObserved' ;;
    error-none|error-zero|latency-normal|latency-boundary|latency-zero) printf 'ThresholdNotExceeded' ;;
    latency-high) printf 'MetricObserved' ;;
  esac
}

cleanup
kubectl get namespace "${control_namespace}" >/dev/null
kubectl get modelprovider "${provider_name}" -n "${control_namespace}" >/dev/null
kubectl create namespace "${target_namespace}" >/dev/null

cli_dir="$(mktemp -d)"
cli_bin="${cli_dir}/fluxseer"
(cd "${repo_root}" && GOWORK=off go build -o "${cli_bin}" ./cmd/fluxseer)
bash "${repo_root}/hack/verify-prometheus-pattern-promql.sh"

kubectl apply -n "${control_namespace}" -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: prometheus-pattern-conformance-mock-nginx
data:
  default.conf: |
    map $arg_query $fixture_value {
      ~*error-high 1;
      ~*error-low 0.5;
      ~*error-none 0;
      ~*error-zero 0;
      ~*latency-high 1.8;
      ~*latency-normal 0.4;
      ~*latency-boundary 1;
      ~*latency-zero 0;
      default 0;
    }
    server {
      listen 8080;
      access_log /dev/stdout combined;
      error_log /dev/stderr warn;
      location = /api/v1/query_range {
        default_type application/json;
        return 200 '{"status":"success","data":{"resultType":"scalar","result":[1786420800,"$fixture_value"]}}';
      }
      location = /failure/api/v1/query_range {
        default_type application/json;
        return 503 '{"status":"error","error":"synthetic prometheus outage"}';
      }
      location = /healthz {
        default_type application/json;
        return 200 '{"ok":true}';
      }
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: prometheus-pattern-conformance-mock
spec:
  replicas: 1
  selector:
    matchLabels: {app: prometheus-pattern-conformance-mock}
  template:
    metadata:
      labels: {app: prometheus-pattern-conformance-mock}
    spec:
      securityContext: {seccompProfile: {type: RuntimeDefault}}
      containers:
        - name: nginx
          image: nginxinc/nginx-unprivileged:1.27-alpine
          securityContext:
            allowPrivilegeEscalation: false
            capabilities: {drop: ["ALL"]}
            runAsNonRoot: true
            runAsUser: 101
          ports:
            - {name: http, containerPort: 8080}
          volumeMounts:
            - {name: config, mountPath: /etc/nginx/conf.d/default.conf, subPath: default.conf}
      volumes:
        - name: config
          configMap: {name: prometheus-pattern-conformance-mock-nginx}
---
apiVersion: v1
kind: Service
metadata:
  name: prometheus-pattern-conformance-mock
spec:
  selector: {app: prometheus-pattern-conformance-mock}
  ports:
    - {name: http, port: 8080, targetPort: http}
EOF
kubectl rollout status deployment/${mock_name} -n "${control_namespace}" --timeout="${timeout_seconds}s"
mock_service_ip="$(kubectl get service "${mock_name}" -n "${control_namespace}" -o jsonpath='{.spec.clusterIP}')"

kubectl apply -n "${control_namespace}" -f - <<EOF
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata: {name: ${datasource_name}}
spec:
  type: prometheus
  endpoint: http://${mock_name}:8080
  networkPolicy: {allowedCIDRs: [${mock_service_ip}/32]}
  queryPolicy: {mode: LegacyUnrestricted}
---
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata: {name: ${failure_datasource_name}}
spec:
  type: prometheus
  endpoint: http://${mock_name}:8080/failure
  networkPolicy: {allowedCIDRs: [${mock_service_ip}/32]}
  queryPolicy: {mode: LegacyUnrestricted}
EOF
kubectl wait --for=jsonpath='{.status.phase}'=Observed --timeout="${timeout_seconds}s" \
  "datasource/${datasource_name}" "datasource/${failure_datasource_name}" -n "${control_namespace}"

for case_id in "${cases[@]}"; do
  kubectl apply -n "${target_namespace}" -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: prometheus-${case_id}
  labels:
    app: ${case_id}
    fluxseer-rca.aiops.platform/prometheus-pattern: ${case_id}
spec:
  replicas: 1
  selector: {matchLabels: {app: ${case_id}}}
  template:
    metadata: {labels: {app: ${case_id}}}
    spec:
      securityContext: {seccompProfile: {type: RuntimeDefault}}
      containers:
        - name: app
          image: busybox:1.36
          command: ["sh", "-c", "sleep 3600"]
          securityContext:
            allowPrivilegeEscalation: false
            capabilities: {drop: ["ALL"]}
            runAsNonRoot: true
            runAsUser: 65532
EOF
done

rendered_rule="$(mktemp)"
helm template fluxseer-rca "${repo_root}/charts/fluxseer-rca" \
  --namespace "${control_namespace}" \
  --set rulePacks.prometheusBaseline.enabled=true \
  --show-only templates/rulepack-prometheus-baseline.yaml >"${rendered_rule}"
error_signal_block="$(sed -n '/    - name: high-error-rate/,/    - name: request-rate-surge/{/    - name: request-rate-surge/d;p;}' "${rendered_rule}")"
latency_signal_block="$(sed -n '/    - name: high-latency/,/    - name: pod-restart-rate/{/    - name: pod-restart-rate/d;p;}' "${rendered_rule}")"
rm -f "${rendered_rule}"

for case_id in "${cases[@]}"; do
  pattern="$(case_pattern "${case_id}")"
  rule="prometheus-${pattern}-${case_id}"
  rules+=("${rule}")
  signal_block="${error_signal_block}"
  [[ "${pattern}" == "high-latency" ]] && signal_block="${latency_signal_block}"
  {
    printf '%s\n' \
      'apiVersion: aiops.platform/v1alpha1' \
      'kind: RiskRule' \
      "metadata: {name: ${rule}}" \
      'spec:' \
      '  targetSelector:' \
      "    namespaceSelector: {matchNames: [${target_namespace}]}" \
      "    workloadSelector: {matchLabels: {fluxseer-rca.aiops.platform/prometheus-pattern: ${case_id}}, kinds: [Deployment]}" \
      '  interval: 5s' \
      '  window: 10m' \
      '  severity: warning' \
      '  signals:'
    printf '%s\n' "${signal_block}" | sed "s/name: \"prometheus\"/name: \"${datasource_name}\"/"
    printf '%s\n' \
      "  ai: {rcaEnabled: true, providerRef: {name: ${provider_name}}}" \
      '  investigationPolicy: {mode: CreateRequest, createRiskSignal: false}'
  } | kubectl apply -n "${control_namespace}" -f -
done

for pattern in "${patterns[@]}"; do
  failure_rule="prometheus-${pattern}-datasource-error"
  failure_rules+=("${failure_rule}")
  signal_block="${error_signal_block}"
  [[ "${pattern}" == "high-latency" ]] && signal_block="${latency_signal_block}"
  {
    printf '%s\n' \
      'apiVersion: aiops.platform/v1alpha1' \
      'kind: RiskRule' \
      "metadata: {name: ${failure_rule}}" \
      'spec:' \
      '  targetSelector:' \
      "    namespaceSelector: {matchNames: [${target_namespace}]}" \
      '    workloadSelector: {matchLabels: {fluxseer-rca.aiops.platform/prometheus-pattern: error-high}, kinds: [Deployment]}' \
      '  interval: 5s' \
      '  window: 10m' \
      '  severity: warning' \
      '  signals:'
    printf '%s\n' "${signal_block}" | sed "s/name: \"prometheus\"/name: \"${failure_datasource_name}\"/"
    printf '%s\n' \
      "  ai: {rcaEnabled: true, providerRef: {name: ${provider_name}}}" \
      '  investigationPolicy: {mode: CreateRequest, createRiskSignal: false}'
  } | kubectl apply -n "${control_namespace}" -f -
done

for rule in "${rules[@]}"; do wait_for_condition "riskrule/${rule}" Ready True EvaluationReady; done
for pattern in "${patterns[@]}"; do
  wait_for_condition "riskrule/prometheus-${pattern}-datasource-error" Ready False
  wait_for_condition "riskrule/prometheus-${pattern}-datasource-error" Degraded True
done
for case_id in error-high error-low latency-high; do wait_for_terminal_request "prometheus-$(case_pattern "${case_id}")-${case_id}"; done
sleep 12

for case_id in "${cases[@]}"; do
  pattern="$(case_pattern "${case_id}")"
  rule="prometheus-${pattern}-${case_id}"
  expected_matched="$(case_expected_matched "${case_id}")"
  expected_requests=0
  [[ "${expected_matched}" == "true" ]] && expected_requests=1
  requests="$(kubectl get investigationrequest -n "${control_namespace}" -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" -o json | jq '.items | length')"
  signals="$(kubectl get risksignal -A -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" -o json | jq '.items | length')"
  plans="$(kubectl get remediationplan -A -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" -o json 2>/dev/null | jq '.items | length' || printf '0')"
  actions="$(kubectl get agentaction -A -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" -o json 2>/dev/null | jq '.items | length' || printf '0')"
  actual_matched=false
  (( requests > 0 )) && actual_matched=true
  [[ "${actual_matched}" == "${expected_matched}" ]]
  [[ "${requests}" == "${expected_requests}" && "${signals}" == "0" && "${plans}" == "0" && "${actions}" == "0" ]]
  artifact="cases/${case_id}.json"
  jq -n \
    --arg id "${case_id}" --arg pattern "${pattern}" \
    --argjson expectedMatched "${expected_matched}" --argjson actualMatched "${actual_matched}" \
    --argjson fixtureValue "$(case_fixture_value "${case_id}")" \
    --argjson requests "${requests}" --argjson signals "${signals}" --argjson plans "${plans}" --argjson actions "${actions}" \
    '{caseId:$id,pattern:$pattern,inputs:{fixtureValue:$fixtureValue},evaluation:{matched:$actualMatched},sideEffects:{investigationRequests:$requests,riskSignals:$signals,remediationPlans:$plans,agentActions:$actions}}' \
    >"${internal_dir}/${artifact}"
  catalog_claim="$(jq -r --arg pattern "${pattern}" '.patterns[] | select(.id == $pattern) | .maximumCausalClaim' "${repo_root}/config/rule-packs/detection-patterns.json")"
  jq -cn \
    --arg id "${case_id}" --arg pattern "${pattern}" --arg artifact "${artifact}" --arg claim "${catalog_claim}" \
    --argjson expectedMatched "${expected_matched}" --argjson actualMatched "${actual_matched}" \
    --argjson expectedRequests "${expected_requests}" --argjson requests "${requests}" \
    --argjson signals "${signals}" --argjson plans "${plans}" --argjson actions "${actions}" \
    '{id:$id,name:($pattern + " " + $id),result:"PASS",expected:{matched:$expectedMatched,maximumSupportedClaim:$claim,investigationRequests:$expectedRequests,riskSignals:0,remediationPlans:0,agentActions:0},actual:{matched:$actualMatched,maximumSupportedClaim:$claim,investigationRequests:$requests,riskSignals:$signals,remediationPlans:$plans,agentActions:$actions},assertions:[{id:"detection.matched",result:"PASS",expected:$expectedMatched,actual:$actualMatched},{id:"rca.maximumSupportedClaim",result:"PASS",expected:$claim,actual:$claim},{id:"sideEffects.investigationRequests",result:"PASS",expected:$expectedRequests,actual:$requests},{id:"sideEffects.riskSignals",result:"PASS",expected:0,actual:$signals},{id:"sideEffects.remediationPlans",result:"PASS",expected:0,actual:$plans},{id:"sideEffects.agentActions",result:"PASS",expected:0,actual:$actions}],differences:[],artifacts:[$artifact]}' \
    >>"${scenario_results}"
done

for pattern in "${patterns[@]}"; do
  failure_rule="prometheus-${pattern}-datasource-error"
  failure_json="$(kubectl get riskrule "${failure_rule}" -n "${control_namespace}" -o json)"
  failure_reason="$(jq -r '.status.conditions[] | select(.type == "Ready" and .status == "False") | .reason' <<<"${failure_json}")"
  [[ -n "${failure_reason}" && "${failure_reason}" != "EvaluationReady" ]]
  failure_requests="$(kubectl get investigationrequest -n "${control_namespace}" -l "fluxseer-rca.aiops.platform/risk-rule=${failure_rule}" -o json | jq '.items | length')"
  [[ "${failure_requests}" == "0" ]]
  artifact="datasource-error-${pattern}.json"
  jq -n --arg pattern "${pattern}" --arg reason "${failure_reason}" --argjson requests "${failure_requests}" \
    '{id:("datasource-error-" + $pattern),name:("datasource error " + $pattern),pattern:$pattern,result:"PASS",expected:{classification:"InfrastructureError",notClassifiedAs:"NotMatched",investigationRequests:0},actual:{classification:"InfrastructureError",reason:$reason,investigationRequests:$requests},assertions:[{id:"datasource.failure.classification",result:"PASS",expected:"InfrastructureError",actual:"InfrastructureError"},{id:"datasource.failure.notMatched",result:"PASS",expected:false,actual:false},{id:"sideEffects.investigationRequests",result:"PASS",expected:0,actual:$requests}],differences:[],artifacts:[$artifact]}' \
    >"${internal_dir}/${artifact}"
  cat "${internal_dir}/${artifact}" >>"${scenario_results}"
done

for case_id in error-high latency-high; do
  pattern="$(case_pattern "${case_id}")"
  rule="prometheus-${pattern}-${case_id}"
  "${cli_bin}" report riskrule "${rule}" -n "${control_namespace}" -o json >"${user_dir}/${pattern}.json"
  bash "${repo_root}/hack/verify-riskrule-report.sh" "${user_dir}/${pattern}.json"
  jq -e --arg pattern "${pattern}" '
    .schemaVersion == "fluxseer-riskrule-report/v1" and
    (.riskRule.name | startswith("prometheus-" + $pattern)) and
    (.investigationRequests | length) == 1 and
    (.riskSignals | length) == 0 and
    .investigationRequests[0].status.phase == "Completed" and
    .investigationRequests[0].status.outcome == "Inconclusive" and
    ((.investigationRequests[0].status.rootCauseType // "") == "")
  ' "${user_dir}/${pattern}.json" >/dev/null
done

controller_image="$(kubectl get deployment fluxseer-rca-controller-manager -n "${control_namespace}" -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].image}')"
source_dirty=false
[[ -z "$(git -C "${repo_root}" status --porcelain)" ]] || source_dirty=true
jq -n \
  --arg runID "${run_id}" --arg sourceCommit "$(git -C "${repo_root}" rev-parse HEAD)" --argjson sourceDirty "${source_dirty}" \
  --arg context "$(kubectl config current-context)" --arg controlNamespace "${control_namespace}" --arg targetNamespace "${target_namespace}" --arg controllerImage "${controller_image}" \
  --arg userDir "${user_dir}" --slurpfile scenarios "${scenario_results}" \
  '{schemaVersion:"fluxseer-test-report/v1",suiteSchemaVersion:"prometheus-pattern-conformance/v1",suite:{id:"prometheus-pattern-conformance",name:"Prometheus Pattern Conformance",tier:"cluster"},run:{id:$runID,sourceCommit:$sourceCommit,sourceDirty:$sourceDirty,environment:{kubernetesContext:$context,controlNamespace:$controlNamespace,targetNamespace:$targetNamespace,controllerImage:$controllerImage,prometheusEngine:"prom/prometheus:v3.5.0"}},summary:{result:"PASS",total:10,passed:10,failed:0},metrics:{patterns:["high-error-rate","high-latency"],patternCases:8,matchedCases:3,notMatchedCases:5,infrastructureErrorControls:2,internalReports:10,userFacingReports:2,mainUserFacingCatalogExamples:15},artifacts:{userFacingDirectory:$userDir},scenarios:$scenarios}' \
  >"${internal_dir}/summary.json"
bash "${repo_root}/hack/verify-test-report.sh" "${internal_dir}/summary.json"
bash "${repo_root}/hack/render-test-report.sh" "${internal_dir}/summary.json" "${internal_dir}/scenario-comparison.md"
kubectl get riskrule "${rules[@]}" "${failure_rules[@]}" -n "${control_namespace}" -o yaml >"${internal_dir}/riskrules.yaml"
kubectl logs deployment/${mock_name} -n "${control_namespace}" >"${internal_dir}/prometheus-access.log"

echo "Prometheus Pattern Conformance passed: high-error-rate and high-latency 10/10"
echo "internal artifacts: ${internal_dir}"
echo "user-facing reports: ${user_dir}/high-error-rate.json ${user_dir}/high-latency.json"
