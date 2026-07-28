package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type DataSourceAuthSpec struct {
	Type      string        `json:"type,omitempty"`
	SecretRef *SecretKeyRef `json:"secretRef,omitempty"`
}

type DataSourceTLSSpec struct {
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

type DataSourceNetworkPolicy struct {
	AllowedHosts []string `json:"allowedHosts,omitempty"`
	AllowedCIDRs []string `json:"allowedCIDRs,omitempty"`
	DeniedCIDRs  []string `json:"deniedCIDRs,omitempty"`
}

type DataSourceSpec struct {
	Type          string                  `json:"type"`
	Endpoint      string                  `json:"endpoint,omitempty"`
	Timeout       metav1.Duration         `json:"timeout,omitempty"`
	NetworkPolicy DataSourceNetworkPolicy `json:"networkPolicy,omitempty"`
	Auth          *DataSourceAuthSpec     `json:"auth,omitempty"`
	TLS           *DataSourceTLSSpec      `json:"tls,omitempty"`
}

type DataSourceStatus struct {
	ResourceStatus `json:",inline"`
	Conditions     []metav1.Condition `json:"conditions,omitempty"`
}

type DataSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DataSourceSpec   `json:"spec,omitempty"`
	Status DataSourceStatus `json:"status,omitempty"`
}

type DataSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DataSource `json:"items"`
}

func (in *DataSource) DeepCopyInto(out *DataSource) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.Auth != nil {
		out.Spec.Auth = &DataSourceAuthSpec{Type: in.Spec.Auth.Type}
		if in.Spec.Auth.SecretRef != nil {
			ref := *in.Spec.Auth.SecretRef
			out.Spec.Auth.SecretRef = &ref
		}
	}
	if in.Spec.TLS != nil {
		tls := *in.Spec.TLS
		out.Spec.TLS = &tls
	}
	if in.Spec.NetworkPolicy.AllowedHosts != nil {
		out.Spec.NetworkPolicy.AllowedHosts = append([]string(nil), in.Spec.NetworkPolicy.AllowedHosts...)
	}
	if in.Spec.NetworkPolicy.AllowedCIDRs != nil {
		out.Spec.NetworkPolicy.AllowedCIDRs = append([]string(nil), in.Spec.NetworkPolicy.AllowedCIDRs...)
	}
	if in.Spec.NetworkPolicy.DeniedCIDRs != nil {
		out.Spec.NetworkPolicy.DeniedCIDRs = append([]string(nil), in.Spec.NetworkPolicy.DeniedCIDRs...)
	}
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		copy(out.Status.Conditions, in.Status.Conditions)
	}
}

func (in *DataSource) DeepCopy() *DataSource {
	if in == nil {
		return nil
	}
	out := new(DataSource)
	in.DeepCopyInto(out)
	return out
}

func (in *DataSource) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *DataSourceList) DeepCopyInto(out *DataSourceList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]DataSource, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *DataSourceList) DeepCopy() *DataSourceList {
	if in == nil {
		return nil
	}
	out := new(DataSourceList)
	in.DeepCopyInto(out)
	return out
}

func (in *DataSourceList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
