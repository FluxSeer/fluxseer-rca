package kubernetes

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func TestCollectSchedulingEvidenceNormalizesPodStateAndBothEventAPIs(t *testing.T) {
	now := time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC)
	pod := &corev1.Pod{
		ObjectMeta: typesMeta("demo", "checkout", "pod-new"),
		Spec: corev1.PodSpec{
			NodeName:        "worker-a",
			SchedulingGates: []corev1.PodSchedulingGate{{Name: "example.com/approval"}},
			NodeSelector:    map[string]string{"kubernetes.io/os": "linux"},
			Affinity:        &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "disk", Operator: corev1.NodeSelectorOpIn, Values: []string{"ssd"}}}}}}}},
			Tolerations:     []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "batch", Effect: corev1.TaintEffectNoSchedule}},
			Containers:      []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resourceQuantity("250m")}}}},
			InitContainers:  []corev1.Container{{Name: "init", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceMemory: resourceQuantity("64Mi")}}}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "waiting for scheduler"}}},
	}
	coreEvent := &corev1.Event{
		ObjectMeta:     typesMetaWithUID("demo", "failed-core", "event-core"),
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod.Name, UID: pod.UID},
		Reason:         "FailedScheduling",
		Message:        "0/2 nodes are available: 2 Insufficient cpu.",
		LastTimestamp:  metav1Time(now.Add(-time.Minute)),
		FirstTimestamp: metav1Time(now.Add(-2 * time.Minute)),
	}
	v1Event := &eventsv1.Event{
		ObjectMeta: typesMetaWithUID("demo", "failed-v1", "event-v1"),
		EventTime:  microTime(now.Add(-30 * time.Second)),
		Regarding:  corev1.ObjectReference{Kind: "Pod", Name: pod.Name, UID: pod.UID},
		Reason:     "FailedScheduling",
		Note:       "0/2 nodes are available: untolerated taint",
		Type:       "Warning",
	}
	reader := newSchedulingFakeClient(t, pod, coreEvent, v1Event)

	result, err := (Adapter{APIReader: reader}).CollectSchedulingEvidence(context.Background(), SchedulingEvidenceRequest{
		Namespace: "demo", Pods: []SchedulingPodRef{{Namespace: "demo", Name: pod.Name, UID: string(pod.UID)}},
		StartTime: now.Add(-5 * time.Minute), EndTime: now,
	})
	if err != nil {
		t.Fatalf("collect scheduling evidence: %v", err)
	}
	if result.Coverage != domain.SchedulingCoverageComplete || len(result.Evidence) != 1 {
		t.Fatalf("expected complete single-Pod evidence, got %#v", result)
	}
	evidence := result.Evidence[0]
	if evidence.NodeName != "worker-a" || evidence.PodScheduledStatus != "False" || len(evidence.SchedulingGates) != 1 || len(evidence.Events) != 2 {
		t.Fatalf("expected normalized Pod state and both event APIs, got %#v", evidence)
	}
	if evidence.ResourceRequests.Requests[string(corev1.ResourceCPU)] != "250m" || len(evidence.InitContainerResources) != 1 {
		t.Fatalf("expected normal and init resource requests, got %#v", evidence)
	}
	if evidence.Events[0].PodUID != string(pod.UID) || evidence.Events[1].PodUID != string(pod.UID) {
		t.Fatalf("event correlation was not UID-scoped: %#v", evidence.Events)
	}
}

func TestDeduplicateSchedulingEventsCollapsesLogicalCrossAPIDuplicates(t *testing.T) {
	now := time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC)
	events := deduplicateSchedulingEvents([]domain.SchedulingEvent{
		{ID: "core/event", UID: "core-uid", PodUID: "pod-1", Reason: "FailedScheduling", Message: "Insufficient cpu", ObservedAt: now},
		{ID: "events/event", UID: "events-uid", PodUID: "pod-1", Reason: "FailedScheduling", Message: "Insufficient cpu", ObservedAt: now},
	})
	if len(events) != 1 {
		t.Fatalf("expected one logical event after cross-API deduplication, got %#v", events)
	}
}

func TestCollectSchedulingEvidenceExcludesReusedPodNameAndMarksStaleEvents(t *testing.T) {
	now := time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC)
	pod := &corev1.Pod{ObjectMeta: typesMeta("demo", "worker", "pod-new"), Status: corev1.PodStatus{Phase: corev1.PodPending}}
	oldUIDEvent := &corev1.Event{ObjectMeta: typesMetaWithUID("demo", "worker-event", "event-old"), InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod.Name, UID: types.UID("pod-old")}, Reason: "FailedScheduling", Message: "old Pod", LastTimestamp: metav1Time(now.Add(-time.Minute))}
	staleEvent := &corev1.Event{ObjectMeta: typesMetaWithUID("demo", "worker-stale", "event-stale"), InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod.Name, UID: pod.UID}, Reason: "FailedScheduling", Message: "stale event", LastTimestamp: metav1Time(now.Add(-2 * time.Hour))}
	reader := newSchedulingFakeClient(t, pod, oldUIDEvent, staleEvent)

	result, err := (Adapter{APIReader: reader}).CollectSchedulingEvidence(context.Background(), SchedulingEvidenceRequest{
		Namespace: "demo", Pods: []SchedulingPodRef{{Name: pod.Name, UID: string(pod.UID)}}, StartTime: now.Add(-10 * time.Minute), EndTime: now,
	})
	if err != nil {
		t.Fatalf("collect scheduling evidence: %v", err)
	}
	evidence := result.Evidence[0]
	if len(evidence.Events) != 1 || evidence.Events[0].UID != "event-stale" {
		t.Fatalf("expected only current-UID event, got %#v", evidence.Events)
	}
	if evidence.Coverage != domain.SchedulingCoverageStale || !evidence.Events[0].Stale || result.Coverage != domain.SchedulingCoverageStale {
		t.Fatalf("expected stale coverage downgrade, got evidence=%#v result=%#v", evidence, result)
	}
}

func TestCollectSchedulingEvidenceDegradesExplicitlyForUnauthorizedEvents(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: typesMeta("demo", "checkout", "pod-auth"), Status: corev1.PodStatus{Phase: corev1.PodPending}}
	base := newSchedulingFakeClient(t, pod)
	reader := denyingSchedulingReader{Reader: base, denyEvents: true}

	result, err := (Adapter{APIReader: reader}).CollectSchedulingEvidence(context.Background(), SchedulingEvidenceRequest{Namespace: "demo", Pods: []SchedulingPodRef{{Name: pod.Name, UID: string(pod.UID)}}})
	if err == nil {
		t.Fatal("expected explicit unauthorized error")
	}
	var collectionErr *SchedulingCollectionError
	if !asSchedulingCollectionError(err, &collectionErr) || collectionErr.Reason != "Unauthorized" {
		t.Fatalf("expected Unauthorized collection error, got %v", err)
	}
	if result.Coverage != domain.SchedulingCoverageUnauthorized || len(result.Evidence) != 1 || result.Evidence[0].Coverage != domain.SchedulingCoverageUnauthorized {
		t.Fatalf("expected Unauthorized coverage instead of empty events, got %#v", result)
	}
	if len(result.Evidence[0].Events) != 0 {
		t.Fatalf("unauthorized collection must not fabricate events: %#v", result.Evidence[0].Events)
	}
}

func TestCollectSchedulingEvidenceDistinguishesBudgetAndDisappearingPod(t *testing.T) {
	now := time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC)
	pod := &corev1.Pod{ObjectMeta: typesMeta("demo", "checkout", "pod-budget"), Status: corev1.PodStatus{Phase: corev1.PodPending}}
	events := []client.Object{}
	for i := 0; i < 3; i++ {
		events = append(events, &corev1.Event{ObjectMeta: typesMetaWithUID("demo", fmt.Sprintf("failed-%d", i), types.UID(fmt.Sprintf("event-%d", i))), InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod.Name, UID: pod.UID}, Reason: "FailedScheduling", Message: "unschedulable", LastTimestamp: metav1Time(now.Add(time.Duration(i) * time.Second))})
	}
	objects := []client.Object{pod}
	objects = append(objects, events...)
	reader := newSchedulingFakeClient(t, objects...)
	result, err := (Adapter{APIReader: reader}).CollectSchedulingEvidence(context.Background(), SchedulingEvidenceRequest{
		Namespace: "demo", Pods: []SchedulingPodRef{{Name: pod.Name, UID: string(pod.UID)}, {Name: "gone", UID: "pod-gone"}}, Limits: SchedulingEvidenceLimits{MaxEvents: 2},
	})
	if err != nil {
		t.Fatalf("collect scheduling evidence: %v", err)
	}
	if result.Coverage != domain.SchedulingCoverageTruncated || !result.Truncated {
		t.Fatalf("expected explicit truncation, got %#v", result)
	}
	if len(result.Evidence) != 2 || result.Evidence[1].Coverage != domain.SchedulingCoverageUnavailable || result.Evidence[1].CoverageReason != "PodNotFound" {
		t.Fatalf("expected disappearing Pod degradation, got %#v", result.Evidence)
	}
}

func TestCollectSchedulingEvidenceTimeoutIsCollectionFailure(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: typesMeta("demo", "checkout", "pod-timeout")}
	reader := timeoutSchedulingReader{}
	result, err := (Adapter{APIReader: reader}).CollectSchedulingEvidence(context.Background(), SchedulingEvidenceRequest{Namespace: "demo", Pods: []SchedulingPodRef{{Name: pod.Name, UID: string(pod.UID)}}})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var collectionErr *SchedulingCollectionError
	if !asSchedulingCollectionError(err, &collectionErr) || collectionErr.Reason != "CollectionTimeout" {
		t.Fatalf("expected CollectionTimeout, got %v", err)
	}
	if result.Coverage != domain.SchedulingCoverageCollectionFailed {
		t.Fatalf("expected CollectionFailed coverage, got %#v", result)
	}
}

func TestCollectSchedulingEvidenceDoesNotTreatImagePullEventsAsSchedulingEvidence(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: typesMeta("demo", "image", "pod-image"), Status: corev1.PodStatus{Phase: corev1.PodPending}}
	event := &corev1.Event{ObjectMeta: typesMetaWithUID("demo", "backoff", "event-image"), InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod.Name, UID: pod.UID}, Reason: "BackOff", Message: "Back-off pulling image"}
	reader := newSchedulingFakeClient(t, pod, event)
	result, err := (Adapter{APIReader: reader}).CollectSchedulingEvidence(context.Background(), SchedulingEvidenceRequest{Namespace: "demo", Pods: []SchedulingPodRef{{Name: pod.Name, UID: string(pod.UID)}}})
	if err != nil {
		t.Fatalf("collect scheduling evidence: %v", err)
	}
	if result.Coverage != domain.SchedulingCoverageComplete || len(result.Evidence) != 1 || len(result.Evidence[0].Events) != 0 {
		t.Fatalf("image pull event must not become scheduling evidence: %#v", result)
	}
}

type denyingSchedulingReader struct {
	client.Reader
	denyEvents bool
}

func (r denyingSchedulingReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if r.denyEvents {
		switch list.(type) {
		case *corev1.EventList, *eventsv1.EventList:
			return apierrors.NewForbidden(schema.GroupResource{Group: "events.k8s.io", Resource: "events"}, "", fmt.Errorf("denied"))
		}
	}
	return r.Reader.List(ctx, list, opts...)
}

type timeoutSchedulingReader struct{}

func (timeoutSchedulingReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return context.DeadlineExceeded
}
func (timeoutSchedulingReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return context.DeadlineExceeded
}

func asSchedulingCollectionError(err error, target **SchedulingCollectionError) bool {
	candidate, ok := err.(*SchedulingCollectionError)
	if ok {
		*target = candidate
	}
	return ok
}

func newSchedulingFakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := eventsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&corev1.Event{}, "involvedObject.uid", func(obj client.Object) []string { return []string{string(obj.(*corev1.Event).InvolvedObject.UID)} }).
		WithIndex(&eventsv1.Event{}, "regarding.uid", func(obj client.Object) []string { return []string{string(obj.(*eventsv1.Event).Regarding.UID)} }).
		WithObjects(objects...).Build()
}

func typesMeta(namespace, name, uid string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: namespace, Name: name, UID: types.UID(uid)}
}
func typesMetaWithUID(namespace, name string, uid types.UID) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: namespace, Name: name, UID: uid}
}
func metav1Time(value time.Time) metav1.Time          { return metav1.NewTime(value) }
func microTime(value time.Time) metav1.MicroTime      { return metav1.NewMicroTime(value) }
func resourceQuantity(value string) resource.Quantity { return resource.MustParse(value) }

func TestCollectSchedulingEvidenceRejectsStalePodReference(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: typesMeta("demo", "checkout", "pod-current"), Status: corev1.PodStatus{Phase: corev1.PodPending}}
	reader := newSchedulingFakeClient(t, pod)
	result, err := (Adapter{APIReader: reader}).CollectSchedulingEvidence(context.Background(), SchedulingEvidenceRequest{
		Namespace: "demo", Pods: []SchedulingPodRef{{Name: pod.Name, UID: "pod-previous"}},
	})
	if err != nil {
		t.Fatalf("collect scheduling evidence: %v", err)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].Coverage != domain.SchedulingCoverageStale || result.Evidence[0].CoverageReason != "PodUIDMismatch" {
		t.Fatalf("expected stale Pod UID degradation, got %#v", result)
	}
	if len(result.Evidence[0].Events) != 0 {
		t.Fatalf("stale Pod reference must not collect current Pod events: %#v", result.Evidence[0].Events)
	}
}
