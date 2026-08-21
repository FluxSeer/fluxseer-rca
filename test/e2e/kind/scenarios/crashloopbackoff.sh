#!/usr/bin/env bash
set -euo pipefail

run_crashloop_case() {
  local namespace="$1"
  local target_name="$2"
  local rule_name="${target_name}-rule"
  local loki_endpoint="$3"
  local expect_outcome="$4"
  local expect_root_type="$5"
  local expect_signal_count="$6"
  local expect_root_entity=""
  local request_name

  if [[ -n "${expect_root_type}" ]]; then
    expect_root_entity="apps/v1/Deployment/${namespace}/${target_name}"
  fi

  scenario_create_namespace "${namespace}"
  scenario_apply_datasources "${namespace}" "${loki_endpoint}"
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
          image: busybox:1.36
          command: ["sh", "-c", "echo 'panic during startup: invalid configuration for checkout'; exit 1"]
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
    - name: crashloop-event
      datasourceRef:
        name: kubernetes-events
      queryType: event
      reasons: [BackOff]
      threshold:
        operator: count_gt
        value: 0
    - name: application-startup-failure
      datasourceRef:
        name: loki
      queryType: log
      queryTemplate: '{app="${target_name}"} |= "panic"'
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
      profile: CrashLoopBackOff
EOF

  scenario_wait_pod_label_event_fragment "${namespace}" app "${target_name}" BackOff "Back-off"

  if [[ "${expect_outcome}" == "Confirmed" ]]; then
    scenario_wait_request_contract "${namespace}" "${rule_name}" \
      Confirmed CrashLoop kubernetes-events BackOff
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
    - name: crashloop-event
      datasourceRef:
        name: kubernetes-events
      queryType: event
      reasons: [BackOff]
    - name: application-startup-failure
      datasourceRef:
        name: loki
      queryType: log
      queryTemplate: '{app="${target_name}"} |= "panic"'
  modelProviderRef:
    name: heuristic-provider
  mode: readOnly
  createRiskSignal: true
  evidenceRequirements:
    profile: CrashLoopBackOff
EOF
    SCENARIO_REQUEST_NAME="${target_name}-manual-investigation"
  fi
  request_name="${SCENARIO_REQUEST_NAME}"
  scenario_wait_request_terminal "${namespace}" "${request_name}"
  scenario_wait_converged "${namespace}" "${rule_name}" "${request_name}"
  scenario_assert_contract "${namespace}" "${rule_name}" "${request_name}" \
    "${expect_outcome}" "${expect_root_type}" "${expect_root_entity}" "${expect_signal_count}" kubernetes-events BackOff
  if [[ "${expect_signal_count}" == "1" ]]; then
    scenario_wait_signal_for_request "${namespace}" "${rule_name}" "${request_name}"
  fi
  scenario_capture_case "${target_name}" "${namespace}" "${rule_name}" "${request_name}" Deployment "${target_name}"
  scenario_cleanup_namespace "${namespace}"
}

run_crashloopbackoff_scenarios() {
  log_section "Kind Scenario: CrashLoopBackOff"
  run_crashloop_case \
    "${SCENARIO_NAMESPACE_PREFIX}-crashloop-positive" \
    crashloop-positive \
    "http://runtime-observability.${SCENARIO_OBSERVABILITY_NAMESPACE}.svc.cluster.local:8080/crash-positive" \
    Confirmed CrashLoop 1
  run_crashloop_case \
    "${SCENARIO_NAMESPACE_PREFIX}-crashloop-negative" \
    crashloop-negative \
    "http://runtime-observability.${SCENARIO_OBSERVABILITY_NAMESPACE}.svc.cluster.local:8080/crash-negative" \
    Inconclusive "" 0
}
