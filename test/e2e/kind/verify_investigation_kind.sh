#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../../.." && pwd)"
source "${script_dir}/common.sh"

request_namespace="${FLUXAGENT_DEMO_NAMESPACE}"
target_namespace="${FLUXAGENT_DEMO_NAMESPACE}"
target_name="fluxagent-sample"
success_request="investigate-sample-success"
missing_ds_request="investigate-sample-missing-ds"
capability_request="investigate-sample-capability-mismatch"
provider_request="investigate-sample-missing-provider"
provider_auth_request="investigate-sample-provider-auth-failed"
provider_rate_request="investigate-sample-provider-rate-limited"
reuse_cluster="${FLUXAGENT_E2E_REUSE_CLUSTER:-false}"

if [[ "${reuse_cluster}" != "true" ]]; then
  preflight_verify_e2e_kind

  cleanup() {
    local exit_code="$1"
    trap - EXIT
    cleanup_demo
    if command -v kind >/dev/null 2>&1; then
      bash "${script_dir}/verify_cleanup.sh"
    fi
    exit "${exit_code}"
  }

  on_exit() {
    local exit_code="$1"
    cleanup "${exit_code}"
  }

  trap 'on_exit $?' EXIT
fi

create_query_file() {
  local path="$1"
  local body="$2"

  cat >"${path}" <<EOF
${body}
EOF
}

verify_successful_investigation() {
  local linked_signal_namespace
  local linked_signal_name
  local investigation_ref

  log_section "Create InvestigationRequest Through CLI"
  (
    cd "${repo_root}"
    GOWORK=off go run ./cmd/fluxagent investigate deployment "${target_name}" \
      --namespace "${target_namespace}" \
      --request-namespace "${request_namespace}" \
      --request-name "${success_request}" \
      --query-file config/samples/investigation-queries.yaml \
      --question "Why is fluxagent-sample failing after fault injection?" \
      --provider heuristic-provider \
      --create-risk-signal \
      --wait
  )

  wait_for_jsonpath_equals \
    "investigationrequest/${success_request}" \
    "${request_namespace}" \
    '{.status.phase}' \
    'Completed'

  wait_for_condition \
    "investigationrequest/${success_request}" \
    "${request_namespace}" \
    "Ready" \
    "True"

  wait_for_condition \
    "investigationrequest/${success_request}" \
    "${request_namespace}" \
    "RCAReady" \
    "True"

  wait_for_nonempty_jsonpath \
    "investigationrequest/${success_request}" \
    "${request_namespace}" \
    '{.status.linkedRiskSignalRef.name}'

  linked_signal_namespace="$(kubectl get investigationrequest "${success_request}" -n "${request_namespace}" -o jsonpath='{.status.linkedRiskSignalRef.namespace}')"
  linked_signal_name="$(kubectl get investigationrequest "${success_request}" -n "${request_namespace}" -o jsonpath='{.status.linkedRiskSignalRef.name}')"
  investigation_ref="${request_namespace}/${success_request}"

  wait_for_command "promoted RiskSignal ${linked_signal_namespace}/${linked_signal_name} exists" \
    kubectl get risksignal "${linked_signal_name}" -n "${linked_signal_namespace}"

  wait_for_nonempty_jsonpath \
    "risksignal/${linked_signal_name}" \
    "${linked_signal_namespace}" \
    '{.status.rcaSummary}'

  wait_for_jsonpath_equals \
    "risksignal/${linked_signal_name}" \
    "${linked_signal_namespace}" \
    '{.metadata.annotations.fluxagent\.aiops\.platform/investigation-request}' \
    "${investigation_ref}"

  echo "verified InvestigationRequest ${success_request} completed and promoted RiskSignal ${linked_signal_namespace}/${linked_signal_name}"
}

verify_missing_datasource_degradation() {
  local query_file
  query_file="$(mktemp)"
  trap 'rm -f "${query_file}"' RETURN

  create_query_file "${query_file}" 'queries:
  - name: missing-logs
    datasourceRef:
      name: logs-missing
    queryType: log
    queryTemplate: |
      {namespace="{{ .namespace }}",app="{{ .app }}"} |= "error"'

  log_section "Verify Investigation Degradation: Missing DataSource"
  (
    cd "${repo_root}"
    GOWORK=off go run ./cmd/fluxagent investigate deployment "${target_name}" \
      --namespace "${target_namespace}" \
      --request-namespace "${request_namespace}" \
      --request-name "${missing_ds_request}" \
      --query-file "${query_file}" \
      --question "Should fail because datasource is missing" \
      --wait=false
  )

  wait_for_jsonpath_equals \
    "investigationrequest/${missing_ds_request}" \
    "${request_namespace}" \
    '{.status.phase}' \
    'Failed'

  wait_for_condition_reason \
    "investigationrequest/${missing_ds_request}" \
    "${request_namespace}" \
    "DatasourceResolved" \
    "DataSourceNotFound"

  wait_for_condition_reason \
    "investigationrequest/${missing_ds_request}" \
    "${request_namespace}" \
    "EvidenceCollectionReady" \
    "DataSourceNotFound"

  wait_for_condition_reason \
    "investigationrequest/${missing_ds_request}" \
    "${request_namespace}" \
    "Degraded" \
    "DataSourceNotFound"

  rm -f "${query_file}"
  trap - RETURN
}

verify_capability_mismatch_degradation() {
  local query_file
  query_file="$(mktemp)"
  trap 'rm -f "${query_file}"' RETURN

  create_query_file "${query_file}" 'queries:
  - name: prometheus-as-log
    datasourceRef:
      name: prometheus
    queryType: log
    queryTemplate: |
      {namespace="{{ .namespace }}",app="{{ .app }}"} |= "error"'

  log_section "Verify Investigation Degradation: Capability Mismatch"
  (
    cd "${repo_root}"
    GOWORK=off go run ./cmd/fluxagent investigate deployment "${target_name}" \
      --namespace "${target_namespace}" \
      --request-namespace "${request_namespace}" \
      --request-name "${capability_request}" \
      --query-file "${query_file}" \
      --question "Should fail because prometheus cannot serve log queries" \
      --wait=false
  )

  wait_for_jsonpath_equals \
    "investigationrequest/${capability_request}" \
    "${request_namespace}" \
    '{.status.phase}' \
    'Failed'

  wait_for_condition_reason \
    "investigationrequest/${capability_request}" \
    "${request_namespace}" \
    "QueryTypeSupported" \
    "CapabilityMismatch"

  wait_for_condition_reason \
    "investigationrequest/${capability_request}" \
    "${request_namespace}" \
    "Ready" \
    "CapabilityMismatch"

  wait_for_condition_reason \
    "investigationrequest/${capability_request}" \
    "${request_namespace}" \
    "Degraded" \
    "CapabilityMismatch"

  rm -f "${query_file}"
  trap - RETURN
}

verify_missing_provider_degradation() {
  log_section "Verify Investigation Degradation: Provider Missing"
  (
    cd "${repo_root}"
    GOWORK=off go run ./cmd/fluxagent investigate deployment "${target_name}" \
      --namespace "${target_namespace}" \
      --request-namespace "${request_namespace}" \
      --request-name "${provider_request}" \
      --query-file config/samples/investigation-queries.yaml \
      --question "Should fail because provider is missing" \
      --provider provider-missing \
      --wait=false
  )

  wait_for_jsonpath_equals \
    "investigationrequest/${provider_request}" \
    "${request_namespace}" \
    '{.status.phase}' \
    'Failed'

  wait_for_condition_reason \
    "investigationrequest/${provider_request}" \
    "${request_namespace}" \
    "RCAReady" \
    "ProviderNotFound"

  wait_for_condition_reason \
    "investigationrequest/${provider_request}" \
    "${request_namespace}" \
    "Ready" \
    "ProviderNotFound"

  wait_for_condition_reason \
    "investigationrequest/${provider_request}" \
    "${request_namespace}" \
    "Degraded" \
    "ProviderNotFound"
}

apply_hosted_provider_mocks() {
  log_section "Prepare Hosted Provider Mock Resources"
  kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: openai-demo-secret
  namespace: ${request_namespace}
type: Opaque
stringData:
  api-key: demo-openai-token
---
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: openai-auth-failed
  namespace: ${request_namespace}
spec:
  provider: openai
  model: gpt-5.1
  endpoint: http://fluxagent-observability:8080/demo/providers/openai/auth-failed
  timeout: 2s
  maxTokens: 256
  apiKeySecretRef:
    name: openai-demo-secret
    key: api-key
---
apiVersion: aiops.platform/v1alpha1
kind: ModelProvider
metadata:
  name: openai-rate-limited
  namespace: ${request_namespace}
spec:
  provider: openai
  model: gpt-5.1
  endpoint: http://fluxagent-observability:8080/demo/providers/openai/rate-limited
  timeout: 2s
  maxTokens: 256
  apiKeySecretRef:
    name: openai-demo-secret
    key: api-key
EOF
}

verify_provider_auth_failed_degradation() {
  log_section "Verify Investigation Degradation: Provider Auth Failed"
  (
    cd "${repo_root}"
    GOWORK=off go run ./cmd/fluxagent investigate deployment "${target_name}" \
      --namespace "${target_namespace}" \
      --request-namespace "${request_namespace}" \
      --request-name "${provider_auth_request}" \
      --query-file config/samples/investigation-queries.yaml \
      --question "Should fail because hosted provider credentials are rejected" \
      --provider openai-auth-failed \
      --wait=false
  )

  wait_for_jsonpath_equals \
    "investigationrequest/${provider_auth_request}" \
    "${request_namespace}" \
    '{.status.phase}' \
    'Failed'

  wait_for_condition_reason \
    "investigationrequest/${provider_auth_request}" \
    "${request_namespace}" \
    "RCAReady" \
    "ProviderAuthFailed"

  wait_for_condition_reason \
    "investigationrequest/${provider_auth_request}" \
    "${request_namespace}" \
    "Ready" \
    "ProviderAuthFailed"

  wait_for_condition_reason \
    "investigationrequest/${provider_auth_request}" \
    "${request_namespace}" \
    "Degraded" \
    "ProviderAuthFailed"
}

verify_provider_rate_limited_degradation() {
  log_section "Verify Investigation Degradation: Provider Rate Limited"
  (
    cd "${repo_root}"
    GOWORK=off go run ./cmd/fluxagent investigate deployment "${target_name}" \
      --namespace "${target_namespace}" \
      --request-namespace "${request_namespace}" \
      --request-name "${provider_rate_request}" \
      --query-file config/samples/investigation-queries.yaml \
      --question "Should fail because hosted provider keeps returning 429" \
      --provider openai-rate-limited \
      --wait=false
  )

  wait_for_jsonpath_equals \
    "investigationrequest/${provider_rate_request}" \
    "${request_namespace}" \
    '{.status.phase}' \
    'Failed'

  wait_for_condition_reason \
    "investigationrequest/${provider_rate_request}" \
    "${request_namespace}" \
    "RCAReady" \
    "ProviderRateLimited"

  wait_for_condition_reason \
    "investigationrequest/${provider_rate_request}" \
    "${request_namespace}" \
    "Ready" \
    "ProviderRateLimited"

  wait_for_condition_reason \
    "investigationrequest/${provider_rate_request}" \
    "${request_namespace}" \
    "Degraded" \
    "ProviderRateLimited"
}

if [[ "${reuse_cluster}" != "true" ]]; then
  log_section "Prepare Demo Cluster"
  make demo-down >/dev/null 2>&1 || true
  make demo-up

  log_section "Wait For Deployments"
  kubectl rollout status deployment/fluxagent-controller-manager -n "${FLUXAGENT_DEMO_NAMESPACE}" --timeout=180s
  kubectl rollout status deployment/fluxagent-observability -n "${FLUXAGENT_DEMO_NAMESPACE}" --timeout=180s
  kubectl rollout status deployment/fluxagent-sample -n "${FLUXAGENT_DEMO_NAMESPACE}" --timeout=180s

  log_section "Inject Fault"
  make inject-fault
fi

verify_successful_investigation
verify_missing_datasource_degradation
verify_capability_mismatch_degradation
verify_missing_provider_degradation
apply_hosted_provider_mocks
verify_provider_auth_failed_degradation
verify_provider_rate_limited_degradation

log_section "Investigation E2E Verification Complete"
echo "verify-investigation-kind passed"
