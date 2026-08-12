#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
catalog="${FLUXSEER_PUBLIC_REPORT_CATALOG:-${repo_root}/test/e2e/runtime/public_report_scenarios.json}"
namespace="${FLUXSEER_RCA_RUNTIME_NAMESPACE:-fluxseer-rca-test}"
report_root="${FLUXSEER_RCA_RUNTIME_REPORT_ROOT:-${repo_root}/reports/runtime/user-facing}"
run_id="${FLUXSEER_RCA_RUNTIME_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
case_filter="${FLUXSEER_PUBLIC_REPORT_CASE:-}"

for command_name in jq kubectl git go; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "required command not found: ${command_name}" >&2; exit 1; }
done
[[ -n "${KUBECONFIG:-}" ]] || { echo "KUBECONFIG must point to the authorized test cluster" >&2; exit 1; }

report_dir="${report_root}/${run_id}"
mkdir -p "${report_dir}"
cli_dir="$(mktemp -d)"
trap 'rm -rf "${cli_dir}"' EXIT
cli_bin="${cli_dir}/fluxseer"
(cd "${repo_root}" && GOWORK=off go build -o "${cli_bin}" ./cmd/fluxseer)

exported=0
while IFS=$'\t' read -r case_id risk_rule output; do
  [[ -n "${case_filter}" && "${case_filter}" != "${case_id}" ]] && continue
  output_path="${report_dir}/${output}"
  "${cli_bin}" report riskrule "${risk_rule}" -n "${namespace}" -o json >"${output_path}"
  bash "${repo_root}/hack/verify-riskrule-report.sh" "${output_path}"
  exported=$((exported + 1))
done < <(jq -r --arg case_filter "${case_filter}" '.scenarios[] | [$caseId, .riskRule, .output] | @tsv' "${catalog}")

[[ "${exported}" -gt 0 ]] || { echo "no public report cases selected" >&2; exit 1; }
printf 'exported %s public RiskRule reports to %s\n' "${exported}" "${report_dir}"
