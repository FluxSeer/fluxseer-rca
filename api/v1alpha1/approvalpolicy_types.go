package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	ApprovalActionAuto   = "auto"
	ApprovalActionManual = "manual"
	ApprovalActionReject = "reject"

	SeverityCritical = "Critical"
	SeverityHigh     = "High"
	SeverityMedium   = "Medium"
	SeverityLow      = "Low"
	SeverityInfo     = "Info"
)

const (
	EscalationTriggerTimeout       = "timeout"
	EscalationTriggerApprovalCount = "approval_count"
	EscalationTriggerManual        = "manual"
)

const (
	NotificationChannelTypeWebhook   = "webhook"
	NotificationChannelTypeSlack     = "slack"
	NotificationChannelTypeEmail     = "email"
	NotificationChannelTypePagerDuty = "pagerduty"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:categories=fluxseer;policies
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Priority",type=integer,JSONPath=`.spec.priority`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

type ApprovalPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApprovalPolicySpec   `json:"spec,omitempty"`
	Status ApprovalPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ApprovalPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApprovalPolicy `json:"items"`
}

// ApprovalPolicySpec defines approval decision rules.
type ApprovalPolicySpec struct {
	// Scope specifies policy scope: "Cluster" or empty for Namespace-scoped.
	// +kubebuilder:validation:Enum=Cluster;""
	// +kubebuilder:default=""
	Scope string `json:"scope,omitempty"`

	// ResourceSelector matches RemediationPlan/AgentAction via label selector.
	// +kubebuilder:validation:Optional
	ResourceSelector *metav1.LabelSelector `json:"resourceSelector,omitempty"`

	// NamespaceSelector matches target namespaces via label selector.
	// +kubebuilder:validation:Optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// ActionTypeRules approval rules per action type.
	// +kubebuilder:validation:Optional
	ActionTypeRules []ActionTypeRule `json:"actionTypeRules,omitempty"`

	// SeverityRules approval rules per severity level.
	// +kubebuilder:validation:Optional
	SeverityRules []SeverityRule `json:"severityRules,omitempty"`

	// DefaultApprovalTimeout default approval timeout in seconds.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=3600
	DefaultApprovalTimeout int64 `json:"defaultApprovalTimeout,omitempty"`

	// Escalation escalation routing configuration.
	// +kubebuilder:validation:Optional
	Escalation *EscalationConfig `json:"escalation,omitempty"`

	// Priority priority level; higher values take precedence.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Priority int32 `json:"priority,omitempty"`

	// Enabled indicates if this policy is enabled.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// ActionTypeRule approval rule for a specific action type.
type ActionTypeRule struct {
	// ActionType action type (e.g., "Restart", "Rollback", "ScaleUp").
	// +kubebuilder:validation:MinLength=1
	ActionType string `json:"actionType"`

	// Action approval decision: auto/manual/reject.
	// +kubebuilder:validation:Enum=auto;manual;reject
	// +kubebuilder:default=manual
	Action string `json:"action,omitempty"`

	// Reason decision rationale for audit trail.
	Reason string `json:"reason,omitempty"`
}

// SeverityRule approval rule per severity level.
type SeverityRule struct {
	// MinSeverity minimum severity level (inclusive).
	// +kubebuilder:validation:Enum=Critical;High;Medium;Low;Info
	MinSeverity string `json:"minSeverity"`

	// MaxSeverity maximum severity level (inclusive).
	// +kubebuilder:validation:Enum=Critical;High;Medium;Low;Info
	MaxSeverity string `json:"maxSeverity"`

	// Action approval decision: auto/manual/reject.
	// +kubebuilder:validation:Enum=auto;manual;reject
	// +kubebuilder:default=manual
	Action string `json:"action,omitempty"`

	// Reason decision rationale.
	Reason string `json:"reason,omitempty"`

	// TimeoutSec approval timeout in seconds for this rule; overrides DefaultApprovalTimeout.
	// +kubebuilder:validation:Minimum=0
	TimeoutSec int64 `json:"timeoutSec,omitempty"`
}

// EscalationConfig escalation routing configuration.
type EscalationConfig struct {
	// EscalationChainRef reference to EscalationChain resource name (same namespace).
	// +kubebuilder:validation:Optional
	EscalationChainRef string `json:"escalationChainRef,omitempty"`

	// Rules escalation rules; used if chainRef is not specified.
	// +kubebuilder:validation:Optional
	Rules []EscalationRule `json:"rules,omitempty"`

	// NotificationChannels notification channel configuration.
	// +kubebuilder:validation:Optional
	NotificationChannels []NotificationChannel `json:"notificationChannels,omitempty"`
}

// EscalationRule escalation rule.
type EscalationRule struct {
	// Trigger trigger condition: timeout/approval_count/manual.
	// +kubebuilder:validation:Enum=timeout;approval_count;manual
	Trigger string `json:"trigger"`

	// Condition condition threshold (e.g., timeout seconds or approval count).
	// +kubebuilder:validation:Minimum=0
	Condition int64 `json:"condition,omitempty"`

	// Action escalation action to execute (e.g., "notify", "reassign", "auto_reject").
	// +kubebuilder:validation:MinLength=1
	Action string `json:"action"`

	// Targets escalation targets (notification recipients).
	// +kubebuilder:validation:Optional
	Targets []string `json:"targets,omitempty"`

	// NotificationTemplate custom notification template name.
	// +kubebuilder:validation:Optional
	NotificationTemplate string `json:"notificationTemplate,omitempty"`
}

// NotificationChannel notification channel configuration.
type NotificationChannel struct {
	// Type notification channel type: webhook/slack/email/pagerduty.
	// +kubebuilder:validation:Enum=webhook;slack;email;pagerduty
	Type string `json:"type"`

	// URL notification endpoint (webhook URL).
	// +kubebuilder:validation:Optional
	URL string `json:"url,omitempty"`

	// Headers HTTP headers for webhook requests.
	// +kubebuilder:validation:Optional
	Headers map[string]string `json:"headers,omitempty"`

	// SlackTeams Slack team identifiers.
	// +kubebuilder:validation:Optional
	SlackTeams []string `json:"slackTeams,omitempty"`

	// EmailAddresses email addresses.
	// +kubebuilder:validation:Optional
	EmailAddresses []string `json:"emailAddresses,omitempty"`

	// PagerDutyKey PagerDuty integration key.
	// +kubebuilder:validation:Optional
	PagerDutyKey string `json:"pagerDutyKey,omitempty"`

	// Enabled indicates if this notification channel is enabled.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// ApprovalPolicyStatus represents the status of an ApprovalPolicy.
type ApprovalPolicyStatus struct {
	// Phase policy status: Pending/Valid/Invalid/Disabled.
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
}

// DeepCopyInto deep copies ApprovalPolicy.
func (in *ApprovalPolicy) DeepCopyInto(out *ApprovalPolicy) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a copy of ApprovalPolicy.
func (in *ApprovalPolicy) DeepCopy() *ApprovalPolicy {
	if in == nil {
		return nil
	}
	out := new(ApprovalPolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object interface.
func (in *ApprovalPolicy) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto deep copies ApprovalPolicyList.
func (in *ApprovalPolicyList) DeepCopyInto(out *ApprovalPolicyList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]ApprovalPolicy, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy creates a copy of ApprovalPolicyList.
func (in *ApprovalPolicyList) DeepCopy() *ApprovalPolicyList {
	if in == nil {
		return nil
	}
	out := new(ApprovalPolicyList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object interface.
func (in *ApprovalPolicyList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto deep copies ApprovalPolicySpec.
func (in *ApprovalPolicySpec) DeepCopyInto(out *ApprovalPolicySpec) {
	*out = *in
	if in.ResourceSelector != nil {
		out.ResourceSelector = in.ResourceSelector.DeepCopy()
	}
	if in.NamespaceSelector != nil {
		out.NamespaceSelector = in.NamespaceSelector.DeepCopy()
	}
	if in.ActionTypeRules != nil {
		out.ActionTypeRules = make([]ActionTypeRule, len(in.ActionTypeRules))
		copy(out.ActionTypeRules, in.ActionTypeRules)
	}
	if in.SeverityRules != nil {
		out.SeverityRules = make([]SeverityRule, len(in.SeverityRules))
		copy(out.SeverityRules, in.SeverityRules)
	}
	if in.Escalation != nil {
		out.Escalation = in.Escalation.DeepCopy()
	}
}

// DeepCopy creates a copy of ApprovalPolicySpec.
func (in *ApprovalPolicySpec) DeepCopy() *ApprovalPolicySpec {
	if in == nil {
		return nil
	}
	out := new(ApprovalPolicySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto deep copies ApprovalPolicyStatus.
func (in *ApprovalPolicyStatus) DeepCopyInto(out *ApprovalPolicyStatus) {
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

// DeepCopy creates a copy of ApprovalPolicyStatus.
func (in *ApprovalPolicyStatus) DeepCopy() *ApprovalPolicyStatus {
	if in == nil {
		return nil
	}
	out := new(ApprovalPolicyStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopy creates a copy of EscalationConfig.
func (in *EscalationConfig) DeepCopy() *EscalationConfig {
	if in == nil {
		return nil
	}
	out := new(EscalationConfig)
	out.EscalationChainRef = in.EscalationChainRef
	if in.Rules != nil {
		out.Rules = make([]EscalationRule, len(in.Rules))
		copy(out.Rules, in.Rules)
	}
	if in.NotificationChannels != nil {
		out.NotificationChannels = make([]NotificationChannel, len(in.NotificationChannels))
		for i := range in.NotificationChannels {
			in.NotificationChannels[i].DeepCopyInto(&out.NotificationChannels[i])
		}
	}
	return out
}

// DeepCopyInto deep copies NotificationChannel.
func (in *NotificationChannel) DeepCopyInto(out *NotificationChannel) {
	*out = *in
	if in.Headers != nil {
		out.Headers = make(map[string]string, len(in.Headers))
		for key, value := range in.Headers {
			out.Headers[key] = value
		}
	}
	if in.SlackTeams != nil {
		out.SlackTeams = make([]string, len(in.SlackTeams))
		copy(out.SlackTeams, in.SlackTeams)
	}
	if in.EmailAddresses != nil {
		out.EmailAddresses = make([]string, len(in.EmailAddresses))
		copy(out.EmailAddresses, in.EmailAddresses)
	}
}
