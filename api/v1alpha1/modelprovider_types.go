package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type ModelProviderSpec struct {
	Provider            string                  `json:"provider"`
	Model               string                  `json:"model,omitempty"`
	Endpoint            string                  `json:"endpoint,omitempty"`
	APIKeySecretRef     *SecretKeyRef           `json:"apiKeySecretRef,omitempty"`
	Timeout             metav1.Duration         `json:"timeout,omitempty"`
	MaxTokens           int                     `json:"maxTokens,omitempty"`
	DataPolicy          ModelProviderDataPolicy `json:"dataPolicy,omitempty"`
	FallbackProviderRef LocalObjectReference    `json:"fallbackProviderRef,omitempty"`
}

type ModelProviderDataPolicy struct {
	AllowExternalTransmission bool     `json:"allowExternalTransmission,omitempty"`
	AllowedEvidenceKinds      []string `json:"allowedEvidenceKinds,omitempty"`
	DeniedSensitivityTags     []string `json:"deniedSensitivityTags,omitempty"`
	AllowLogSamples           bool     `json:"allowLogSamples,omitempty"`
	RequireRedaction          bool     `json:"requireRedaction,omitempty"`
	MaximumClassification     string   `json:"maximumClassification,omitempty"`
}

type ModelProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelProviderSpec `json:"spec,omitempty"`
	Status ResourceStatus    `json:"status,omitempty"`
}

type ModelProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelProvider `json:"items"`
}

func (in *ModelProvider) DeepCopyInto(out *ModelProvider) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.APIKeySecretRef != nil {
		ref := *in.Spec.APIKeySecretRef
		out.Spec.APIKeySecretRef = &ref
	}
	if in.Spec.DataPolicy.AllowedEvidenceKinds != nil {
		out.Spec.DataPolicy.AllowedEvidenceKinds = append([]string(nil), in.Spec.DataPolicy.AllowedEvidenceKinds...)
	}
	if in.Spec.DataPolicy.DeniedSensitivityTags != nil {
		out.Spec.DataPolicy.DeniedSensitivityTags = append([]string(nil), in.Spec.DataPolicy.DeniedSensitivityTags...)
	}
}

func (in *ModelProvider) DeepCopy() *ModelProvider {
	if in == nil {
		return nil
	}
	out := new(ModelProvider)
	in.DeepCopyInto(out)
	return out
}

func (in *ModelProvider) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *ModelProviderList) DeepCopyInto(out *ModelProviderList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]ModelProvider, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *ModelProviderList) DeepCopy() *ModelProviderList {
	if in == nil {
		return nil
	}
	out := new(ModelProviderList)
	in.DeepCopyInto(out)
	return out
}

func (in *ModelProviderList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
