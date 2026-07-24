package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	AgentExecutorTypeCodexCLI  = "codex-cli"
	AgentExecutorTypeClaudeCLI = "claude-cli"
	AgentExecutorTypeGeminiCLI = "gemini-cli"
)

type SecretKeyReference struct {
	Name string `json:"name,omitempty"`
	Key  string `json:"key,omitempty"`
}

type AgentExecutorSpec struct {
	Type                    string              `json:"type"`
	Image                   string              `json:"image"`
	Command                 []string            `json:"command,omitempty"`
	Args                    []string            `json:"args,omitempty"`
	CredentialEnvName       string              `json:"credentialEnvName,omitempty"`
	CredentialSecretRef     *SecretKeyReference `json:"credentialSecretRef,omitempty"`
	ServiceAccountName      string              `json:"serviceAccountName,omitempty"`
	TimeoutSeconds          int64               `json:"timeoutSeconds,omitempty"`
	BackoffLimit            int32               `json:"backoffLimit,omitempty"`
	TTLSecondsAfterFinished int32               `json:"ttlSecondsAfterFinished,omitempty"`
	MaxToolCalls            int32               `json:"maxToolCalls,omitempty"`
}

type AgentExecutorStatus struct {
	ResourceStatus `json:",inline"`
	Conditions     []metav1.Condition `json:"conditions,omitempty"`
}

type AgentExecutor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentExecutorSpec   `json:"spec,omitempty"`
	Status AgentExecutorStatus `json:"status,omitempty"`
}

type AgentExecutorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentExecutor `json:"items"`
}

type AgentAnalysisSourceRef struct {
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type AgentAnalysisJobRef struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type AgentAnalysisResultSpec struct {
	SourceRef    AgentAnalysisSourceRef `json:"sourceRef"`
	ExecutorRef  LocalObjectReference   `json:"executorRef"`
	ExecutionKey string                 `json:"executionKey"`
	TTLSeconds   int64                  `json:"ttlSeconds,omitempty"`
}

type AgentAnalysisResultStatus struct {
	ResourceStatus  `json:",inline"`
	JobRef          *AgentAnalysisJobRef `json:"jobRef,omitempty"`
	Summary         string               `json:"summary,omitempty"`
	RootCause       string               `json:"rootCause,omitempty"`
	Confidence      float64              `json:"confidence,omitempty"`
	ValidationSteps []string             `json:"validationSteps,omitempty"`
	Recommendations []string             `json:"recommendations,omitempty"`
	MissingEvidence []string             `json:"missingEvidence,omitempty"`
	StartedAt       *metav1.Time         `json:"startedAt,omitempty"`
	CompletedAt     *metav1.Time         `json:"completedAt,omitempty"`
	Conditions      []metav1.Condition   `json:"conditions,omitempty"`
}

type AgentAnalysisResult struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentAnalysisResultSpec   `json:"spec,omitempty"`
	Status AgentAnalysisResultStatus `json:"status,omitempty"`
}

type AgentAnalysisResultList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentAnalysisResult `json:"items"`
}

func (in *AgentExecutor) DeepCopyInto(out *AgentExecutor) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.Command != nil {
		out.Spec.Command = append([]string(nil), in.Spec.Command...)
	}
	if in.Spec.Args != nil {
		out.Spec.Args = append([]string(nil), in.Spec.Args...)
	}
	if in.Spec.CredentialSecretRef != nil {
		ref := *in.Spec.CredentialSecretRef
		out.Spec.CredentialSecretRef = &ref
	}
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		copy(out.Status.Conditions, in.Status.Conditions)
	}
}

func (in *AgentExecutor) DeepCopy() *AgentExecutor {
	if in == nil {
		return nil
	}
	out := new(AgentExecutor)
	in.DeepCopyInto(out)
	return out
}

func (in *AgentExecutor) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *AgentExecutorList) DeepCopyInto(out *AgentExecutorList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]AgentExecutor, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *AgentExecutorList) DeepCopy() *AgentExecutorList {
	if in == nil {
		return nil
	}
	out := new(AgentExecutorList)
	in.DeepCopyInto(out)
	return out
}

func (in *AgentExecutorList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *AgentAnalysisResult) DeepCopyInto(out *AgentAnalysisResult) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Status.JobRef != nil {
		ref := *in.Status.JobRef
		out.Status.JobRef = &ref
	}
	if in.Status.StartedAt != nil {
		started := in.Status.StartedAt.DeepCopy()
		out.Status.StartedAt = started
	}
	if in.Status.CompletedAt != nil {
		completed := in.Status.CompletedAt.DeepCopy()
		out.Status.CompletedAt = completed
	}
	if in.Status.ValidationSteps != nil {
		out.Status.ValidationSteps = append([]string(nil), in.Status.ValidationSteps...)
	}
	if in.Status.Recommendations != nil {
		out.Status.Recommendations = append([]string(nil), in.Status.Recommendations...)
	}
	if in.Status.MissingEvidence != nil {
		out.Status.MissingEvidence = append([]string(nil), in.Status.MissingEvidence...)
	}
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		copy(out.Status.Conditions, in.Status.Conditions)
	}
}

func (in *AgentAnalysisResult) DeepCopy() *AgentAnalysisResult {
	if in == nil {
		return nil
	}
	out := new(AgentAnalysisResult)
	in.DeepCopyInto(out)
	return out
}

func (in *AgentAnalysisResult) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *AgentAnalysisResultList) DeepCopyInto(out *AgentAnalysisResultList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]AgentAnalysisResult, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *AgentAnalysisResultList) DeepCopy() *AgentAnalysisResultList {
	if in == nil {
		return nil
	}
	out := new(AgentAnalysisResultList)
	in.DeepCopyInto(out)
	return out
}

func (in *AgentAnalysisResultList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
