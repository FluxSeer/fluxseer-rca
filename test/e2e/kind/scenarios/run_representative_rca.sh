#!/usr/bin/env bash
set -euo pipefail

scenario_runner_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${scenario_runner_dir}/common.sh"
source "${scenario_runner_dir}/service-port-mismatch.sh"
source "${scenario_runner_dir}/failed-scheduling.sh"
source "${scenario_runner_dir}/crashloopbackoff.sh"
source "${scenario_runner_dir}/high-http-error-rate.sh"

run_representative_rca_scenarios() {
  scenario_setup_observability
  run_service_port_mismatch_scenario
  run_failed_scheduling_scenario
  run_crashloopbackoff_scenarios
  run_high_http_error_rate_scenarios
}
