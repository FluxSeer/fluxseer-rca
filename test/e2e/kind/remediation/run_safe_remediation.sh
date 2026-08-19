#!/usr/bin/env bash
set -euo pipefail

remediation_runner_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${remediation_runner_dir}/common.sh"

run_safe_remediation_scenarios() {
  remediation_assert_rbac
  remediation_run_case effective Effective
  remediation_run_case ineffective Ineffective
  remediation_run_case inconclusive Inconclusive
}
