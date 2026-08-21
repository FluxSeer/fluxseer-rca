#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${RELEASE_VERSION:?RELEASE_VERSION is required}"
: "${SOURCE_COMMIT:?SOURCE_COMMIT is required}"
: "${RELEASE_QUALIFICATION_ARTIFACT_ROOT:?RELEASE_QUALIFICATION_ARTIFACT_ROOT is required}"

state_file="${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/gate-status.tsv"
report_file="${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/qualification.json"
exit_code="${QUALIFICATION_EXIT_CODE:-1}"

source_status=NOT_RUN
candidate_publication_status=NOT_RUN
candidate_publication_mode=not_run
anonymous_distribution_status=NOT_RUN
default_clean_room_status=NOT_RUN
experimental_clean_room_status=NOT_RUN
artifact_integrity_status=NOT_RUN
cleanup_status=NOT_RUN

if [[ -f "${state_file}" ]]; then
  while IFS=$'\t' read -r gate status _ mode; do
    [[ -n "${gate}" ]] || continue
    case "${gate}" in
      source) source_status="${status}" ;;
      candidatePublication)
        candidate_publication_status="${status}"
        candidate_publication_mode="${mode:-not_run}"
        ;;
      anonymousDistribution) anonymous_distribution_status="${status}" ;;
      defaultCleanRoom) default_clean_room_status="${status}" ;;
      experimentalCleanRoom) experimental_clean_room_status="${status}" ;;
      artifactIntegrity) artifact_integrity_status="${status}" ;;
      cleanup) cleanup_status="${status}" ;;
    esac
  done <"${state_file}"
fi

operator_digest=""
demo_digest=""
chart_digest=""
chart_package_sha256=""
if [[ -f "${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/anonymous-pulls/operator-imagetools.txt" ]]; then
  operator_digest="$(awk '/Digest:/ {print $2; exit}' "${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/anonymous-pulls/operator-imagetools.txt")"
fi
if [[ -f "${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/anonymous-pulls/demo-imagetools.txt" ]]; then
  demo_digest="$(awk '/Digest:/ {print $2; exit}' "${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/anonymous-pulls/demo-imagetools.txt")"
fi
if [[ -f "${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/anonymous-pulls/chart-oci-digest.txt" ]]; then
  chart_digest="$(tr -d '[:space:]' <"${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/anonymous-pulls/chart-oci-digest.txt")"
fi
chart_package="$(find "${RELEASE_QUALIFICATION_ARTIFACT_ROOT}/anonymous-pulls" -maxdepth 1 -type f -name '*.tgz' -print -quit 2>/dev/null || true)"
if [[ -n "${chart_package}" ]]; then
  chart_package_sha256="$(shasum -a 256 "${chart_package}" | awk '{print $1}')"
fi

gates_json="$(jq -n \
  --arg source "${source_status}" \
  --arg candidatePublication "${candidate_publication_status}" \
  --arg anonymousDistribution "${anonymous_distribution_status}" \
  --arg defaultCleanRoom "${default_clean_room_status}" \
  --arg experimentalCleanRoom "${experimental_clean_room_status}" \
  --arg artifactIntegrity "${artifact_integrity_status}" \
  --arg cleanup "${cleanup_status}" \
  '{source:$source,candidatePublication:$candidatePublication,anonymousDistribution:$anonymousDistribution,defaultCleanRoom:$defaultCleanRoom,experimentalCleanRoom:$experimentalCleanRoom,artifactIntegrity:$artifactIntegrity,cleanup:$cleanup}')"

required_pass=true
for gate_status_value in \
  "${source_status}" \
  "${candidate_publication_status}" \
  "${anonymous_distribution_status}" \
  "${default_clean_room_status}" \
  "${experimental_clean_room_status}" \
  "${artifact_integrity_status}" \
  "${cleanup_status}"; do
  if [[ "${gate_status_value}" != "PASS" ]]; then
    required_pass=false
  fi
done
if [[ "${exit_code}" != "0" ]]; then
  required_pass=false
fi
for artifact_value in "${operator_digest}" "${demo_digest}" "${chart_digest}" "${chart_package_sha256}"; do
  if [[ -z "${artifact_value}" ]]; then
    required_pass=false
  fi
done

result=FAIL
if [[ "${required_pass}" == "true" ]]; then
  result=PASS
fi

operator_ref="${RELEASE_OPERATOR_IMAGE_REPOSITORY:-}"
if [[ -n "${operator_ref}" ]]; then
  operator_ref="${operator_ref}:${RELEASE_VERSION}"
fi
demo_ref="${RELEASE_DEMO_IMAGE_REPOSITORY:-}"
if [[ -n "${demo_ref}" ]]; then
  demo_ref="${demo_ref}:${RELEASE_VERSION}"
fi

jq -n \
  --arg schema "fluxseer-release-qualification/v1" \
  --arg version "${RELEASE_VERSION}" \
  --arg sourceCommit "${SOURCE_COMMIT}" \
  --arg result "${result}" \
  --arg candidatePublicationResult "${candidate_publication_status}" \
  --arg candidatePublicationMode "${candidate_publication_mode}" \
  --arg anonymousPull "${anonymous_distribution_status}" \
  --arg operatorRef "${operator_ref}" \
  --arg operatorDigest "${operator_digest}" \
  --arg demoRef "${demo_ref}" \
  --arg demoDigest "${demo_digest}" \
  --arg chartOCI "${RELEASE_CHART_OCI:-}" \
  --arg chartVersion "${RELEASE_CHART_VERSION:-}" \
  --arg chartDigest "${chart_digest}" \
  --arg chartPackageSHA256 "${chart_package_sha256}" \
  --arg kindVersion "${QUALIFICATION_KIND_VERSION:-unknown}" \
  --arg kubectlVersion "${QUALIFICATION_KUBECTL_VERSION:-unknown}" \
  --arg helmVersion "${QUALIFICATION_HELM_VERSION:-unknown}" \
  --arg qualifiedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg artifactRoot "${RELEASE_QUALIFICATION_ARTIFACT_ROOT}" \
  --argjson gates "${gates_json}" \
  '{schemaVersion:$schema,version:$version,sourceCommit:$sourceCommit,candidate:true,result:$result,gates:$gates,candidatePublication:{result:$candidatePublicationResult,mode:$candidatePublicationMode,publishedThisRun:($candidatePublicationMode == "published"),version:$version,sourceCommit:$sourceCommit,artifacts:{operator:{ref:$operatorRef,digest:$operatorDigest},demoObservability:{ref:$demoRef,digest:$demoDigest},chart:{oci:$chartOCI,version:$chartVersion,digest:$chartDigest}}},artifacts:{operator:{ref:$operatorRef,digest:$operatorDigest},demoObservability:{ref:$demoRef,digest:$demoDigest},chart:{oci:$chartOCI,version:$chartVersion,digest:$chartDigest,downloadedPackageSHA256:$chartPackageSHA256}},environment:{kind:$kindVersion,kubectl:$kubectlVersion,helm:$helmVersion},evidence:{artifactRoot:$artifactRoot,anonymousPull:($anonymousPull == "PASS")},qualifiedAt:$qualifiedAt}' \
  >"${report_file}"

if [[ "${result}" != "PASS" ]]; then
  echo "release qualification report: FAIL (${report_file})" >&2
  exit 1
fi
echo "release qualification report: PASS (${report_file})"
