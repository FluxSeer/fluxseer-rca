package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	ProtectionLevelStandard   = "standard"
	ProtectionLevelStrict     = "strict"
	ProtectionLevelPermissive = "permissive"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:categories=fluxseer;policies
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Protection Level",type=string,JSONPath=`.spec.protectionLevel`
// +kubebuilder:printcolumn:name="Priority",type=integer,JSONPath=`.spec.priority`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

type NamespaceThreshold struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NamespaceThresholdSpec   `json:"spec,omitempty"`
	Status NamespaceThresholdStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type NamespaceThresholdList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NamespaceThreshold `json:"items"`
}

// NamespaceThresholdSpec defines namespace-level resource limits.
type NamespaceThresholdSpec struct {
	// NamespaceSelector label selector for applicable namespaces.
	// Omitted selector applies to the threshold's own namespace only (safe default).
	// Explicit empty selector {} applies to all namespaces.
	// +kubebuilder:validation:Optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// ActivePlansLimit maximum number of active RemediationPlans per namespace.
	// 0 means unlimited.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=100
	ActivePlansLimit int32 `json:"activePlansLimit,omitempty"`

	// PendingApprovalsLimit maximum number of pending approval actions.
	// 0 means unlimited.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=50
	PendingApprovalsLimit int32 `json:"pendingApprovalsLimit,omitempty"`

	// DefaultTTLSeconds default TTL in seconds for resources in this namespace.
	// 0 means no default.
	// +kubebuilder:validation:Minimum=0
	DefaultTTLSeconds int64 `json:"defaultTTLSeconds,omitempty"`

	// DefaultApprovalTimeoutSeconds default approval timeout in seconds.
	// 0 means no default.
	// +kubebuilder:validation:Minimum=0
	DefaultApprovalTimeoutSeconds int64 `json:"defaultApprovalTimeoutSeconds,omitempty"`

	// ProtectionLevel protection level: standard/strict/permissive.
	// standard: default behavior; strict: stricter validation; permissive: relaxed policies.
	// +kubebuilder:validation:Enum=standard;strict;permissive
	// +kubebuilder:default=standard
	ProtectionLevel string `json:"protectionLevel,omitempty"`

	// Priority priority level; higher values take precedence.
	// Resolves conflicts when multiple NamespaceThresholds match the same namespace.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Priority int32 `json:"priority,omitempty"`

	// Enabled indicates if this threshold configuration is enabled.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// NamespaceThresholdStatus represents the status of a NamespaceThreshold.
type NamespaceThresholdStatus struct {
	// Phase configuration status: Pending/Valid/Invalid/Disabled.
	// +kubebuilder:validation:Enum=Pending;Valid;Invalid;Disabled
	// +kubebuilder:default=Pending
	Phase string `json:"phase,omitempty"`

	// EnforcedNamespaces list of namespaces where this threshold is being enforced.
	// +kubebuilder:validation:Optional
	EnforcedNamespaces []string `json:"enforcedNamespaces,omitempty"`

	// Conditions detailed status conditions.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// UpdatedAt last update time.
	// +kubebuilder:validation:Optional
	UpdatedAt *metav1.Time `json:"updatedAt,omitempty"`

	// ValidationErrors validation error messages.
	// +kubebuilder:validation:Optional
	ValidationErrors []string `json:"validationErrors,omitempty"`

	// LastAppliedGeneration last successfully applied generation.
	// +kubebuilder:validation:Optional
	LastAppliedGeneration int64 `json:"lastAppliedGeneration,omitempty"`

	// TotalEnforcedCount total number of namespaces currently being enforced.
	// +kubebuilder:validation:Minimum=0
	TotalEnforcedCount int32 `json:"totalEnforcedCount,omitempty"`
}

// DeepCopyInto deep copies NamespaceThreshold.
func (in *NamespaceThreshold) DeepCopyInto(out *NamespaceThreshold) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a copy of NamespaceThreshold.
func (in *NamespaceThreshold) DeepCopy() *NamespaceThreshold {
	if in == nil {
		return nil
	}
	out := new(NamespaceThreshold)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object interface.
func (in *NamespaceThreshold) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto deep copies NamespaceThresholdList.
func (in *NamespaceThresholdList) DeepCopyInto(out *NamespaceThresholdList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]NamespaceThreshold, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy creates a copy of NamespaceThresholdList.
func (in *NamespaceThresholdList) DeepCopy() *NamespaceThresholdList {
	if in == nil {
		return nil
	}
	out := new(NamespaceThresholdList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object interface.
func (in *NamespaceThresholdList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto deep copies NamespaceThresholdSpec.
func (in *NamespaceThresholdSpec) DeepCopyInto(out *NamespaceThresholdSpec) {
	*out = *in
	if in.NamespaceSelector != nil {
		out.NamespaceSelector = in.NamespaceSelector.DeepCopy()
	}
}

// DeepCopy creates a copy of NamespaceThresholdSpec.
func (in *NamespaceThresholdSpec) DeepCopy() *NamespaceThresholdSpec {
	if in == nil {
		return nil
	}
	out := new(NamespaceThresholdSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto deep copies NamespaceThresholdStatus.
func (in *NamespaceThresholdStatus) DeepCopyInto(out *NamespaceThresholdStatus) {
	*out = *in
	if in.EnforcedNamespaces != nil {
		out.EnforcedNamespaces = make([]string, len(in.EnforcedNamespaces))
		copy(out.EnforcedNamespaces, in.EnforcedNamespaces)
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		copy(out.Conditions, in.Conditions)
	}
	if in.UpdatedAt != nil {
		out.UpdatedAt = in.UpdatedAt.DeepCopy()
	}
	if in.ValidationErrors != nil {
		out.ValidationErrors = make([]string, len(in.ValidationErrors))
		copy(out.ValidationErrors, in.ValidationErrors)
	}
}

// DeepCopy creates a copy of NamespaceThresholdStatus.
func (in *NamespaceThresholdStatus) DeepCopy() *NamespaceThresholdStatus {
	if in == nil {
		return nil
	}
	out := new(NamespaceThresholdStatus)
	in.DeepCopyInto(out)
	return out
}
