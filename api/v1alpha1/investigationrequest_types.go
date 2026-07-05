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

type InvestigationRequestSpec struct {
	Target           TargetRef              `json:"target"`
	TimeRange        InvestigationTimeRange `json:"timeRange,omitempty"`
	Question         string                 `json:"question,omitempty"`
	DataSources      []LocalObjectReference `json:"dataSources,omitempty"`
	ModelProviderRef LocalObjectReference   `json:"modelProviderRef,omitempty"`
	Mode             string                 `json:"mode,omitempty"`
	CreateRiskSignal bool                   `json:"createRiskSignal,omitempty"`
}

type InvestigationRequestStatus struct {
	ResourceStatus      `json:",inline"`
	Summary             string                     `json:"summary,omitempty"`
	Hypothesis          string                     `json:"hypothesis,omitempty"`
	Confidence          float64                    `json:"confidence,omitempty"`
	Provider            string                     `json:"provider,omitempty"`
	StartedAt           *metav1.Time               `json:"startedAt,omitempty"`
	CompletedAt         *metav1.Time               `json:"completedAt,omitempty"`
	EvidenceRefs        []EvidenceRef              `json:"evidenceRefs,omitempty"`
	LinkedRiskSignalRef *NamespacedObjectReference `json:"linkedRiskSignalRef,omitempty"`
	Conditions          []metav1.Condition         `json:"conditions,omitempty"`
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
	if in.Status.StartedAt != nil {
		started := in.Status.StartedAt.DeepCopy()
		out.Status.StartedAt = started
	}
	if in.Status.CompletedAt != nil {
		completed := in.Status.CompletedAt.DeepCopy()
		out.Status.CompletedAt = completed
	}
	if in.Status.EvidenceRefs != nil {
		out.Status.EvidenceRefs = append([]EvidenceRef(nil), in.Status.EvidenceRefs...)
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
