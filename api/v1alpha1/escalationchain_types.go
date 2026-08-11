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

// EscalationChainSpec 定義升級路由的分階段配置
type EscalationChainSpec struct {
	// ResourceSelector 此 escalation chain 適用的資源標籤選擇器
	// 空選擇器表示適用所有資源
	// +kubebuilder:validation:Optional
	ResourceSelector *metav1.LabelSelector `json:"resourceSelector,omitempty"`

	// Priority 優先級（數字越大優先級越高）
	// 解決多個 chain 匹配同一資源的衝突
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Priority int32 `json:"priority,omitempty"`

	// Stages escalation 的分階段定義
	// 按序執行，後續階段只有在觸發條件滿足時才執行
	// +kubebuilder:validation:MinItems=1
	Stages []EscalationStage `json:"stages"`

	// Enabled 是否啟用此 escalation chain
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// EscalationStage 單個升級階段
type EscalationStage struct {
	// Name 階段名稱（例如 "L1-OnCall", "L2-Manager", "L3-CTO"）
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// Delay 從前一階段開始後延遲多久觸發此階段（秒）
	// 第一個階段的 Delay 表示從進入待審批狀態開始的延遲
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Delay int64 `json:"delay,omitempty"`

	// Condition 觸發此階段的條件
	// +kubebuilder:validation:Optional
	Condition *EscalationCondition `json:"condition,omitempty"`

	// Actions 此階段執行的升級動作
	// +kubebuilder:validation:Optional
	Actions []EscalationAction `json:"actions,omitempty"`

	// Assignees 此階段的受理者（通知/分配給誰）
	// +kubebuilder:validation:Optional
	Assignees []Assignee `json:"assignees,omitempty"`

	// NotificationTemplate 自訂通知模板名稱
	// +kubebuilder:validation:Optional
	NotificationTemplate string `json:"notificationTemplate,omitempty"`

	// Timeout 此階段的超時時間（秒）
	// 0 表示使用預設超時
	// +kubebuilder:validation:Minimum=0
	Timeout int64 `json:"timeout,omitempty"`
}

// EscalationCondition 升級階段的觸發條件
type EscalationCondition struct {
	// Type 條件類型: pending_time/approval_count/severity/manual
	// +kubebuilder:validation:Enum=pending_time;approval_count;severity;manual
	Type string `json:"type"`

	// Threshold 閾值
	// - pending_time: 待審批的秒數
	// - approval_count: 拒絕或無回應的次數
	// +kubebuilder:validation:Minimum=0
	Threshold int64 `json:"threshold,omitempty"`

	// SeverityMin 最低 severity（包含）
	// +kubebuilder:validation:Enum=Critical;High;Medium;Low;Info
	// +kubebuilder:validation:Optional
	SeverityMin string `json:"severityMin,omitempty"`

	// SeverityMax 最高 severity（包含）
	// +kubebuilder:validation:Enum=Critical;High;Medium;Low;Info
	// +kubebuilder:validation:Optional
	SeverityMax string `json:"severityMax,omitempty"`
}

// EscalationAction 升級階段的執行動作
type EscalationAction struct {
	// Type 動作類型: notify/reassign/auto_reject/force_execute
	// +kubebuilder:validation:Enum=notify;reassign;auto_reject;force_execute
	Type string `json:"type"`

	// Details 動作的詳細配置（JSON map）
	// 例如 notify 可指定通知通道、reassign 可指定新的審批者
	// +kubebuilder:validation:Optional
	Details map[string]string `json:"details,omitempty"`

	// Enabled 是否啟用此動作
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// Assignee 升級階段的受理者
type Assignee struct {
	// Type 受理者類型: user/team/role
	// +kubebuilder:validation:Enum=user;team;role
	Type string `json:"type"`

	// Name 用戶/team/role 名稱
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Channels 通知渠道（例如 slack, email, pagerduty）
	// 空列表表示使用預設通知渠道
	// +kubebuilder:validation:Optional
	Channels []string `json:"channels,omitempty"`

	// Enabled 是否啟用此受理者
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// EscalationChainStatus 表示 EscalationChain 的狀態
type EscalationChainStatus struct {
	// Phase 鏈狀態: Pending/Valid/Invalid/Disabled
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

	// TotalStages 此鏈中的總階段數
	// +kubebuilder:validation:Minimum=0
	TotalStages int32 `json:"totalStages,omitempty"`
}

// DeepCopyInto 深度複製 EscalationChain
func (in *EscalationChain) DeepCopyInto(out *EscalationChain) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy 建立 EscalationChain 的副本
func (in *EscalationChain) DeepCopy() *EscalationChain {
	if in == nil {
		return nil
	}
	out := new(EscalationChain)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject 實現 runtime.Object 界面
func (in *EscalationChain) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto 深度複製 EscalationChainList
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

// DeepCopy 建立 EscalationChainList 的副本
func (in *EscalationChainList) DeepCopy() *EscalationChainList {
	if in == nil {
		return nil
	}
	out := new(EscalationChainList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject 實現 runtime.Object 界面
func (in *EscalationChainList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto 深度複製 EscalationChainSpec
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

// DeepCopy 建立 EscalationChainSpec 的副本
func (in *EscalationChainSpec) DeepCopy() *EscalationChainSpec {
	if in == nil {
		return nil
	}
	out := new(EscalationChainSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto 深度複製 EscalationStage
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

// DeepCopy 建立 EscalationStage 的副本
func (in *EscalationStage) DeepCopy() *EscalationStage {
	if in == nil {
		return nil
	}
	out := new(EscalationStage)
	in.DeepCopyInto(out)
	return out
}

// DeepCopy 建立 EscalationCondition 的副本
func (in *EscalationCondition) DeepCopy() *EscalationCondition {
	if in == nil {
		return nil
	}
	out := new(EscalationCondition)
	*out = *in
	return out
}

// DeepCopyInto 深度複製 EscalationAction
func (in *EscalationAction) DeepCopyInto(out *EscalationAction) {
	*out = *in
	if in.Details != nil {
		out.Details = make(map[string]string, len(in.Details))
		for key, value := range in.Details {
			out.Details[key] = value
		}
	}
}

// DeepCopy 建立 EscalationAction 的副本
func (in *EscalationAction) DeepCopy() *EscalationAction {
	if in == nil {
		return nil
	}
	out := new(EscalationAction)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto 深度複製 Assignee
func (in *Assignee) DeepCopyInto(out *Assignee) {
	*out = *in
	if in.Channels != nil {
		out.Channels = make([]string, len(in.Channels))
		copy(out.Channels, in.Channels)
	}
}

// DeepCopy 建立 Assignee 的副本
func (in *Assignee) DeepCopy() *Assignee {
	if in == nil {
		return nil
	}
	out := new(Assignee)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto 深度複製 EscalationChainStatus
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

// DeepCopy 建立 EscalationChainStatus 的副本
func (in *EscalationChainStatus) DeepCopy() *EscalationChainStatus {
	if in == nil {
		return nil
	}
	out := new(EscalationChainStatus)
	in.DeepCopyInto(out)
	return out
}
