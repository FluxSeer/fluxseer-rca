#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/scenarios/run_representative_rca.sh"

live_harness_run run_representative_rca_scenarios

echo "verify-v0.5-alpha1-kind-rca passed"
