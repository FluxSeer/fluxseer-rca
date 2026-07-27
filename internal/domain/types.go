package domain

import "time"

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
	QueryTypeMetric              QueryType = "metric"
	QueryTypeLog                 QueryType = "log"
	QueryTypeTrace               QueryType = "trace"
	QueryTypeEvent               QueryType = "event"
	QueryTypeDeploymentCondition QueryType = "deploymentCondition"
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
	Logs       []string           `json:"logs,omitempty"`
	Metrics    map[string]float64 `json:"metrics,omitempty"`
	Traces     []string           `json:"traces,omitempty"`
	Events     []string           `json:"events,omitempty"`
	References []Evidence         `json:"references,omitempty"`
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
	RiskTitle   string      `json:"riskTitle"`
	RiskSummary string      `json:"riskSummary"`
	Severity    Severity    `json:"severity"`
	Confidence  Confidence  `json:"confidence"`
	RCA         RCASummary  `json:"rca"`
	Remediation Remediation `json:"remediation"`
	RunbookRefs []string    `json:"runbookRefs"`
	ServiceDocs []string    `json:"serviceDocs"`
	Provider    string      `json:"provider"`
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

type ExecutionResult struct {
	Executor   string            `json:"executor"`
	Status     string            `json:"status"`
	Summary    string            `json:"summary"`
	Outputs    map[string]string `json:"outputs,omitempty"`
	FinishedAt time.Time         `json:"finishedAt"`
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
	Provider   string         `json:"provider"`
	Model      string         `json:"model"`
	Output     map[string]any `json:"output,omitempty"`
	RawText    string         `json:"rawText,omitempty"`
	Structured bool           `json:"structured"`
}
