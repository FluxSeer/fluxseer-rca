// Package diagnostic contains provider-neutral, deterministic diagnostic
// capabilities. Diagnostics evaluate normalized evidence and never perform
// Kubernetes or external datasource calls.
package diagnostic

import (
	"time"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/scope"
)

type Status string

const (
	StatusSupported      Status = "Supported"
	StatusConfirmed      Status = "Confirmed"
	StatusRefuted        Status = "Refuted"
	StatusInconclusive   Status = "Inconclusive"
	StatusNotApplicable  Status = "NotApplicable"
	StatusCollectionFail Status = "CollectionFailed"
)

type ClaimType string

const (
	ClaimObservation      ClaimType = "Observation"
	ClaimFailureMechanism ClaimType = "FailureMechanism"
	ClaimRootCause        ClaimType = "RootCause"
)

// Requirement describes evidence a planner may collect for a diagnostic.
// The requirement is declarative; collection remains the planner's job.
type Requirement struct {
	ID          string
	Capability  domain.QueryType
	Description string
	Required    bool
}

// CandidateClaim is a diagnostic proposal. Claim Verification owns the
// evidence threshold for exposing a claim as a verified RCA conclusion.
type CandidateClaim struct {
	ID           string
	Type         ClaimType
	Statement    string
	EvidenceRefs []string
}

// EvidenceSet is the normalized, bounded input to a diagnostic evaluator.
type EvidenceSet struct {
	Observations  []domain.Observation
	EvidenceRefs  []v1alpha1.EvidenceRef
	Scope         scope.Result
	IncidentStart time.Time
	IncidentEnd   time.Time
}

type MatchResult struct {
	Applicable bool
	Reason     string
}

type Result struct {
	DiagnosticID        string
	DiagnosticVersion   string
	Status              Status
	Summary             string
	Claims              []CandidateClaim
	MissingRequirements []Requirement
	Warnings            []string
}

// Diagnostic is the reusable evaluator boundary. Implementations must be
// deterministic for the same evidence and version.
type Diagnostic interface {
	ID() string
	Version() string
	Match(EvidenceSet) MatchResult
	Requirements(EvidenceSet) []Requirement
	Evaluate(EvidenceSet) Result
}
