package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	AssigneeTypeUser = "user"
	AssigneeTypeTeam = "team"
	AssigneeTypeRole = "role"
)

const (
	EscalationConditionTypePendingTime   = "pending_time"
	EscalationConditionTypeApprovalCount = "approval_count"
	EscalationConditionTypeSeverity      = "severity"
	EscalationConditionTypeManual        = "manual"
)

const (
	EscalationActionTypeNotify     = "notify"
	EscalationActionTypeReassign   = "reassign"
	EscalationActionTypeAutoReject = "auto_reject"
	EscalationActionTypeForceExecute = "force_execute"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:categories=fluxseer;policies
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Priority",type=integer,JSONPath=`.spec.priority`
// +kubebuilder:printcolumn:name="Stages",type=integer,JSONPath=`.spec.stages | length`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

type EscalationChain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EscalationChainSpec   `json:"spec,omitempty"`
	Status EscalationChainStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type EscalationChainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EscalationChain `json:"items"`
}

// EscalationChainSpec defines multi-stage escalation routing configuration.
type EscalationChainSpec struct {
	// ResourceSelector label selector for resources to which this chain applies.
	// Empty selector applies to all resources.
	// +kubebuilder:validation:Optional
	ResourceSelector *metav1.LabelSelector `json:"resourceSelector,omitempty"`

	// Priority priority level; higher values take precedence.
	// Resolves conflicts when multiple chains match the same resource.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Priority int32 `json:"priority,omitempty"`

	// Stages multi-stage escalation definitions.
	// Executed in order; subsequent stages run only if their conditions are met.
	// +kubebuilder:validation:MinItems=1
	Stages []EscalationStage `json:"stages"`

	// Enabled indicates if this escalation chain is enabled.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// EscalationStage represents a single escalation stage.
type EscalationStage struct {
	// Name stage name (e.g., "L1-OnCall", "L2-Manager", "L3-CTO").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// Delay delay in seconds before this stage is triggered from the previous stage.
	// For the first stage, delay is measured from entering pending approval state.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Delay int64 `json:"delay,omitempty"`

	// Condition triggers this stage.
	// +kubebuilder:validation:Optional
	Condition *EscalationCondition `json:"condition,omitempty"`

	// Actions escalation actions executed in this stage.
	// +kubebuilder:validation:Optional
	Actions []EscalationAction `json:"actions,omitempty"`

	// Assignees assignees for this stage (who receives notifications/assignments).
	// +kubebuilder:validation:Optional
	Assignees []Assignee `json:"assignees,omitempty"`

	// NotificationTemplate custom notification template name.
	// +kubebuilder:validation:Optional
	NotificationTemplate string `json:"notificationTemplate,omitempty"`

	// Timeout timeout for this stage in seconds.
	// 0 means use default timeout.
	// +kubebuilder:validation:Minimum=0
	Timeout int64 `json:"timeout,omitempty"`
}

// EscalationCondition trigger condition for escalation stage.
type EscalationCondition struct {
	// Type condition type: pending_time/approval_count/severity/manual.
	// +kubebuilder:validation:Enum=pending_time;approval_count;severity;manual
	Type string `json:"type"`

	// Threshold threshold value.
	// - pending_time: seconds of pending approval
	// - approval_count: number of rejections or no-responses
	// +kubebuilder:validation:Minimum=0
	Threshold int64 `json:"threshold,omitempty"`

	// SeverityMin minimum severity level (inclusive).
	// +kubebuilder:validation:Enum=Critical;High;Medium;Low;Info
	// +kubebuilder:validation:Optional
	SeverityMin string `json:"severityMin,omitempty"`

	// SeverityMax maximum severity level (inclusive).
	// +kubebuilder:validation:Enum=Critical;High;Medium;Low;Info
	// +kubebuilder:validation:Optional
	SeverityMax string `json:"severityMax,omitempty"`
}

// EscalationAction escalation action executed in a stage.
type EscalationAction struct {
	// Type action type: notify/reassign/auto_reject/force_execute.
	// +kubebuilder:validation:Enum=notify;reassign;auto_reject;force_execute
	Type string `json:"type"`

	// Details action details configuration (JSON map).
	// E.g., notify can specify notification channel, reassign can specify new approver.
	// +kubebuilder:validation:Optional
	Details map[string]string `json:"details,omitempty"`

	// Enabled indicates if this action is enabled.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// Assignee stage assignee.
type Assignee struct {
	// Type assignee type: user/team/role.
	// +kubebuilder:validation:Enum=user;team;role
	Type string `json:"type"`

	// Name user/team/role name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Channels notification channels (e.g., slack, email, pagerduty).
	// Empty list means use default notification channels.
	// +kubebuilder:validation:Optional
	Channels []string `json:"channels,omitempty"`

	// Enabled indicates if this assignee is enabled.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// EscalationChainStatus represents the status of an EscalationChain.
type EscalationChainStatus struct {
	// Phase chain status: Pending/Valid/Invalid/Disabled.
	// +kubebuilder:validation:Enum=Pending;Valid;Invalid;Disabled
	// +kubebuilder:default=Pending
	Phase string `json:"phase,omitempty"`

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

	// TotalStages total number of stages in this chain.
	// +kubebuilder:validation:Minimum=0
	TotalStages int32 `json:"totalStages,omitempty"`
}

// DeepCopyInto deep copies EscalationChain.
func (in *EscalationChain) DeepCopyInto(out *EscalationChain) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a copy of EscalationChain.
func (in *EscalationChain) DeepCopy() *EscalationChain {
	if in == nil {
		return nil
	}
	out := new(EscalationChain)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object interface.
func (in *EscalationChain) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto deep copies EscalationChainList.
func (in *EscalationChainList) DeepCopyInto(out *EscalationChainList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]EscalationChain, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy creates a copy of EscalationChainList.
func (in *EscalationChainList) DeepCopy() *EscalationChainList {
	if in == nil {
		return nil
	}
	out := new(EscalationChainList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object interface.
func (in *EscalationChainList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto deep copies EscalationChainSpec.
func (in *EscalationChainSpec) DeepCopyInto(out *EscalationChainSpec) {
	*out = *in
	if in.ResourceSelector != nil {
		out.ResourceSelector = in.ResourceSelector.DeepCopy()
	}
	if in.Stages != nil {
		out.Stages = make([]EscalationStage, len(in.Stages))
		for i := range in.Stages {
			in.Stages[i].DeepCopyInto(&out.Stages[i])
		}
	}
}

// DeepCopy creates a copy of EscalationChainSpec.
func (in *EscalationChainSpec) DeepCopy() *EscalationChainSpec {
	if in == nil {
		return nil
	}
	out := new(EscalationChainSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto deep copies EscalationStage.
func (in *EscalationStage) DeepCopyInto(out *EscalationStage) {
	*out = *in
	if in.Condition != nil {
		out.Condition = in.Condition.DeepCopy()
	}
	if in.Actions != nil {
		out.Actions = make([]EscalationAction, len(in.Actions))
		for i := range in.Actions {
			in.Actions[i].DeepCopyInto(&out.Actions[i])
		}
	}
	if in.Assignees != nil {
		out.Assignees = make([]Assignee, len(in.Assignees))
		for i := range in.Assignees {
			in.Assignees[i].DeepCopyInto(&out.Assignees[i])
		}
	}
}

// DeepCopy creates a copy of EscalationStage.
func (in *EscalationStage) DeepCopy() *EscalationStage {
	if in == nil {
		return nil
	}
	out := new(EscalationStage)
	in.DeepCopyInto(out)
	return out
}

// DeepCopy creates a copy of EscalationCondition.
func (in *EscalationCondition) DeepCopy() *EscalationCondition {
	if in == nil {
		return nil
	}
	out := new(EscalationCondition)
	*out = *in
	return out
}

// DeepCopyInto deep copies EscalationAction.
func (in *EscalationAction) DeepCopyInto(out *EscalationAction) {
	*out = *in
	if in.Details != nil {
		out.Details = make(map[string]string, len(in.Details))
		for key, value := range in.Details {
			out.Details[key] = value
		}
	}
}

// DeepCopy creates a copy of EscalationAction.
func (in *EscalationAction) DeepCopy() *EscalationAction {
	if in == nil {
		return nil
	}
	out := new(EscalationAction)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto deep copies Assignee.
func (in *Assignee) DeepCopyInto(out *Assignee) {
	*out = *in
	if in.Channels != nil {
		out.Channels = make([]string, len(in.Channels))
		copy(out.Channels, in.Channels)
	}
}

// DeepCopy creates a copy of Assignee.
func (in *Assignee) DeepCopy() *Assignee {
	if in == nil {
		return nil
	}
	out := new(Assignee)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto deep copies EscalationChainStatus.
func (in *EscalationChainStatus) DeepCopyInto(out *EscalationChainStatus) {
	*out = *in
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

// DeepCopy creates a copy of EscalationChainStatus.
func (in *EscalationChainStatus) DeepCopy() *EscalationChainStatus {
	if in == nil {
		return nil
	}
	out := new(EscalationChainStatus)
	in.DeepCopyInto(out)
	return out
}
