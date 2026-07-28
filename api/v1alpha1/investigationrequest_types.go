package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	InvestigationModeReadOnly = "readOnly"

	InvestigationOutcomeConfirmed    = "Confirmed"
	InvestigationOutcomeInconclusive = "Inconclusive"
	InvestigationOutcomeNoIssueFound = "NoIssueFound"
	InvestigationOutcomeUnknown      = "Unknown"

	// Deprecated: workflow execution failure is represented by
	// status.phase=Failed, status.outcome=Unknown, and status.failure.
	InvestigationOutcomeFailed = "ExecutionFailed"
)

const (
	InvestigationStageValidation         = "Validation"
	InvestigationStageTargetResolution   = "TargetResolution"
	InvestigationStageEvidenceCollection = "EvidenceCollection"
	InvestigationStageReasoning          = "Reasoning"
	InvestigationStageVerification       = "Verification"
	InvestigationStagePersistence        = "Persistence"
)

type InvestigationTimeRange struct {
	Lookback metav1.Duration `json:"lookback,omitempty"`
}

type NamespacedObjectReference struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type InvestigationQuery struct {
	Name          string               `json:"name,omitempty"`
	DatasourceRef LocalObjectReference `json:"datasourceRef,omitempty"`
	QueryType     string               `json:"queryType,omitempty"`
	Query         string               `json:"query,omitempty"`
	QueryTemplate string               `json:"queryTemplate,omitempty"`
	Reasons       []string             `json:"reasons,omitempty"`
}

type InvestigationRequestSpec struct {
	Target           TargetRef              `json:"target"`
	TimeRange        InvestigationTimeRange `json:"timeRange,omitempty"`
	Question         string                 `json:"question,omitempty"`
	DataSources      []LocalObjectReference `json:"dataSources,omitempty"`
	Queries          []InvestigationQuery   `json:"queries,omitempty"`
	ModelProviderRef LocalObjectReference   `json:"modelProviderRef,omitempty"`
	Mode             string                 `json:"mode,omitempty"`
	CreateRiskSignal bool                   `json:"createRiskSignal,omitempty"`
	TTLSeconds       int64                  `json:"ttlSeconds,omitempty"`
}

type RCAVerdict struct {
	Outcome          string         `json:"outcome,omitempty"`
	Summary          string         `json:"summary,omitempty"`
	RootCauseEntity  TargetRef      `json:"rootCauseEntity,omitempty"`
	RootCauseType    string         `json:"rootCauseType,omitempty"`
	Confidence       float64        `json:"confidence,omitempty"`
	ConfidenceDetail *RCAConfidence `json:"confidenceDetail,omitempty"`
}

type RCAConfidence struct {
	ProviderScore float64 `json:"providerScore,omitempty"`
	VerifiedScore float64 `json:"verifiedScore,omitempty"`
	Level         string  `json:"level,omitempty"`
	Method        string  `json:"method,omitempty"`
}

type RCAClaim struct {
	ID           string   `json:"id,omitempty"`
	Statement    string   `json:"statement,omitempty"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
	Verification string   `json:"verification,omitempty"`
}

type RCAAlternativeHypothesis struct {
	Statement    string   `json:"statement,omitempty"`
	Disposition  string   `json:"disposition,omitempty"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type RCAMissingEvidence struct {
	Source string `json:"source,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type InvestigationFailure struct {
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	Stage     string `json:"stage,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

type RCADegradationReason struct {
	Code      string                     `json:"code,omitempty"`
	Stage     string                     `json:"stage,omitempty"`
	SourceRef *NamespacedObjectReference `json:"sourceRef,omitempty"`
	Message   string                     `json:"message,omitempty"`
}

type RCADegradation struct {
	Partial            bool                   `json:"partial,omitempty"`
	UnavailableSources []string               `json:"unavailableSources,omitempty"`
	Reasons            []RCADegradationReason `json:"reasons,omitempty"`
}

type RCAExecutionAttempt struct {
	ID                string       `json:"id,omitempty"`
	ProviderRequestID string       `json:"providerRequestID,omitempty"`
	IdempotencyKey    string       `json:"idempotencyKey,omitempty"`
	RetryReason       string       `json:"retryReason,omitempty"`
	Result            string       `json:"result,omitempty"`
	StartedAt         *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt       *metav1.Time `json:"completedAt,omitempty"`
}

type RCAExecution struct {
	ID                      string                     `json:"id,omitempty"`
	State                   string                     `json:"state,omitempty"`
	Provider                string                     `json:"provider,omitempty"`
	ProviderRef             *NamespacedObjectReference `json:"providerRef,omitempty"`
	ProviderGeneration      int64                      `json:"providerGeneration,omitempty"`
	ProviderType            string                     `json:"providerType,omitempty"`
	Model                   string                     `json:"model,omitempty"`
	RCASchemaVersion        string                     `json:"rcaSchemaVersion,omitempty"`
	CanonicalizationVersion string                     `json:"canonicalizationVersion,omitempty"`
	ReasoningPolicyVersion  string                     `json:"reasoningPolicyVersion,omitempty"`
	ControllerVersion       string                     `json:"controllerVersion,omitempty"`
	AttemptCount            int32                      `json:"attemptCount,omitempty"`
	Attempts                []RCAExecutionAttempt      `json:"attempts,omitempty"`
	DurationSeconds         int64                      `json:"durationSeconds,omitempty"`
	InputTokens             int64                      `json:"inputTokens,omitempty"`
	OutputTokens            int64                      `json:"outputTokens,omitempty"`
}

type InvestigationLineageSource struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
	UID        string `json:"uid,omitempty"`
	Generation int64  `json:"generation,omitempty"`
}

type InvestigationLineage struct {
	Source             InvestigationLineageSource `json:"source,omitempty"`
	TargetUID          string                     `json:"targetUID,omitempty"`
	FindingFingerprint string                     `json:"findingFingerprint,omitempty"`
	InvestigationDepth int32                      `json:"investigationDepth,omitempty"`
}

type InvestigationRequestStatus struct {
	ResourceStatus        `json:",inline"`
	Outcome               string                     `json:"outcome,omitempty"`
	Failure               *InvestigationFailure      `json:"failure,omitempty"`
	Summary               string                     `json:"summary,omitempty"`
	Hypothesis            string                     `json:"hypothesis,omitempty"`
	Confidence            float64                    `json:"confidence,omitempty"`
	Provider              string                     `json:"provider,omitempty"`
	Verdict               *RCAVerdict                `json:"verdict,omitempty"`
	Claims                []RCAClaim                 `json:"claims,omitempty"`
	AlternativeHypotheses []RCAAlternativeHypothesis `json:"alternativeHypotheses,omitempty"`
	MissingEvidence       []RCAMissingEvidence       `json:"missingEvidence,omitempty"`
	Degradation           *RCADegradation            `json:"degradation,omitempty"`
	Execution             *RCAExecution              `json:"execution,omitempty"`
	StartedAt             *metav1.Time               `json:"startedAt,omitempty"`
	CompletedAt           *metav1.Time               `json:"completedAt,omitempty"`
	EvidenceRefs          []EvidenceRef              `json:"evidenceRefs,omitempty"`
	Lineage               *InvestigationLineage      `json:"lineage,omitempty"`
	LinkedRiskSignalRef   *NamespacedObjectReference `json:"linkedRiskSignalRef,omitempty"`
	Conditions            []metav1.Condition         `json:"conditions,omitempty"`
}

type InvestigationRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InvestigationRequestSpec   `json:"spec,omitempty"`
	Status InvestigationRequestStatus `json:"status,omitempty"`
}

type InvestigationRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InvestigationRequest `json:"items"`
}

func (in *InvestigationRequest) DeepCopyInto(out *InvestigationRequest) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.DataSources != nil {
		out.Spec.DataSources = append([]LocalObjectReference(nil), in.Spec.DataSources...)
	}
	if in.Spec.Queries != nil {
		out.Spec.Queries = make([]InvestigationQuery, len(in.Spec.Queries))
		copy(out.Spec.Queries, in.Spec.Queries)
		for i := range out.Spec.Queries {
			if in.Spec.Queries[i].Reasons != nil {
				out.Spec.Queries[i].Reasons = append([]string(nil), in.Spec.Queries[i].Reasons...)
			}
		}
	}
	if in.Status.StartedAt != nil {
		started := in.Status.StartedAt.DeepCopy()
		out.Status.StartedAt = started
	}
	if in.Status.CompletedAt != nil {
		completed := in.Status.CompletedAt.DeepCopy()
		out.Status.CompletedAt = completed
	}
	if in.Status.EvidenceRefs != nil {
		out.Status.EvidenceRefs = deepcopyEvidenceRefs(in.Status.EvidenceRefs)
	}
	if in.Status.Verdict != nil {
		verdict := *in.Status.Verdict
		if in.Status.Verdict.ConfidenceDetail != nil {
			confidence := *in.Status.Verdict.ConfidenceDetail
			verdict.ConfidenceDetail = &confidence
		}
		out.Status.Verdict = &verdict
	}
	if in.Status.Claims != nil {
		out.Status.Claims = make([]RCAClaim, len(in.Status.Claims))
		copy(out.Status.Claims, in.Status.Claims)
		for i := range out.Status.Claims {
			if in.Status.Claims[i].EvidenceRefs != nil {
				out.Status.Claims[i].EvidenceRefs = append([]string(nil), in.Status.Claims[i].EvidenceRefs...)
			}
		}
	}
	if in.Status.AlternativeHypotheses != nil {
		out.Status.AlternativeHypotheses = make([]RCAAlternativeHypothesis, len(in.Status.AlternativeHypotheses))
		copy(out.Status.AlternativeHypotheses, in.Status.AlternativeHypotheses)
		for i := range out.Status.AlternativeHypotheses {
			if in.Status.AlternativeHypotheses[i].EvidenceRefs != nil {
				out.Status.AlternativeHypotheses[i].EvidenceRefs = append([]string(nil), in.Status.AlternativeHypotheses[i].EvidenceRefs...)
			}
		}
	}
	if in.Status.MissingEvidence != nil {
		out.Status.MissingEvidence = append([]RCAMissingEvidence(nil), in.Status.MissingEvidence...)
	}
	if in.Status.Failure != nil {
		failure := *in.Status.Failure
		out.Status.Failure = &failure
	}
	if in.Status.Degradation != nil {
		degradation := *in.Status.Degradation
		if in.Status.Degradation.UnavailableSources != nil {
			degradation.UnavailableSources = append([]string(nil), in.Status.Degradation.UnavailableSources...)
		}
		if in.Status.Degradation.Reasons != nil {
			degradation.Reasons = make([]RCADegradationReason, len(in.Status.Degradation.Reasons))
			copy(degradation.Reasons, in.Status.Degradation.Reasons)
			for i := range degradation.Reasons {
				if in.Status.Degradation.Reasons[i].SourceRef != nil {
					ref := *in.Status.Degradation.Reasons[i].SourceRef
					degradation.Reasons[i].SourceRef = &ref
				}
			}
		}
		out.Status.Degradation = &degradation
	}
	if in.Status.Execution != nil {
		execution := *in.Status.Execution
		if in.Status.Execution.ProviderRef != nil {
			ref := *in.Status.Execution.ProviderRef
			execution.ProviderRef = &ref
		}
		if in.Status.Execution.Attempts != nil {
			execution.Attempts = make([]RCAExecutionAttempt, len(in.Status.Execution.Attempts))
			copy(execution.Attempts, in.Status.Execution.Attempts)
			for i := range execution.Attempts {
				if in.Status.Execution.Attempts[i].StartedAt != nil {
					execution.Attempts[i].StartedAt = in.Status.Execution.Attempts[i].StartedAt.DeepCopy()
				}
				if in.Status.Execution.Attempts[i].CompletedAt != nil {
					execution.Attempts[i].CompletedAt = in.Status.Execution.Attempts[i].CompletedAt.DeepCopy()
				}
			}
		}
		out.Status.Execution = &execution
	}
	if in.Status.Lineage != nil {
		lineage := *in.Status.Lineage
		out.Status.Lineage = &lineage
	}
	if in.Status.LinkedRiskSignalRef != nil {
		ref := *in.Status.LinkedRiskSignalRef
		out.Status.LinkedRiskSignalRef = &ref
	}
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		copy(out.Status.Conditions, in.Status.Conditions)
	}
}

func (in *InvestigationRequest) DeepCopy() *InvestigationRequest {
	if in == nil {
		return nil
	}
	out := new(InvestigationRequest)
	in.DeepCopyInto(out)
	return out
}

func (in *InvestigationRequest) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *InvestigationRequestList) DeepCopyInto(out *InvestigationRequestList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]InvestigationRequest, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *InvestigationRequestList) DeepCopy() *InvestigationRequestList {
	if in == nil {
		return nil
	}
	out := new(InvestigationRequestList)
	in.DeepCopyInto(out)
	return out
}

func (in *InvestigationRequestList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
