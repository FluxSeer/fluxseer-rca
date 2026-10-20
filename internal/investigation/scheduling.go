package investigation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/diagnostic"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	evidencepkg "github.com/FluxSeer/fluxseer-rca/internal/evidence"
)

// adaptSchedulingEvidence converts one bounded provider result into the
// provider-neutral EvidenceSet input. Authorization and coverage boundaries
// are enforced here so a diagnostic cannot mistake a failed collection for an
// empty, healthy event set.
func adaptSchedulingEvidence(records []domain.SchedulingEvidence) ([]diagnostic.SchedulingObservation, []v1alpha1.EvidenceRef) {
	observations := make([]diagnostic.SchedulingObservation, 0, len(records))
	refs := make([]v1alpha1.EvidenceRef, 0, len(records))
	redactor := evidencepkg.NewPatternRedactor()

	for _, record := range records {
		if strings.TrimSpace(record.PodUID) == "" {
			// UID is the correlation authority. A name-only record cannot safely
			// enter a cross-source Diagnostic or report.
			continue
		}
		coverage := record.Coverage
		if coverage == "" {
			coverage = domain.SchedulingCoverageUnavailable
		}
		// An unauthorized provider response is never allowed to become a
		// diagnostic input or a report reference.
		if coverage == domain.SchedulingCoverageUnauthorized {
			continue
		}

		observation := diagnostic.SchedulingObservation{
			ID:                  strings.TrimSpace(record.ID),
			PodUID:              strings.TrimSpace(record.PodUID),
			PodNamespace:        strings.TrimSpace(record.Namespace),
			PodName:             strings.TrimSpace(record.Name),
			Source:              strings.TrimSpace(record.Source),
			Phase:               record.Phase,
			NodeName:            record.NodeName,
			PodScheduledStatus:  record.PodScheduledStatus,
			PodScheduledReason:  record.PodScheduledReason,
			PodScheduledMessage: record.PodScheduledMessage,
			SchedulingGates:     append([]string(nil), record.SchedulingGates...),
			EvidenceRefs:        nil,
			CollectedAt:         record.CollectedAt,
			IncidentStart:       record.IncidentStart,
			IncidentEnd:         record.IncidentEnd,
			Coverage:            coverage,
			CoverageReason:      record.CoverageReason,
			StateCollected:      coverage == domain.SchedulingCoverageComplete || schedulingStateFieldsPresent(record),
			Truncated:           record.Truncated || coverage == domain.SchedulingCoverageTruncated,
			Stale:               record.Stale || coverage == domain.SchedulingCoverageStale,
		}
		refIDs := make([]string, 0, len(record.Events)+1)
		if observation.ID != "" {
			refIDs = append(refIDs, observation.ID)
			refs = append(refs, schedulingEvidenceRef(record, observation, redactor))
		}

		// Keep all current, UID-correlated event identities for traceability, but
		// expose one deterministic event to the pure evaluator's scalar fields.
		events := make([]domain.SchedulingEvent, 0, len(record.Events))
		for _, event := range record.Events {
			if event.Stale || (event.PodUID != "" && event.PodUID != record.PodUID) {
				continue
			}
			events = append(events, event)
			if event.ID != "" {
				refIDs = append(refIDs, event.ID)
				refs = append(refs, schedulingEventRef(record, event, observation, redactor))
			}
		}
		if selected, ok := selectSchedulingEvent(events); ok {
			observation.EventPodUID = selected.PodUID
			observation.EventReason = selected.Reason
			observation.EventMessage = selected.Message
		}
		observation.EvidenceRefs = uniqueSortedStrings(refIDs)
		observations = append(observations, observation)
	}

	sort.SliceStable(observations, func(i, j int) bool {
		if observations[i].PodUID != observations[j].PodUID {
			return observations[i].PodUID < observations[j].PodUID
		}
		return observations[i].ID < observations[j].ID
	})
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	return observations, deduplicateEvidenceRefs(refs)
}

func schedulingEvidenceRef(record domain.SchedulingEvidence, observation diagnostic.SchedulingObservation, redactor evidencepkg.PatternRedactor) v1alpha1.EvidenceRef {
	summary := fmt.Sprintf("Pod %s/%s UID %s scheduling state: phase=%s nodeName=%s podScheduled=%s coverage=%s", record.Namespace, record.Name, record.PodUID, record.Phase, record.NodeName, record.PodScheduledStatus, observation.Coverage)
	if !observation.StateCollected && strings.TrimSpace(record.CoverageReason) != "" {
		summary = fmt.Sprintf("Scheduling evidence for Pod %s/%s UID %s is incomplete: %s", record.Namespace, record.Name, record.PodUID, record.CoverageReason)
	}
	return v1alpha1.EvidenceRef{
		ID:               observation.ID,
		Kind:             "scheduling",
		Source:           firstNonEmpty(record.Source, "kubernetes/pod"),
		Summary:          redactor.RedactText(summary),
		Reason:           redactor.RedactText(record.CoverageReason),
		Truncated:        observation.Truncated,
		TruncationReason: record.CoverageReason,
		CollectedAt:      schedulingCollectedAt(record.CollectedAt),
		RelatedTargets: []v1alpha1.TargetRef{{
			APIVersion: "v1",
			Kind:       "Pod",
			Namespace:  record.Namespace,
			Name:       record.Name,
		}},
	}
}

func schedulingStateFieldsPresent(record domain.SchedulingEvidence) bool {
	return strings.TrimSpace(record.Phase) != "" ||
		strings.TrimSpace(record.NodeName) != "" ||
		strings.TrimSpace(record.PodScheduledStatus) != "" ||
		strings.TrimSpace(record.PodScheduledReason) != "" ||
		strings.TrimSpace(record.PodScheduledMessage) != "" ||
		len(record.SchedulingGates) > 0
}

func schedulingEventRef(record domain.SchedulingEvidence, event domain.SchedulingEvent, observation diagnostic.SchedulingObservation, redactor evidencepkg.PatternRedactor) v1alpha1.EvidenceRef {
	collectedAt := event.LastObservedAt
	if collectedAt.IsZero() {
		collectedAt = event.ObservedAt
	}
	if collectedAt.IsZero() {
		collectedAt = record.CollectedAt
	}
	return v1alpha1.EvidenceRef{
		ID:               event.ID,
		Kind:             "event",
		Source:           firstNonEmpty(event.Source, record.Source, "kubernetes-events"),
		Reason:           redactor.RedactText(event.Reason),
		Summary:          redactor.RedactText(event.Message),
		Truncated:        observation.Truncated,
		TruncationReason: record.CoverageReason,
		CollectedAt:      schedulingCollectedAt(collectedAt),
		RelatedTargets: []v1alpha1.TargetRef{{
			APIVersion: "v1",
			Kind:       "Pod",
			Namespace:  record.Namespace,
			Name:       record.Name,
		}},
	}
}

func selectSchedulingEvent(events []domain.SchedulingEvent) (domain.SchedulingEvent, bool) {
	if len(events) == 0 {
		return domain.SchedulingEvent{}, false
	}
	sort.SliceStable(events, func(i, j int) bool {
		iFailed := strings.EqualFold(strings.TrimSpace(events[i].Reason), "FailedScheduling")
		jFailed := strings.EqualFold(strings.TrimSpace(events[j].Reason), "FailedScheduling")
		if iFailed != jFailed {
			return iFailed
		}
		return events[i].ID < events[j].ID
	})
	return events[0], true
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func deduplicateEvidenceRefs(refs []v1alpha1.EvidenceRef) []v1alpha1.EvidenceRef {
	seen := make(map[string]struct{}, len(refs))
	result := make([]v1alpha1.EvidenceRef, 0, len(refs))
	for _, ref := range refs {
		if ref.ID == "" {
			continue
		}
		if _, ok := seen[ref.ID]; ok {
			continue
		}
		seen[ref.ID] = struct{}{}
		result = append(result, ref)
	}
	return result
}

func schedulingCollectedAt(value time.Time) *metav1.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &metav1.Time{Time: value}
}
