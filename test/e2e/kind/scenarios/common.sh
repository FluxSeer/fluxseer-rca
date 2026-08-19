#!/usr/bin/env bash
set -euo pipefail

scenario_script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${scenario_script_dir}/../live_qualification_harness.sh"

SCENARIO_NAMESPACE_PREFIX="${SCENARIO_NAMESPACE_PREFIX:-fluxseer-live-rca}"
SCENARIO_OBSERVABILITY_NAMESPACE="${SCENARIO_OBSERVABILITY_NAMESPACE:-fluxseer-live-observability}"
SCENARIO_RULE_LABEL="fluxseer-rca.aiops.platform/risk-rule"

scenario_kubectl() {
  live_harness_kubectl "$@"
}

scenario_apply_yaml() {
  scenario_kubectl apply -f -
}

scenario_assert_jsonpath_equals() {
  local resource="$1"
  local namespace="$2"
  local jsonpath="$3"
  local expected="$4"
  local actual

  actual="$(scenario_kubectl get "${resource}" -n "${namespace}" -o "jsonpath=${jsonpath}" 2>/dev/null)"
  [[ "${actual}" == "${expected}" ]]
}

scenario_wait_jsonpath_equals() {
  local description="$1"
  local resource="$2"
  local namespace="$3"
  local jsonpath="$4"
  local expected="$5"

  live_harness_wait_for_command \
    "${description}" \
    scenario_assert_jsonpath_equals "${resource}" "${namespace}" "${jsonpath}" "${expected}"
}

scenario_assert_json_jq() {
  local resource="$1"
  local namespace="$2"
  local object_json

  shift 2

  object_json="$(scenario_kubectl get "${resource}" -n "${namespace}" -o json)"
  jq -e "$@" <<<"${object_json}" >/dev/null
}

scenario_wait_json_jq() {
  local description="$1"
  local resource="$2"
  local namespace="$3"
  local expression="$4"

  live_harness_wait_for_command \
    "${description}" \
    scenario_assert_json_jq "${resource}" "${namespace}" "${expression}"
}

scenario_create_namespace() {
  local namespace="$1"
  scenario_kubectl create namespace "${namespace}" >/dev/null
}

scenario_apply_datasources() {
  local namespace="$1"
  local loki_endpoint="${2:-}"
  local prometheus_endpoint="${3:-}"

  scenario_apply_yaml <<EOF
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata:
  name: kubernetes-events
  namespace: ${namespace}
spec:
  type: kubernetesEvents
EOF

  scenario_apply_yaml <<EOF
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: heuristic-provider
  namespace: ${namespace}
spec:
  provider: heuristic
  model: built-in
  timeout: 10s
  maxTokens: 512
EOF

  if [[ -n "${loki_endpoint}" ]]; then
    scenario_apply_yaml <<EOF
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata:
  name: loki
  namespace: ${namespace}
spec:
  type: loki
  endpoint: ${loki_endpoint}
  timeout: 10s
EOF
  fi

  if [[ -n "${prometheus_endpoint}" ]]; then
    scenario_apply_yaml <<EOF
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata:
  name: prometheus
  namespace: ${namespace}
spec:
  type: prometheus
  endpoint: ${prometheus_endpoint}
  timeout: 10s
EOF
  fi

  scenario_wait_json_jq "heuristic provider persisted in ${namespace}" modelprovider/heuristic-provider "${namespace}" \
    '(.metadata.resourceVersion | length > 0) and (.spec.provider == "heuristic")'
  scenario_wait_jsonpath_equals "kubernetes event datasource observed in ${namespace}" datasource/kubernetes-events "${namespace}" '{.status.phase}' Observed
  if [[ -n "${loki_endpoint}" ]]; then
    scenario_wait_jsonpath_equals "Loki datasource observed in ${namespace}" datasource/loki "${namespace}" '{.status.phase}' Observed
  fi
  if [[ -n "${prometheus_endpoint}" ]]; then
    scenario_wait_jsonpath_equals "Prometheus datasource observed in ${namespace}" datasource/prometheus "${namespace}" '{.status.phase}' Observed
  fi
}

scenario_wait_deployment_available() {
  local namespace="$1"
  local name="$2"
  scenario_kubectl rollout status "deployment/${name}" -n "${namespace}" --timeout=180s
}

scenario_wait_pod_waiting_reason() {
  local namespace="$1"
  local name="$2"
  local reason="$3"
  scenario_wait_json_jq \
    "Pod ${namespace}/${name} waiting reason=${reason}" \
    "pod/${name}" \
    "${namespace}" \
    ".status.containerStatuses | any(.[]?; .state.waiting.reason == \"${reason}\")"
}

scenario_assert_pod_label_waiting_reason() {
  local namespace="$1"
  local label_key="$2"
  local label_value="$3"
  local reason="$4"
  local pods_json

  pods_json="$(scenario_kubectl get pods -n "${namespace}" -l "${label_key}=${label_value}" -o json)"
  jq -e --arg reason "${reason}" \
    '.items | any(.[]; .status.containerStatuses | any(.[]?; .state.waiting.reason == $reason))' \
    <<<"${pods_json}" >/dev/null
}

scenario_wait_pod_label_waiting_reason() {
  local namespace="$1"
  local label_key="$2"
  local label_value="$3"
  local reason="$4"
  live_harness_wait_for_command \
    "Pod ${namespace} label=${label_key}/${label_value} waiting reason=${reason}" \
    scenario_assert_pod_label_waiting_reason "${namespace}" "${label_key}" "${label_value}" "${reason}"
}

scenario_assert_pod_label_event_fragment() {
  local namespace="$1"
  local label_key="$2"
  local label_value="$3"
  local reason="$4"
  local fragment="$5"
  local pod_names

  pod_names="$(scenario_kubectl get pods -n "${namespace}" -l "${label_key}=${label_value}" -o json | jq -c '[.items[].metadata.name]')"
  scenario_kubectl get events -n "${namespace}" -o json | \
    jq -e --argjson podNames "${pod_names}" --arg reason "${reason}" --arg fragment "${fragment}" \
      '.items | any(.[]; (.involvedObject.name as $name | ($podNames | index($name)) != null) and .reason == $reason and (.message // "" | contains($fragment)))' \
    >/dev/null
}

scenario_wait_pod_label_event_fragment() {
  local namespace="$1"
  local label_key="$2"
  local label_value="$3"
  local reason="$4"
  local fragment="$5"
  live_harness_wait_for_command \
    "${reason} Event for Pod label=${label_key}/${label_value}" \
    scenario_assert_pod_label_event_fragment "${namespace}" "${label_key}" "${label_value}" "${reason}" "${fragment}"
}

scenario_wait_event_fragment() {
  local namespace="$1"
  local involved_name="$2"
  local reason="$3"
  local fragment="$4"
  scenario_wait_json_jq \
    "${reason} Event for ${namespace}/${involved_name}" \
    events \
    "${namespace}" \
    ".items | any(.[]; .involvedObject.name == \"${involved_name}\" and .reason == \"${reason}\" and (.message | contains(\"${fragment}\")))"
}

scenario_wait_request_name() {
  local namespace="$1"
  local rule_name="$2"
  local deadline=$((SECONDS + LIVE_HARNESS_TIMEOUT_SECONDS))
  local name=""

  until [[ -n "${name}" ]]; do
    name="$(scenario_kubectl get investigationrequests -n "${namespace}" -l "${SCENARIO_RULE_LABEL}=${rule_name}" -o json 2>/dev/null | jq -r '.items | sort_by(.metadata.creationTimestamp) | last.metadata.name // empty')"
    if [[ -n "${name}" ]]; then
      SCENARIO_REQUEST_NAME="${name}"
      export SCENARIO_REQUEST_NAME
      return 0
    fi
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for InvestigationRequest from ${namespace}/${rule_name}" >&2
      return 1
    fi
    sleep "${LIVE_HARNESS_POLL_SECONDS}"
  done
}

scenario_wait_request_terminal() {
  local namespace="$1"
  local request_name="$2"
  scenario_wait_json_jq \
    "InvestigationRequest ${namespace}/${request_name} terminal" \
    "investigationrequest/${request_name}" \
    "${namespace}" \
    '.status.phase == "Completed" or .status.phase == "Failed"'
}

scenario_wait_signal_for_request() {
  local namespace="$1"
  local rule_name="$2"
  local request_name="$3"
  scenario_wait_json_jq \
    "RiskSignal projected for ${namespace}/${request_name}" \
    risksignals \
    "${namespace}" \
    ".items | any(.[]; .spec.investigationRef.name == \"${request_name}\" and (.spec.investigationRef.namespace // \"${namespace}\") == \"${namespace}\")"
}

scenario_assert_converged() {
  local resource="$1"
  local namespace="$2"
  scenario_assert_json_jq "${resource}" "${namespace}" \
    '(.metadata.resourceVersion | length > 0) and (.metadata.generation > 0) and (.status.observedGeneration == .metadata.generation)'
}

scenario_wait_converged() {
  local namespace="$1"
  local rule_name="$2"
  local request_name="$3"
  scenario_wait_json_jq "RiskRule ${namespace}/${rule_name} converged" "riskrule/${rule_name}" "${namespace}" \
    '(.metadata.resourceVersion | length > 0) and (.metadata.generation > 0) and (.status.observedGeneration == .metadata.generation)'
  scenario_wait_json_jq "InvestigationRequest ${namespace}/${request_name} converged" "investigationrequest/${request_name}" "${namespace}" \
    '(.metadata.resourceVersion | length > 0) and (.metadata.generation > 0) and (.status.observedGeneration == .metadata.generation)'
}

scenario_assert_contract() {
  local namespace="$1"
  local rule_name="$2"
  local request_name="$3"
  local expected_outcome="$4"
  local expected_root_type="$5"
  local expected_root_entity="$6"
  local expected_signal_count="$7"
  local evidence_source="$8"
  local evidence_reason="${9:-}"

  scenario_assert_json_jq "investigationrequest/${request_name}" "${namespace}" \
    --arg outcome "${expected_outcome}" \
    --arg rootType "${expected_root_type}" \
    --arg rootEntity "${expected_root_entity}" \
    --arg source "${evidence_source}" \
    --arg reason "${evidence_reason}" \
    '(.status.phase == "Completed") and (.status.outcome == $outcome) and ((.status.verdict.rootCauseType // "") == $rootType) and ((.status.verdict.rootCauseEntity // "" | if type == "object" then if ((.apiVersion // "") == "" and (.kind // "") == "" and (.namespace // "") == "" and (.name // "") == "") then "" else ((.apiVersion // "") + "/" + (.kind // "") + "/" + (.namespace // "") + "/" + (.name // "")) end else . end) == $rootEntity) and ((.status.failure.code // "") == "") and (any(.status.evidenceRefs[]?; .source == $source)) and (if $reason == "" then true else any(.status.evidenceRefs[]?; .reason == $reason) end)'

  local signal_count
  signal_count="$(scenario_kubectl get risksignals -n "${namespace}" -o json | jq --arg request "${request_name}" --arg namespace "${namespace}" '[.items[] | select(.spec.investigationRef.name == $request and (.spec.investigationRef.namespace // $namespace) == $namespace)] | length')"
  if [[ "${signal_count}" != "${expected_signal_count}" ]]; then
    echo "RiskSignal count mismatch for ${namespace}/${request_name}: got=${signal_count} want=${expected_signal_count}" >&2
    return 1
  fi
  if [[ "${expected_signal_count}" == "1" ]]; then
    scenario_assert_json_jq risksignals "${namespace}" \
      --arg request "${request_name}" \
      --arg namespace "${namespace}" \
      '[.items[] | select(.spec.investigationRef.name == $request and (.spec.investigationRef.namespace // $namespace) == $namespace)] | any(.[]; .status.phase == "Confirmed" and (.metadata.resourceVersion | length > 0) and (.status.observedGeneration == .metadata.generation))'
  fi

  local side_effects
  side_effects="$(scenario_kubectl get remediationplans,agentactions -n "${namespace}" -o json | jq '[.items[]] | length')"
  if [[ "${side_effects}" != "0" ]]; then
    echo "unexpected remediation side effects in ${namespace}: ${side_effects}" >&2
    return 1
  fi
}

scenario_capture_case() {
  local case_id="$1"
  local namespace="$2"
  local rule_name="$3"
  local request_name="$4"
  local target_kind="$5"
  local target_name="$6"
  local case_dir="${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${case_id}"

  mkdir -p "${case_dir}"
  scenario_kubectl get "${target_kind}/${target_name}" -n "${namespace}" -o json >"${case_dir}/target.json"
  scenario_kubectl get riskrule "${rule_name}" -n "${namespace}" -o json >"${case_dir}/riskrule.json"
  scenario_kubectl get "investigationrequest/${request_name}" -n "${namespace}" -o json >"${case_dir}/investigationrequest.json"
  scenario_kubectl get risksignals -n "${namespace}" -o json >"${case_dir}/risksignals.json"
  scenario_kubectl get events,pods,deployments,services -n "${namespace}" -o json >"${case_dir}/cluster-objects.json"
  (cd "${live_harness_repo_root}" && GOWORK=off go run ./cmd/fluxseer report riskrule "${rule_name}" --namespace "${namespace}" --output json) >"${case_dir}/public-report.json"
}

scenario_cleanup_namespace() {
  local namespace="$1"
  scenario_kubectl delete namespace "${namespace}" --wait=true --timeout=180s >/dev/null
  live_harness_wait_for_command "namespace ${namespace} deleted" scenario_namespace_absent "${namespace}"
}

scenario_namespace_absent() {
  local namespace="$1"
  ! scenario_kubectl get namespace "${namespace}" >/dev/null 2>&1
}

scenario_setup_observability() {
  local namespace="${SCENARIO_OBSERVABILITY_NAMESPACE}"
  local high_namespace="${SCENARIO_NAMESPACE_PREFIX}-high-http-positive"

  scenario_create_namespace "${namespace}"
  scenario_apply_yaml <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: runtime-observability-nginx
  namespace: ${namespace}
data:
  nginx.conf: |
    events {}
    http {
      server {
        listen 8080;
        location = /healthz { return 200 'ok'; }
        location = /high/api/v1/query_range {
          default_type application/json;
          return 200 '{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"http_5xx_error_rate","app":"checkout"},"values":[[1787058000,"0.25"]]}]}}';
        }
        location = /high-positive/loki/api/v1/query_range {
          default_type application/json;
          return 200 '{"status":"success","data":{"resultType":"streams","result":[{"stream":{"app":"checkout","dependency_kind":"Service","dependency_name":"inventory","dependency_namespace":"${high_namespace}","dependency_api_version":"v1"},"values":[["1787058000000000000","upstream inventory unavailable: connection refused"]]}]}}';
        }
        location = /high-negative/loki/api/v1/query_range {
          default_type application/json;
          return 200 '{"status":"success","data":{"resultType":"streams","result":[]}}';
        }
        location = /crash-positive/loki/api/v1/query_range {
          default_type application/json;
          return 200 '{"status":"success","data":{"resultType":"streams","result":[{"stream":{"namespace":"${SCENARIO_NAMESPACE_PREFIX}-crashloop-positive","app":"crashloop-positive"},"values":[["1787058000000000000","panic during startup: invalid configuration for checkout"]]}]}}';
        }
        location = /crash-negative/loki/api/v1/query_range {
          default_type application/json;
          return 200 '{"status":"success","data":{"resultType":"streams","result":[]}}';
        }
      }
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: runtime-observability
  namespace: ${namespace}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: runtime-observability
  template:
    metadata:
      labels:
        app: runtime-observability
    spec:
      containers:
        - name: nginx
          image: nginx:1.27-alpine
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: 8080
          volumeMounts:
            - name: config
              mountPath: /etc/nginx/nginx.conf
              subPath: nginx.conf
      volumes:
        - name: config
          configMap:
            name: runtime-observability-nginx
---
apiVersion: v1
kind: Service
metadata:
  name: runtime-observability
  namespace: ${namespace}
spec:
  selector:
    app: runtime-observability
  ports:
    - name: http
      port: 8080
      targetPort: 8080
EOF
  scenario_wait_deployment_available "${namespace}" runtime-observability
}
