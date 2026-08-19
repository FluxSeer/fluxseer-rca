#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export LIVE_HARNESS_RBAC_PROFILE="experimentalExecutor"
export LIVE_HARNESS_ENABLE_REMEDIATION="true"
export LIVE_HARNESS_ENABLE_EXPERIMENTAL_EXECUTOR="true"
export LIVE_HARNESS_VERIFY_READ_ONLY="false"
export LIVE_HARNESS_TIMEOUT_SECONDS="${LIVE_HARNESS_TIMEOUT_SECONDS:-900}"
export LIVE_HARNESS_ARTIFACT_ROOT="${LIVE_HARNESS_ARTIFACT_ROOT:-${script_dir}/../../../reports/runtime/v0.5-alpha1-kind-remediation/$(date -u +%Y%m%dT%H%M%SZ)-$$}"

source "${script_dir}/remediation/run_safe_remediation.sh"

live_harness_run run_safe_remediation_scenarios

echo "verify-v0.5-alpha1-kind-remediation passed"
