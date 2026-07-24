package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
)

func TestAgentAnalysisReconcilerCreatesCLIJobForOptInRiskSignal(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add client-go scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add fluxagent scheme: %v", err)
	}

	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	riskSignal := agentAnalysisRiskSignal()
	executor := &v1alpha1.AgentExecutor{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "AgentExecutor",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "codex-cli",
			Namespace: "payments",
		},
		Spec: v1alpha1.AgentExecutorSpec{
			Type:                v1alpha1.AgentExecutorTypeCodexCLI,
			Image:               "example.com/fluxagent/codex-executor:test",
			Command:             []string{"/bin/sh", "-c"},
			Args:                []string{"codex exec --json < ${FLUXAGENT_EVIDENCE_PATH} > ${FLUXAGENT_RESULT_PATH}"},
			ImagePullSecrets:    []v1alpha1.LocalObjectReference{{Name: "harbor-registry-creds"}},
			CredentialEnvName:   "CODEX_API_KEY",
			CredentialSecretRef: &v1alpha1.SecretKeyReference{Name: "codex-secret", Key: "api-key"},
			ServiceAccountName:  "fluxagent-investigator",
			TimeoutSeconds:      600,
			BackoffLimit:        0,
			MaxToolCalls:        12,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.AgentAnalysisResult{}).
		WithObjects(riskSignal, executor).
		Build()

	reconciler := &AgentAnalysisReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Enabled: true,
		Now:     func() time.Time { return now },
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var results v1alpha1.AgentAnalysisResultList
	if err := fakeClient.List(context.Background(), &results); err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(results.Items) != 1 {
		t.Fatalf("expected one AgentAnalysisResult, got %d", len(results.Items))
	}
	result := results.Items[0]
	if result.Spec.ExecutorRef.Name != "codex-cli" {
		t.Fatalf("expected codex executor ref, got %q", result.Spec.ExecutorRef.Name)
	}
	if result.Status.Phase != v1alpha1.PhaseExecuting {
		t.Fatalf("expected executing result, got %q", result.Status.Phase)
	}
	if result.Status.JobRef == nil || result.Status.JobRef.Name == "" {
		t.Fatalf("expected job ref in result status")
	}

	var job batchv1.Job
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: result.Status.JobRef.Name, Namespace: "payments"}, &job); err != nil {
		t.Fatalf("expected job: %v", err)
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 600 {
		t.Fatalf("expected active deadline 600, got %#v", job.Spec.ActiveDeadlineSeconds)
	}
	if job.Spec.Template.Spec.ServiceAccountName != "fluxagent-investigator" {
		t.Fatalf("unexpected service account %q", job.Spec.Template.Spec.ServiceAccountName)
	}
	if len(job.Spec.Template.Spec.ImagePullSecrets) != 1 || job.Spec.Template.Spec.ImagePullSecrets[0].Name != "harbor-registry-creds" {
		t.Fatalf("expected image pull secret, got %#v", job.Spec.Template.Spec.ImagePullSecrets)
	}
	if len(job.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected one container, got %d", len(job.Spec.Template.Spec.Containers))
	}
	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != executor.Spec.Image {
		t.Fatalf("unexpected image %q", container.Image)
	}
	if !hasEnv(container.Env, "CODEX_API_KEY") {
		t.Fatalf("expected CODEX_API_KEY env from secret")
	}
	if !hasEnvValue(container.Env, "FLUXAGENT_MAX_TOOL_CALLS", "12") {
		t.Fatalf("expected max tool calls env")
	}

	var configMaps corev1.ConfigMapList
	if err := fakeClient.List(context.Background(), &configMaps); err != nil {
		t.Fatalf("list configmaps: %v", err)
	}
	if len(configMaps.Items) != 1 {
		t.Fatalf("expected one evidence ConfigMap, got %d", len(configMaps.Items))
	}
	evidence := configMaps.Items[0].Data["risk-signal.json"]
	if !strings.Contains(evidence, `"rcaSummary": "Pods are crash looping"`) {
		t.Fatalf("expected redacted RCA evidence payload, got %s", evidence)
	}
	if !strings.Contains(configMaps.Items[0].Data["prompt.txt"], "read-only Kubernetes SRE investigator") {
		t.Fatalf("expected bounded prompt")
	}
}

func TestAgentAnalysisReconcilerSkipsRiskSignalWithoutOptIn(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add client-go scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add fluxagent scheme: %v", err)
	}

	riskSignal := agentAnalysisRiskSignal()
	riskSignal.Annotations = nil
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.AgentAnalysisResult{}).
		WithObjects(riskSignal).
		Build()

	reconciler := &AgentAnalysisReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Enabled: true,
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: riskSignal.Namespace},
	}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var jobs batchv1.JobList
	if err := fakeClient.List(context.Background(), &jobs); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("expected no jobs, got %d", len(jobs.Items))
	}
}

func agentAnalysisRiskSignal() *v1alpha1.RiskSignal {
	return &v1alpha1.RiskSignal{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "RiskSignal",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payments-api-risk",
			Namespace: "payments",
			UID:       types.UID("risk-uid"),
			Annotations: map[string]string{
				annotationAgentAnalysis: agentAnalysisEnabled,
				annotationAgentExecutor: "codex-cli",
			},
			Generation: 3,
		},
		Spec: v1alpha1.RiskSignalSpec{
			Target: v1alpha1.TargetRef{
				Namespace:  "payments",
				Kind:       "Deployment",
				Name:       "payments-api",
				APIVersion: "apps/v1",
			},
			SignalType: "availability",
			Severity:   "warning",
			Confidence: 80,
			DryRun:     true,
			Evidence: []v1alpha1.EvidenceRef{
				{Kind: "event", Source: "kubernetes-events", Reason: "BackOff", Summary: "Pod entered BackOff"},
			},
		},
		Status: v1alpha1.RiskSignalStatus{
			ResourceStatus: v1alpha1.ResourceStatus{
				Phase:              v1alpha1.PhaseConfirmed,
				ObservedGeneration: 3,
			},
			RCASummary:    "Pods are crash looping",
			RCAHypothesis: "A recent rollout introduced a startup failure",
			RCAProvider:   "heuristic-provider",
			Conditions: []metav1.Condition{
				{
					Type:               conditionRCAReady,
					Status:             metav1.ConditionTrue,
					Reason:             "ProviderSucceeded",
					Message:            "RCA completed",
					ObservedGeneration: 3,
				},
			},
		},
	}
}

func hasEnv(env []corev1.EnvVar, name string) bool {
	for _, item := range env {
		if item.Name == name {
			return true
		}
	}
	return false
}

func hasEnvValue(env []corev1.EnvVar, name, value string) bool {
	for _, item := range env {
		if item.Name == name && item.Value == value {
			return true
		}
	}
	return false
}
