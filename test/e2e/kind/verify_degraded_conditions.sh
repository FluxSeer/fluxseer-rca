#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/common.sh"

log_section "Verify Missing DataSource Degradation"
bash examples/kind/degraded-demo.sh missing-datasource

wait_for_condition \
  "riskrule/${FLUXAGENT_RULE_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "DatasourceResolved" \
  "False"

wait_for_condition_reason \
  "riskrule/${FLUXAGENT_RULE_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "DatasourceResolved" \
  "DataSourceNotFound"

wait_for_condition \
  "risksignal/${FLUXAGENT_SIGNAL_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "EvidenceCollectionReady" \
  "False"

wait_for_condition_reason \
  "risksignal/${FLUXAGENT_SIGNAL_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "EvidenceCollectionReady" \
  "DataSourceNotFound"

log_section "Reset Baseline Rule"
bash examples/kind/degraded-demo.sh reset

wait_for_condition \
  "riskrule/${FLUXAGENT_RULE_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "Ready" \
  "True"

log_section "Verify Capability Mismatch Degradation"
bash examples/kind/degraded-demo.sh capability-mismatch

wait_for_condition \
  "riskrule/${FLUXAGENT_RULE_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "QueryTypeSupported" \
  "False"

wait_for_condition_reason \
  "riskrule/${FLUXAGENT_RULE_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "QueryTypeSupported" \
  "CapabilityMismatch"

wait_for_condition \
  "risksignal/${FLUXAGENT_SIGNAL_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "EvidenceCollectionReady" \
  "False"

wait_for_condition_reason \
  "risksignal/${FLUXAGENT_SIGNAL_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "EvidenceCollectionReady" \
  "CapabilityMismatch"

log_section "Restore Baseline Rule"
bash examples/kind/degraded-demo.sh reset

wait_for_condition \
  "riskrule/${FLUXAGENT_RULE_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "Ready" \
  "True"

log_section "Verify Hosted Provider Degradation"
bash examples/kind/degraded-demo.sh provider-auth-failed

wait_for_condition \
  "riskrule/${FLUXAGENT_RULE_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "Ready" \
  "True"

wait_for_condition \
  "risksignal/${FLUXAGENT_SIGNAL_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "EvidenceCollectionReady" \
  "True"

wait_for_condition \
  "risksignal/${FLUXAGENT_SIGNAL_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "RCAReady" \
  "False"

wait_for_condition_reason \
  "risksignal/${FLUXAGENT_SIGNAL_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "RCAReady" \
  "ProviderAuthFailed"

log_section "Restore Baseline Rule After Hosted Provider Degradation"
bash examples/kind/degraded-demo.sh reset

wait_for_condition \
  "riskrule/${FLUXAGENT_RULE_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "Ready" \
  "True"

wait_for_condition \
  "risksignal/${FLUXAGENT_SIGNAL_NAME}" \
  "${FLUXAGENT_DEMO_NAMESPACE}" \
  "RCAReady" \
  "True"
