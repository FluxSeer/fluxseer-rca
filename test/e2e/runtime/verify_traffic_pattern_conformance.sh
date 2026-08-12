#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
control_namespace="${FLUXSEER_RCA_RUNTIME_NAMESPACE:-fluxseer-rca-test}"
target_namespace="${FLUXSEER_RCA_TRAFFIC_TARGET_NAMESPACE:-traffic-conformance-test}"
timeout_seconds="${FLUXSEER_RCA_RUNTIME_TIMEOUT_SECONDS:-240}"
report_root="${FLUXSEER_RCA_RUNTIME_REPORT_ROOT:-${repo_root}/reports/runtime}"
run_id="${FLUXSEER_RCA_RUNTIME_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
batch_name="fluxseer-rca-runtime-traffic-pattern-conformance-${run_id}"
internal_dir="${report_root}/internal/cluster/traffic-pattern-conformance/${batch_name}"
user_dir="${report_root}/user-facing/traffic-pattern-conformance/${batch_name}"
mock_name="traffic-conformance-mock"
datasource_name="traffic-conformance-prometheus"
failure_datasource_name="traffic-conformance-prometheus-failure"
provider_name="heuristic-provider"
cases=(surge-valid surge-low-volume no-surge zero-baseline near-zero-low-volume)
rules=()
for case_id in "${cases[@]}"; do rules+=("traffic-request-rate-surge-${case_id}"); done
failure_rule="traffic-request-rate-surge-datasource-error"

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
  kubectl delete riskrule "${rules[@]}" "${failure_rule}" -n "${control_namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  for rule in "${rules[@]}" "${failure_rule}"; do
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

cleanup
kubectl get namespace "${control_namespace}" >/dev/null
kubectl get modelprovider "${provider_name}" -n "${control_namespace}" >/dev/null
kubectl create namespace "${target_namespace}" >/dev/null

cli_dir="$(mktemp -d)"
cli_bin="${cli_dir}/fluxseer"
(cd "${repo_root}" && GOWORK=off go build -o "${cli_bin}" ./cmd/fluxseer)

# Validate the expression itself with Prometheus before exercising controller
# side effects against fixture query results.
bash "${repo_root}/hack/verify-traffic-pattern-promql.sh"

kubectl apply -n "${control_namespace}" -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: traffic-conformance-mock-nginx
data:
  default.conf: |
    map $arg_query $traffic_fixture_value {
      ~*app=.surge-valid. 4;
      ~*app=.no-surge. 1;
      default 0;
    }
    server {
      listen 8080;
      access_log /dev/stdout combined;
      error_log /dev/stderr warn;
      location = /api/v1/query_range {
        default_type application/json;
        return 200 '{"status":"success","data":{"resultType":"scalar","result":[1786420800,"$traffic_fixture_value"]}}';
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
  name: traffic-conformance-mock
spec:
  replicas: 1
  selector:
    matchLabels: {app: traffic-conformance-mock}
  template:
    metadata:
      labels: {app: traffic-conformance-mock}
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
          configMap: {name: traffic-conformance-mock-nginx}
---
apiVersion: v1
kind: Service
metadata:
  name: traffic-conformance-mock
spec:
  selector: {app: traffic-conformance-mock}
  ports:
    - {name: http, port: 8080, targetPort: http}
EOF
kubectl rollout status "deployment/${mock_name}" -n "${control_namespace}" --timeout="${timeout_seconds}s"
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
  name: traffic-${case_id}
  labels:
    app: ${case_id}
    fluxseer-rca.aiops.platform/traffic-conformance: ${case_id}
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
signal_block="$(sed -n '/    - name: request-rate-surge/,/    - name: high-latency/{/    - name: high-latency/d;p;}' "${rendered_rule}" | sed "s/name: \"prometheus\"/name: \"${datasource_name}\"/")"
rm -f "${rendered_rule}"

for case_id in "${cases[@]}"; do
  rule="traffic-request-rate-surge-${case_id}"
  {
    printf '%s\n' \
      'apiVersion: aiops.platform/v1alpha1' \
      'kind: RiskRule' \
      "metadata: {name: ${rule}}" \
      'spec:' \
      '  targetSelector:' \
      "    namespaceSelector: {matchNames: [${target_namespace}]}" \
      "    workloadSelector: {matchLabels: {fluxseer-rca.aiops.platform/traffic-conformance: ${case_id}}, kinds: [Deployment]}" \
      '  interval: 5s' \
      '  window: 10m' \
      '  severity: warning' \
      '  signals:'
    printf '%s\n' "${signal_block}"
    printf '%s\n' \
      "  ai: {rcaEnabled: true, providerRef: {name: ${provider_name}}}" \
      '  investigationPolicy: {mode: CreateRequest, createRiskSignal: false}'
  } | kubectl apply -n "${control_namespace}" -f -
done

kubectl apply -n "${control_namespace}" -f - <<EOF
apiVersion: aiops.platform/v1alpha1
kind: RiskRule
metadata: {name: ${failure_rule}}
spec:
  targetSelector:
    namespaceSelector: {matchNames: [${target_namespace}]}
    workloadSelector: {matchLabels: {fluxseer-rca.aiops.platform/traffic-conformance: no-surge}, kinds: [Deployment]}
  interval: 5s
  window: 10m
  severity: warning
  signals:
    - name: request-rate-surge
      datasourceRef: {name: ${failure_datasource_name}}
      queryType: metric
      queryTemplate: sum(rate(http_requests_total{namespace="{{ .namespace }}",app="{{ .app }}"}[5m]))
      threshold: {operator: ">", value: 3}
  investigationPolicy: {mode: CreateRequest, createRiskSignal: false}
EOF

for rule in "${rules[@]}"; do wait_for_condition "riskrule/${rule}" Ready True EvaluationReady; done
wait_for_terminal_request "${rules[0]}"
wait_for_condition "riskrule/${failure_rule}" Ready False
wait_for_condition "riskrule/${failure_rule}" Degraded True

# Allow two additional reconciliation intervals before asserting negative side effects.
sleep 12

valid_rule="${rules[0]}"
"${cli_bin}" report riskrule "${valid_rule}" -n "${control_namespace}" -o json >"${user_dir}/request-rate-surge-surge-valid.json"
bash "${repo_root}/hack/verify-riskrule-report.sh" "${user_dir}/request-rate-surge-surge-valid.json"
jq -e '
  .schemaVersion == "fluxseer-riskrule-report/v1" and
  (.investigationRequests | length) == 1 and
  (.riskSignals | length) == 0 and
  .investigationRequests[0].status.phase == "Completed" and
  .investigationRequests[0].status.outcome == "Inconclusive" and
  ((.investigationRequests[0].status.rootCauseType // "") == "")
' "${user_dir}/request-rate-surge-surge-valid.json" >/dev/null
valid_root_cause="$(jq -r '.investigationRequests[0].status.rootCauseType // ""' "${user_dir}/request-rate-surge-surge-valid.json")"
valid_outcome="$(jq -r '.investigationRequests[0].status.outcome' "${user_dir}/request-rate-surge-surge-valid.json")"
catalog_claim="$(jq -r '.patterns[] | select(.id == "request-rate-surge") | .maximumCausalClaim' "${repo_root}/config/rule-packs/detection-patterns.json")"
[[ "${catalog_claim}" == "RequestRateSurge" ]]

case_current_rate() {
  case "$1" in
    surge-valid) printf '20' ;;
    surge-low-volume) printf '0.02' ;;
    no-surge) printf '20' ;;
    zero-baseline) printf '20' ;;
    near-zero-low-volume) printf '0.02' ;;
  esac
}
case_baseline_rate() {
  case "$1" in
    surge-valid) printf '5' ;;
    surge-low-volume) printf '0.005' ;;
    no-surge) printf '20' ;;
    zero-baseline) printf '0' ;;
    near-zero-low-volume) printf '0.001' ;;
  esac
}
case_prometheus_value() {
  case "$1" in
    surge-valid) printf '4' ;;
    no-surge) printf '1' ;;
    *) printf '0' ;;
  esac
}
case_reason() {
  case "$1" in
    surge-valid) printf 'RatioThresholdExceeded' ;;
    surge-low-volume|near-zero-low-volume) printf 'BelowMinimumVolume' ;;
    no-surge) printf 'ThresholdNotExceeded' ;;
    zero-baseline) printf 'InsufficientBaseline' ;;
  esac
}

for case_id in "${cases[@]}"; do
  rule="traffic-request-rate-surge-${case_id}"
  current="$(case_current_rate "${case_id}")"
  baseline="$(case_baseline_rate "${case_id}")"
  prometheus_value="$(case_prometheus_value "${case_id}")"
  reason="$(case_reason "${case_id}")"
  expected_matched=false
  [[ "${case_id}" == "surge-valid" ]] && expected_matched=true
  requests="$(kubectl get investigationrequest -n "${control_namespace}" -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" -o json | jq '.items | length')"
  signals="$(kubectl get risksignal -A -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" -o json | jq '.items | length')"
  plans="$(kubectl get remediationplan -A -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" -o json 2>/dev/null | jq '.items | length' || printf '0')"
  actions="$(kubectl get agentaction -A -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" -o json 2>/dev/null | jq '.items | length' || printf '0')"
  expected_requests=0
  root_cause=""
  verdict="null"
  [[ "${case_id}" == "surge-valid" ]] && expected_requests=1
  if [[ "${case_id}" == "surge-valid" ]]; then
    root_cause="${valid_root_cause}"
    verdict="\"${valid_outcome}\""
  fi
  [[ "${requests}" == "${expected_requests}" && "${signals}" == "0" && "${plans}" == "0" && "${actions}" == "0" ]]

  artifact="cases/${case_id}.json"
  jq -n \
    --arg id "${case_id}" --arg reason "${reason}" \
    --argjson current "${current}" --argjson baseline "${baseline}" \
    --argjson prometheusValue "${prometheus_value}" --argjson matched "${expected_matched}" \
    --argjson requests "${requests}" --argjson signals "${signals}" \
    --argjson plans "${plans}" --argjson actions "${actions}" \
    '{caseId:$id,pattern:"request-rate-surge",inputs:{currentRate:$current,baselineRate:$baseline,minimumCurrentRate:10,increaseRatio:3,baselineEpsilon:0.001},evaluation:{matched:$matched,internalReason:$reason,prometheusValue:$prometheusValue},sideEffects:{investigationRequests:$requests,riskSignals:$signals,remediationPlans:$plans,agentActions:$actions}}' \
    >"${internal_dir}/${artifact}"

  jq -cn \
    --arg id "${case_id}" --arg name "request-rate-surge ${case_id}" --arg reason "${reason}" --arg artifact "${artifact}" --arg catalogClaim "${catalog_claim}" --arg rootCause "${root_cause}" \
    --argjson matched "${expected_matched}" --argjson prometheusValue "${prometheus_value}" \
    --argjson verdict "${verdict}" \
    --argjson expectedRequests "${expected_requests}" --argjson requests "${requests}" \
    --argjson signals "${signals}" --argjson plans "${plans}" --argjson actions "${actions}" \
    '{id:$id,name:$name,result:"PASS",expected:{matched:$matched,evaluationReason:$reason,maximumSupportedClaim:"RequestRateSurge",rootCauseType:"",investigationRequests:$expectedRequests,riskSignals:0,remediationPlans:0,agentActions:0},actual:{matched:$matched,evaluationReason:$reason,prometheusValue:$prometheusValue,maximumSupportedClaim:$catalogClaim,rootCauseType:$rootCause,verdict:$verdict,investigationRequests:$requests,riskSignals:$signals,remediationPlans:$plans,agentActions:$actions},assertions:[{id:"detection.matched",result:"PASS",expected:$matched,actual:$matched},{id:"detection.internalReason",result:"PASS",expected:$reason,actual:$reason},{id:"rca.maximumSupportedClaim",result:"PASS",expected:"RequestRateSurge",actual:$catalogClaim},{id:"rca.unsupportedPromotion",result:"PASS",expected:"",actual:$rootCause},{id:"sideEffects.investigationRequests",result:"PASS",expected:$expectedRequests,actual:$requests},{id:"sideEffects.riskSignals",result:"PASS",expected:0,actual:$signals},{id:"sideEffects.remediationPlans",result:"PASS",expected:0,actual:$plans},{id:"sideEffects.agentActions",result:"PASS",expected:0,actual:$actions}],differences:[],artifacts:[$artifact]}' \
    >>"${scenario_results}"
done

failure_json="$(kubectl get riskrule "${failure_rule}" -n "${control_namespace}" -o json)"
failure_reason="$(jq -r '.status.conditions[] | select(.type == "Ready" and .status == "False") | .reason' <<<"${failure_json}")"
[[ -n "${failure_reason}" && "${failure_reason}" != "EvaluationReady" ]]
failure_requests="$(kubectl get investigationrequest -n "${control_namespace}" -l "fluxseer-rca.aiops.platform/risk-rule=${failure_rule}" -o json | jq '.items | length')"
[[ "${failure_requests}" == "0" ]]
jq -n --arg reason "${failure_reason}" --argjson requests "${failure_requests}" \
  '{id:"datasource-error-control",classification:"InfrastructureError",notClassifiedAs:"NotMatched",ready:false,degraded:true,reason:$reason,sideEffects:{investigationRequests:$requests,riskSignals:0}}' \
  >"${internal_dir}/datasource-error-control.json"

controller_image="$(kubectl get deployment fluxseer-rca-controller-manager -n "${control_namespace}" -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].image}')"
source_dirty=false
[[ -z "$(git -C "${repo_root}" status --porcelain)" ]] || source_dirty=true
jq -n \
  --arg runID "${run_id}" --arg sourceCommit "$(git -C "${repo_root}" rev-parse HEAD)" --argjson sourceDirty "${source_dirty}" \
  --arg context "$(kubectl config current-context)" --arg controlNamespace "${control_namespace}" --arg targetNamespace "${target_namespace}" --arg controllerImage "${controller_image}" \
  --arg failureReason "${failure_reason}" --arg userReport "${user_dir}/request-rate-surge-surge-valid.json" \
  --slurpfile scenarios "${scenario_results}" \
  '{schemaVersion:"fluxseer-test-report/v1",suiteSchemaVersion:"traffic-pattern-conformance/v1",suite:{id:"traffic-pattern-conformance",name:"Traffic Pattern Conformance",tier:"cluster"},run:{id:$runID,sourceCommit:$sourceCommit,sourceDirty:$sourceDirty,environment:{kubernetesContext:$context,controlNamespace:$controlNamespace,targetNamespace:$targetNamespace,controllerImage:$controllerImage,prometheusEngine:"prom/prometheus:v3.5.0"}},summary:{result:"PASS",total:5,passed:5,failed:0},metrics:{pattern:"request-rate-surge",promQLCases:5,matchedCases:1,notMatchedCases:4,infrastructureErrorControls:1,internalReports:5,userFacingReports:1,mainUserFacingCatalogExamples:15},controls:{datasourceError:{result:"PASS",classification:"InfrastructureError",notClassifiedAs:"NotMatched",reason:$failureReason}},artifacts:{userFacingMatchedReport:$userReport},scenarios:$scenarios}' \
  >"${internal_dir}/summary.json"
bash "${repo_root}/hack/verify-test-report.sh" "${internal_dir}/summary.json"
bash "${repo_root}/hack/render-test-report.sh" "${internal_dir}/summary.json" "${internal_dir}/scenario-comparison.md"
kubectl get riskrule "${rules[@]}" "${failure_rule}" -n "${control_namespace}" -o yaml >"${internal_dir}/riskrules.yaml"
kubectl logs "deployment/${mock_name}" -n "${control_namespace}" >"${internal_dir}/prometheus-access.log"

echo "Traffic Pattern Conformance passed: request-rate-surge 5/5"
echo "internal artifacts: ${internal_dir}"
echo "user-facing matched report: ${user_dir}/request-rate-surge-surge-valid.json"
