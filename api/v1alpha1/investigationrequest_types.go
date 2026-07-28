package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	InvestigationModeReadOnly = "readOnly"
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
	Summary         string    `json:"summary,omitempty"`
	RootCauseEntity TargetRef `json:"rootCauseEntity,omitempty"`
	RootCauseType   string    `json:"rootCauseType,omitempty"`
	Confidence      float64   `json:"confidence,omitempty"`
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

type RCADegradation struct {
	Partial            bool     `json:"partial,omitempty"`
	UnavailableSources []string `json:"unavailableSources,omitempty"`
}

type RCAExecution struct {
	Provider        string `json:"provider,omitempty"`
	Attempts        int32  `json:"attempts,omitempty"`
	DurationSeconds int64  `json:"durationSeconds,omitempty"`
	InputTokens     int64  `json:"inputTokens,omitempty"`
	OutputTokens    int64  `json:"outputTokens,omitempty"`
}

type InvestigationRequestStatus struct {
	ResourceStatus        `json:",inline"`
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
	if in.Status.Degradation != nil {
		degradation := *in.Status.Degradation
		if in.Status.Degradation.UnavailableSources != nil {
			degradation.UnavailableSources = append([]string(nil), in.Status.Degradation.UnavailableSources...)
		}
		out.Status.Degradation = &degradation
	}
	if in.Status.Execution != nil {
		execution := *in.Status.Execution
		out.Status.Execution = &execution
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
