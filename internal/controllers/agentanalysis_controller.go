package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"fluxagent/api/v1alpha1"
)

const (
	annotationAgentAnalysis = "fluxagent.aiops.platform/agent-analysis"
	annotationAgentExecutor = "fluxagent.aiops.platform/agent-executor"
	annotationExecutionKey  = "fluxagent.aiops.platform/execution-key"

	agentAnalysisEnabled = "enabled"
	defaultExecutorName  = "codex-cli"

	conditionAgentJobReady = "AgentJobReady"
)

type AgentAnalysisReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Enabled bool
	Now     func() time.Time
}

func (r *AgentAnalysisReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var riskSignal v1alpha1.RiskSignal
	if err := r.Get(ctx, req.NamespacedName, &riskSignal); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !r.Enabled || !agentAnalysisRequested(&riskSignal) || !riskSignalRCAReady(&riskSignal) {
		return ctrl.Result{}, nil
	}

	executorName := riskSignal.Annotations[annotationAgentExecutor]
	if executorName == "" {
		executorName = defaultExecutorName
	}
	var executor v1alpha1.AgentExecutor
	if err := r.Get(ctx, types.NamespacedName{Name: executorName, Namespace: riskSignal.Namespace}, &executor); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if executor.Spec.Image == "" {
		return ctrl.Result{}, nil
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	executionKey, err := agentAnalysisExecutionKey(&riskSignal, &executor)
	if err != nil {
		return ctrl.Result{}, err
	}
	resultName := agentAnalysisResultName(&riskSignal, executor.Name, executionKey)
	configMapName := resultName + "-evidence"
	jobName := resultName + "-job"

	result := &v1alpha1.AgentAnalysisResult{}
	result.Name = resultName
	result.Namespace = riskSignal.Namespace
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, result, func() error {
		if err := controllerutil.SetControllerReference(&riskSignal, result, r.Scheme); err != nil {
			return err
		}
		result.Labels = mergeStringMap(result.Labels, map[string]string{
			labelManagedBy: "fluxagent",
		})
		result.Annotations = mergeStringMap(result.Annotations, map[string]string{
			annotationExecutionKey: executionKey,
		})
		result.Spec.SourceRef = v1alpha1.AgentAnalysisSourceRef{
			Kind:      "RiskSignal",
			Name:      riskSignal.Name,
			Namespace: riskSignal.Namespace,
		}
		result.Spec.ExecutorRef = v1alpha1.LocalObjectReference{Name: executor.Name}
		result.Spec.ExecutionKey = executionKey
		result.Spec.TTLSeconds = riskSignal.Spec.TTLSeconds
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.ensureEvidenceConfigMap(ctx, &riskSignal, &executor, configMapName, executionKey); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureAgentJob(ctx, &riskSignal, &executor, jobName, configMapName, resultName, executionKey); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.updateAgentAnalysisResultStatus(ctx, resultName, riskSignal.Namespace, jobName, now()); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AgentAnalysisReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("risksignal-agent-analysis").
		For(&v1alpha1.RiskSignal{}).
		Owns(&v1alpha1.AgentAnalysisResult{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}

func (r *AgentAnalysisReconciler) ensureEvidenceConfigMap(ctx context.Context, riskSignal *v1alpha1.RiskSignal, executor *v1alpha1.AgentExecutor, name, executionKey string) error {
	payload, err := json.MarshalIndent(riskSignal, "", "  ")
	if err != nil {
		return err
	}
	prompt := agentAnalysisPrompt(riskSignal, executor)
	configMap := &corev1.ConfigMap{}
	configMap.Name = name
	configMap.Namespace = riskSignal.Namespace
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, configMap, func() error {
		if err := controllerutil.SetControllerReference(riskSignal, configMap, r.Scheme); err != nil {
			return err
		}
		configMap.Labels = mergeStringMap(configMap.Labels, map[string]string{
			labelManagedBy: "fluxagent",
		})
		configMap.Annotations = mergeStringMap(configMap.Annotations, map[string]string{
			annotationExecutionKey: executionKey,
		})
		configMap.Data = map[string]string{
			"risk-signal.json": string(payload),
			"prompt.txt":       prompt,
		}
		return nil
	})
	return err
}

func (r *AgentAnalysisReconciler) ensureAgentJob(ctx context.Context, riskSignal *v1alpha1.RiskSignal, executor *v1alpha1.AgentExecutor, name, configMapName, resultName, executionKey string) error {
	job := &batchv1.Job{}
	job.Name = name
	job.Namespace = riskSignal.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, job, func() error {
		if err := controllerutil.SetControllerReference(riskSignal, job, r.Scheme); err != nil {
			return err
		}
		job.Labels = mergeStringMap(job.Labels, map[string]string{
			labelManagedBy: "fluxagent",
		})
		job.Annotations = mergeStringMap(job.Annotations, map[string]string{
			annotationExecutionKey: executionKey,
		})
		backoffLimit := executor.Spec.BackoffLimit
		timeout := executor.Spec.TimeoutSeconds
		if timeout <= 0 {
			timeout = 900
		}
		ttl := executor.Spec.TTLSecondsAfterFinished
		if ttl <= 0 {
			ttl = 3600
		}
		serviceAccountName := executor.Spec.ServiceAccountName
		if serviceAccountName == "" {
			serviceAccountName = "fluxagent-investigator"
		}
		job.Spec.BackoffLimit = &backoffLimit
		job.Spec.ActiveDeadlineSeconds = &timeout
		job.Spec.TTLSecondsAfterFinished = &ttl
		job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyNever
		job.Spec.Template.Spec.ServiceAccountName = serviceAccountName
		job.Spec.Template.Spec.Containers = []corev1.Container{agentExecutorContainer(executor, resultName)}
		job.Spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: "evidence",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
					},
				},
			},
			{
				Name: "result",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		}
		return nil
	})
	return err
}

func (r *AgentAnalysisReconciler) updateAgentAnalysisResultStatus(ctx context.Context, name, namespace, jobName string, now time.Time) error {
	var result v1alpha1.AgentAnalysisResult
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &result); err != nil {
		return err
	}
	var job batchv1.Job
	if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job); err != nil {
		return err
	}
	original := result.DeepCopy()
	if result.Status.StartedAt == nil {
		started := metav1.NewTime(now)
		result.Status.StartedAt = &started
	}
	result.Status.JobRef = &v1alpha1.AgentAnalysisJobRef{Name: job.Name, Namespace: job.Namespace}
	phase := v1alpha1.PhaseExecuting
	message := "agent analysis job is running"
	conditionStatus := metav1.ConditionTrue
	reason := "JobRunning"
	for _, condition := range job.Status.Conditions {
		switch condition.Type {
		case batchv1.JobComplete:
			if condition.Status == corev1.ConditionTrue {
				phase = v1alpha1.PhaseSucceeded
				message = "agent analysis job completed"
				reason = "JobCompleted"
				completed := metav1.NewTime(now)
				result.Status.CompletedAt = &completed
			}
		case batchv1.JobFailed:
			if condition.Status == corev1.ConditionTrue {
				phase = v1alpha1.PhaseFailed
				message = condition.Message
				if message == "" {
					message = "agent analysis job failed"
				}
				conditionStatus = metav1.ConditionFalse
				reason = "JobFailed"
				completed := metav1.NewTime(now)
				result.Status.CompletedAt = &completed
			}
		}
	}
	setResourceStatus(&result.Status.ResourceStatus, phase, message, result.Generation, now)
	setStatusCondition(&result.Status.Conditions, conditionAgentJobReady, conditionStatus, reason, message, result.Generation, now)
	if !statusChangedAgentAnalysisResult(original, &result) {
		return nil
	}
	if err := r.Status().Update(ctx, &result); err != nil && !apierrors.IsConflict(err) {
		return err
	}
	return nil
}

func agentAnalysisRequested(riskSignal *v1alpha1.RiskSignal) bool {
	return riskSignal.Annotations[annotationAgentAnalysis] == agentAnalysisEnabled
}

func riskSignalRCAReady(riskSignal *v1alpha1.RiskSignal) bool {
	if riskSignal.Status.RCASummary != "" || riskSignal.Status.RCAHypothesis != "" {
		return true
	}
	condition := apimeta.FindStatusCondition(riskSignal.Status.Conditions, conditionRCAReady)
	return condition != nil && condition.Status == metav1.ConditionTrue
}

func agentAnalysisExecutionKey(riskSignal *v1alpha1.RiskSignal, executor *v1alpha1.AgentExecutor) (string, error) {
	executorSpec, err := json.Marshal(executor.Spec)
	if err != nil {
		return "", err
	}
	riskSignalSpec, err := json.Marshal(riskSignal.Spec)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(string(riskSignal.UID)))
	_, _ = hash.Write([]byte(fmt.Sprintf("|%d|", riskSignal.Generation)))
	_, _ = hash.Write(riskSignalSpec)
	_, _ = hash.Write([]byte("|" + riskSignal.Status.RCASummary + "|" + riskSignal.Status.RCAHypothesis + "|" + riskSignal.Status.RCAProvider + "|"))
	_, _ = hash.Write([]byte(executor.Name + "|"))
	_, _ = hash.Write(executorSpec)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func agentAnalysisResultName(riskSignal *v1alpha1.RiskSignal, executorName, executionKey string) string {
	base := sanitizeDNSLabel(fmt.Sprintf("%s-%s", riskSignal.Name, executorName))
	if len(base) > 46 {
		base = base[:46]
		base = strings.Trim(base, "-")
	}
	return fmt.Sprintf("%s-%s", base, executionKey[:10])
}

func agentExecutorContainer(executor *v1alpha1.AgentExecutor, resultName string) corev1.Container {
	env := []corev1.EnvVar{
		{Name: "FLUXAGENT_ANALYSIS_RESULT_NAME", Value: resultName},
		{Name: "FLUXAGENT_EVIDENCE_PATH", Value: "/var/run/fluxagent/evidence/risk-signal.json"},
		{Name: "FLUXAGENT_PROMPT_PATH", Value: "/var/run/fluxagent/evidence/prompt.txt"},
		{Name: "FLUXAGENT_RESULT_PATH", Value: "/var/run/fluxagent/result/result.json"},
	}
	if executor.Spec.MaxToolCalls > 0 {
		env = append(env, corev1.EnvVar{Name: "FLUXAGENT_MAX_TOOL_CALLS", Value: fmt.Sprintf("%d", executor.Spec.MaxToolCalls)})
	}
	if executor.Spec.CredentialSecretRef != nil && executor.Spec.CredentialEnvName != "" {
		env = append(env, corev1.EnvVar{
			Name: executor.Spec.CredentialEnvName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: executor.Spec.CredentialSecretRef.Name},
					Key:                  executor.Spec.CredentialSecretRef.Key,
				},
			},
		})
	}
	return corev1.Container{
		Name:            "agent-executor",
		Image:           executor.Spec.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         append([]string(nil), executor.Spec.Command...),
		Args:            append([]string(nil), executor.Spec.Args...),
		Env:             env,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "evidence", MountPath: "/var/run/fluxagent/evidence", ReadOnly: true},
			{Name: "result", MountPath: "/var/run/fluxagent/result"},
		},
	}
}

func agentAnalysisPrompt(riskSignal *v1alpha1.RiskSignal, executor *v1alpha1.AgentExecutor) string {
	return fmt.Sprintf(`You are a read-only Kubernetes SRE investigator.
Analyze the provided FluxAgent RiskSignal evidence and RCA.
Do not mutate Kubernetes resources.
Do not request or print secrets.
Return structured JSON with summary, rootCause, confidence, validationSteps, recommendations, and missingEvidence.

source:
  kind: RiskSignal
  namespace: %s
  name: %s
executor:
  name: %s
  type: %s
`, riskSignal.Namespace, riskSignal.Name, executor.Name, executor.Spec.Type)
}

func statusChangedAgentAnalysisResult(before, after *v1alpha1.AgentAnalysisResult) bool {
	return !reflect.DeepEqual(before.Status, after.Status)
}

func sanitizeDNSLabel(input string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(input) {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteRune('-')
			lastDash = true
		}
	}
	out := strings.Trim(builder.String(), "-")
	if out == "" {
		return "agent-analysis"
	}
	return out
}

func mergeStringMap(current map[string]string, values map[string]string) map[string]string {
	if current == nil {
		current = map[string]string{}
	}
	for key, value := range values {
		current[key] = value
	}
	return current
}
