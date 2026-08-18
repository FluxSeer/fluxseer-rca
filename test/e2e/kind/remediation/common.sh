#!/usr/bin/env bash
set -euo pipefail

remediation_script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${remediation_script_dir}/../scenarios/common.sh"

REMEDIATION_NAMESPACE_PREFIX="${REMEDIATION_NAMESPACE_PREFIX:-fluxseer-live-remediation}"
REMEDIATION_OBSERVATION_TIMEOUT_SECONDS="${REMEDIATION_OBSERVATION_TIMEOUT_SECONDS:-900}"
REMEDIATION_APPROVER="${REMEDIATION_APPROVER:-sre-live-qualification}"
REMEDIATION_DATASOURCE="kubernetes-events"
REMEDIATION_ACTION_TYPE="kubernetes.rolloutRestart"
REMEDIATION_EXECUTION_ANNOTATION="fluxseer.io/execution-id"
REMEDIATION_TARGET_UID_ANNOTATION="fluxseer-rca.aiops.platform/target-uid"

remediation_wait_json_jq() {
  local description="$1"
  local resource="$2"
  local namespace="$3"
  local expression="$4"

  live_harness_wait_for_command \
    "${description}" \
    scenario_assert_json_jq "${resource}" "${namespace}" "${expression}"
}

remediation_wait_action() {
  local namespace="$1"
  local action_name="$2"
  local expression="$3"
  remediation_wait_json_jq "AgentAction ${namespace}/${action_name}" "agentaction/${action_name}" "${namespace}" "${expression}"
}

remediation_wait_plan_and_action() {
  local namespace="$1"
  local plan_name="$2"
  local action_name="$3"

  remediation_wait_json_jq "RemediationPlan ${namespace}/${plan_name} created" "remediationplan/${plan_name}" "${namespace}" \
    '.metadata.resourceVersion != null and .spec.steps[0].actionType == "kubernetes.rolloutRestart"'
  remediation_wait_action "${namespace}" "${action_name}" \
    '.status.phase == "WaitingApproval" and .status.approval != null and .status.approval.source == "ManualApprovalRequired" and ((.status.approval.approved // false) == false)'
}

remediation_wait_unhealthy_baseline() {
  local namespace="$1"
  local deployment_name="$2"

  remediation_wait_json_jq "unhealthy baseline ${namespace}/${deployment_name}" "deployment/${deployment_name}" "${namespace}" \
    '((.status.availableReplicas // 0) == 0) and ((.status.updatedReplicas // 0) >= 1) and ((.status.observedGeneration // 0) >= (.metadata.generation // 0))'
}

remediation_approve_action() {
  local namespace="$1"
  local action_name="$2"

  scenario_kubectl patch "agentaction/${action_name}" -n "${namespace}" --type merge \
    -p "$(jq -cn --arg approver "${REMEDIATION_APPROVER}" '{spec:{approvedBy:$approver}}')" >/dev/null
  remediation_wait_action "${namespace}" "${action_name}" \
    '.status.phase == "Executing" or .status.phase == "Succeeded" or .status.phase == "Failed"'
}

remediation_wait_execution_succeeded() {
  local namespace="$1"
  local action_name="$2"

  remediation_wait_action "${namespace}" "${action_name}" \
    '.status.phase == "Succeeded" and .status.execution.outcome == "Succeeded" and (.status.execution.executionID // "") != "" and (.status.execution.idempotencyKey // "") != ""'
}

remediation_wait_verification_ref() {
  local namespace="$1"
  local action_name="$2"

  remediation_wait_action "${namespace}" "${action_name}" \
    '(.status.effectiveness.verificationRef.name // "") != "" and .status.effectiveness.phase == "Verifying"'
}

remediation_wait_effectiveness_terminal() {
  local namespace="$1"
  local action_name="$2"
  local expected_outcome="$3"

  remediation_wait_action "${namespace}" "${action_name}" \
    ".status.effectiveness.phase == \"Completed\" and .status.effectiveness.outcome == \"${expected_outcome}\""
}

remediation_apply_datasource() {
  local namespace="$1"
  scenario_apply_yaml <<EOF
apiVersion: aiops.platform/v1alpha1
kind: DataSource
metadata:
  name: ${REMEDIATION_DATASOURCE}
  namespace: ${namespace}
spec:
  type: kubernetesEvents
EOF
  scenario_wait_jsonpath_equals "verification datasource observed in ${namespace}" \
    "datasource/${REMEDIATION_DATASOURCE}" "${namespace}" '{.status.phase}' Observed
}

remediation_apply_fixture() {
  local namespace="$1"
  local target_name="$2"
  local fixture_mode="$3"
  local target_uid
  local command
  local readiness_command

  if [[ "${fixture_mode}" == "ineffective" ]]; then
    command='while true; do sleep 30; done'
    readiness_command='echo "deterministic bad configuration remains after restart"; exit 1'
  else
    command='while true; do sleep 30; done'
    readiness_command='test -n "${FLUXSEER_EXECUTION_ID}"'
  fi

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
          command: ["sh", "-c"]
          args:
            - |-
              ${command}
          readinessProbe:
            exec:
              command:
                - sh
                - -c
                - |-
                  ${readiness_command}
            initialDelaySeconds: 1
            periodSeconds: 2
            failureThreshold: 1
          env:
            - name: FLUXSEER_EXECUTION_ID
              valueFrom:
                fieldRef:
                  fieldPath: metadata.annotations['fluxseer.io/execution-id']
EOF

  remediation_wait_unhealthy_baseline "${namespace}" "${target_name}"
  scenario_kubectl get "deployment/${target_name}" -n "${namespace}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/deployment-before.json"
  target_uid="$(jq -r '.metadata.uid' "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/deployment-before.json")"
  if [[ -z "${target_uid}" || "${target_uid}" == "null" ]]; then
    echo "failed to resolve Deployment UID for ${namespace}/${target_name}" >&2
    return 1
  fi

  scenario_kubectl get pods -n "${namespace}" -l "app=${target_name}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/pods-before.json"
  scenario_kubectl get replicasets -n "${namespace}" -l "app=${target_name}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/replicasets-before.json"

  scenario_apply_yaml <<EOF
apiVersion: aiops.platform/v1alpha1
kind: RiskSignal
metadata:
  name: ${target_name}-risk
  namespace: ${namespace}
  annotations:
    ${REMEDIATION_TARGET_UID_ANNOTATION}: ${target_uid}
spec:
  target:
    cluster: kind
    namespace: ${namespace}
    kind: Deployment
    name: ${target_name}
    apiVersion: apps/v1
  signalType: incident
  actionType: ${REMEDIATION_ACTION_TYPE}
  severity: high
  confidence: 95
  dryRun: false
  ttlSeconds: 1800
  evidence:
    - kind: incident-context
      source: ${REMEDIATION_DATASOURCE}
      reason: BackOff
      summary: deterministic unhealthy workload fixture
EOF
}

remediation_assert_rbac() {
  local service_account="system:serviceaccount:${LIVE_HARNESS_RELEASE_NAMESPACE}:fluxseer-rca-controller-manager"
  local can_patch
  can_patch="$(live_harness_kubectl auth can-i patch deployments --as="${service_account}" || true)"
  if [[ "${can_patch}" != "yes" ]]; then
    echo "experimental remediation profile cannot patch Deployments: ${can_patch}" >&2
    return 1
  fi
}

remediation_assert_chain_contract() {
  local namespace="$1"
  local target_name="$2"
  local fixture_mode="$3"
  local expected_outcome="$4"
  local plan_name="${target_name}-risk-plan"
  local action_name="${plan_name}-action"
  local verification_name
  local execution_id
  local idempotency_key
  local target_uid
  local before_generation
  local after_generation

  remediation_wait_effectiveness_terminal "${namespace}" "${action_name}" "${expected_outcome}"
  verification_name="$(scenario_kubectl get "agentaction/${action_name}" -n "${namespace}" -o json | jq -r '.status.effectiveness.verificationRef.name')"
  execution_id="$(scenario_kubectl get "agentaction/${action_name}" -n "${namespace}" -o json | jq -r '.status.execution.executionID')"
  idempotency_key="$(scenario_kubectl get "agentaction/${action_name}" -n "${namespace}" -o json | jq -r '.status.execution.idempotencyKey')"
  target_uid="$(scenario_kubectl get "deployment/${target_name}" -n "${namespace}" -o json | jq -r '.metadata.uid')"

  scenario_assert_json_jq "agentaction/${action_name}" "${namespace}" \
    --arg expectedOutcome "${expected_outcome}" \
    --arg expectedActionType "${REMEDIATION_ACTION_TYPE}" \
    --arg executionID "${execution_id}" \
    --arg targetUID "${target_uid}" \
    --arg verification "${verification_name}" \
    '(.status.phase == "Succeeded") and (.spec.actionType == $expectedActionType) and (.status.approval.approved == true) and (.status.execution.outcome == "Succeeded") and (.status.execution.executionID == $executionID) and (.status.execution.idempotencyKey != "") and (.status.effectiveness.outcome == $expectedOutcome) and (.status.effectiveness.verificationRef.name == $verification) and ((.metadata.annotations["fluxseer-rca.aiops.platform/target-uid"] // "") == $targetUID) and ((.status.effectiveness.baseline.capturedAt // "") < (.status.execution.startedAt // ""))'

  scenario_assert_json_jq "remediationplan/${plan_name}" "${namespace}" \
    --arg targetUID "${target_uid}" \
    '.status.phase == "WaitingApproval" and ((.metadata.annotations["fluxseer-rca.aiops.platform/target-uid"] // "") == $targetUID)'

  scenario_assert_json_jq "investigationrequest/${verification_name}" "${namespace}" \
    --arg actionName "${action_name}" \
    --arg executionID "${execution_id}" \
    '.spec.mode == "readOnly" and .spec.purpose == "effectivenessVerification" and ((.spec.createRiskSignal // false) == false) and .spec.correlation.agentActionRef.name == $actionName and .spec.correlation.executionID == $executionID and (.metadata.ownerReferences | any(.[]; .kind == "AgentAction" and .name == $actionName))'

  if [[ "${expected_outcome}" == "Effective" ]]; then
    remediation_wait_json_jq "healthy post-action Deployment ${namespace}/${target_name}" "deployment/${target_name}" "${namespace}" \
      '(.status.availableReplicas // 0) == (.spec.replicas // 0) and (.status.readyReplicas // 0) == (.spec.replicas // 0) and (.status.updatedReplicas // 0) == (.spec.replicas // 0) and .status.observedGeneration == .metadata.generation'
  elif [[ "${expected_outcome}" == "Ineffective" ]]; then
    scenario_assert_json_jq "deployment/${target_name}" "${namespace}" \
      '((.status.availableReplicas // 0) < (.spec.replicas // 0)) and ((.status.readyReplicas // 0) < (.spec.replicas // 0))'
  fi

  before_generation="$(jq -r '.metadata.generation' "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/deployment-before.json")"
  after_generation="$(scenario_kubectl get "deployment/${target_name}" -n "${namespace}" -o json | jq -r '.metadata.generation')"
  if (( after_generation <= before_generation )); then
    echo "Deployment generation did not advance for ${namespace}/${target_name}" >&2
    return 1
  fi
  scenario_assert_json_jq "deployment/${target_name}" "${namespace}" \
    --arg executionID "${execution_id}" \
    '.spec.template.metadata.annotations["fluxseer.io/execution-id"] == $executionID and .status.observedGeneration == .metadata.generation'

  scenario_kubectl get "deployment/${target_name}" -n "${namespace}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/deployment-after.json"
  scenario_kubectl get pods -n "${namespace}" -l "app=${target_name}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/pods-after.json"
  scenario_kubectl get replicasets -n "${namespace}" -l "app=${target_name}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/replicasets-after.json"
  scenario_kubectl get events -n "${namespace}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/events.json"
  scenario_kubectl get "risksignal/${target_name}-risk" -n "${namespace}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/risksignal.json"
  scenario_kubectl get "remediationplan/${plan_name}" -n "${namespace}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/remediationplan.json"
  scenario_kubectl get "agentaction/${action_name}" -n "${namespace}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/agentaction.json"
  scenario_kubectl get "investigationrequest/${verification_name}" -n "${namespace}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/verification-request.json"

  (cd "${live_harness_repo_root}" && GOWORK=off go run ./cmd/fluxseer report agentaction "${action_name}" --namespace "${namespace}" --output json) >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/public-agentaction-report.json"
  jq -e --arg expectedOutcome "${expected_outcome}" \
    '.agentAction.status.execution.outcome == "Succeeded" and .agentAction.status.effectiveness.outcome == $expectedOutcome and (.verification.spec.mode == "readOnly")' \
    "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/public-agentaction-report.json" >/dev/null

  local plan_count action_count
  plan_count="$(scenario_kubectl get remediationplans -n "${namespace}" -o json | jq '.items | length')"
  action_count="$(scenario_kubectl get agentactions -n "${namespace}" -o json | jq '.items | length')"
  if [[ "${plan_count}" != "1" || "${action_count}" != "1" ]]; then
    echo "unexpected remediation chain count in ${namespace}: plans=${plan_count} actions=${action_count}" >&2
    return 1
  fi

  jq -n \
    --arg schema "fluxseer-safe-remediation-kind-internal-summary/v1" \
    --arg scenario "rolloutRestart-${fixture_mode}" \
    --arg expectedOutcome "${expected_outcome}" \
    --arg executionID "${execution_id}" \
    --arg idempotencyKey "${idempotency_key}" \
    --arg verificationName "${verification_name}" \
    --arg baselineCapturedAt "$(jq -r '.status.effectiveness.baseline.capturedAt' "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/agentaction.json")" \
    --arg executionStartedAt "$(jq -r '.status.execution.startedAt' "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/agentaction.json")" \
    --arg executionFinishedAt "$(jq -r '.status.execution.finishedAt' "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/agentaction.json")" \
    --arg verificationCreatedAt "$(jq -r '.metadata.creationTimestamp' "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/verification-request.json")" \
    --arg verifiedAt "$(jq -r '.status.updatedAt' "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/agentaction.json")" \
    '{schemaVersion:$schema,scenario:$scenario,expectedEffectiveness:$expectedOutcome,executionID:$executionID,idempotencyKey:$idempotencyKey,verificationRequest:$verificationName,sideEffects:{deploymentMutations:1,remediationPlans:1,agentActions:1,verificationRemediationPlans:0,verificationAgentActions:0},invariants:{executionSucceededIndependentlyOfEffectiveness:true,targetUIDBound:true,baselineBeforeExecution:true,verificationReadOnly:true,verificationCorrelated:true},timeline:{baselineCapturedAt:$baselineCapturedAt,executionStartedAt:$executionStartedAt,executionFinishedAt:$executionFinishedAt,verificationCreatedAt:$verificationCreatedAt,verifiedAt:$verifiedAt}}' \
    >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/internal-summary.json"
  cp "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/internal-summary.json" "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/timeline.json"
}

remediation_run_case() {
  local fixture_mode="$1"
  local expected_outcome="$2"
  local namespace="${REMEDIATION_NAMESPACE_PREFIX}-${fixture_mode}"
  local target_name="rollout-restart-${fixture_mode}"
  local plan_name="${target_name}-risk-plan"
  local action_name="${plan_name}-action"

  log_section "Kind Scenario: rolloutRestart-${fixture_mode}"
  mkdir -p "${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}"
  scenario_create_namespace "${namespace}"
  remediation_apply_datasource "${namespace}"
  remediation_apply_fixture "${namespace}" "${target_name}" "${fixture_mode}"

  remediation_wait_plan_and_action "${namespace}" "${plan_name}" "${action_name}"
  scenario_kubectl get "risksignal/${target_name}-risk" -n "${namespace}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/risksignal-before.json"
  scenario_kubectl get "remediationplan/${plan_name}" -n "${namespace}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/remediationplan-before.json"
  scenario_kubectl get "agentaction/${action_name}" -n "${namespace}" -o json >"${LIVE_HARNESS_ARTIFACT_ROOT}/scenarios/${fixture_mode}/agentaction-before.json"

  remediation_approve_action "${namespace}" "${action_name}"
  remediation_wait_execution_succeeded "${namespace}" "${action_name}"

  if [[ "${fixture_mode}" == "inconclusive" ]]; then
    scenario_kubectl delete "datasource/${REMEDIATION_DATASOURCE}" -n "${namespace}" --wait=true >/dev/null
  fi

  remediation_wait_verification_ref "${namespace}" "${action_name}"
  remediation_wait_effectiveness_terminal "${namespace}" "${action_name}" "${expected_outcome}"
  remediation_assert_chain_contract "${namespace}" "${target_name}" "${fixture_mode}" "${expected_outcome}"
  scenario_cleanup_namespace "${namespace}"
}
