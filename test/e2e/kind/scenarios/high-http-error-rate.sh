#!/usr/bin/env bash
set -euo pipefail

run_high_http_error_case() {
  local namespace="$1"
  local target_name="checkout"
  local rule_name="$2"
  local prometheus_endpoint="$3"
  local loki_endpoint="$4"
  local expect_outcome="$5"
  local expect_root_type="$6"
  local expect_signal_count="$7"
  local request_name

  scenario_create_namespace "${namespace}"
  scenario_apply_datasources "${namespace}" "${loki_endpoint}" "${prometheus_endpoint}"
  scenario_apply_yaml <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${target_name}
  namespace: ${namespace}
  labels:
    app: ${target_name}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ${target_name}
  template:
    metadata:
      labels:
        app: ${target_name}
    spec:
      containers:
        - name: app
          image: hashicorp/http-echo:1.0.0
          args: ["-listen=:8080", "-text=checkout"]
          ports:
            - name: http
              containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: inventory
  namespace: ${namespace}
  labels:
    app: inventory
spec:
  selector:
    app: inventory
  ports:
    - name: http
      port: 8080
      targetPort: 8080
---
apiVersion: aiops.platform/v1alpha1
kind: RiskRule
metadata:
  name: ${rule_name}
  namespace: ${namespace}
spec:
  interval: 5s
  window: 10m
  severity: high
  targetSelector:
    namespaceSelector:
      matchNames: [${namespace}]
    workloadSelector:
      matchLabels:
        app: ${target_name}
      kinds: [Deployment]
  signals:
    - name: http-5xx
      datasourceRef:
        name: prometheus
      queryType: metric
      query: http_5xx_error_rate{app="${target_name}"}
      threshold:
        operator: ">"
        value: 0.1
    - name: dependency-errors
      datasourceRef:
        name: loki
      queryType: log
      queryTemplate: '{app="${target_name}"} |= "unavailable"'
      threshold:
        operator: count_gt
        value: 0
  ai:
    rcaEnabled: true
    providerRef:
      name: heuristic-provider
  investigationPolicy:
    mode: CreateRequest
    createRiskSignal: true
    evidenceRequirements:
      profile: HighHTTPErrorRate
EOF

  scenario_wait_deployment_available "${namespace}" "${target_name}"
  if [[ "${expect_outcome}" == "Confirmed" ]]; then
    scenario_wait_request_contract "${namespace}" "${rule_name}" \
      Confirmed HighHTTPErrorRate prometheus
  else
    scenario_apply_yaml <<EOF
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: ${target_name}-manual-investigation
  namespace: ${namespace}
  labels:
    ${SCENARIO_RULE_LABEL}: ${rule_name}
spec:
  target:
    apiVersion: apps/v1
    kind: Deployment
    namespace: ${namespace}
    name: ${target_name}
  queries:
    - name: http-5xx
      datasourceRef:
        name: prometheus
      queryType: metric
      query: http_5xx_error_rate{app="${target_name}"}
    - name: dependency-errors
      datasourceRef:
        name: loki
      queryType: log
      queryTemplate: '{app="${target_name}"} |= "unavailable"'
  modelProviderRef:
    name: heuristic-provider
  mode: readOnly
  createRiskSignal: true
  evidenceRequirements:
    profile: HighHTTPErrorRate
EOF
    SCENARIO_REQUEST_NAME="${target_name}-manual-investigation"
  fi
  request_name="${SCENARIO_REQUEST_NAME}"
  scenario_wait_request_terminal "${namespace}" "${request_name}"
  scenario_wait_converged "${namespace}" "${rule_name}" "${request_name}"
  if [[ "${expect_root_type}" == "HighHTTPErrorRate" ]]; then
    scenario_assert_contract "${namespace}" "${rule_name}" "${request_name}" \
      "${expect_outcome}" "${expect_root_type}" "v1/Service/${namespace}/inventory" "${expect_signal_count}" prometheus
    scenario_assert_json_jq "investigationrequest/${request_name}" "${namespace}" \
      --arg targetNamespace "${namespace}" \
      'any(.status.evidenceRefs[]?; .source == "loki" and ((.relatedTargets // []) | any(.[]; .kind == "Service" and .name == "inventory" and .namespace == $targetNamespace)))'
  else
    scenario_assert_contract "${namespace}" "${rule_name}" "${request_name}" \
      "${expect_outcome}" "" "" "${expect_signal_count}" prometheus
  fi
  if [[ "${expect_signal_count}" == "1" ]]; then
    scenario_wait_signal_for_request "${namespace}" "${rule_name}" "${request_name}"
  fi
  scenario_capture_case "${rule_name}" "${namespace}" "${rule_name}" "${request_name}" Deployment "${target_name}"
  scenario_cleanup_namespace "${namespace}"
}

run_high_http_error_rate_scenarios() {
  log_section "Kind Scenario: HighHTTPErrorRate"
  run_high_http_error_case \
    "${SCENARIO_NAMESPACE_PREFIX}-high-http-positive" \
    high-http-error-positive-rule \
    "http://runtime-observability.${SCENARIO_OBSERVABILITY_NAMESPACE}.svc.cluster.local:8080/high" \
    "http://runtime-observability.${SCENARIO_OBSERVABILITY_NAMESPACE}.svc.cluster.local:8080/high-positive" \
    Confirmed HighHTTPErrorRate 1
  run_high_http_error_case \
    "${SCENARIO_NAMESPACE_PREFIX}-high-http-negative" \
    high-http-error-negative-rule \
    "http://runtime-observability.${SCENARIO_OBSERVABILITY_NAMESPACE}.svc.cluster.local:8080/high" \
    "http://runtime-observability.${SCENARIO_OBSERVABILITY_NAMESPACE}.svc.cluster.local:8080/high-negative" \
    Inconclusive "" 0
}
