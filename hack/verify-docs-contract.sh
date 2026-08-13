#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart="${root}/charts/fluxseer-rca/Chart.yaml"

for command_name in awk grep sed; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "required command not found: ${command_name}" >&2
    exit 1
  }
done

chart_version="$(awk -F': *' '$1 == "version" {gsub(/"/, "", $2); print $2; exit}' "${chart}")"
app_version="$(awk -F': *' '$1 == "appVersion" {gsub(/"/, "", $2); print $2; exit}' "${chart}")"
release_version="v${chart_version}"
make_version="$(awk -F'\\?= *' '$1 ~ /^V0_4_RELEASE_VERSION / {print $2; exit}' "${root}/Makefile")"
ci_version="$(awk '$1 == "RELEASE_VERSION:" {print $2; exit}' "${root}/.github/workflows/ci.yml")"

[[ "${app_version}" == "${release_version}" ]] || {
  echo "Chart appVersion mismatch: expected ${release_version}, got ${app_version}" >&2
  exit 1
}
[[ "${make_version}" == "${release_version}" ]] || {
  echo "Makefile release mismatch: expected ${release_version}, got ${make_version}" >&2
  exit 1
}
[[ "${ci_version}" == "${release_version}" ]] || {
  echo "CI release mismatch: expected ${release_version}, got ${ci_version}" >&2
  exit 1
}

grep -Fq "Current release: \`${release_version}\`" "${root}/README.md"
grep -Fq "Current published release: \`${release_version}\`" "${root}/docs/README.md"
grep -Fq "Current release baseline: \`${release_version}\`" "${root}/docs/runtime-modes.md"
grep -Fq "## [${release_version}]" "${root}/CHANGELOG.md"

glossary="${root}/docs/glossary.md"
for heading in \
  "## Detection Pattern" \
  "## Signal Template" \
  "## Evidence Profile" \
  "## Evidence Sufficiency" \
  "## Verification" \
  "## Verdict And Outcome" \
  "## User-facing Report" \
  "## Internal Validation Report"; do
  grep -Fq "${heading}" "${glossary}" || {
    echo "glossary heading missing: ${heading}" >&2
    exit 1
  }
done
grep -Fq "Detection success does not imply RCA confirmation." "${glossary}"
grep -Fq "The RCA verdict MUST NOT be more specific than the strongest" "${glossary}"
grep -Fq "Condition and failure reasons are not outcomes." "${glossary}"
grep -Fq "parameterized signal templates" "${root}/docs/helm-rulepacks.md"

rulepack_docs="${root}/docs/helm-rulepacks.md"
for tuning_contract in \
  "## Request-rate Surge Tuning Guide" \
  "### Parameters And Units" \
  "Requests per second" \
  'current / max(baseline, baselineEpsilon) > increaseRatio' \
  'baselineEpsilon` is the single source' \
  "### Match Examples" \
  'internal reason `InsufficientBaseline`' \
  "### Choose And Apply Values" \
  "traffic-tuning.yaml" \
  "helm upgrade fluxseer-rca charts/fluxseer-rca" \
  "### Inspect The Rendered RiskRule" \
  "helm get manifest fluxseer-rca" \
  "clamp_min(..., 0.01)" \
  "helm rollback fluxseer-rca PREVIOUS_REVISION"; do
  grep -Fq "${tuning_contract}" "${rulepack_docs}" || {
    echo "request-rate-surge tuning contract missing: ${tuning_contract}" >&2
    exit 1
  }
done
grep -Fq 'do not change `high-error-rate` or' "${rulepack_docs}"
grep -Fq '`high-latency`.' "${rulepack_docs}"

reporting="${root}/docs/reporting.md"
grep -Fq "Internal Validation Report = test the product." "${reporting}"
grep -Fq "User-facing Report = product output." "${reporting}"
grep -Fq 'User-facing Report — `fluxseer-riskrule-report/v1`' "${reporting}"
grep -Fq 'Internal Validation Report — `fluxseer-test-report/v1`' "${reporting}"
grep -Fq "Formal cluster baselines are immutable evidence" "${reporting}"
grep -Fq '`sourceCommit`, `sourceDirty` flag' "${reporting}"
grep -Fq 'of the current `HEAD`' "${reporting}"
for coverage_contract in \
  "Built-in RulePack Detection Patterns" \
  "Internal P0 runtime validation scenarios passed" \
  "User-facing RiskRule Report catalog examples" \
  "Internal canonical workload validation scenarios passed" \
  "Internal high-error-rate and high-latency Prometheus Pattern Conformance cases passed"; do
  grep -Fq "${coverage_contract}" "${root}/README.md" || {
    echo "README reporting coverage contract missing: ${coverage_contract}" >&2
    exit 1
  }
  grep -Fq "${coverage_contract}" "${reporting}" || {
    echo "reporting coverage contract missing: ${coverage_contract}" >&2
    exit 1
  }
done

if grep -Eq 'InvestigationOutcome[A-Za-z0-9_]*[[:space:]]*=.*RequiredEvidenceMissing' "${root}/api/v1alpha1/investigationrequest_types.go"; then
  echo "RequiredEvidenceMissing must remain a reason, not an InvestigationOutcome" >&2
  exit 1
fi

documents=(
  README.md
  CONTRIBUTING.md
  docs/README.md
  docs/glossary.md
  docs/reporting.md
  docs/riskrule-reports.md
  docs/product-requirements.md
  docs/architecture/overview.md
  docs/architecture/investigation-flow.md
  docs/helm-rulepacks.md
  docs/crd-reference/investigationrequest.md
  docs/crd-reference/riskrule.md
)

for relative_file in "${documents[@]}"; do
  file="${root}/${relative_file}"
  while IFS= read -r markdown_link; do
    target="${markdown_link#*](}"
    target="${target%)}"
    target="${target%%#*}"
    target="${target#<}"
    target="${target%>}"
    case "${target}" in
      ""|http://*|https://*|mailto:*) continue ;;
    esac
    if [[ "${target}" = /* ]]; then
      resolved="${target}"
    else
      resolved="$(dirname "${file}")/${target}"
    fi
    if [[ ! -e "${resolved}" ]]; then
      echo "broken documentation link: ${relative_file} -> ${target}" >&2
      exit 1
    fi
  done < <(grep -oE '\[[^][]+\]\([^)]+\)' "${file}" || true)
done

echo "documentation contract passed for ${release_version}"
