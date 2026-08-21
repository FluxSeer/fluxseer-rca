#!/usr/bin/env bash
set -euo pipefail

run_service_port_mismatch_scenario() {
  local namespace="${SCENARIO_NAMESPACE_PREFIX}-service-port"
  local rule_name="service-port-mismatch-rule"
  local request_name
  local target_name="service-port-mismatch"

  log_section "Kind Scenario: ServicePortMismatch"
  scenario_create_namespace "${namespace}"
  scenario_apply_datasources "${namespace}"
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
          args: ["-listen=:3000", "-text=service-port-mismatch"]
          ports:
            - name: http
              containerPort: 3000
---
apiVersion: v1
kind: Service
metadata:
  name: ${target_name}
  namespace: ${namespace}
spec:
  selector:
    app: ${target_name}
  ports:
    - name: http
      port: 80
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
    - name: service-port-mismatch
      datasourceRef:
        name: kubernetes-events
      queryType: serviceConfiguration
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
      profile: serviceportmismatch
EOF

  scenario_wait_deployment_available "${namespace}" "${target_name}"
  scenario_wait_request_contract "${namespace}" "${rule_name}" \
    Confirmed ServicePortMismatch kubernetes-events ServicePortMismatch
  request_name="${SCENARIO_REQUEST_NAME}"
  scenario_wait_request_terminal "${namespace}" "${request_name}"
  scenario_wait_converged "${namespace}" "${rule_name}" "${request_name}"
  scenario_assert_contract "${namespace}" "${rule_name}" "${request_name}" \
    Confirmed ServicePortMismatch "apps/v1/Deployment/${namespace}/${target_name}" 1 kubernetes-events ServicePortMismatch
  scenario_wait_signal_for_request "${namespace}" "${rule_name}" "${request_name}"
  scenario_capture_case "service-port-mismatch" "${namespace}" "${rule_name}" "${request_name}" Deployment "${target_name}"
  scenario_cleanup_namespace "${namespace}"
}
