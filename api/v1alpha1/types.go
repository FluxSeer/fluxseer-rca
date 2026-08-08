package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	PhasePending          = "Pending"
	PhaseObserved         = "Observed"
	PhaseCollecting       = "Collecting"
	PhaseReasoning        = "Reasoning"
	PhaseVerifying        = "Verifying"
	PhaseConfirmed        = "Confirmed"
	PhaseNotified         = "Notified"
	PhasePolicyChecked    = "PolicyChecked"
	PhaseDryRunSucceeded  = "DryRunSucceeded"
	PhaseWaitingApproval  = "WaitingApproval"
	PhaseApproved         = "Approved"
	PhaseExecuting        = "Executing"
	PhaseSucceeded        = "Succeeded"
	PhaseFailed           = "Failed"
	PhaseRejected         = "Rejected"
	PhaseRecommendation   = "Recommendation"
	PhaseReadyForApproval = "ReadyForApproval"
	PhaseCompleted        = "Completed"
	PhaseEscalated        = "Escalated"
)

type TargetRef struct {
	Cluster    string `json:"cluster,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Name       string `json:"name,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
	Service    string `json:"service,omitempty"`
}

const (
	DataClassificationPolicyVersion = "fluxseer-rca-data-classification-v1"

	DataClassificationLevelPublic       = "Public"
	DataClassificationLevelInternal     = "Internal"
	DataClassificationLevelConfidential = "Confidential"
	DataClassificationLevelRestricted   = "Restricted"

	DataClassificationSourceDefault          = "Default"
	DataClassificationSourceExplicit         = "Explicit"
	DataClassificationSourceInherited        = "Inherited"
	DataClassificationSourceContentDetection = "ContentDetection"
	DataClassificationSourceRedactionPolicy  = "RedactionPolicy"

	SensitivityTagCredentialLike         = "CredentialLike"
	SensitivityTagPersonalData           = "PersonalData"
	SensitivityTagCustomerData           = "CustomerData"
	SensitivityTagSourceCode             = "SourceCode"
	SensitivityTagInfrastructureMetadata = "InfrastructureMetadata"
	SensitivityTagSecuritySensitive      = "SecuritySensitive"
)

type DataClassification struct {
	Level           string   `json:"level,omitempty"`
	SensitivityTags []string `json:"sensitivityTags,omitempty"`
	Source          string   `json:"source,omitempty"`
	PolicyVersion   string   `json:"policyVersion,omitempty"`
}

type EvidenceRef struct {
	ID                     string              `json:"id,omitempty"`
	Kind                   string              `json:"kind,omitempty"`
	Source                 string              `json:"source,omitempty"`
	Summary                string              `json:"summary,omitempty"`
	Query                  string              `json:"query,omitempty"`
	QueryDigest            string              `json:"queryDigest,omitempty"`
	ContentDigest          string              `json:"contentDigest,omitempty"`
	DigestAlgorithm        string              `json:"digestAlgorithm,omitempty"`
	DigestCanonicalization string              `json:"digestCanonicalization,omitempty"`
	RedactionProfile       string              `json:"redactionProfile,omitempty"`
	Classification         *DataClassification `json:"classification,omitempty"`
	Truncated              bool                `json:"truncated,omitempty"`
	TruncationReason       string              `json:"truncationReason,omitempty"`
	LimitDimension         string              `json:"limitDimension,omitempty"`
	Limit                  int64               `json:"limit,omitempty"`
	OriginalCount          int32               `json:"originalCount,omitempty"`
	RetainedCount          int32               `json:"retainedCount,omitempty"`
	OriginalBytes          int32               `json:"originalBytes,omitempty"`
	RetainedBytes          int32               `json:"retainedBytes,omitempty"`
	CollectedAt            *metav1.Time        `json:"collectedAt,omitempty"`
	PayloadRef             *PayloadRef         `json:"payloadRef,omitempty"`
	Reason                 string              `json:"reason,omitempty"`
	Link                   string              `json:"link,omitempty"`
}

type PayloadRef struct {
	Scheme         string       `json:"scheme,omitempty"`
	Digest         string       `json:"digest,omitempty"`
	Encrypted      bool         `json:"encrypted,omitempty"`
	ExpiresAt      *metav1.Time `json:"expiresAt,omitempty"`
	RetentionClass string       `json:"retentionClass,omitempty"`
}

type FindingIdentity struct {
	SchemaVersion          string `json:"schemaVersion,omitempty"`
	ObjectFindingIdentity  string `json:"objectFindingIdentity,omitempty"`
	LogicalFindingIdentity string `json:"logicalFindingIdentity,omitempty"`
	IncidentOccurrence     string `json:"incidentOccurrence,omitempty"`
	FindingType            string `json:"findingType,omitempty"`
	TargetGeneration       int64  `json:"targetGeneration,omitempty"`
	WindowBucket           string `json:"windowBucket,omitempty"`
}

type ResourceStatus struct {
	Phase              string      `json:"phase,omitempty"`
	Message            string      `json:"message,omitempty"`
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	UpdatedAt          metav1.Time `json:"updatedAt,omitempty"`
}

type RCACause struct {
	Cause      string `json:"cause,omitempty"`
	Confidence int    `json:"confidence,omitempty"`
}

type RiskSignalRCAProjectionStatus struct {
	Mode              string                     `json:"mode,omitempty"`
	ProjectedFrom     *NamespacedObjectReference `json:"projectedFrom,omitempty"`
	CompatibilityPath bool                       `json:"compatibilityPath,omitempty"`
	Message           string                     `json:"message,omitempty"`
}

type RiskSignalStatus struct {
	ResourceStatus `json:",inline"`
	RCASummary     string                         `json:"rcaSummary,omitempty"`
	RCAHypothesis  string                         `json:"rcaHypothesis,omitempty"`
	RCAProvider    string                         `json:"rcaProvider,omitempty"`
	RCACauses      []RCACause                     `json:"rcaCauses,omitempty"`
	Projection     *RiskSignalRCAProjectionStatus `json:"projection,omitempty"`
	Conditions     []metav1.Condition             `json:"conditions,omitempty"`
}

type RiskSignalSpec struct {
	Target                 TargetRef                  `json:"target"`
	SignalType             string                     `json:"signalType"`
	FindingIdentity        *FindingIdentity           `json:"findingIdentity,omitempty"`
	InvestigationRef       *NamespacedObjectReference `json:"investigationRef,omitempty"`
	ActionType             string                     `json:"actionType,omitempty"`
	Severity               string                     `json:"severity"`
	Confidence             int                        `json:"confidence"`
	DryRun                 bool                       `json:"dryRun"`
	TTLSeconds             int64                      `json:"ttlSeconds,omitempty"`
	Evidence               []EvidenceRef              `json:"evidence,omitempty"`
	Parameters             map[string]string          `json:"parameters,omitempty"`
	ApprovalTimeoutSeconds int64                      `json:"approvalTimeoutSeconds,omitempty"`
}

type RemediationStep struct {
	Name        string            `json:"name"`
	ActionType  string            `json:"actionType"`
	Description string            `json:"description,omitempty"`
	Parameters  map[string]string `json:"parameters,omitempty"`
}

type RemediationPlanSpec struct {
	Target                 TargetRef         `json:"target"`
	RecommendedBy          string            `json:"recommendedBy,omitempty"`
	Severity               string            `json:"severity"`
	Confidence             int               `json:"confidence"`
	DryRun                 bool              `json:"dryRun"`
	TTLSeconds             int64             `json:"ttlSeconds,omitempty"`
	ApprovalTimeoutSeconds int64             `json:"approvalTimeoutSeconds,omitempty"`
	Summary                string            `json:"summary,omitempty"`
	RollbackPlan           []string          `json:"rollbackPlan,omitempty"`
	References             []string          `json:"references,omitempty"`
	Steps                  []RemediationStep `json:"steps,omitempty"`
}

type AgentActionSpec struct {
	Target                 TargetRef         `json:"target"`
	ActionType             string            `json:"actionType"`
	Parameters             map[string]string `json:"parameters,omitempty"`
	ApprovedBy             string            `json:"approvedBy,omitempty"`
	DryRunResult           string            `json:"dryRunResult,omitempty"`
	TTLSeconds             int64             `json:"ttlSeconds,omitempty"`
	ApprovalTimeoutSeconds int64             `json:"approvalTimeoutSeconds,omitempty"`
	RollbackPlan           []string          `json:"rollbackPlan,omitempty"`
}

type AgentActionApprovalStatus struct {
	Approved           bool         `json:"approved,omitempty"`
	ApprovedBy         string       `json:"approvedBy,omitempty"`
	Source             string       `json:"source,omitempty"`
	ActionDigest       string       `json:"actionDigest,omitempty"`
	ApprovedGeneration int64        `json:"approvedGeneration,omitempty"`
	ApprovedAt         *metav1.Time `json:"approvedAt,omitempty"`
	EscalatedAt        *metav1.Time `json:"escalatedAt,omitempty"`
	DecidedAt          *metav1.Time `json:"decidedAt,omitempty"`
	DecidedBy          string       `json:"decidedBy,omitempty"`
}

type AgentActionNotificationStatus struct {
	LastAttemptAt *metav1.Time `json:"lastAttemptAt,omitempty"`
	RetryCount    int32        `json:"retryCount,omitempty"`
	LastError     string       `json:"lastError,omitempty"`
}

type AgentActionDryRunStatus struct {
	Result     string       `json:"result,omitempty"`
	RecordedAt *metav1.Time `json:"recordedAt,omitempty"`
}

type AgentActionExecutionStatus struct {
	Phase      string       `json:"phase,omitempty"`
	Executor   string       `json:"executor,omitempty"`
	Summary    string       `json:"summary,omitempty"`
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
}

type AgentActionEffectivenessStatus struct {
	Phase           string                     `json:"phase,omitempty"`
	Message         string                     `json:"message,omitempty"`
	VerificationRef *NamespacedObjectReference `json:"verificationRef,omitempty"`
}

type AgentActionStatus struct {
	ResourceStatus `json:",inline"`
	Approval       *AgentActionApprovalStatus      `json:"approval,omitempty"`
	Notification   *AgentActionNotificationStatus  `json:"notification,omitempty"`
	DryRunResult   *AgentActionDryRunStatus        `json:"dryRunResult,omitempty"`
	Execution      *AgentActionExecutionStatus     `json:"execution,omitempty"`
	Effectiveness  *AgentActionEffectivenessStatus `json:"effectiveness,omitempty"`
}

type RiskSignal struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RiskSignalSpec   `json:"spec,omitempty"`
	Status RiskSignalStatus `json:"status,omitempty"`
}

type RiskSignalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RiskSignal `json:"items"`
}

type RemediationPlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RemediationPlanSpec `json:"spec,omitempty"`
	Status ResourceStatus      `json:"status,omitempty"`
}

type RemediationPlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RemediationPlan `json:"items"`
}

type AgentAction struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentActionSpec   `json:"spec,omitempty"`
	Status AgentActionStatus `json:"status,omitempty"`
}

type AgentActionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentAction `json:"items"`
}

func (in *RiskSignal) DeepCopyInto(out *RiskSignal) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.Evidence != nil {
		out.Spec.Evidence = deepcopyEvidenceRefs(in.Spec.Evidence)
	}
	if in.Spec.FindingIdentity != nil {
		identity := *in.Spec.FindingIdentity
		out.Spec.FindingIdentity = &identity
	}
	if in.Spec.Parameters != nil {
		out.Spec.Parameters = make(map[string]string, len(in.Spec.Parameters))
		for key, value := range in.Spec.Parameters {
			out.Spec.Parameters[key] = value
		}
	}
	if in.Status.RCACauses != nil {
		out.Status.RCACauses = append([]RCACause(nil), in.Status.RCACauses...)
	}
	if in.Status.Projection != nil {
		projection := *in.Status.Projection
		if in.Status.Projection.ProjectedFrom != nil {
			ref := *in.Status.Projection.ProjectedFrom
			projection.ProjectedFrom = &ref
		}
		out.Status.Projection = &projection
	}
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		copy(out.Status.Conditions, in.Status.Conditions)
	}
	if in.Spec.InvestigationRef != nil {
		ref := *in.Spec.InvestigationRef
		out.Spec.InvestigationRef = &ref
	}
}

func deepcopyEvidenceRefs(in []EvidenceRef) []EvidenceRef {
	if in == nil {
		return nil
	}
	out := make([]EvidenceRef, len(in))
	copy(out, in)
	for index := range out {
		if in[index].Classification != nil {
			classification := *in[index].Classification
			if in[index].Classification.SensitivityTags != nil {
				classification.SensitivityTags = append([]string(nil), in[index].Classification.SensitivityTags...)
			}
			out[index].Classification = &classification
		}
		if in[index].CollectedAt != nil {
			collectedAt := in[index].CollectedAt.DeepCopy()
			out[index].CollectedAt = collectedAt
		}
		if in[index].PayloadRef != nil {
			payloadRef := *in[index].PayloadRef
			if in[index].PayloadRef.ExpiresAt != nil {
				payloadRef.ExpiresAt = in[index].PayloadRef.ExpiresAt.DeepCopy()
			}
			out[index].PayloadRef = &payloadRef
		}
	}
	return out
}

func (in *RiskSignal) DeepCopy() *RiskSignal {
	if in == nil {
		return nil
	}
	out := new(RiskSignal)
	in.DeepCopyInto(out)
	return out
}

func (in *RiskSignal) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *RiskSignalList) DeepCopyInto(out *RiskSignalList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]RiskSignal, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *RiskSignalList) DeepCopy() *RiskSignalList {
	if in == nil {
		return nil
	}
	out := new(RiskSignalList)
	in.DeepCopyInto(out)
	return out
}

func (in *RiskSignalList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *RemediationPlan) DeepCopyInto(out *RemediationPlan) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.RollbackPlan != nil {
		out.Spec.RollbackPlan = append([]string(nil), in.Spec.RollbackPlan...)
	}
	if in.Spec.References != nil {
		out.Spec.References = append([]string(nil), in.Spec.References...)
	}
	if in.Spec.Steps != nil {
		out.Spec.Steps = make([]RemediationStep, len(in.Spec.Steps))
		copy(out.Spec.Steps, in.Spec.Steps)
		for i := range out.Spec.Steps {
			if in.Spec.Steps[i].Parameters != nil {
				out.Spec.Steps[i].Parameters = make(map[string]string, len(in.Spec.Steps[i].Parameters))
				for key, value := range in.Spec.Steps[i].Parameters {
					out.Spec.Steps[i].Parameters[key] = value
				}
			}
		}
	}
}

func (in *RemediationPlan) DeepCopy() *RemediationPlan {
	if in == nil {
		return nil
	}
	out := new(RemediationPlan)
	in.DeepCopyInto(out)
	return out
}

func (in *RemediationPlan) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *RemediationPlanList) DeepCopyInto(out *RemediationPlanList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]RemediationPlan, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *RemediationPlanList) DeepCopy() *RemediationPlanList {
	if in == nil {
		return nil
	}
	out := new(RemediationPlanList)
	in.DeepCopyInto(out)
	return out
}

func (in *RemediationPlanList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *AgentAction) DeepCopyInto(out *AgentAction) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.Parameters != nil {
		out.Spec.Parameters = make(map[string]string, len(in.Spec.Parameters))
		for key, value := range in.Spec.Parameters {
			out.Spec.Parameters[key] = value
		}
	}
	if in.Spec.RollbackPlan != nil {
		out.Spec.RollbackPlan = append([]string(nil), in.Spec.RollbackPlan...)
	}
	if in.Status.Approval != nil {
		approval := *in.Status.Approval
		if in.Status.Approval.ApprovedAt != nil {
			approval.ApprovedAt = in.Status.Approval.ApprovedAt.DeepCopy()
		}
		if in.Status.Approval.EscalatedAt != nil {
			approval.EscalatedAt = in.Status.Approval.EscalatedAt.DeepCopy()
		}
		if in.Status.Approval.DecidedAt != nil {
			approval.DecidedAt = in.Status.Approval.DecidedAt.DeepCopy()
		}
		out.Status.Approval = &approval
	}
	if in.Status.Notification != nil {
		notification := *in.Status.Notification
		if in.Status.Notification.LastAttemptAt != nil {
			notification.LastAttemptAt = in.Status.Notification.LastAttemptAt.DeepCopy()
		}
		out.Status.Notification = &notification
	}
	if in.Status.DryRunResult != nil {
		dryRun := *in.Status.DryRunResult
		if in.Status.DryRunResult.RecordedAt != nil {
			dryRun.RecordedAt = in.Status.DryRunResult.RecordedAt.DeepCopy()
		}
		out.Status.DryRunResult = &dryRun
	}
	if in.Status.Execution != nil {
		execution := *in.Status.Execution
		if in.Status.Execution.FinishedAt != nil {
			execution.FinishedAt = in.Status.Execution.FinishedAt.DeepCopy()
		}
		out.Status.Execution = &execution
	}
	if in.Status.Effectiveness != nil {
		effectiveness := *in.Status.Effectiveness
		if in.Status.Effectiveness.VerificationRef != nil {
			ref := *in.Status.Effectiveness.VerificationRef
			effectiveness.VerificationRef = &ref
		}
		out.Status.Effectiveness = &effectiveness
	}
}

func (in *AgentAction) DeepCopy() *AgentAction {
	if in == nil {
		return nil
	}
	out := new(AgentAction)
	in.DeepCopyInto(out)
	return out
}

func (in *AgentAction) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *AgentActionList) DeepCopyInto(out *AgentActionList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]AgentAction, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *AgentActionList) DeepCopy() *AgentActionList {
	if in == nil {
		return nil
	}
	out := new(AgentActionList)
	in.DeepCopyInto(out)
	return out
}

func (in *AgentActionList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
