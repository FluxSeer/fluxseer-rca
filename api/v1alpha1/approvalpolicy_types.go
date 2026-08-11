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
	NotificationChannelTypeWebhook = "webhook"
	NotificationChannelTypeSlack   = "slack"
	NotificationChannelTypeEmail   = "email"
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

// ApprovalPolicySpec 定義審批決策規則
type ApprovalPolicySpec struct {
	// Scope 指定策略適用範圍: "Cluster" 或空表示 Namespace-scoped
	// +kubebuilder:validation:Enum=Cluster;""
	// +kubebuilder:default=""
	Scope string `json:"scope,omitempty"`

	// ResourceSelector 用標籤選擇器匹配 RemediationPlan/AgentAction
	// +kubebuilder:validation:Optional
	ResourceSelector *metav1.LabelSelector `json:"resourceSelector,omitempty"`

	// NamespaceSelector 匹配目標 namespace 的標籤選擇器
	// +kubebuilder:validation:Optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// ActionTypeRules 針對不同 action type 的審批規則
	// +kubebuilder:validation:Optional
	ActionTypeRules []ActionTypeRule `json:"actionTypeRules,omitempty"`

	// SeverityRules 按 severity 級別的審批規則
	// +kubebuilder:validation:Optional
	SeverityRules []SeverityRule `json:"severityRules,omitempty"`

	// DefaultApprovalTimeout 預設審批超時（秒）
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=3600
	DefaultApprovalTimeout int64 `json:"defaultApprovalTimeout,omitempty"`

	// Escalation escalation 路由規則配置
	// +kubebuilder:validation:Optional
	Escalation *EscalationConfig `json:"escalation,omitempty"`

	// Priority 優先級（數字越大優先級越高）
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Priority int32 `json:"priority,omitempty"`

	// Enabled 是否啟用此 policy
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// ActionTypeRule 針對特定 action type 的審批規則
type ActionTypeRule struct {
	// ActionType action 類型（例如: "Restart", "Rollback", "ScaleUp"）
	// +kubebuilder:validation:MinLength=1
	ActionType string `json:"actionType"`

	// Action 審批決策: auto/manual/reject
	// +kubebuilder:validation:Enum=auto;manual;reject
	// +kubebuilder:default=manual
	Action string `json:"action,omitempty"`

	// Reason 決策理由（用於審計）
	Reason string `json:"reason,omitempty"`
}

// SeverityRule 按 severity 級別的審批規則
type SeverityRule struct {
	// MinSeverity 最低 severity（包含）
	// +kubebuilder:validation:Enum=Critical;High;Medium;Low;Info
	MinSeverity string `json:"minSeverity"`

	// MaxSeverity 最高 severity（包含）
	// +kubebuilder:validation:Enum=Critical;High;Medium;Low;Info
	MaxSeverity string `json:"maxSeverity"`

	// Action 審批決策: auto/manual/reject
	// +kubebuilder:validation:Enum=auto;manual;reject
	// +kubebuilder:default=manual
	Action string `json:"action,omitempty"`

	// Reason 決策理由
	Reason string `json:"reason,omitempty"`

	// TimeoutSec 此規則的審批超時（秒），覆蓋 DefaultApprovalTimeout
	// +kubebuilder:validation:Minimum=0
	TimeoutSec int64 `json:"timeoutSec,omitempty"`
}

// EscalationConfig escalation 路由規則配置
type EscalationConfig struct {
	// EscalationChainRef 參考的 EscalationChain 資源名稱（同一 namespace）
	// +kubebuilder:validation:Optional
	EscalationChainRef string `json:"escalationChainRef,omitempty"`

	// Rules escalation 規則（若未指定 chainRef 則使用）
	// +kubebuilder:validation:Optional
	Rules []EscalationRule `json:"rules,omitempty"`

	// NotificationChannels 通知渠道配置
	// +kubebuilder:validation:Optional
	NotificationChannels []NotificationChannel `json:"notificationChannels,omitempty"`
}

// EscalationRule escalation 規則
type EscalationRule struct {
	// Trigger 觸發條件: timeout/approval_count/manual
	// +kubebuilder:validation:Enum=timeout;approval_count;manual
	Trigger string `json:"trigger"`

	// ConditionThreshold 觸發條件的閾值（例如超時秒數或審批次數）
	// +kubebuilder:validation:Minimum=0
	Condition int64 `json:"condition,omitempty"`

	// Action 執行的 escalation 動作（例如 "notify", "reassign", "auto_reject"）
	// +kubebuilder:validation:MinLength=1
	Action string `json:"action"`

	// Targets escalation 目標（通知的收件人）
	// +kubebuilder:validation:Optional
	Targets []string `json:"targets,omitempty"`

	// NotificationTemplate 自訂通知模板名稱
	// +kubebuilder:validation:Optional
	NotificationTemplate string `json:"notificationTemplate,omitempty"`
}

// NotificationChannel 通知渠道配置
type NotificationChannel struct {
	// Type 通知渠道類型: webhook/slack/email/pagerduty
	// +kubebuilder:validation:Enum=webhook;slack;email;pagerduty
	Type string `json:"type"`

	// URL 通知端點（webhook URL）
	// +kubebuilder:validation:Optional
	URL string `json:"url,omitempty"`

	// Headers HTTP headers（用於 webhook）
	// +kubebuilder:validation:Optional
	Headers map[string]string `json:"headers,omitempty"`

	// SlackTeams Slack teams 標識符
	// +kubebuilder:validation:Optional
	SlackTeams []string `json:"slackTeams,omitempty"`

	// EmailAddresses 電子郵件地址
	// +kubebuilder:validation:Optional
	EmailAddresses []string `json:"emailAddresses,omitempty"`

	// PagerDutyKey PagerDuty integration key
	// +kubebuilder:validation:Optional
	PagerDutyKey string `json:"pagerDutyKey,omitempty"`

	// Enabled 是否啟用此通知渠道
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// ApprovalPolicyStatus 表示 ApprovalPolicy 的狀態
type ApprovalPolicyStatus struct {
	// Phase 策略狀態: Pending/Valid/Invalid/Disabled
	// +kubebuilder:validation:Enum=Pending;Valid;Invalid;Disabled
	// +kubebuilder:default=Pending
	Phase string `json:"phase,omitempty"`

	// Conditions 詳細狀態條件
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// UpdatedAt 上次更新時間
	// +kubebuilder:validation:Optional
	UpdatedAt *metav1.Time `json:"updatedAt,omitempty"`

	// ValidationErrors 驗證錯誤信息
	// +kubebuilder:validation:Optional
	ValidationErrors []string `json:"validationErrors,omitempty"`

	// LastAppliedGeneration 上次成功應用的 generation
	// +kubebuilder:validation:Optional
	LastAppliedGeneration int64 `json:"lastAppliedGeneration,omitempty"`
}

// DeepCopyInto 深度複製 ApprovalPolicy
func (in *ApprovalPolicy) DeepCopyInto(out *ApprovalPolicy) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy 建立 ApprovalPolicy 的副本
func (in *ApprovalPolicy) DeepCopy() *ApprovalPolicy {
	if in == nil {
		return nil
	}
	out := new(ApprovalPolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject 實現 runtime.Object 界面
func (in *ApprovalPolicy) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto 深度複製 ApprovalPolicyList
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

// DeepCopy 建立 ApprovalPolicyList 的副本
func (in *ApprovalPolicyList) DeepCopy() *ApprovalPolicyList {
	if in == nil {
		return nil
	}
	out := new(ApprovalPolicyList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject 實現 runtime.Object 界面
func (in *ApprovalPolicyList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto 深度複製 ApprovalPolicySpec
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

// DeepCopy 建立 ApprovalPolicySpec 的副本
func (in *ApprovalPolicySpec) DeepCopy() *ApprovalPolicySpec {
	if in == nil {
		return nil
	}
	out := new(ApprovalPolicySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto 深度複製 ApprovalPolicyStatus
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

// DeepCopy 建立 ApprovalPolicyStatus 的副本
func (in *ApprovalPolicyStatus) DeepCopy() *ApprovalPolicyStatus {
	if in == nil {
		return nil
	}
	out := new(ApprovalPolicyStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopy 建立 EscalationConfig 的副本
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

// DeepCopyInto 深度複製 NotificationChannel
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

