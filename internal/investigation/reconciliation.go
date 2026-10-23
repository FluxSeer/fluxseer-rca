package investigation

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/FluxSeer/fluxseer-rca/internal/diagnostic"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

// ReconciliationStatus describes the runtime adapter boundary. It is kept
// separate from diagnostic claim status and the public RCA verdict because
// reconciliation is initially executed in shadow mode.
type ReconciliationStatus string

const (
	ReconciliationCompleted    ReconciliationStatus = "ReconciliationCompleted"
	ReconciliationFailed       ReconciliationStatus = "ReconciliationFailed"
	ReconciliationNoCandidates ReconciliationStatus = "NoCandidates"
)

// diagnosticEvidenceInventory converts already collected, authorized evidence
// references into the trusted inventory consumed by reconciliation. Generic
// EvidenceRef values do not carry a UID, while scheduling observations do;
// only the latter can safely authorize a Pod-scoped candidate in this slice.
func diagnosticEvidenceInventory(result EvidenceCollectionResult, targetUIDs []string, incidentStart, incidentEnd time.Time) []diagnostic.EvidenceIdentity {
	type scopedEvidence struct {
		targetUID     string
		incidentStart time.Time
		incidentEnd   time.Time
	}
	scoped := make(map[string][]scopedEvidence)
	for _, observation := range result.Scheduling {
		uid := strings.TrimSpace(observation.PodUID)
		if uid == "" {
			continue
		}
		start, end := observation.IncidentStart, observation.IncidentEnd
		if start.IsZero() {
			start = incidentStart
		}
		if end.IsZero() {
			end = incidentEnd
		}
		for _, refID := range append([]string{observation.ID}, observation.EvidenceRefs...) {
			refID = strings.TrimSpace(refID)
			if refID == "" {
				continue
			}
			scoped[refID] = append(scoped[refID], scopedEvidence{targetUID: uid, incidentStart: start, incidentEnd: end})
		}
	}

	inventory := make([]diagnostic.EvidenceIdentity, 0, len(result.EvidenceRefs))
	singleTargetUID := ""
	if len(targetUIDs) == 1 {
		singleTargetUID = strings.TrimSpace(targetUIDs[0])
	}
	for _, ref := range result.EvidenceRefs {
		id := strings.TrimSpace(ref.ID)
		if id == "" {
			continue
		}
		matches := scoped[id]
		if len(matches) == 0 {
			inventory = append(inventory, diagnostic.EvidenceIdentity{
				ID:            id,
				TargetUID:     singleTargetUID,
				IncidentStart: incidentStart,
				IncidentEnd:   incidentEnd,
				Authorized:    result.RelatedTargetAuthorization.AuthorizedEvidenceRef(ref),
				Valid:         true,
				Incomplete:    ref.Truncated,
			})
			continue
		}
		for _, match := range matches {
			inventory = append(inventory, diagnostic.EvidenceIdentity{
				ID:            id,
				TargetUID:     match.targetUID,
				IncidentStart: match.incidentStart,
				IncidentEnd:   match.incidentEnd,
				Authorized:    result.RelatedTargetAuthorization.AuthorizedEvidenceRef(ref),
				Valid:         true,
				Incomplete:    ref.Truncated,
			})
		}
	}
	sort.SliceStable(inventory, func(i, j int) bool {
		if inventory[i].ID != inventory[j].ID {
			return inventory[i].ID < inventory[j].ID
		}
		if inventory[i].TargetUID != inventory[j].TargetUID {
			return inventory[i].TargetUID < inventory[j].TargetUID
		}
		return inventory[i].IncidentStart.Before(inventory[j].IncidentStart)
	})
	deduplicated := inventory[:0]
	for _, identity := range inventory {
		if len(deduplicated) > 0 {
			previous := deduplicated[len(deduplicated)-1]
			if previous.ID == identity.ID && previous.TargetUID == identity.TargetUID && previous.IncidentStart.Equal(identity.IncidentStart) && previous.IncidentEnd.Equal(identity.IncidentEnd) && previous.Authorized == identity.Authorized && previous.Valid == identity.Valid && previous.Incomplete == identity.Incomplete {
				continue
			}
		}
		deduplicated = append(deduplicated, identity)
	}
	return deduplicated
}

// CandidateReconciliationInput is the trusted runtime input to the adapter.
// Evidence is constructed by the collection layer; candidate-provided
// EvidenceRefs only select entries from this inventory and never authorize
// access to evidence by themselves.
type CandidateReconciliationInput struct {
	Diagnostics            []diagnostic.Result
	Evidence               []diagnostic.EvidenceIdentity
	EvidenceInventoryReady bool
	IncidentStart          time.Time
	IncidentEnd            time.Time
}

// CandidateReconciliationResult is an internal lineage result. It is not
// serialized into the public RCA report or CRD in Phase 3A-2.
type CandidateReconciliationResult struct {
	Status     ReconciliationStatus
	Records    []diagnostic.CandidateRecord
	Accounting diagnostic.AccountingResult
	Error      string
}

func (s *Service) reconciliationTargetUIDs(ctx context.Context, preflight PreflightResult) []string {
	var refs []domain.SchedulingPodRef
	if strings.EqualFold(strings.TrimSpace(preflight.Target.Kind), "Deployment") {
		if s == nil || s.ScopeResolver == nil {
			return nil
		}
		resolved, err := s.ScopeResolver.ResolveDeployment(ctx, preflight.Target.Namespace, preflight.Target.Name)
		if err != nil || !resolved.Complete {
			return nil
		}
		refs = make([]domain.SchedulingPodRef, 0, len(resolved.Pods))
		for _, pod := range resolved.Pods {
			if pod.UID == "" {
				continue
			}
			refs = append(refs, domain.SchedulingPodRef{Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID)})
		}
	} else {
		var ok bool
		refs, ok = s.resolveSchedulingPodRefs(ctx, preflight)
		if !ok {
			return nil
		}
	}
	seen := make(map[string]struct{}, len(refs))
	uids := make([]string, 0, len(refs))
	for _, ref := range refs {
		uid := strings.TrimSpace(ref.UID)
		if uid == "" {
			continue
		}
		if _, exists := seen[uid]; exists {
			continue
		}
		seen[uid] = struct{}{}
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	return uids
}

const (
	reconciliationErrorMissingDiagnosticIdentity = "DiagnosticIdentityUnavailable"
	reconciliationErrorEvidenceInventory         = "EvidenceInventoryUnavailable"
)

// ReconcileDiagnosticCandidates maps runtime diagnostic results into the
// shared pure accounting engine. It performs no Kubernetes/API calls and has
// no metric or other side effects.
func ReconcileDiagnosticCandidates(input CandidateReconciliationInput) CandidateReconciliationResult {
	evidence := append([]diagnostic.EvidenceIdentity(nil), input.Evidence...)
	sort.SliceStable(evidence, func(i, j int) bool {
		if strings.TrimSpace(evidence[i].ID) != strings.TrimSpace(evidence[j].ID) {
			return strings.TrimSpace(evidence[i].ID) < strings.TrimSpace(evidence[j].ID)
		}
		if strings.TrimSpace(evidence[i].TargetUID) != strings.TrimSpace(evidence[j].TargetUID) {
			return strings.TrimSpace(evidence[i].TargetUID) < strings.TrimSpace(evidence[j].TargetUID)
		}
		return evidence[i].IncidentStart.Before(evidence[j].IncidentStart)
	})
	records := make([]diagnostic.CandidateRecord, 0)
	for _, result := range input.Diagnostics {
		if len(result.Claims) == 0 {
			continue
		}
		if strings.TrimSpace(result.DiagnosticID) == "" || strings.TrimSpace(result.DiagnosticVersion) == "" {
			return CandidateReconciliationResult{Status: ReconciliationFailed, Error: reconciliationErrorMissingDiagnosticIdentity}
		}
		for _, candidate := range result.Claims {
			evidenceRefs := append([]string(nil), candidate.EvidenceRefs...)
			sort.Strings(evidenceRefs)
			records = append(records, diagnostic.CandidateRecord{
				DiagnosticID:      strings.TrimSpace(result.DiagnosticID),
				DiagnosticVersion: strings.TrimSpace(result.DiagnosticVersion),
				IncidentStart:     input.IncidentStart,
				IncidentEnd:       input.IncidentEnd,
				Candidate: diagnostic.CandidateClaim{
					ID:           strings.TrimSpace(candidate.ID),
					Type:         candidate.Type,
					Statement:    strings.TrimSpace(candidate.Statement),
					TargetUID:    strings.TrimSpace(candidate.TargetUID),
					EvidenceRefs: evidenceRefs,
				},
				Evidence: append([]diagnostic.EvidenceIdentity(nil), evidence...),
			})
		}
	}

	if len(records) == 0 {
		return CandidateReconciliationResult{
			Status:     ReconciliationNoCandidates,
			Accounting: diagnostic.AccountingResult{Summary: diagnostic.DispositionSummary{CoverageKnown: false}},
		}
	}
	if !input.EvidenceInventoryReady {
		return CandidateReconciliationResult{Status: ReconciliationFailed, Error: reconciliationErrorEvidenceInventory}
	}

	// Keep the internal lineage record order deterministic for replay and
	// diagnostics, independent of planner or provider result ordering.
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].DiagnosticID != records[j].DiagnosticID {
			return records[i].DiagnosticID < records[j].DiagnosticID
		}
		if records[i].DiagnosticVersion != records[j].DiagnosticVersion {
			return records[i].DiagnosticVersion < records[j].DiagnosticVersion
		}
		if records[i].Candidate.ID != records[j].Candidate.ID {
			return records[i].Candidate.ID < records[j].Candidate.ID
		}
		return records[i].Candidate.Statement < records[j].Candidate.Statement
	})

	return CandidateReconciliationResult{
		Status:     ReconciliationCompleted,
		Records:    records,
		Accounting: diagnostic.AccountCandidates(records),
	}
}
