package diagnostic

import (
	"sort"
	"strings"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

const (
	servicePortDiagnosticID      = "service-port"
	servicePortDiagnosticVersion = "v1"
)

// ServicePortDiagnostic turns normalized service configuration evidence into
// a candidate claim. It does not perform Kubernetes reads or confirm the
// public RCA; the shared verifier owns that decision.
type ServicePortDiagnostic struct{}

func (ServicePortDiagnostic) ID() string      { return servicePortDiagnosticID }
func (ServicePortDiagnostic) Version() string { return servicePortDiagnosticVersion }

func (ServicePortDiagnostic) Match(evidence EvidenceSet) MatchResult {
	if len(servicePortEvidenceIDs(evidence)) == 0 {
		return MatchResult{Reason: "no confirmed service-port mismatch evidence"}
	}
	if servicePortTargetUID(evidence) == "" {
		return MatchResult{Reason: "service-port evidence has no single validated Pod scope"}
	}
	return MatchResult{Applicable: true, Reason: "service-port mismatch evidence is present"}
}

func (ServicePortDiagnostic) Requirements(_ EvidenceSet) []Requirement {
	return []Requirement{{
		ID:          "service-port-mismatch-evidence",
		Capability:  domain.QueryTypeServiceConfiguration,
		Description: "confirmed Service targetPort and resolved workload/container port evidence",
		Required:    true,
	}}
}

func (ServicePortDiagnostic) Evaluate(evidence EvidenceSet) Result {
	result := Result{
		DiagnosticID:        servicePortDiagnosticID,
		DiagnosticVersion:   servicePortDiagnosticVersion,
		Status:              StatusInconclusive,
		Claims:              []CandidateClaim{},
		MissingRequirements: []Requirement{},
		Warnings:            []string{},
	}
	ids := servicePortEvidenceIDs(evidence)
	if len(ids) == 0 {
		result.Status = StatusNotApplicable
		result.Summary = "ServicePortDiagnostic did not find confirmed service-port mismatch evidence."
		result.MissingRequirements = append(result.MissingRequirements, (ServicePortDiagnostic{}).Requirements(evidence)[0])
		return result
	}
	targetUID := servicePortTargetUID(evidence)
	if targetUID == "" {
		result.Summary = "Service-port mismatch evidence is not scoped to one validated Pod."
		result.Warnings = append(result.Warnings, "a shared service configuration cannot be assigned to one Pod without a validated single-Pod scope")
		return result
	}
	result.Claims = append(result.Claims, CandidateClaim{
		ID:           "service-port-mismatch",
		Type:         ClaimRootCause,
		Statement:    "ServicePortMismatch is confirmed by Kubernetes Service targetPort and resolved workload/container port evidence.",
		TargetUID:    targetUID,
		EvidenceRefs: ids,
	})
	result.Status = StatusSupported
	result.Summary = "Service-port configuration evidence matched a bounded targetPort/container-port mismatch; final verification remains separate."
	return result
}

func servicePortEvidenceIDs(evidence EvidenceSet) []string {
	ids := map[string]struct{}{}
	for _, observation := range evidence.Observations {
		if observation.Type != domain.ObservationTypeServiceConfiguration || observation.Value.ServiceConfiguration == nil {
			continue
		}
		if !observation.Value.ServiceConfiguration.MismatchConfirmed || strings.TrimSpace(observation.ID) == "" {
			continue
		}
		ids[strings.TrimSpace(observation.ID)] = struct{}{}
	}
	for _, ref := range evidence.EvidenceRefs {
		if strings.EqualFold(strings.TrimSpace(ref.Kind), "serviceConfiguration") && strings.EqualFold(strings.TrimSpace(ref.Reason), "ServicePortMismatch") && strings.TrimSpace(ref.ID) != "" {
			ids[strings.TrimSpace(ref.ID)] = struct{}{}
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func servicePortTargetUID(evidence EvidenceSet) string {
	if !evidence.Scope.Complete || len(evidence.Scope.Pods) != 1 {
		return ""
	}
	return strings.TrimSpace(string(evidence.Scope.Pods[0].UID))
}
