package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

const (
	defaultSchedulingMaxPods        = 20
	defaultSchedulingMaxEvents      = 100
	defaultSchedulingMaxPages       = 10
	defaultSchedulingMaxAPIRequests = 64
)

// These aliases keep the Kubernetes adapter's established test and package
// API source-compatible while the request/result contract lives in domain.
type SchedulingPodRef = domain.SchedulingPodRef
type SchedulingEvidenceLimits = domain.SchedulingEvidenceLimits
type SchedulingEvidenceRequest = domain.SchedulingEvidenceRequest
type SchedulingEvidenceResult = domain.SchedulingEvidenceResult

// SchedulingCollectionError keeps collection failure semantics explicit for
// callers. A forbidden Event list is not represented as an empty event set.
type SchedulingCollectionError struct {
	Reason string
	Err    error
}

func (e *SchedulingCollectionError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Reason
	}
	return fmt.Sprintf("%s: %v", e.Reason, e.Err)
}

func (e *SchedulingCollectionError) Unwrap() error { return e.Err }

func (a Adapter) CollectSchedulingEvidence(ctx context.Context, req SchedulingEvidenceRequest) (SchedulingEvidenceResult, error) {
	limits := normalizeSchedulingLimits(req.Limits)
	if strings.TrimSpace(req.Namespace) == "" {
		return SchedulingEvidenceResult{}, &SchedulingCollectionError{Reason: "InvalidScope", Err: fmt.Errorf("scheduling evidence namespace is required")}
	}
	refs, err := normalizeSchedulingPodRefs(req.Namespace, req.Pods)
	if err != nil {
		return SchedulingEvidenceResult{}, &SchedulingCollectionError{Reason: "InvalidScope", Err: err}
	}
	reader := a.APIReader
	if reader == nil {
		reader = a.Client
	}
	if reader == nil {
		return SchedulingEvidenceResult{}, &SchedulingCollectionError{Reason: "CollectionFailed", Err: fmt.Errorf("kubernetes reader is nil")}
	}

	result := SchedulingEvidenceResult{Evidence: make([]domain.SchedulingEvidence, 0, min(len(refs), limits.MaxPods))}
	if len(refs) > limits.MaxPods {
		refs = refs[:limits.MaxPods]
		result.Truncated = true
	}
	budget := schedulingCollectionBudget{limits: limits}
	valid := make([]*domain.SchedulingEvidence, 0, len(refs))
	for index, ref := range refs {
		if !budget.consumeAPI() {
			result.Truncated = true
			result.Evidence = append(result.Evidence, unavailableSchedulingEvidence(ref, domain.SchedulingCoverageTruncated, "API request budget exhausted"))
			for _, remaining := range refs[index+1:] {
				result.Evidence = append(result.Evidence, unavailableSchedulingEvidence(remaining, domain.SchedulingCoverageTruncated, "API request budget exhausted"))
			}
			break
		}
		var pod corev1.Pod
		err := reader.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: ref.Name}, &pod)
		if err != nil {
			switch {
			case apierrors.IsNotFound(err):
				result.Evidence = append(result.Evidence, unavailableSchedulingEvidence(ref, domain.SchedulingCoverageUnavailable, "PodNotFound"))
				continue
			case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
				result.Evidence = append(result.Evidence, unavailableSchedulingEvidence(ref, domain.SchedulingCoverageUnauthorized, "PodReadUnauthorized"))
				result.Evidence = markSchedulingCoverage(result.Evidence, domain.SchedulingCoverageUnauthorized, "PodReadUnauthorized")
				return finalizeSchedulingEvidence(result, budget), &SchedulingCollectionError{Reason: "Unauthorized", Err: err}
			case isContextFailure(err), ctx.Err() != nil:
				result.Evidence = append(result.Evidence, unavailableSchedulingEvidence(ref, domain.SchedulingCoverageCollectionFailed, "CollectionTimeout"))
				result.Evidence = markSchedulingCoverage(result.Evidence, domain.SchedulingCoverageCollectionFailed, "CollectionTimeout")
				return finalizeSchedulingEvidence(result, budget), &SchedulingCollectionError{Reason: "CollectionTimeout", Err: contextError(ctx, err)}
			default:
				result.Evidence = append(result.Evidence, unavailableSchedulingEvidence(ref, domain.SchedulingCoverageUnavailable, "PodReadFailed"))
				result.Evidence = markSchedulingCoverage(result.Evidence, domain.SchedulingCoverageUnavailable, "PodReadFailed")
				return finalizeSchedulingEvidence(result, budget), &SchedulingCollectionError{Reason: "CollectionFailed", Err: err}
			}
		}
		result.ScannedPods++
		if ref.UID != "" && string(pod.UID) != ref.UID {
			result.Evidence = append(result.Evidence, unavailableSchedulingEvidence(ref, domain.SchedulingCoverageStale, "PodUIDMismatch"))
			continue
		}
		record := normalizeSchedulingPod(pod, req.StartTime, req.EndTime)
		result.Evidence = append(result.Evidence, record)
		valid = append(valid, &result.Evidence[len(result.Evidence)-1])
	}

	for _, record := range valid {
		if record.Coverage != domain.SchedulingCoverageComplete {
			continue
		}
		coreEvents, coreTruncated, coreErr := a.listCoreSchedulingEvents(ctx, reader, req.Namespace, record.PodUID, &budget)
		eventsV1, eventsV1Truncated, eventsV1Err := a.listV1SchedulingEvents(ctx, reader, req.Namespace, record.PodUID, &budget)
		result.ScannedEvents += budget.lastScanned
		budget.lastScanned = 0
		if coreErr != nil {
			if isUnsupportedEventAPI(coreErr) {
				coreErr = nil
			} else {
				applySchedulingSourceFailure(record, coreErr)
			}
		}
		if eventsV1Err != nil {
			if isUnsupportedEventAPI(eventsV1Err) {
				eventsV1Err = nil
			} else {
				applySchedulingSourceFailure(record, eventsV1Err)
			}
		}
		allEvents := append(coreEvents, eventsV1...)
		sort.SliceStable(allEvents, func(i, j int) bool {
			iInWindow := schedulingEventWithinWindow(allEvents[i], record.IncidentStart, record.IncidentEnd)
			jInWindow := schedulingEventWithinWindow(allEvents[j], record.IncidentStart, record.IncidentEnd)
			if iInWindow != jInWindow {
				return iInWindow
			}
			return schedulingEventSortKey(allEvents[i]) < schedulingEventSortKey(allEvents[j])
		})
		allEvents = deduplicateSchedulingEvents(allEvents)
		if len(allEvents) > limits.MaxEvents {
			allEvents = allEvents[:limits.MaxEvents]
			result.Truncated = true
			record.Truncated = true
			record.Coverage = domain.SchedulingCoverageTruncated
			record.CoverageReason = "EventRecordBudgetExceeded"
		}
		for index := range allEvents {
			allEvents[index].Stale = !schedulingEventWithinWindow(allEvents[index], record.IncidentStart, record.IncidentEnd)
		}
		record.Events = append(record.Events, allEvents...)
		if coreTruncated || eventsV1Truncated || budget.exhausted {
			result.Truncated = true
			record.Truncated = true
			if record.Coverage == domain.SchedulingCoverageComplete {
				record.Coverage = domain.SchedulingCoverageTruncated
				record.CoverageReason = schedulingFirstNonEmpty(record.CoverageReason, "EventCollectionBoundExceeded")
			}
		}
		if record.Coverage == domain.SchedulingCoverageComplete {
			for _, event := range record.Events {
				if event.Stale {
					record.Stale = true
					record.Coverage = domain.SchedulingCoverageStale
					record.CoverageReason = "EventOutsideIncidentWindow"
					break
				}
			}
		}
		if (coreErr != nil || eventsV1Err != nil) && (isContextFailure(coreErr) || isContextFailure(eventsV1Err) || ctx.Err() != nil) {
			result.Evidence = markSchedulingCoverage(result.Evidence, domain.SchedulingCoverageCollectionFailed, "CollectionTimeout")
			return finalizeSchedulingEvidence(result, budget), &SchedulingCollectionError{Reason: "CollectionTimeout", Err: contextError(ctx, firstError(coreErr, eventsV1Err))}
		}
		if (coreErr != nil || eventsV1Err != nil) && (apierrors.IsForbidden(coreErr) || apierrors.IsUnauthorized(coreErr) || apierrors.IsForbidden(eventsV1Err) || apierrors.IsUnauthorized(eventsV1Err)) {
			result.Evidence = markSchedulingCoverage(result.Evidence, domain.SchedulingCoverageUnauthorized, "EventReadUnauthorized")
			return finalizeSchedulingEvidence(result, budget), &SchedulingCollectionError{Reason: "Unauthorized", Err: firstError(coreErr, eventsV1Err)}
		}
	}
	return finalizeSchedulingEvidence(result, budget), nil
}

type schedulingCollectionBudget struct {
	limits      SchedulingEvidenceLimits
	apiRequests int
	scanned     int
	lastScanned int
	exhausted   bool
}

func (b *schedulingCollectionBudget) consumeAPI() bool {
	if b.apiRequests >= b.limits.MaxAPIRequests {
		b.exhausted = true
		return false
	}
	b.apiRequests++
	return true
}

func normalizeSchedulingLimits(limits SchedulingEvidenceLimits) SchedulingEvidenceLimits {
	if limits.MaxPods <= 0 {
		limits.MaxPods = defaultSchedulingMaxPods
	}
	if limits.MaxEvents <= 0 {
		limits.MaxEvents = defaultSchedulingMaxEvents
	}
	if limits.MaxPages <= 0 {
		limits.MaxPages = defaultSchedulingMaxPages
	}
	if limits.MaxAPIRequests <= 0 {
		limits.MaxAPIRequests = defaultSchedulingMaxAPIRequests
	}
	return limits
}

func normalizeSchedulingPodRefs(namespace string, refs []SchedulingPodRef) ([]SchedulingPodRef, error) {
	result := make([]SchedulingPodRef, 0, len(refs))
	seen := map[string]struct{}{}
	for _, ref := range refs {
		ref.Namespace = strings.TrimSpace(ref.Namespace)
		if ref.Namespace == "" {
			ref.Namespace = namespace
		}
		ref.Name = strings.TrimSpace(ref.Name)
		ref.UID = strings.TrimSpace(ref.UID)
		if ref.Namespace != namespace {
			return nil, fmt.Errorf("Pod %s/%s is outside authorized namespace %q", ref.Namespace, ref.Name, namespace)
		}
		if ref.Name == "" {
			return nil, fmt.Errorf("Pod name is required")
		}
		key := ref.Namespace + "/" + ref.Name + "/" + ref.UID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, ref)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].UID < result[j].UID
	})
	return result, nil
}

func normalizeSchedulingPod(pod corev1.Pod, start, end time.Time) domain.SchedulingEvidence {
	record := domain.SchedulingEvidence{
		ID:              "pod/" + string(pod.UID),
		PodUID:          string(pod.UID),
		Namespace:       pod.Namespace,
		Name:            pod.Name,
		Source:          "kubernetes/pod",
		Phase:           string(pod.Status.Phase),
		NodeName:        pod.Spec.NodeName,
		SchedulingGates: make([]string, 0, len(pod.Spec.SchedulingGates)),
		NodeSelector:    copyStringMap(pod.Spec.NodeSelector),
		Tolerations:     normalizeTolerations(pod.Spec.Tolerations),
		IncidentStart:   start,
		IncidentEnd:     end,
		CollectedAt:     time.Now().UTC(),
		Coverage:        domain.SchedulingCoverageComplete,
	}
	for _, gate := range pod.Spec.SchedulingGates {
		record.SchedulingGates = append(record.SchedulingGates, gate.Name)
	}
	sort.Strings(record.SchedulingGates)
	for _, condition := range pod.Status.Conditions {
		if condition.Type != corev1.PodScheduled {
			continue
		}
		record.PodScheduledStatus = string(condition.Status)
		record.PodScheduledReason = condition.Reason
		record.PodScheduledMessage = condition.Message
		break
	}
	record.ContainerResources, record.ResourceRequests = normalizeContainerResources(pod.Spec.Containers)
	record.InitContainerResources, record.InitRequests = normalizeInitResources(pod.Spec.InitContainers)
	if pod.Spec.Affinity != nil && pod.Spec.Affinity.NodeAffinity != nil {
		affinity := pod.Spec.Affinity.NodeAffinity
		if affinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
			record.NodeAffinityRequired = []domain.SchedulingNodeSelectorTerm{}
			for _, term := range affinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
				record.NodeAffinityRequired = append(record.NodeAffinityRequired, normalizeNodeSelectorTerm(term, 0))
			}
		}
		for _, preferred := range affinity.PreferredDuringSchedulingIgnoredDuringExecution {
			record.NodeAffinityPreferred = append(record.NodeAffinityPreferred, normalizeNodeSelectorTerm(preferred.Preference, preferred.Weight))
		}
	}
	return record
}

func normalizeContainerResources(containers []corev1.Container) ([]domain.SchedulingContainerResources, domain.SchedulingResourceRequirements) {
	result := make([]domain.SchedulingContainerResources, 0, len(containers))
	aggregate := domain.SchedulingResourceRequirements{Requests: map[string]string{}, Limits: map[string]string{}}
	requestTotals, limitTotals := map[string]resource.Quantity{}, map[string]resource.Quantity{}
	for _, container := range containers {
		requirements := normalizeResourceRequirements(container.Resources)
		result = append(result, domain.SchedulingContainerResources{Name: container.Name, Requests: requirements.Requests, Limits: requirements.Limits})
		addQuantities(requestTotals, container.Resources.Requests)
		addQuantities(limitTotals, container.Resources.Limits)
	}
	aggregate.Requests = quantitiesToStrings(requestTotals)
	aggregate.Limits = quantitiesToStrings(limitTotals)
	return result, aggregate
}

func normalizeInitResources(containers []corev1.Container) ([]domain.SchedulingContainerResources, []domain.SchedulingResourceRequirements) {
	resources := make([]domain.SchedulingContainerResources, 0, len(containers))
	requirements := make([]domain.SchedulingResourceRequirements, 0, len(containers))
	for _, container := range containers {
		normalized := normalizeResourceRequirements(container.Resources)
		resources = append(resources, domain.SchedulingContainerResources{Name: container.Name, Requests: normalized.Requests, Limits: normalized.Limits})
		requirements = append(requirements, normalized)
	}
	return resources, requirements
}

func normalizeResourceRequirements(input corev1.ResourceRequirements) domain.SchedulingResourceRequirements {
	return domain.SchedulingResourceRequirements{Requests: resourceListToStrings(input.Requests), Limits: resourceListToStrings(input.Limits)}
}

func addQuantities(target map[string]resource.Quantity, values corev1.ResourceList) {
	for name, value := range values {
		if existing, ok := target[string(name)]; ok {
			existing.Add(value)
			target[string(name)] = existing
			continue
		}
		target[string(name)] = value.DeepCopy()
	}
}

func quantitiesToStrings(values map[string]resource.Quantity) map[string]string {
	result := make(map[string]string, len(values))
	for name, value := range values {
		result[name] = value.String()
	}
	return result
}

func resourceListToStrings(values corev1.ResourceList) map[string]string {
	result := make(map[string]string, len(values))
	for name, value := range values {
		result[string(name)] = value.String()
	}
	return result
}

func normalizeNodeSelectorTerm(term corev1.NodeSelectorTerm, weight int32) domain.SchedulingNodeSelectorTerm {
	result := domain.SchedulingNodeSelectorTerm{Weight: weight}
	for _, expression := range term.MatchExpressions {
		result.MatchExpressions = append(result.MatchExpressions, domain.SchedulingNodeSelectorRequirement{Key: expression.Key, Operator: string(expression.Operator), Values: append([]string(nil), expression.Values...)})
	}
	for _, field := range term.MatchFields {
		result.MatchFields = append(result.MatchFields, domain.SchedulingNodeSelectorRequirement{Key: field.Key, Operator: string(field.Operator), Values: append([]string(nil), field.Values...)})
	}
	sort.SliceStable(result.MatchExpressions, func(i, j int) bool {
		return selectorRequirementKey(result.MatchExpressions[i]) < selectorRequirementKey(result.MatchExpressions[j])
	})
	sort.SliceStable(result.MatchFields, func(i, j int) bool {
		return selectorRequirementKey(result.MatchFields[i]) < selectorRequirementKey(result.MatchFields[j])
	})
	return result
}

func normalizeTolerations(tolerations []corev1.Toleration) []domain.SchedulingToleration {
	result := make([]domain.SchedulingToleration, 0, len(tolerations))
	for _, toleration := range tolerations {
		var seconds *int64
		if toleration.TolerationSeconds != nil {
			value := *toleration.TolerationSeconds
			seconds = &value
		}
		result = append(result, domain.SchedulingToleration{Key: toleration.Key, Operator: string(toleration.Operator), Value: toleration.Value, Effect: string(toleration.Effect), TolerationSeconds: seconds})
	}
	sort.SliceStable(result, func(i, j int) bool {
		return fmt.Sprintf("%s/%s/%s/%s", result[i].Key, result[i].Operator, result[i].Value, result[i].Effect) < fmt.Sprintf("%s/%s/%s/%s", result[j].Key, result[j].Operator, result[j].Value, result[j].Effect)
	})
	return result
}

func selectorRequirementKey(requirement domain.SchedulingNodeSelectorRequirement) string {
	return requirement.Key + "/" + requirement.Operator + "/" + strings.Join(requirement.Values, ",")
}

func (a Adapter) listCoreSchedulingEvents(ctx context.Context, reader client.Reader, namespace, podUID string, budget *schedulingCollectionBudget) ([]domain.SchedulingEvent, bool, error) {
	return a.listSchedulingEvents(ctx, reader, namespace, podUID, budget, false)
}

func (a Adapter) listV1SchedulingEvents(ctx context.Context, reader client.Reader, namespace, podUID string, budget *schedulingCollectionBudget) ([]domain.SchedulingEvent, bool, error) {
	return a.listSchedulingEvents(ctx, reader, namespace, podUID, budget, true)
}

func (a Adapter) listSchedulingEvents(ctx context.Context, reader client.Reader, namespace, podUID string, budget *schedulingCollectionBudget, v1 bool) ([]domain.SchedulingEvent, bool, error) {
	result := make([]domain.SchedulingEvent, 0)
	continueToken := ""
	for page := 0; page < budget.limits.MaxPages; page++ {
		if !budget.consumeAPI() {
			return result, true, nil
		}
		pageLimit := budget.limits.MaxEvents - budget.scanned
		if pageLimit <= 0 {
			budget.exhausted = true
			return result, true, nil
		}
		if pageLimit > 100 {
			pageLimit = 100
		}
		if v1 {
			var events eventsv1.EventList
			err := reader.List(ctx, &events, client.InNamespace(namespace), client.MatchingFields{"regarding.uid": podUID}, client.Limit(int64(pageLimit+1)), client.Continue(continueToken))
			if err != nil {
				return result, false, err
			}
			items := events.Items
			if len(items) > pageLimit {
				items = items[:pageLimit]
				budget.scanned += pageLimit
				budget.lastScanned += pageLimit
				for _, event := range items {
					if schedulingEventReason(event.Reason) && string(event.Regarding.UID) == podUID {
						result = append(result, normalizeV1SchedulingEvent(event))
					}
				}
				return result, true, nil
			}
			budget.scanned += len(items)
			budget.lastScanned += len(items)
			for _, event := range items {
				if schedulingEventReason(event.Reason) && string(event.Regarding.UID) == podUID {
					result = append(result, normalizeV1SchedulingEvent(event))
				}
			}
			continueToken = events.Continue
		} else {
			var events corev1.EventList
			err := reader.List(ctx, &events, client.InNamespace(namespace), client.MatchingFields{"involvedObject.uid": podUID}, client.Limit(int64(pageLimit+1)), client.Continue(continueToken))
			if err != nil {
				return result, false, err
			}
			items := events.Items
			if len(items) > pageLimit {
				items = items[:pageLimit]
				budget.scanned += pageLimit
				budget.lastScanned += pageLimit
				for _, event := range items {
					if schedulingEventReason(event.Reason) && string(event.InvolvedObject.UID) == podUID {
						result = append(result, normalizeCoreSchedulingEvent(event))
					}
				}
				return result, true, nil
			}
			budget.scanned += len(items)
			budget.lastScanned += len(items)
			for _, event := range items {
				if schedulingEventReason(event.Reason) && string(event.InvolvedObject.UID) == podUID {
					result = append(result, normalizeCoreSchedulingEvent(event))
				}
			}
			continueToken = events.Continue
		}
		if continueToken == "" {
			return result, false, nil
		}
	}
	return result, true, nil
}

func normalizeCoreSchedulingEvent(event corev1.Event) domain.SchedulingEvent {
	observed, last, known := coreEventTimes(event)
	return domain.SchedulingEvent{ID: schedulingEventID("core", event.Namespace, event.Name, event.UID), UID: string(event.UID), PodUID: string(event.InvolvedObject.UID), Source: "kubernetes/core-v1", Reason: event.Reason, Message: event.Message, Type: event.Type, ObservedAt: observed, LastObservedAt: last, TimeKnown: known}
}

func normalizeV1SchedulingEvent(event eventsv1.Event) domain.SchedulingEvent {
	observed := event.EventTime.Time.UTC()
	last := observed
	if event.Series != nil && !event.Series.LastObservedTime.IsZero() {
		last = event.Series.LastObservedTime.Time.UTC()
	}
	return domain.SchedulingEvent{ID: schedulingEventID("events", event.Namespace, event.Name, event.UID), UID: string(event.UID), PodUID: string(event.Regarding.UID), Source: "kubernetes/events-v1", Reason: event.Reason, Message: event.Note, Type: event.Type, ObservedAt: observed, LastObservedAt: last, TimeKnown: !observed.IsZero()}
}

func coreEventTimes(event corev1.Event) (time.Time, time.Time, bool) {
	observed := event.EventTime.Time
	last := event.LastTimestamp.Time
	if observed.IsZero() {
		observed = event.FirstTimestamp.Time
	}
	if observed.IsZero() {
		observed = event.CreationTimestamp.Time
	}
	if last.IsZero() {
		last = observed
	}
	return observed.UTC(), last.UTC(), !observed.IsZero()
}

func schedulingEventReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "failedscheduling", "scheduled", "preempted", "nominated", "schedulinggated":
		return true
	default:
		return false
	}
}

func schedulingEventID(source, namespace, name string, uid types.UID) string {
	if uid != "" {
		return source + "/" + string(uid)
	}
	return source + "/" + namespace + "/" + name
}

func deduplicateSchedulingEvents(events []domain.SchedulingEvent) []domain.SchedulingEvent {
	seenUID := map[string]struct{}{}
	seenFingerprint := map[string]struct{}{}
	result := make([]domain.SchedulingEvent, 0, len(events))
	for _, event := range events {
		if event.UID != "" {
			if _, ok := seenUID[event.UID]; ok {
				continue
			}
			seenUID[event.UID] = struct{}{}
		}
		fingerprint := strings.Join([]string{
			event.PodUID,
			strings.ToLower(strings.TrimSpace(event.Reason)),
			strings.TrimSpace(event.Message),
			event.ObservedAt.UTC().Format(time.RFC3339Nano),
			event.LastObservedAt.UTC().Format(time.RFC3339Nano),
		}, "\x00")
		if _, ok := seenFingerprint[fingerprint]; ok {
			continue
		}
		seenFingerprint[fingerprint] = struct{}{}
		result = append(result, event)
	}
	return result
}

func schedulingEventSortKey(event domain.SchedulingEvent) string {
	return event.ObservedAt.UTC().Format(time.RFC3339Nano) + "/" + event.ID
}

func applySchedulingSourceFailure(record *domain.SchedulingEvidence, err error) {
	if record == nil || err == nil {
		return
	}
	record.CoverageReason = schedulingFirstNonEmpty(record.CoverageReason, schedulingCoverageReason(err))
	switch {
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		record.Coverage = domain.SchedulingCoverageUnauthorized
	case isContextFailure(err):
		record.Coverage = domain.SchedulingCoverageCollectionFailed
	default:
		record.Coverage = domain.SchedulingCoverageUnavailable
	}
}

func schedulingCoverageReason(err error) string {
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		return "EventReadUnauthorized"
	}
	if isContextFailure(err) {
		return "CollectionTimeout"
	}
	return "EventReadFailed"
}

func unavailableSchedulingEvidence(ref SchedulingPodRef, coverage domain.SchedulingCoverage, reason string) domain.SchedulingEvidence {
	return domain.SchedulingEvidence{ID: schedulingEvidenceID(ref), PodUID: ref.UID, Namespace: ref.Namespace, Name: ref.Name, Source: "kubernetes/pod", Coverage: coverage, CoverageReason: reason, Stale: coverage == domain.SchedulingCoverageStale, Truncated: coverage == domain.SchedulingCoverageTruncated, CollectedAt: time.Now().UTC()}
}

func schedulingEvidenceID(ref SchedulingPodRef) string {
	if ref.UID != "" {
		return "pod/" + ref.UID
	}
	return "pod/" + ref.Namespace + "/" + ref.Name
}

func markSchedulingCoverage(evidence []domain.SchedulingEvidence, coverage domain.SchedulingCoverage, reason string) []domain.SchedulingEvidence {
	for index := range evidence {
		// Preserve PodNotFound, UID mismatch, and other per-target degradation;
		// only records whose state was actually collected inherit a datasource-wide
		// event or timeout failure.
		if evidence[index].Coverage != domain.SchedulingCoverageComplete && evidence[index].Coverage != domain.SchedulingCoveragePartial {
			continue
		}
		evidence[index].Coverage = coverage
		evidence[index].CoverageReason = reason
	}
	return evidence
}

func coverageRank(coverage domain.SchedulingCoverage) int {
	switch coverage {
	case domain.SchedulingCoverageCollectionFailed:
		return 7
	case domain.SchedulingCoverageUnauthorized:
		return 6
	case domain.SchedulingCoverageTruncated:
		return 5
	case domain.SchedulingCoverageStale:
		return 4
	case domain.SchedulingCoverageUnavailable:
		return 3
	case domain.SchedulingCoveragePartial:
		return 2
	case domain.SchedulingCoverageComplete:
		return 1
	default:
		return 0
	}
}

func finalizeSchedulingEvidence(result SchedulingEvidenceResult, budget schedulingCollectionBudget) SchedulingEvidenceResult {
	result.APIRequests = budget.apiRequests
	result.ScannedEvents = max(result.ScannedEvents, budget.scanned)
	if budget.exhausted {
		result.Truncated = true
	}
	result.Coverage, result.CoverageReason = summarizeSchedulingCoverage(result.Evidence, result.Truncated)
	return result
}

func summarizeSchedulingCoverage(evidence []domain.SchedulingEvidence, truncated bool) (domain.SchedulingCoverage, string) {
	if len(evidence) == 0 {
		if truncated {
			return domain.SchedulingCoverageTruncated, "CollectionBudgetExceeded"
		}
		return domain.SchedulingCoverageComplete, "NoPodsInScope"
	}
	best := domain.SchedulingCoverageComplete
	reason := ""
	for _, record := range evidence {
		if coverageRank(record.Coverage) > coverageRank(best) {
			best = record.Coverage
			reason = record.CoverageReason
		}
	}
	if truncated && coverageRank(domain.SchedulingCoverageTruncated) > coverageRank(best) {
		best = domain.SchedulingCoverageTruncated
		reason = "CollectionBudgetExceeded"
	}
	return best, schedulingFirstNonEmpty(reason, "CollectionComplete")
}

func copyStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func schedulingFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func isContextFailure(err error) bool {
	return err != nil && (err == context.Canceled || err == context.DeadlineExceeded || strings.Contains(strings.ToLower(err.Error()), "context deadline exceeded"))
}

func contextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func firstError(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}

func isUnsupportedEventAPI(err error) bool {
	if err == nil {
		return false
	}
	if apierrors.IsNotFound(err) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no kind is registered") || strings.Contains(text, "the server could not find the requested resource")
}

func schedulingEventWithinWindow(event domain.SchedulingEvent, start, end time.Time) bool {
	if !event.TimeKnown {
		return false
	}
	at := event.ObservedAt
	if !start.IsZero() && at.Before(start) {
		return false
	}
	if !end.IsZero() && at.After(end) {
		return false
	}
	return true
}

// min is local to keep this collector independent of the Go version's generic
// helpers and to make the bounds visible at the call site.
func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
