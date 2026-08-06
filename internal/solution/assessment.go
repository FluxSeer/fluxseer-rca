package solution

import (
	"strings"

	"fluxseer/api/v1alpha1"
)

const (
	SchemaVersion = "fluxagent-solution-assessment-v1"

	VerificationSupported  = "Supported"
	VerificationInferred   = "Inferred"
	VerificationUnverified = "Unverified"
)

type Assessment struct {
	SchemaVersion string      `json:"schemaVersion"`
	Source        Source      `json:"source"`
	Candidates    []Candidate `json:"candidates,omitempty"`
}

type Source struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
}

type Candidate struct {
	ID                  string   `json:"id"`
	Summary             string   `json:"summary"`
	ActionType          string   `json:"actionType,omitempty"`
	Risk                string   `json:"risk,omitempty"`
	BlastRadius         string   `json:"blastRadius,omitempty"`
	Prerequisites       []string `json:"prerequisites,omitempty"`
	RollbackNotes       []string `json:"rollbackNotes,omitempty"`
	EvidenceRefs        []string `json:"evidenceRefs,omitempty"`
	Verification        string   `json:"verification"`
	ApprovalRequired    bool     `json:"approvalRequired"`
	ExecutionAuthorized bool     `json:"executionAuthorized"`
}

func Assess(request *v1alpha1.InvestigationRequest) Assessment {
	out := Assessment{SchemaVersion: SchemaVersion}
	if request == nil {
		return out
	}
	out.Source = Source{Namespace: request.Namespace, Name: request.Name}
	if request.Status.Execution == nil ||
		request.Status.Execution.ProviderResult == nil ||
		request.Status.Execution.ProviderResult.NormalizedResult == nil {
		return out
	}
	normalized := request.Status.Execution.ProviderResult.NormalizedResult
	summary := strings.TrimSpace(normalized.ActionDescription)
	if summary == "" {
		summary = strings.TrimSpace(normalized.ActionType)
	}
	if summary == "" {
		return out
	}
	evidenceRefs := supportedEvidenceRefs(request.Status.Claims)
	verification := VerificationSupported
	if len(evidenceRefs) == 0 {
		verification = VerificationUnverified
	}
	out.Candidates = []Candidate{{
		ID:                  "solution-001",
		Summary:             summary,
		ActionType:          strings.TrimSpace(normalized.ActionType),
		Risk:                "Unknown",
		BlastRadius:         "Unknown",
		Prerequisites:       []string{"human review"},
		RollbackNotes:       append([]string(nil), normalized.RollbackPlan...),
		EvidenceRefs:        evidenceRefs,
		Verification:        verification,
		ApprovalRequired:    true,
		ExecutionAuthorized: false,
	}}
	return out
}

func supportedEvidenceRefs(claims []v1alpha1.RCAClaim) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, claim := range claims {
		if !strings.EqualFold(claim.Verification, VerificationSupported) {
			continue
		}
		for _, ref := range claim.EvidenceRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			if _, ok := seen[ref]; ok {
				continue
			}
			seen[ref] = struct{}{}
			out = append(out, ref)
		}
	}
	return out
}
