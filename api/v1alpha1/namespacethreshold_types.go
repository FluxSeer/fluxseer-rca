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

// NamespaceThresholdSpec 定義 namespace 級別的資源限制
type NamespaceThresholdSpec struct {
	// NamespaceSelector 適用的 namespace 標籤選擇器
	// 空選擇器表示適用所有 namespace
	// +kubebuilder:validation:Optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// ActivePlansLimit 單個 namespace 最多活躍的 RemediationPlan 數
	// 0 表示無限制
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=100
	ActivePlansLimit int32 `json:"activePlansLimit,omitempty"`

	// PendingApprovalsLimit 最多待審批的 action 數
	// 0 表示無限制
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=50
	PendingApprovalsLimit int32 `json:"pendingApprovalsLimit,omitempty"`

	// DefaultTTLSeconds 此 namespace 中資源的預設 TTL（秒）
	// 0 表示無預設值
	// +kubebuilder:validation:Minimum=0
	DefaultTTLSeconds int64 `json:"defaultTTLSeconds,omitempty"`

	// DefaultApprovalTimeoutSeconds 預設審批超時（秒）
	// 0 表示無預設值
	// +kubebuilder:validation:Minimum=0
	DefaultApprovalTimeoutSeconds int64 `json:"defaultApprovalTimeoutSeconds,omitempty"`

	// ProtectionLevel 保護級別: standard/strict/permissive
	// - standard: 預設行為
	// - strict: 更嚴格的驗證和限制
	// - permissive: 更寬鬆的策略
	// +kubebuilder:validation:Enum=standard;strict;permissive
	// +kubebuilder:default=standard
	ProtectionLevel string `json:"protectionLevel,omitempty"`

	// Priority 優先級（數字越大優先級越高）
	// 解決多個 NamespaceThreshold 匹配同一 namespace 的衝突
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Priority int32 `json:"priority,omitempty"`

	// Enabled 是否啟用此 threshold 配置
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// NamespaceThresholdStatus 表示 NamespaceThreshold 的狀態
type NamespaceThresholdStatus struct {
	// Phase 配置狀態: Pending/Valid/Invalid/Disabled
	// +kubebuilder:validation:Enum=Pending;Valid;Invalid;Disabled
	// +kubebuilder:default=Pending
	Phase string `json:"phase,omitempty"`

	// EnforcedNamespaces 此 threshold 正在執行的 namespace 清單
	// +kubebuilder:validation:Optional
	EnforcedNamespaces []string `json:"enforcedNamespaces,omitempty"`

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

	// TotalEnforcedCount 當前強制執行的 namespace 總數
	// +kubebuilder:validation:Minimum=0
	TotalEnforcedCount int32 `json:"totalEnforcedCount,omitempty"`
}

// DeepCopyInto 深度複製 NamespaceThreshold
func (in *NamespaceThreshold) DeepCopyInto(out *NamespaceThreshold) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy 建立 NamespaceThreshold 的副本
func (in *NamespaceThreshold) DeepCopy() *NamespaceThreshold {
	if in == nil {
		return nil
	}
	out := new(NamespaceThreshold)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject 實現 runtime.Object 界面
func (in *NamespaceThreshold) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto 深度複製 NamespaceThresholdList
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

// DeepCopy 建立 NamespaceThresholdList 的副本
func (in *NamespaceThresholdList) DeepCopy() *NamespaceThresholdList {
	if in == nil {
		return nil
	}
	out := new(NamespaceThresholdList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject 實現 runtime.Object 界面
func (in *NamespaceThresholdList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto 深度複製 NamespaceThresholdSpec
func (in *NamespaceThresholdSpec) DeepCopyInto(out *NamespaceThresholdSpec) {
	*out = *in
	if in.NamespaceSelector != nil {
		out.NamespaceSelector = in.NamespaceSelector.DeepCopy()
	}
}

// DeepCopy 建立 NamespaceThresholdSpec 的副本
func (in *NamespaceThresholdSpec) DeepCopy() *NamespaceThresholdSpec {
	if in == nil {
		return nil
	}
	out := new(NamespaceThresholdSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto 深度複製 NamespaceThresholdStatus
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

// DeepCopy 建立 NamespaceThresholdStatus 的副本
func (in *NamespaceThresholdStatus) DeepCopy() *NamespaceThresholdStatus {
	if in == nil {
		return nil
	}
	out := new(NamespaceThresholdStatus)
	in.DeepCopyInto(out)
	return out
}
