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
  "## Verdict And Outcome"; do
  grep -Fq "${heading}" "${glossary}" || {
    echo "glossary heading missing: ${heading}" >&2
    exit 1
  }
done
grep -Fq "Detection success does not imply RCA confirmation." "${glossary}"
grep -Fq "The RCA verdict MUST NOT be more specific than the strongest" "${glossary}"
grep -Fq "Condition and failure reasons are not outcomes." "${glossary}"
grep -Fq "parameterized signal templates" "${root}/docs/helm-rulepacks.md"

if grep -Eq 'InvestigationOutcome[A-Za-z0-9_]*[[:space:]]*=.*RequiredEvidenceMissing' "${root}/api/v1alpha1/investigationrequest_types.go"; then
  echo "RequiredEvidenceMissing must remain a reason, not an InvestigationOutcome" >&2
  exit 1
fi

documents=(
  README.md
  CONTRIBUTING.md
  docs/README.md
  docs/glossary.md
  docs/product-requirements.md
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
