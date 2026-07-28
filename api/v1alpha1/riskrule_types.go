package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type NamespaceSelector struct {
	MatchNames []string `json:"matchNames,omitempty"`
}

type WorkloadSelector struct {
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
	Kinds       []string          `json:"kinds,omitempty"`
}

type TargetSelector struct {
	NamespaceSelector NamespaceSelector `json:"namespaceSelector,omitempty"`
	WorkloadSelector  WorkloadSelector  `json:"workloadSelector,omitempty"`
}

type RiskThreshold struct {
	Operator string  `json:"operator,omitempty"`
	Value    float64 `json:"value,omitempty"`
}

type RiskRuleSignal struct {
	Name          string               `json:"name"`
	Type          string               `json:"type,omitempty"`
	DatasourceRef LocalObjectReference `json:"datasourceRef,omitempty"`
	QueryType     string               `json:"queryType,omitempty"`
	Query         string               `json:"query,omitempty"`
	QueryTemplate string               `json:"queryTemplate,omitempty"`
	Reasons       []string             `json:"reasons,omitempty"`
	Threshold     RiskThreshold        `json:"threshold,omitempty"`
}

type SecretKeyRef struct {
	Name string `json:"name,omitempty"`
	Key  string `json:"key,omitempty"`
}

type LocalObjectReference struct {
	Name string `json:"name,omitempty"`
}

type RiskRuleNotification struct {
	WebhookRef *SecretKeyRef `json:"webhookRef,omitempty"`
}

type RiskRuleAI struct {
	RCAEnabled  bool                 `json:"rcaEnabled,omitempty"`
	ProviderRef LocalObjectReference `json:"providerRef,omitempty"`
}

const (
	RiskRuleInvestigationModeDirectRiskSignal = "DirectRiskSignal"
	RiskRuleInvestigationModeCreateRequest    = "CreateRequest"
)

type RiskRuleInvestigationPolicy struct {
	Mode             string `json:"mode,omitempty"`
	CreateRiskSignal bool   `json:"createRiskSignal,omitempty"`
}

type RiskRuleSpec struct {
	TargetSelector      TargetSelector              `json:"targetSelector"`
	Interval            metav1.Duration             `json:"interval,omitempty"`
	Window              metav1.Duration             `json:"window,omitempty"`
	Severity            string                      `json:"severity,omitempty"`
	Signals             []RiskRuleSignal            `json:"signals,omitempty"`
	Notification        RiskRuleNotification        `json:"notification,omitempty"`
	AI                  RiskRuleAI                  `json:"ai,omitempty"`
	InvestigationPolicy RiskRuleInvestigationPolicy `json:"investigationPolicy,omitempty"`
}

type RiskRuleStatus struct {
	ResourceStatus `json:",inline"`
	Conditions     []metav1.Condition `json:"conditions,omitempty"`
}

type RiskRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RiskRuleSpec   `json:"spec,omitempty"`
	Status RiskRuleStatus `json:"status,omitempty"`
}

type RiskRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RiskRule `json:"items"`
}

func (in *RiskRule) DeepCopyInto(out *RiskRule) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.TargetSelector.WorkloadSelector.MatchLabels != nil {
		out.Spec.TargetSelector.WorkloadSelector.MatchLabels = make(map[string]string, len(in.Spec.TargetSelector.WorkloadSelector.MatchLabels))
		for key, value := range in.Spec.TargetSelector.WorkloadSelector.MatchLabels {
			out.Spec.TargetSelector.WorkloadSelector.MatchLabels[key] = value
		}
	}
	if in.Spec.TargetSelector.NamespaceSelector.MatchNames != nil {
		out.Spec.TargetSelector.NamespaceSelector.MatchNames = append([]string(nil), in.Spec.TargetSelector.NamespaceSelector.MatchNames...)
	}
	if in.Spec.TargetSelector.WorkloadSelector.Kinds != nil {
		out.Spec.TargetSelector.WorkloadSelector.Kinds = append([]string(nil), in.Spec.TargetSelector.WorkloadSelector.Kinds...)
	}
	if in.Spec.Signals != nil {
		out.Spec.Signals = make([]RiskRuleSignal, len(in.Spec.Signals))
		copy(out.Spec.Signals, in.Spec.Signals)
		for i := range out.Spec.Signals {
			if in.Spec.Signals[i].Reasons != nil {
				out.Spec.Signals[i].Reasons = append([]string(nil), in.Spec.Signals[i].Reasons...)
			}
		}
	}
	if in.Spec.Notification.WebhookRef != nil {
		ref := *in.Spec.Notification.WebhookRef
		out.Spec.Notification.WebhookRef = &ref
	}
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		copy(out.Status.Conditions, in.Status.Conditions)
	}
}

func (in *RiskRule) DeepCopy() *RiskRule {
	if in == nil {
		return nil
	}
	out := new(RiskRule)
	in.DeepCopyInto(out)
	return out
}

func (in *RiskRule) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *RiskRuleList) DeepCopyInto(out *RiskRuleList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]RiskRule, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *RiskRuleList) DeepCopy() *RiskRuleList {
	if in == nil {
		return nil
	}
	out := new(RiskRuleList)
	in.DeepCopyInto(out)
	return out
}

func (in *RiskRuleList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
