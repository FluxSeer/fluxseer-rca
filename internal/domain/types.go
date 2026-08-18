package domain

import (
	"time"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
	SeverityUnsafe Severity = "unsafe"
)

type ResourceRef struct {
	Cluster    string `json:"cluster"`
	Namespace  string `json:"namespace"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	APIVersion string `json:"apiVersion"`
	Service    string `json:"service"`
}

type Signal struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Source     string            `json:"source"`
	Severity   Severity          `json:"severity"`
	Message    string            `json:"message"`
	Resource   ResourceRef       `json:"resource"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
}

type QueryType string

const (
	QueryTypeMetric               QueryType = "metric"
	QueryTypeLog                  QueryType = "log"
	QueryTypeTrace                QueryType = "trace"
	QueryTypeEvent                QueryType = "event"
	QueryTypeDeploymentCondition  QueryType = "deploymentCondition"
	QueryTypeServiceConfiguration QueryType = "serviceConfiguration"
)

type TimelineEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
}

type Evidence struct {
	Kind    string `json:"kind"`
	Source  string `json:"source,omitempty"`
	Summary string `json:"summary"`
	Query   string `json:"query,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Link    string `json:"link,omitempty"`
}

type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type ObservationType string

const (
	ObservationTypeMetric               ObservationType = "metric"
	ObservationTypeLog                  ObservationType = "log"
	ObservationTypeEvent                ObservationType = "event"
	ObservationTypeDeploymentCondition  ObservationType = "deploymentCondition"
	ObservationTypeServiceConfiguration ObservationType = "serviceConfiguration"
)

type ObservationValue struct {
	Metric               *MetricObservation               `json:"metric,omitempty"`
	Log                  *LogObservation                  `json:"log,omitempty"`
	Event                *EventObservation                `json:"event,omitempty"`
	DeploymentCondition  *DeploymentConditionObservation  `json:"deploymentCondition,omitempty"`
	ServiceConfiguration *ServiceConfigurationObservation `json:"serviceConfiguration,omitempty"`
}

type MetricObservation struct {
	Name  string  `json:"name,omitempty"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

type LogObservation struct {
	Line string `json:"line,omitempty"`
}

type EventObservation struct {
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type DeploymentConditionObservation struct {
	Type              string `json:"type,omitempty"`
	Status            string `json:"status,omitempty"`
	Reason            string `json:"reason,omitempty"`
	Message           string `json:"message,omitempty"`
	AvailableReplicas int32  `json:"availableReplicas,omitempty"`
	DesiredReplicas   int32  `json:"desiredReplicas,omitempty"`
}

type ServiceConfigurationObservation struct {
	ServiceName        string `json:"serviceName,omitempty"`
	ServicePortName    string `json:"servicePortName,omitempty"`
	ServicePort        int32  `json:"servicePort,omitempty"`
	TargetPortRaw      string `json:"targetPortRaw,omitempty"`
	TargetPortResolved int32  `json:"targetPortResolved,omitempty"`
	TargetPortNamed    bool   `json:"targetPortNamed,omitempty"`
	WorkloadKind       string `json:"workloadKind,omitempty"`
	WorkloadName       string `json:"workloadName,omitempty"`
	ContainerName      string `json:"containerName,omitempty"`
	ContainerPortName  string `json:"containerPortName,omitempty"`
	ContainerPort      int32  `json:"containerPort,omitempty"`
	Resolution         string `json:"resolution,omitempty"`
	MismatchConfirmed  bool   `json:"mismatchConfirmed,omitempty"`
	Reason             string `json:"reason,omitempty"`
}

type Observation struct {
	ID                     string                       `json:"id"`
	SchemaVersion          string                       `json:"schemaVersion"`
	DataSourceRef          string                       `json:"dataSourceRef"`
	Capability             QueryType                    `json:"capability"`
	QueryDigest            string                       `json:"queryDigest"`
	TimeRange              TimeRange                    `json:"timeRange"`
	Type                   ObservationType              `json:"type"`
	Value                  ObservationValue             `json:"value"`
	Summary                string                       `json:"summary"`
	ContentDigest          string                       `json:"contentDigest"`
	DigestAlgorithm        string                       `json:"digestAlgorithm"`
	DigestCanonicalization string                       `json:"digestCanonicalization"`
	RedactionProfile       string                       `json:"redactionProfile"`
	Classification         *v1alpha1.DataClassification `json:"classification,omitempty"`
	Truncated              bool                         `json:"truncated"`
	TruncationReason       string                       `json:"truncationReason,omitempty"`
	LimitDimension         string                       `json:"limitDimension,omitempty"`
	Limit                  int64                        `json:"limit,omitempty"`
	OriginalCount          int                          `json:"originalCount"`
	RetainedCount          int                          `json:"retainedCount"`
	OriginalBytes          int                          `json:"originalBytes"`
	RetainedBytes          int                          `json:"retainedBytes"`
	CollectedAt            time.Time                    `json:"collectedAt"`
}

type IncidentContext struct {
	ID          string            `json:"id"`
	Cluster     string            `json:"cluster"`
	Service     string            `json:"service"`
	Resource    ResourceRef       `json:"resource"`
	Summary     string            `json:"summary"`
	Signals     []Signal          `json:"signals"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	GeneratedAt time.Time         `json:"generatedAt"`
}

type EvidenceBundle struct {
	Logs         []string           `json:"logs,omitempty"`
	Metrics      map[string]float64 `json:"metrics,omitempty"`
	Traces       []string           `json:"traces,omitempty"`
	Events       []string           `json:"events,omitempty"`
	References   []Evidence         `json:"references,omitempty"`
	Observations []Observation      `json:"observations,omitempty"`
}

type ResourceTimeline struct {
	Resource ResourceRef     `json:"resource"`
	Events   []TimelineEvent `json:"events"`
}

type IngestionOutput struct {
	Context     IncidentContext  `json:"context"`
	Evidence    EvidenceBundle   `json:"evidence"`
	Signals     []Signal         `json:"signals"`
	Timeline    ResourceTimeline `json:"timeline"`
	DedupedFrom int              `json:"dedupedFrom"`
}

type RCASummary struct {
	Hypothesis string   `json:"hypothesis"`
	Causes     []string `json:"causes"`
	Evidence   []string `json:"evidence"`
}

type Remediation struct {
	ActionType   string            `json:"actionType"`
	Description  string            `json:"description"`
	Parameters   map[string]string `json:"parameters,omitempty"`
	RollbackPlan []string          `json:"rollbackPlan,omitempty"`
}

type Confidence struct {
	Score            int      `json:"score"`
	Severity         Severity `json:"severity"`
	Rationale        string   `json:"rationale"`
	EvidenceCoverage string   `json:"evidenceCoverage"`
}

type ReasoningOutput struct {
	RiskTitle         string      `json:"riskTitle"`
	RiskSummary       string      `json:"riskSummary"`
	Severity          Severity    `json:"severity"`
	Confidence        Confidence  `json:"confidence"`
	RCA               RCASummary  `json:"rca"`
	Remediation       Remediation `json:"remediation"`
	RunbookRefs       []string    `json:"runbookRefs"`
	ServiceDocs       []string    `json:"serviceDocs"`
	Provider          string      `json:"provider"`
	ProviderRequestID string      `json:"providerRequestID,omitempty"`
	InputTokens       int64       `json:"inputTokens,omitempty"`
	OutputTokens      int64       `json:"outputTokens,omitempty"`
}

type ApprovalAction string

const (
	ApprovalAuto   ApprovalAction = "auto_approve"
	ApprovalManual ApprovalAction = "needs_approval"
	ApprovalReject ApprovalAction = "reject"
)

type ApprovalDecision struct {
	Action       ApprovalAction `json:"action"`
	ApprovedBy   string         `json:"approvedBy,omitempty"`
	Reason       string         `json:"reason"`
	DryRunResult string         `json:"dryRunResult,omitempty"`
}

type ExecutionOutcome string

const (
	ExecutionOutcomeSucceeded          ExecutionOutcome = "Succeeded"
	ExecutionOutcomeFailed             ExecutionOutcome = "Failed"
	ExecutionOutcomeTimedOut           ExecutionOutcome = "TimedOut"
	ExecutionOutcomeUnknown            ExecutionOutcome = "Unknown"
	ExecutionOutcomePartiallySucceeded ExecutionOutcome = "PartiallySucceeded"
)

// ExecutionResult is the backend-neutral result contract for an executor.
// Status, Summary, and Outputs are retained as compatibility fields while
// Outcome, identity, timing, and retry semantics become the authoritative
// v0.5 execution contract.
type ExecutionResult struct {
	ExecutionID   string            `json:"executionID,omitempty"`
	Outcome       ExecutionOutcome  `json:"outcome,omitempty"`
	FailureReason string            `json:"failureReason,omitempty"`
	StartedAt     time.Time         `json:"startedAt,omitempty"`
	FinishedAt    time.Time         `json:"finishedAt,omitempty"`
	ExternalRef   string            `json:"externalRef,omitempty"`
	Retryable     bool              `json:"retryable,omitempty"`
	Executor      string            `json:"executor"`
	Status        string            `json:"status"`
	Summary       string            `json:"summary"`
	Outputs       map[string]string `json:"outputs,omitempty"`
}

type ModelMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ModelRequest struct {
	ProviderHint string         `json:"providerHint,omitempty"`
	SystemPrompt string         `json:"systemPrompt,omitempty"`
	Messages     []ModelMessage `json:"messages,omitempty"`
	Context      map[string]any `json:"context,omitempty"`
}

type ModelResponse struct {
	Provider          string         `json:"provider"`
	Model             string         `json:"model"`
	ProviderRequestID string         `json:"providerRequestID,omitempty"`
	InputTokens       int64          `json:"inputTokens,omitempty"`
	OutputTokens      int64          `json:"outputTokens,omitempty"`
	Output            map[string]any `json:"output,omitempty"`
	RawText           string         `json:"rawText,omitempty"`
	Structured        bool           `json:"structured"`
}
