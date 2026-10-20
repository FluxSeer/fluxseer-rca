package diagnostic

import (
	"fmt"
	"sort"
	"strings"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

const (
	schedulingDiagnosticID       = "scheduling"
	schedulingDiagnosticVersion  = "v1"
	schedulingStateRequirement   = "scheduling-pod-state"
	schedulingFailureRequirement = "scheduling-failure-evidence"
)

// SchedulingDiagnostic classifies normalized Pod scheduling evidence. It does
// not query Kubernetes and never confirms a root cause by itself.
type SchedulingDiagnostic struct{}

func (SchedulingDiagnostic) ID() string      { return schedulingDiagnosticID }
func (SchedulingDiagnostic) Version() string { return schedulingDiagnosticVersion }

func (SchedulingDiagnostic) Match(evidence EvidenceSet) MatchResult {
	if len(evidence.Scheduling) == 0 {
		return MatchResult{Reason: "no normalized scheduling evidence"}
	}
	return MatchResult{Applicable: true, Reason: "normalized scheduling evidence is present"}
}

func (SchedulingDiagnostic) Requirements(_ EvidenceSet) []Requirement {
	return []Requirement{
		{ID: schedulingStateRequirement, Description: "target-scoped Pod scheduling state with a Pod UID", Required: true},
		{ID: schedulingFailureRequirement, Capability: domain.QueryTypeEvent, Description: "correlated PodScheduled or FailedScheduling evidence", Required: false},
	}
}

func (SchedulingDiagnostic) Evaluate(evidence EvidenceSet) Result {
	result := Result{
		DiagnosticID:        schedulingDiagnosticID,
		DiagnosticVersion:   schedulingDiagnosticVersion,
		Status:              StatusInconclusive,
		Claims:              []CandidateClaim{},
		MissingRequirements: []Requirement{},
		Warnings:            []string{},
	}

	if len(evidence.Scheduling) == 0 {
		result.Status = StatusNotApplicable
		result.Summary = "SchedulingDiagnostic did not find normalized Pod scheduling evidence."
		result.MissingRequirements = append(result.MissingRequirements, resultRequirements()[0])
		return result
	}

	claimKeys := map[string]struct{}{}
	validState := false
	observedFailure := false
	observedGate := false
	observedUnassigned := false
	assignedPod := false
	for _, observation := range evidence.Scheduling {
		podUID := strings.TrimSpace(observation.PodUID)
		if podUID == "" {
			result.Warnings = append(result.Warnings, "scheduling evidence is missing a Pod UID")
			continue
		}
		if observation.Stale {
			result.Warnings = append(result.Warnings, fmt.Sprintf("scheduling evidence for Pod UID %q is stale", podUID))
			continue
		}
		validState = true
		refs := schedulingEvidenceRefs(observation)
		phase := strings.ToLower(strings.TrimSpace(observation.Phase))
		if strings.TrimSpace(observation.NodeName) == "" && phase != "running" {
			observedUnassigned = true
			appendSchedulingClaim(&result, claimKeys, CandidateClaim{
				ID:           "scheduling-observation-unassigned/" + podUID,
				Type:         ClaimObservation,
				Statement:    "The Pod has not been assigned to a Node.",
				TargetUID:    podUID,
				EvidenceRefs: refs,
			})
		} else if strings.TrimSpace(observation.NodeName) != "" {
			assignedPod = true
		}

		if len(observation.SchedulingGates) > 0 {
			observedGate = true
			appendSchedulingClaim(&result, claimKeys, CandidateClaim{
				ID:           "scheduling-mechanism-gated/" + podUID,
				Type:         ClaimFailureMechanism,
				Statement:    "The Pod is blocked by an active scheduling gate.",
				TargetUID:    podUID,
				EvidenceRefs: refs,
			})
		}

		eventText := ""
		if eventMatchesPod(observation) {
			eventText = strings.ToLower(strings.Join([]string{observation.EventReason, observation.EventMessage}, " "))
		}
		conditionText := strings.ToLower(strings.Join([]string{observation.PodScheduledStatus, observation.PodScheduledReason, observation.PodScheduledMessage}, " "))
		conditionUnschedulable := strings.EqualFold(strings.TrimSpace(observation.PodScheduledStatus), "False") && containsAny(conditionText, "unschedulable")
		eventUnschedulable := containsAny(eventText, "failedscheduling", "failed scheduling")
		if !conditionUnschedulable && !eventUnschedulable {
			if observation.Truncated {
				result.Warnings = append(result.Warnings, fmt.Sprintf("scheduling evidence for Pod UID %q was truncated", podUID))
			}
			continue
		}

		observedFailure = true
		failureRefs := refs
		appendSchedulingClaim(&result, claimKeys, CandidateClaim{
			ID:           "scheduling-mechanism-unschedulable/" + podUID,
			Type:         ClaimFailureMechanism,
			Statement:    "The scheduler reported that the Pod could not be scheduled.",
			TargetUID:    podUID,
			EvidenceRefs: failureRefs,
		})
		for _, candidate := range schedulingCandidates(podUID, eventText+" "+conditionText, failureRefs) {
			appendSchedulingClaim(&result, claimKeys, candidate)
		}
		if observation.Truncated {
			result.Warnings = append(result.Warnings, fmt.Sprintf("specific scheduling predicates for Pod UID %q may be incomplete", podUID))
		}
	}

	if observedFailure || observedGate {
		result.Status = StatusSupported
	} else if !validState || observedUnassigned || !assignedPod {
		result.Status = StatusInconclusive
		result.MissingRequirements = append(result.MissingRequirements, resultRequirements()[1])
	} else {
		result.Status = StatusNotApplicable
	}
	if !observedFailure && observedUnassigned && !observedGate {
		result.Warnings = append(result.Warnings, "Pod is unassigned but no scheduling failure evidence was collected")
	}
	if result.Status == StatusInconclusive && len(result.MissingRequirements) == 0 {
		result.MissingRequirements = append(result.MissingRequirements, resultRequirements()[1])
	}
	result.Summary = fmt.Sprintf("Scheduling evidence evaluated for %d normalized Pod record(s); causal verification remains separate.", len(evidence.Scheduling))
	sortSchedulingClaims(result.Claims)
	sort.Strings(result.Warnings)
	return result
}

func resultRequirements() []Requirement {
	return (SchedulingDiagnostic{}).Requirements(EvidenceSet{})
}

func eventMatchesPod(observation SchedulingObservation) bool {
	eventReason := strings.TrimSpace(observation.EventReason)
	eventMessage := strings.TrimSpace(observation.EventMessage)
	if eventReason == "" && eventMessage == "" {
		return false
	}
	if observation.EventPodUID != "" && observation.EventPodUID != observation.PodUID {
		return false
	}
	return !observation.Stale && !observation.Truncated
}

func schedulingEvidenceRefs(observation SchedulingObservation) []string {
	refs := append([]string(nil), observation.EvidenceRefs...)
	if len(refs) == 0 && observation.ID != "" {
		refs = append(refs, observation.ID)
	}
	seen := make(map[string]struct{}, len(refs))
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		result = append(result, ref)
	}
	sort.Strings(result)
	return result
}

func schedulingCandidates(podUID, text string, refs []string) []CandidateClaim {
	text = strings.ToLower(text)
	candidates := make([]CandidateClaim, 0, 2)
	if containsAny(text, "insufficient cpu", "insufficient vcpu") {
		candidates = append(candidates, CandidateClaim{
			ID:           "scheduling-cause-insufficient-cpu/" + podUID,
			Type:         ClaimRootCause,
			Statement:    "The scheduler reported insufficient CPU for the Pod.",
			TargetUID:    podUID,
			EvidenceRefs: append([]string(nil), refs...),
		})
	}
	if containsAny(text, "insufficient memory") {
		candidates = append(candidates, CandidateClaim{
			ID:           "scheduling-cause-insufficient-memory/" + podUID,
			Type:         ClaimRootCause,
			Statement:    "The scheduler reported insufficient memory for the Pod.",
			TargetUID:    podUID,
			EvidenceRefs: append([]string(nil), refs...),
		})
	}
	if strings.Contains(text, "taint") && strings.Contains(text, "tolerat") {
		candidates = append(candidates, CandidateClaim{
			ID:           "scheduling-cause-taint-mismatch/" + podUID,
			Type:         ClaimRootCause,
			Statement:    "The scheduler reported an untolerated Node taint for the Pod.",
			TargetUID:    podUID,
			EvidenceRefs: append([]string(nil), refs...),
		})
	}
	if containsAny(text, "node selector", "node affinity", "affinity mismatch", "didn't match node") {
		candidates = append(candidates, CandidateClaim{
			ID:           "scheduling-cause-node-affinity-mismatch/" + podUID,
			Type:         ClaimRootCause,
			Statement:    "The scheduler reported a Node selector or affinity mismatch for the Pod.",
			TargetUID:    podUID,
			EvidenceRefs: append([]string(nil), refs...),
		})
	}
	return candidates
}

func appendSchedulingClaim(result *Result, seen map[string]struct{}, claim CandidateClaim) {
	if claim.ID == "" {
		return
	}
	if _, exists := seen[claim.ID]; exists {
		return
	}
	seen[claim.ID] = struct{}{}
	result.Claims = append(result.Claims, claim)
}

func sortSchedulingClaims(claims []CandidateClaim) {
	priority := func(claimType ClaimType) int {
		switch claimType {
		case ClaimObservation:
			return 0
		case ClaimFailureMechanism:
			return 1
		case ClaimRootCause:
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(claims, func(i, j int) bool {
		leftPriority, rightPriority := priority(claims[i].Type), priority(claims[j].Type)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		if claims[i].TargetUID != claims[j].TargetUID {
			return claims[i].TargetUID < claims[j].TargetUID
		}
		return claims[i].ID < claims[j].ID
	})
}
