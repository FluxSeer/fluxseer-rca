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
  local profile="${7:-CrashLoopBackOff}"
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
      profile: ${profile}
EOF

  scenario_wait_pod_label_event_fragment "${namespace}" app "${target_name}" BackOff "Back-off"
  if [[ "${profile}" == "CrashLoopBackOffV2" ]]; then
    scenario_wait_pod_label_waiting_reason "${namespace}" app "${target_name}" CrashLoopBackOff
  fi

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
    profile: ${profile}
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
  if [[ "${profile}" == "CrashLoopBackOffV2" ]]; then
    scenario_assert_json_jq "investigationrequest/${request_name}" "${namespace}" \
      '.status.evidenceCoverage.profile == "CrashLoopBackOffV2" and any(.status.evidenceRefs[]?; .kind == "containerTermination" and (.summary | contains("lastTerminatedReason=Error") and contains("exitCode=1")))'
    grep -Fq 'lastTerminatedReason=Error exitCode=1' "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${target_name}/public-report.md"
    grep -Fq 'termination status alone does not explain why the process exited' "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${target_name}/public-report.md"
  fi
  scenario_cleanup_namespace "${namespace}"
}

run_crashloop_v2_evidence_gap_case() {
  local mode="$1"
  local expected_reason="$2"
  local namespace="${SCENARIO_NAMESPACE_PREFIX}-crashloop-v2-${mode}"
  local target_name="crashloop-v2-${mode}"
  local rule_name="${target_name}-report"
  local request_name="${target_name}-investigation"
  local pod_name pod_uid event_time case_dir

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
      schedulerName: fluxseer-no-scheduler
      containers:
        - name: app
          image: busybox:1.36
          command: ["sh", "-c", "sleep 3600"]
EOF

  live_harness_wait_for_command "target Pod created" bash -c \
    '[[ "$(kubectl --context "$1" get pods -n "$2" -l app="$3" -o json | jq ".items | length")" -ge 1 ]]' \
    _ "${LIVE_HARNESS_CONTEXT}" "${namespace}" "${target_name}"
  pod_name="$(scenario_kubectl get pods -n "${namespace}" -l app="${target_name}" -o jsonpath='{.items[0].metadata.name}')"

  case "${mode}" in
    stale)
      scenario_kubectl patch pod "${pod_name}" -n "${namespace}" --subresource=status --type=merge \
        -p '{"status":{"containerStatuses":[{"name":"app","restartCount":1,"state":{"waiting":{"reason":"CrashLoopBackOff"}},"lastState":{"terminated":{"reason":"Error","exitCode":1,"finishedAt":"2020-01-01T00:00:00Z"}}}]}}' >/dev/null
      ;;
    truncated)
      scenario_kubectl scale deployment "${target_name}" -n "${namespace}" --replicas=21 >/dev/null
      live_harness_wait_for_command "21 target Pods created" bash -c \
        '[[ "$(kubectl --context "$1" get pods -n "$2" -l app="$3" -o json | jq ".items | length")" -ge 21 ]]' \
        _ "${LIVE_HARNESS_CONTEXT}" "${namespace}" "${target_name}"
      ;;
  esac

  pod_uid="$(scenario_kubectl get pod "${pod_name}" -n "${namespace}" -o jsonpath='{.metadata.uid}')"
  event_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  scenario_apply_yaml <<EOF
apiVersion: v1
kind: Event
metadata:
  name: ${target_name}-backoff
  namespace: ${namespace}
involvedObject:
  apiVersion: v1
  kind: Pod
  name: ${pod_name}
  namespace: ${namespace}
  uid: ${pod_uid}
reason: BackOff
message: Back-off restarting failed container app
source:
  component: kubelet
firstTimestamp: ${event_time}
lastTimestamp: ${event_time}
count: 1
type: Warning
EOF

  if [[ "${mode}" == permission ]]; then
    scenario_kubectl patch clusterrole fluxseer-rca-manager-role --type=json \
      -p='[{"op":"replace","path":"/rules/0/resources","value":["events","namespaces","services"]}]' >/dev/null
  fi

  scenario_apply_yaml <<EOF
apiVersion: aiops.platform/v1alpha1
kind: InvestigationRequest
metadata:
  name: ${request_name}
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
  modelProviderRef:
    name: heuristic-provider
  mode: readOnly
  createRiskSignal: false
  evidenceRequirements:
    profile: CrashLoopBackOffV2
EOF
  scenario_wait_request_terminal "${namespace}" "${request_name}"

  if [[ "${mode}" == permission ]]; then
    scenario_kubectl patch clusterrole fluxseer-rca-manager-role --type=json \
      -p='[{"op":"replace","path":"/rules/0/resources","value":["events","pods","namespaces","services"]}]' >/dev/null
    scenario_assert_json_jq "investigationrequest/${request_name}" "${namespace}" \
      '.status.phase == "Failed" and .status.failure.code == "DatasourceQueryFailed" and (.status.observedGeneration == .metadata.generation)'
  else
    scenario_assert_json_jq "investigationrequest/${request_name}" "${namespace}" \
      ".status.phase == \"Completed\" and .status.outcome == \"Inconclusive\" and any(.status.missingEvidence[]?; .reason == \"${expected_reason}\") and any(.status.evidenceCoverage.incompleteChecks[]?; . == \"containerTermination:LastTermination\") and (.status.observedGeneration == .metadata.generation)"
    if [[ "${mode}" == truncated ]]; then
      scenario_assert_json_jq "investigationrequest/${request_name}" "${namespace}" \
        'any(.status.evidenceRefs[]?; .kind == "containerTermination" and .truncated == true and .truncationReason == "TargetPodLimitExceeded")'
    fi
  fi
  if [[ "$(scenario_kubectl get risksignals -n "${namespace}" -o json | jq '.items | length')" != 0 ]]; then
    echo "unexpected RiskSignal for evidence gap case ${namespace}" >&2
    return 1
  fi
  if [[ "$(scenario_kubectl get remediationplans,agentactions -n "${namespace}" -o json | jq '[.items[]] | length')" != 0 ]]; then
    echo "unexpected remediation resources for evidence gap case ${namespace}" >&2
    return 1
  fi

  # Link the manual request to a quiet rule so the public report can render it.
  scenario_apply_yaml <<EOF
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
    - name: non-matching-event
      datasourceRef:
        name: kubernetes-events
      queryType: event
      reasons: [NeverMatch]
      threshold:
        operator: count_gt
        value: 0
  ai:
    rcaEnabled: true
    providerRef:
      name: heuristic-provider
EOF
  scenario_capture_case "${target_name}" "${namespace}" "${rule_name}" "${request_name}" Deployment "${target_name}"
  case_dir="${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${target_name}"
  case "${mode}" in
    missing) grep -Fq 'target workload still has in-scope Pods' "${case_dir}/public-report.md" ;;
    stale) grep -Fq 'outside the requested window' "${case_dir}/public-report.md" ;;
    truncated) grep -Fq 'bounded termination status scan' "${case_dir}/public-report.md" ;;
    permission) grep -Fq 'target Kubernetes Pods' "${case_dir}/public-report.md" ;;
  esac
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
  run_crashloop_case \
    "${SCENARIO_NAMESPACE_PREFIX}-crashloop-v2" \
    crashloop-v2 \
    "http://runtime-observability.${SCENARIO_OBSERVABILITY_NAMESPACE}.svc.cluster.local:8080/crash-negative" \
    Inconclusive "" 0 CrashLoopBackOffV2
  run_crashloop_v2_evidence_gap_case missing ContainerTerminationEvidenceMissing
  run_crashloop_v2_evidence_gap_case stale ContainerTerminationEvidenceStale
  run_crashloop_v2_evidence_gap_case truncated ContainerTerminationEvidenceTruncated
  run_crashloop_v2_evidence_gap_case permission DatasourceQueryFailed
}
