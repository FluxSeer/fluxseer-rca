#!/usr/bin/env bash
set -euo pipefail

run_failed_scheduling_scenario() {
  local namespace="${SCENARIO_NAMESPACE_PREFIX}-failed-scheduling"
  local rule_name="failed-scheduling-rule"
  local target_name="failed-scheduling"
  local request_name

  log_section "Kind Scenario: FailedScheduling"
  scenario_create_namespace "${namespace}"
  scenario_apply_datasources "${namespace}"
  scenario_apply_yaml <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${target_name}
  namespace: ${namespace}
  labels:
    app: ${target_name}
spec:
  containers:
    - name: app
      image: hashicorp/http-echo:1.0.0
      resources:
        requests:
          memory: 1Ti
        limits:
          memory: 1Ti
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
      kinds: [Pod]
  signals:
    - name: failed-scheduling
      datasourceRef:
        name: kubernetes-events
      queryType: event
      reasons: [FailedScheduling]
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
      profile: failedscheduling
EOF

  scenario_wait_jsonpath_equals "Pending Pod observed" "pod/${target_name}" "${namespace}" '{.status.phase}' Pending
  scenario_wait_event_fragment "${namespace}" "${target_name}" FailedScheduling "Insufficient memory"
  scenario_wait_request_name "${namespace}" "${rule_name}"
  request_name="${SCENARIO_REQUEST_NAME}"
  scenario_wait_request_terminal "${namespace}" "${request_name}"
  scenario_wait_converged "${namespace}" "${rule_name}" "${request_name}"
  scenario_assert_contract "${namespace}" "${rule_name}" "${request_name}" \
    Confirmed SchedulingFailure "v1/Pod/${namespace}/${target_name}" 1 kubernetes-events FailedScheduling
  scenario_wait_signal_for_request "${namespace}" "${rule_name}" "${request_name}"
  scenario_capture_case "failed-scheduling" "${namespace}" "${rule_name}" "${request_name}" Pod "${target_name}"
  scenario_cleanup_namespace "${namespace}"
}
