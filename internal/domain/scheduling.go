package domain

import "time"

// SchedulingCoverage describes how completely a target Pod's scheduling
// evidence was collected. It is deliberately separate from diagnostic status:
// an empty event set can still be Complete, while an authorization or budget
// failure must never be interpreted as an empty event set.
type SchedulingCoverage string

const (
	SchedulingCoverageComplete         SchedulingCoverage = "Complete"
	SchedulingCoveragePartial          SchedulingCoverage = "Partial"
	SchedulingCoverageUnavailable      SchedulingCoverage = "Unavailable"
	SchedulingCoverageTruncated        SchedulingCoverage = "Truncated"
	SchedulingCoverageStale            SchedulingCoverage = "Stale"
	SchedulingCoverageUnauthorized     SchedulingCoverage = "Unauthorized"
	SchedulingCoverageCollectionFailed SchedulingCoverage = "CollectionFailed"
)

// SchedulingEvent is a normalized Kubernetes Event from either core/v1 or
// events.k8s.io/v1. The Pod UID is required for correlation; names alone are
// insufficient because a Pod name can be reused after deletion.
type SchedulingEvent struct {
	ID             string    `json:"id"`
	UID            string    `json:"uid,omitempty"`
	PodUID         string    `json:"podUID"`
	Source         string    `json:"source,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	Message        string    `json:"message,omitempty"`
	Type           string    `json:"type,omitempty"`
	ObservedAt     time.Time `json:"observedAt,omitempty"`
	LastObservedAt time.Time `json:"lastObservedAt,omitempty"`
	TimeKnown      bool      `json:"timeKnown"`
	Stale          bool      `json:"stale"`
}

// SchedulingResourceRequirements preserves resource requests without
// introducing Kubernetes API types into the normalized evidence model.
type SchedulingResourceRequirements struct {
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}

// SchedulingContainerResources preserves the container boundary while keeping
// resource quantities provider-neutral.
type SchedulingContainerResources struct {
	Name     string            `json:"name"`
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}

// SchedulingToleration is the normalized subset of a Pod toleration needed by
// scheduling diagnostics. Values are kept as strings so the model is stable
// across Kubernetes API versions.
type SchedulingToleration struct {
	Key               string `json:"key,omitempty"`
	Operator          string `json:"operator,omitempty"`
	Value             string `json:"value,omitempty"`
	Effect            string `json:"effect,omitempty"`
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty"`
}

// SchedulingNodeSelectorTerm is a provider-neutral representation of a node
// affinity term. MatchFields is included because the scheduler evaluates both
// labels and node fields.
type SchedulingNodeSelectorTerm struct {
	Weight           int32                               `json:"weight,omitempty"`
	MatchExpressions []SchedulingNodeSelectorRequirement `json:"matchExpressions,omitempty"`
	MatchFields      []SchedulingNodeSelectorRequirement `json:"matchFields,omitempty"`
}

type SchedulingNodeSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// SchedulingEvidence is the bounded, normalized state collected for one Pod.
// It contains observations and configuration only; it does not assert a root
// cause. Collectors should set Coverage explicitly even when Events is empty.
type SchedulingEvidence struct {
	ID                     string                           `json:"id"`
	PodUID                 string                           `json:"podUID"`
	Namespace              string                           `json:"namespace"`
	Name                   string                           `json:"name"`
	Source                 string                           `json:"source,omitempty"`
	Phase                  string                           `json:"phase,omitempty"`
	NodeName               string                           `json:"nodeName,omitempty"`
	PodScheduledStatus     string                           `json:"podScheduledStatus,omitempty"`
	PodScheduledReason     string                           `json:"podScheduledReason,omitempty"`
	PodScheduledMessage    string                           `json:"podScheduledMessage,omitempty"`
	SchedulingGates        []string                         `json:"schedulingGates,omitempty"`
	ResourceRequests       SchedulingResourceRequirements   `json:"resourceRequests,omitempty"`
	ContainerResources     []SchedulingContainerResources   `json:"containerResources,omitempty"`
	InitRequests           []SchedulingResourceRequirements `json:"initRequests,omitempty"`
	InitContainerResources []SchedulingContainerResources   `json:"initContainerResources,omitempty"`
	NodeSelector           map[string]string                `json:"nodeSelector,omitempty"`
	NodeAffinityRequired   []SchedulingNodeSelectorTerm     `json:"nodeAffinityRequired,omitempty"`
	NodeAffinityPreferred  []SchedulingNodeSelectorTerm     `json:"nodeAffinityPreferred,omitempty"`
	Tolerations            []SchedulingToleration           `json:"tolerations,omitempty"`
	Events                 []SchedulingEvent                `json:"events,omitempty"`
	Coverage               SchedulingCoverage               `json:"coverage"`
	CoverageReason         string                           `json:"coverageReason,omitempty"`
	IncidentStart          time.Time                        `json:"incidentStart,omitempty"`
	IncidentEnd            time.Time                        `json:"incidentEnd,omitempty"`
	CollectedAt            time.Time                        `json:"collectedAt"`
	Truncated              bool                             `json:"truncated"`
	Stale                  bool                             `json:"stale"`
}

// SchedulingPodRef is an already scope-resolved, authorized Pod reference.
// It is shared by provider adapters and the investigation runtime so the
// collector cannot silently widen the target scope.
type SchedulingPodRef struct {
	Namespace string
	Name      string
	UID       string
}

// SchedulingEvidenceLimits bound every dimension of a scheduling collection.
type SchedulingEvidenceLimits struct {
	MaxPods        int
	MaxEvents      int
	MaxPages       int
	MaxAPIRequests int
}

// SchedulingEvidenceRequest supplies the incident window and exact Pod scope
// to an optional datasource scheduling collector.
type SchedulingEvidenceRequest struct {
	Namespace string
	Pods      []SchedulingPodRef
	StartTime time.Time
	EndTime   time.Time
	Limits    SchedulingEvidenceLimits
}

// SchedulingEvidenceResult is the provider-neutral result of one bounded
// scheduling collection. The runtime invokes this at most once per
// investigation and adapts the returned records into EvidenceSet.
type SchedulingEvidenceResult struct {
	Evidence       []SchedulingEvidence
	Coverage       SchedulingCoverage
	CoverageReason string
	APIRequests    int
	ScannedPods    int
	ScannedEvents  int
	Truncated      bool
}
