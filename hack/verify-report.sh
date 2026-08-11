#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 1 ]]; then
  echo "usage: $0 <report.json>" >&2
  exit 2
fi

report="$1"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
schema="$(jq -r '.schemaVersion // empty' "${report}")"

case "${schema}" in
  fluxseer-test-report/v1)
    exec bash "${script_dir}/verify-test-report.sh" "${report}"
    ;;
  fluxseer-riskrule-report/v1)
    exec bash "${script_dir}/verify-riskrule-report.sh" "${report}"
    ;;
  *)
    echo "unsupported or missing report schema in ${report}: ${schema:-<empty>}" >&2
    echo "expected fluxseer-test-report/v1 or fluxseer-riskrule-report/v1" >&2
    exit 1
    ;;
esac
