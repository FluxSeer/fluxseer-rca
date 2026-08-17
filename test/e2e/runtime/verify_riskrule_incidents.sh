#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
control_namespace="${FLUXSEER_RCA_RUNTIME_NAMESPACE:-fluxseer-rca-test}"
target_namespace="${FLUXSEER_RCA_RUNTIME_TARGET_NAMESPACE:-database-test}"
timeout_seconds="${FLUXSEER_RCA_RUNTIME_TIMEOUT_SECONDS:-240}"
report_root="${FLUXSEER_RCA_RUNTIME_REPORT_ROOT:-${repo_root}/reports/runtime}"
run_id="${FLUXSEER_RCA_RUNTIME_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
report_dir="${report_root}/fluxseer-rca-riskrule-incidents-${run_id}"
run_label="$(tr '[:upper:]' '[:lower:]' <<<"${run_id}" | tr -cd 'a-z0-9.-' | cut -c1-50)"
target_label_key="fluxseer.com/runtime-anomaly"
runtime_test_image="${FLUXSEER_RCA_RUNTIME_TEST_IMAGE:-registry.example.com/fluxseer/runtime-anomaly:does-not-exist}"
rules=(
  runtime-anomaly-event-direct
  runtime-anomaly-condition-direct
  runtime-anomaly-prometheus-direct
  runtime-anomaly-loki-direct
  runtime-anomaly-event-canonical
)

case_id_for_rule() {
  local rule="$1"
  printf '%s\n' "${rule#runtime-anomaly-}"
}

if [[ -z "${KUBECONFIG:-}" ]]; then
  echo "KUBECONFIG must point to the explicitly authorized test cluster" >&2
  exit 1
fi
for command_name in kubectl jq git go; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "required command not found: ${command_name}" >&2; exit 1; }
done

mkdir -p "${report_dir}/incidents"
cli_dir=""
cli_bin=""
scenario_results="${report_dir}/scenarios.jsonl"
: >"${scenario_results}"

cleanup() {
  # Stop reconciliation before deleting generated outputs so the controller
  # cannot recreate a RiskSignal between the report-driven cleanup steps.
  kubectl delete riskrule "${rules[@]}" -n "${control_namespace}" --ignore-not-found >/dev/null 2>&1 || true
  for report in "${report_dir}"/incidents/*.json; do
    [[ -f "${report}" ]] || continue
    while IFS=$'\t' read -r namespace name; do
      [[ -z "${name}" ]] || kubectl delete risksignal "${name}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    done < <(jq -r '.riskSignals[] | [.metadata.namespace,.metadata.name] | @tsv' "${report}" 2>/dev/null || true)
    while IFS=$'\t' read -r namespace name; do
      [[ -z "${name}" ]] || kubectl delete investigationrequest "${name}" -n "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    done < <(jq -r '.investigationRequests[] | [.metadata.namespace,.metadata.name] | @tsv' "${report}" 2>/dev/null || true)
  done
  for rule in "${rules[@]}"; do
    kubectl delete risksignal -A -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    kubectl delete investigationrequest -n "${control_namespace}" -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  done
  kubectl delete event runtime-anomaly-backoff -n "${target_namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete deployment runtime-anomaly-unavailable runtime-anomaly-logs -n "${target_namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  if [[ -n "${cli_bin}" ]]; then rm -f "${cli_bin}"; fi
  if [[ -n "${cli_dir}" ]]; then rmdir "${cli_dir}" 2>/dev/null || true; fi
}
trap cleanup EXIT INT TERM

cleanup
cli_dir="$(mktemp -d)"
cli_bin="${cli_dir}/fluxseer"
(
  cd "${repo_root}"
  GOWORK=off go build -o "${cli_bin}" ./cmd/fluxseer
)

kubectl get namespace "${control_namespace}" >/dev/null
kubectl get namespace "${target_namespace}" >/dev/null
kubectl get datasource kubernetes-events prometheus loki -n "${control_namespace}" -o yaml >"${report_dir}/datasources.yaml"
kubectl get deployment fluxseer-rca-controller-manager -n "${control_namespace}" -o yaml >"${report_dir}/controller-deployment.yaml"

kubectl apply -n "${target_namespace}" -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: runtime-anomaly-unavailable
  labels:
    ${target_label_key}: "${run_label}"
    app.kubernetes.io/name: runtime-anomaly-unavailable
    app.kubernetes.io/component: report-validation
spec:
  replicas: 1
  selector:
    matchLabels: {app: runtime-anomaly-unavailable}
  template:
    metadata:
      labels:
        app: runtime-anomaly-unavailable
        ${target_label_key}: "${run_label}"
        app.kubernetes.io/name: runtime-anomaly-unavailable
        app.kubernetes.io/component: report-validation
    spec:
      securityContext: {seccompProfile: {type: RuntimeDefault}}
      containers:
        - name: unavailable
          image: ${runtime_test_image}
          securityContext:
            allowPrivilegeEscalation: false
            capabilities: {drop: ["ALL"]}
            runAsNonRoot: true
            runAsUser: 65532
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: runtime-anomaly-logs
  labels:
    ${target_label_key}: "${run_label}"
    app.kubernetes.io/name: runtime-anomaly-logs
    app.kubernetes.io/component: report-validation
spec:
  replicas: 1
  selector:
    matchLabels: {app: runtime-anomaly-logs}
  template:
    metadata:
      labels:
        app: runtime-anomaly-logs
        ${target_label_key}: "${run_label}"
        app.kubernetes.io/name: runtime-anomaly-logs
        app.kubernetes.io/component: report-validation
    spec:
      securityContext: {seccompProfile: {type: RuntimeDefault}}
      containers:
        - name: logger
          image: busybox:1.36
          command: ["sh", "-c", "while true; do echo 'error runtime anomaly report validation'; sleep 5; done"]
          securityContext:
            allowPrivilegeEscalation: false
            capabilities: {drop: ["ALL"]}
            runAsNonRoot: true
            runAsUser: 65532
EOF

kubectl rollout status deployment/runtime-anomaly-logs -n "${target_namespace}" --timeout=120s
unavailable_uid="$(kubectl get deployment runtime-anomaly-unavailable -n "${target_namespace}" -o jsonpath='{.metadata.uid}')"
now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
kubectl apply -n "${target_namespace}" -f - <<EOF
apiVersion: v1
kind: Event
metadata: {name: runtime-anomaly-backoff}
involvedObject:
  apiVersion: apps/v1
  kind: Deployment
  name: runtime-anomaly-unavailable
  namespace: ${target_namespace}
  uid: ${unavailable_uid}
reason: BackOff
message: runtime anomaly workload repeatedly failed to start
source: {component: fluxseer-runtime-validation}
type: Warning
firstTimestamp: "${now}"
lastTimestamp: "${now}"
count: 1
EOF

kubectl apply -n "${control_namespace}" -f - <<EOF
apiVersion: aiops.platform/v1alpha1
kind: RiskRule
metadata: {name: runtime-anomaly-event-direct}
spec:
  targetSelector:
    namespaceSelector: {matchNames: [${target_namespace}]}
    workloadSelector: {matchLabels: {${target_label_key}: "${run_label}"}, kinds: [Deployment]}
  interval: 5s
  window: 10m
  severity: warning
  signals:
    - name: backoff
      datasourceRef: {name: kubernetes-events}
      queryType: event
      reasons: [BackOff]
      threshold: {operator: count_gt, value: 0}
  ai: {rcaEnabled: true, providerRef: {name: heuristic-provider}}
  investigationPolicy: {mode: DirectRiskSignal}
---
apiVersion: aiops.platform/v1alpha1
kind: RiskRule
metadata: {name: runtime-anomaly-condition-direct}
spec:
  targetSelector:
    namespaceSelector: {matchNames: [${target_namespace}]}
    workloadSelector: {matchLabels: {${target_label_key}: "${run_label}"}, kinds: [Deployment]}
  interval: 5s
  window: 10m
  severity: warning
  signals:
    - name: unavailable
      datasourceRef: {name: kubernetes-events}
      queryType: deploymentCondition
      reasons: [Available=False]
      threshold: {operator: count_gt, value: 0}
  ai: {rcaEnabled: true, providerRef: {name: heuristic-provider}}
  investigationPolicy: {mode: DirectRiskSignal}
---
apiVersion: aiops.platform/v1alpha1
kind: RiskRule
metadata: {name: runtime-anomaly-prometheus-direct}
spec:
  targetSelector:
    namespaceSelector: {matchNames: [${target_namespace}]}
    workloadSelector: {matchLabels: {${target_label_key}: "${run_label}"}, kinds: [Deployment]}
  interval: 5s
  window: 10m
  severity: warning
  signals:
    - name: unavailable-replicas
      datasourceRef: {name: prometheus}
      queryType: metric
      queryTemplate: |
        kube_deployment_spec_replicas{namespace="{{ .namespace }}",deployment="{{ .name }}"}
        - kube_deployment_status_replicas_available{namespace="{{ .namespace }}",deployment="{{ .name }}"}
      threshold: {operator: ">", value: 0}
  ai: {rcaEnabled: true, providerRef: {name: heuristic-provider}}
  investigationPolicy: {mode: DirectRiskSignal}
---
apiVersion: aiops.platform/v1alpha1
kind: RiskRule
metadata: {name: runtime-anomaly-loki-direct}
spec:
  targetSelector:
    namespaceSelector: {matchNames: [${target_namespace}]}
    workloadSelector: {matchLabels: {${target_label_key}: "${run_label}"}, kinds: [Deployment]}
  interval: 5s
  window: 10m
  severity: warning
  signals:
    - name: error-log
      datasourceRef: {name: loki}
      queryType: log
      queryTemplate: |
        {namespace="{{ .namespace }}", app="{{ index .labels "app.kubernetes.io/name" }}", component="{{ index .labels "app.kubernetes.io/component" }}"} |= "error"
      threshold: {operator: count_gt, value: 0}
  ai: {rcaEnabled: true, providerRef: {name: heuristic-provider}}
  investigationPolicy: {mode: DirectRiskSignal}
---
apiVersion: aiops.platform/v1alpha1
kind: RiskRule
metadata: {name: runtime-anomaly-event-canonical}
spec:
  targetSelector:
    namespaceSelector: {matchNames: [${target_namespace}]}
    workloadSelector: {matchLabels: {${target_label_key}: "${run_label}"}, kinds: [Deployment]}
  interval: 5s
  window: 10m
  severity: warning
  signals:
    - name: backoff
      datasourceRef: {name: kubernetes-events}
      queryType: event
      reasons: [BackOff]
      threshold: {operator: count_gt, value: 0}
  ai: {rcaEnabled: true, providerRef: {name: heuristic-provider}}
  investigationPolicy: {mode: CreateRequest, createRiskSignal: true}
EOF

wait_for_rule_output() {
  local rule="$1"
  local mode="$2"
  local deadline=$((SECONDS + timeout_seconds))
  while (( SECONDS < deadline )); do
    if [[ "${mode}" == "CreateRequest" ]]; then
      count="$(kubectl get investigationrequest -n "${control_namespace}" -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" -o json | jq '[.items[] | select((.status.phase == "Completed" or .status.phase == "Failed") and (.status.linkedRiskSignalRef.name | length > 0))] | length')"
    else
      count="$(kubectl get risksignal -A -l "fluxseer-rca.aiops.platform/risk-rule=${rule}" -o json | jq '[.items[] | select(.status.phase != null)] | length')"
    fi
    if (( count > 0 )); then return 0; fi
    sleep 5
  done
  echo "timed out waiting for RiskRule output: ${rule}" >&2
  kubectl get riskrule "${rule}" -n "${control_namespace}" -o yaml >&2 || true
  return 1
}

wait_for_rule_output runtime-anomaly-event-direct DirectRiskSignal
wait_for_rule_output runtime-anomaly-condition-direct DirectRiskSignal
wait_for_rule_output runtime-anomaly-prometheus-direct DirectRiskSignal
wait_for_rule_output runtime-anomaly-loki-direct DirectRiskSignal
wait_for_rule_output runtime-anomaly-event-canonical CreateRequest

for rule in "${rules[@]}"; do
  case_id="$(case_id_for_rule "${rule}")"
  "${cli_bin}" report riskrule "${rule}" -n "${control_namespace}" -o json >"${report_dir}/incidents/${case_id}.json"
  bash "${repo_root}/hack/verify-riskrule-report.sh" "${report_dir}/incidents/${case_id}.json"
done

append_direct_case() {
  local rule="$1"
  local name="$2"
  local source="$3"
  local expected_reason="$4"
  local case_id="$(case_id_for_rule "${rule}")"
  local report="${report_dir}/incidents/${case_id}.json"
  local count phases reasons
  count="$(jq '.riskSignals | length' "${report}")"
  phases="$(jq -c '[.riskSignals[].status.phase] | unique' "${report}")"
  reasons="$(jq -c '[.riskSignals[].status.conditions[].reason] | unique' "${report}")"
  jq -e --arg reason "${expected_reason}" '(.riskSignals | length) > 0 and all(.riskSignals[]; .status.phase == "Confirmed") and any(.riskSignals[].status.conditions[]; .reason == $reason)' "${report}" >/dev/null
  jq -cn --arg id "${rule}" --arg name "${name}" --arg source "${source}" --arg reason "${expected_reason}" --arg artifact "incidents/${case_id}.json" --argjson count "${count}" --argjson phases "${phases}" --argjson reasons "${reasons}" '{id:$id,name:$name,result:"PASS",expected:{reportSchema:"fluxseer-riskrule-report/v1",mode:"DirectRiskSignal",source:$source,riskSignals:{minimum:1},investigationRequests:0,riskSignalPhases:["Confirmed"],requiredConditionReason:$reason},actual:{reportSchema:"fluxseer-riskrule-report/v1",mode:"DirectRiskSignal",source:$source,riskSignals:$count,investigationRequests:0,riskSignalPhases:$phases,conditionReasons:$reasons},assertions:[{id:"report.schemaVersion",result:"PASS",expected:"fluxseer-riskrule-report/v1",actual:"fluxseer-riskrule-report/v1"},{id:"riskSignals.minimum",result:"PASS",expected:1,actual:$count},{id:"investigationRequests",result:"PASS",expected:0,actual:0},{id:"riskSignal.phase",result:"PASS",expected:["Confirmed"],actual:$phases},{id:"riskSignal.conditionReason",result:"PASS",expected:$reason,actual:$reasons}],differences:[],artifacts:[$artifact]}' >>"${scenario_results}"
}

append_direct_case runtime-anomaly-event-direct "Kubernetes event anomaly" kubernetes-events EventBackOffObserved
append_direct_case runtime-anomaly-condition-direct "Deployment condition anomaly" kubernetes-events DeploymentconditionMinimumreplicasunavailableObserved
append_direct_case runtime-anomaly-prometheus-direct "Prometheus availability anomaly" prometheus MetricObserved
append_direct_case runtime-anomaly-loki-direct "Loki error-log anomaly" loki LogObserved

canonical_report="${report_dir}/incidents/event-canonical.json"
canonical_requests="$(jq '.investigationRequests | length' "${canonical_report}")"
canonical_signals="$(jq '.riskSignals | length' "${canonical_report}")"
canonical_request_phases="$(jq -c '[.investigationRequests[].status.phase] | unique' "${canonical_report}")"
canonical_request_outcomes="$(jq -c '[.investigationRequests[].status.outcome] | unique' "${canonical_report}")"
canonical_signal_phases="$(jq -c '[.riskSignals[].status.phase] | unique' "${canonical_report}")"
jq -e '(.investigationRequests | length) > 0 and (.riskSignals | length) > 0 and all(.investigationRequests[]; .status.phase == "Completed" and .status.outcome == "Inconclusive") and all(.riskSignals[]; .status.phase == "Inconclusive")' "${canonical_report}" >/dev/null
jq -cn --argjson requests "${canonical_requests}" --argjson signals "${canonical_signals}" --argjson requestPhases "${canonical_request_phases}" --argjson requestOutcomes "${canonical_request_outcomes}" --argjson signalPhases "${canonical_signal_phases}" '{id:"runtime-anomaly-event-canonical",name:"Canonical RiskRule investigation and projection",result:"PASS",expected:{reportSchema:"fluxseer-riskrule-report/v1",mode:"CreateRequest",investigationRequests:{minimum:1},riskSignals:{minimum:1},investigationRequestPhases:["Completed"],investigationRequestOutcomes:["Inconclusive"],riskSignalPhases:["Inconclusive"]},actual:{reportSchema:"fluxseer-riskrule-report/v1",mode:"CreateRequest",investigationRequests:$requests,riskSignals:$signals,investigationRequestPhases:$requestPhases,investigationRequestOutcomes:$requestOutcomes,riskSignalPhases:$signalPhases},assertions:[{id:"report.schemaVersion",result:"PASS",expected:"fluxseer-riskrule-report/v1",actual:"fluxseer-riskrule-report/v1"},{id:"investigationRequests.minimum",result:"PASS",expected:1,actual:$requests},{id:"riskSignals.minimum",result:"PASS",expected:1,actual:$signals},{id:"investigationRequest.phase",result:"PASS",expected:["Completed"],actual:$requestPhases},{id:"investigationRequest.outcome",result:"PASS",expected:["Inconclusive"],actual:$requestOutcomes},{id:"riskSignal.phase",result:"PASS",expected:["Inconclusive"],actual:$signalPhases}],differences:[],artifacts:["incidents/event-canonical.json"]}' >>"${scenario_results}"

controller_image="$(kubectl get deployment fluxseer-rca-controller-manager -n "${control_namespace}" -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].image}')"
source_dirty=false
[[ -z "$(git -C "${repo_root}" status --porcelain)" ]] || source_dirty=true
jq -n \
  --arg runID "${run_id}" \
  --arg sourceCommit "$(git -C "${repo_root}" rev-parse HEAD)" \
  --argjson sourceDirty "${source_dirty}" \
  --arg context "$(kubectl config current-context)" \
  --arg controlNamespace "${control_namespace}" \
  --arg targetNamespace "${target_namespace}" \
  --arg controllerImage "${controller_image}" \
  --slurpfile scenarios "${scenario_results}" \
  '{schemaVersion:"fluxseer-test-report/v1",suiteSchemaVersion:"riskrule-anomaly-matrix/v1",suite:{id:"riskrule-anomaly-matrix",name:"RiskRule User Report Anomaly Matrix",tier:"cluster"},run:{id:$runID,sourceCommit:$sourceCommit,sourceDirty:$sourceDirty,environment:{kubernetesContext:$context,controlNamespace:$controlNamespace,targetNamespace:$targetNamespace,controllerImage:$controllerImage}},summary:{result:"PASS",total:5,passed:5,failed:0},metrics:{riskRuleCases:5,userReportSchema:"fluxseer-riskrule-report/v1"},scenarios:$scenarios}' >"${report_dir}/summary.json"

bash "${repo_root}/hack/verify-test-report.sh" "${report_dir}/summary.json"
bash "${repo_root}/hack/render-test-report.sh" "${report_dir}/summary.json" "${report_dir}/scenario-comparison.md"
kubectl get riskrule "${rules[@]}" -n "${control_namespace}" -o yaml >"${report_dir}/riskrules.yaml"
kubectl get deployment runtime-anomaly-unavailable runtime-anomaly-logs -n "${target_namespace}" -o yaml >"${report_dir}/targets.yaml"
kubectl get event runtime-anomaly-backoff -n "${target_namespace}" -o yaml >"${report_dir}/synthetic-event.yaml"

echo "RiskRule user-report anomaly matrix passed"
echo "artifacts: ${report_dir}"
