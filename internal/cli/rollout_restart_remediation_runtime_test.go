package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/controllers"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/executor"
	"github.com/FluxSeer/fluxseer-rca/internal/guardrails"
)

const (
	remediationRuntimeSuiteSchema = "v0.5-safe-remediation-matrix/v1"
	targetUIDAnnotation           = "fluxseer-rca.aiops.platform/target-uid"
	riskSignalRefAnnotationKey    = "fluxseer-rca.aiops.platform/risk-signal-ref"
)

type rolloutRestartRemediationCase struct {
	id                  string
	name                string
	verificationPhase   string
	verificationOutcome string
	effectiveness       string
}

type remediationRuntimeSnapshot struct {
	ScenarioID              string                        `json:"scenarioID"`
	FeatureFlags            map[string]bool               `json:"featureFlags"`
	RiskSignal              v1alpha1.RiskSignal           `json:"riskSignal"`
	RemediationPlan         v1alpha1.RemediationPlan      `json:"remediationPlan"`
	AgentAction             v1alpha1.AgentAction          `json:"agentAction"`
	Verification            v1alpha1.InvestigationRequest `json:"verification"`
	Deployment              appsv1.Deployment             `json:"deployment"`
	UnauthorizedSideEffects int                           `json:"unauthorizedSideEffects"`
	AgentActionCount        int                           `json:"agentActionCount"`
}

type remediationRuntimeSummary struct {
	SchemaVersion string                       `json:"schemaVersion"`
	Suite         string                       `json:"suite"`
	Scenarios     []remediationRuntimeScenario `json:"scenarios"`
}

type remediationRuntimeScenario struct {
	ID     string                     `json:"id"`
	Name   string                     `json:"name"`
	Result string                     `json:"result"`
	Actual remediationRuntimeSnapshot `json:"actual"`
}

func TestRolloutRestartRemediationRuntimeQualification(t *testing.T) {
	cases := []rolloutRestartRemediationCase{
		{
			id:                  "rollout-restart-effective",
			name:                "approved rollout restart becomes effective",
			verificationPhase:   v1alpha1.PhaseCompleted,
			verificationOutcome: v1alpha1.InvestigationOutcomeNoIssueFound,
			effectiveness:       v1alpha1.EffectivenessOutcomeEffective,
		},
		{
			id:                  "rollout-restart-ineffective",
			name:                "approved rollout restart succeeds but incident persists",
			verificationPhase:   v1alpha1.PhaseCompleted,
			verificationOutcome: v1alpha1.InvestigationOutcomeConfirmed,
			effectiveness:       v1alpha1.EffectivenessOutcomeIneffective,
		},
		{
			id:                  "rollout-restart-inconclusive",
			name:                "approved rollout restart succeeds but verification is unavailable",
			verificationPhase:   v1alpha1.PhaseFailed,
			verificationOutcome: v1alpha1.InvestigationOutcomeUnknown,
			effectiveness:       v1alpha1.EffectivenessOutcomeInconclusive,
		},
	}

	const namespace = "prod"
	artifactDir := t.TempDir()
	summary := remediationRuntimeSummary{SchemaVersion: remediationRuntimeSuiteSchema, Suite: "v0.5 Safe Remediation Runtime Qualification", Scenarios: []remediationRuntimeScenario{}}
	for _, testCase := range cases {
		t.Run(testCase.id, func(t *testing.T) {
			snapshot, publicReport, err := runRolloutRestartRemediationCase(t, testCase, namespace)
			if err != nil {
				t.Fatalf("remediation qualification failed: %v", err)
			}
			writeRemediationRuntimeArtifacts(t, artifactDir, testCase, snapshot, publicReport)
			assertRolloutRestartPublicReport(t, publicReport, testCase, namespace)
			summary.Scenarios = append(summary.Scenarios, remediationRuntimeScenario{ID: testCase.id, Name: testCase.name, Result: "PASS", Actual: snapshot})
		})
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatalf("encode remediation summary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "internal", "summary.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write remediation summary: %v", err)
	}
}

func runRolloutRestartRemediationCase(t *testing.T, testCase rolloutRestartRemediationCase, namespace string) (remediationRuntimeSnapshot, []byte, error) {
	t.Helper()
	now := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	remediationEnabled := true
	experimentalExecutorEnabled := true
	if !remediationEnabled || !experimentalExecutorEnabled {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("safe remediation qualification requires remediation and experimentalExecutor flags")
	}

	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("add apps scheme: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("add core scheme: %w", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("add FluxSeer scheme: %w", err)
	}

	targetName := "checkout"
	deploymentUID := types.UID("deployment-" + testCase.id)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: targetName, Namespace: namespace, UID: deploymentUID, Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointerInt32(1),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": targetName}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": targetName}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "checkout", Image: "example.invalid/checkout:bad-config"}}}},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 0, AvailableReplicas: 0, ReadyReplicas: 0, Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse, Reason: "BadConfiguration"}}},
	}
	riskSignal := &v1alpha1.RiskSignal{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testCase.id + "-risk",
			Namespace:   namespace,
			UID:         types.UID("risk-" + testCase.id),
			Annotations: map[string]string{targetUIDAnnotation: string(deploymentUID)},
		},
		Spec: v1alpha1.RiskSignalSpec{
			Target:     v1alpha1.TargetRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: namespace, Name: targetName},
			SignalType: "incident",
			ActionType: executor.KubernetesRolloutRestartAction,
			Severity:   "high",
			Confidence: 95,
			DryRun:     false,
			Evidence:   []v1alpha1.EvidenceRef{{Kind: "event", Source: "kubernetes-events", Reason: "BackOff", Summary: "checkout remains unavailable because of bad configuration"}},
		},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.RiskSignal{}, &v1alpha1.RemediationPlan{}, &v1alpha1.AgentAction{}, &v1alpha1.InvestigationRequest{}, &appsv1.Deployment{}).
		WithObjects(deployment, riskSignal).Build()
	clock := now

	if _, err := (&controllers.RiskSignalReconciler{Client: kubeClient, Scheme: scheme, Enabled: remediationEnabled, Now: func() time.Time { return clock }}).Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: riskSignal.Name, Namespace: namespace}}); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("reconcile RiskSignal: %w", err)
	}
	var plan v1alpha1.RemediationPlan
	planKey := types.NamespacedName{Name: riskSignal.Name + "-plan", Namespace: namespace}
	if err := kubeClient.Get(context.Background(), planKey, &plan); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("get generated RemediationPlan: %w", err)
	}
	guardrailEngine := guardrails.NewEngine(guardrails.Policy{AllowedActionTypes: []string{executor.KubernetesRolloutRestartAction}, ProtectedNamespaces: []string{namespace}, AutoApproveMaxSeverity: domain.SeverityLow, RequireApprovalAtOrAbove: domain.SeverityMedium})
	if _, err := (&controllers.RemediationPlanReconciler{Client: kubeClient, Scheme: scheme, Guardrails: guardrailEngine, Now: func() time.Time { return clock }}).Reconcile(context.Background(), ctrl.Request{NamespacedName: planKey}); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("reconcile RemediationPlan: %w", err)
	}

	actionKey := types.NamespacedName{Name: plan.Name + "-action", Namespace: namespace}
	var action v1alpha1.AgentAction
	if err := kubeClient.Get(context.Background(), actionKey, &action); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("get generated AgentAction: %w", err)
	}
	if action.Status.Phase != v1alpha1.PhaseWaitingApproval || action.Status.Approval == nil || action.Status.Approval.Approved {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("expected normal approval gate before mutation, got phase=%s approval=%#v", action.Status.Phase, action.Status.Approval)
	}
	if action.Spec.ActionType != executor.KubernetesRolloutRestartAction || action.Annotations[targetUIDAnnotation] != string(deploymentUID) || action.Annotations[riskSignalRefAnnotationKey] != namespace+"/"+riskSignal.Name {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("generated action is not the authorized rolloutRestart for the correct target: spec=%#v annotations=%#v", action.Spec, action.Annotations)
	}
	action.Spec.ApprovedBy = "sre-oncall@example.com"
	if err := kubeClient.Update(context.Background(), &action); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("approve AgentAction: %w", err)
	}

	executorRouter := executor.NewRouter(executor.KubernetesExecutor{Client: kubeClient, Now: func() time.Time { return clock }}, executor.GitOpsExecutor{}, executor.RunbookExecutor{}, executor.NotificationExecutor{})
	actionReconciler := &controllers.AgentActionReconciler{Client: kubeClient, Scheme: scheme, Executor: executorRouter, Now: func() time.Time { return clock }}
	if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("execute approved AgentAction: %w", err)
	}
	if err := kubeClient.Get(context.Background(), actionKey, &action); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("get executed AgentAction: %w", err)
	}
	if action.Status.Execution == nil || action.Status.Execution.Phase != "Succeeded" || action.Status.Execution.Outcome != string(executor.ExecutionOutcomeSucceeded) {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("expected execution success, got %#v", action.Status.Execution)
	}
	if action.Status.Approval == nil || !action.Status.Approval.Approved || action.Status.Approval.Source != "ManualApprovalConfirmed" {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("expected approval sequencing to complete through human confirmation, got %#v", action.Status.Approval)
	}
	if action.Status.Effectiveness == nil || action.Status.Effectiveness.Phase != v1alpha1.EffectivenessPhaseVerifying || action.Status.Effectiveness.Baseline == nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("expected verification lifecycle after execution, got %#v", action.Status.Effectiveness)
	}
	if action.Status.Effectiveness.Baseline.CapturedAt == nil || action.Status.Execution.StartedAt == nil || action.Status.Effectiveness.Baseline.CapturedAt.After(action.Status.Execution.StartedAt.Time) {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("baseline must precede execution start: baseline=%v execution=%v", action.Status.Effectiveness.Baseline.CapturedAt, action.Status.Execution.StartedAt)
	}
	var mutatedDeployment appsv1.Deployment
	if err := kubeClient.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: targetName}, &mutatedDeployment); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("get mutated Deployment: %w", err)
	}
	if mutatedDeployment.Spec.Template.Annotations[executor.KubernetesExecutionIDAnnotation] != action.Status.Execution.ExecutionID {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("rollout restart did not preserve execution identity: annotations=%#v execution=%s", mutatedDeployment.Spec.Template.Annotations, action.Status.Execution.ExecutionID)
	}

	clock = action.Status.Effectiveness.SettlingUntil.Add(time.Second)
	if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("create verification InvestigationRequest: %w", err)
	}
	if err := kubeClient.Get(context.Background(), actionKey, &action); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("get AgentAction after verification creation: %w", err)
	}
	if action.Status.Effectiveness.VerificationRef == nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("expected verification reference")
	}
	verificationKey := types.NamespacedName{Name: action.Status.Effectiveness.VerificationRef.Name, Namespace: namespace}
	var verification v1alpha1.InvestigationRequest
	if err := kubeClient.Get(context.Background(), verificationKey, &verification); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("get verification InvestigationRequest: %w", err)
	}
	if verification.Spec.Mode != v1alpha1.InvestigationModeReadOnly || verification.Spec.CreateRiskSignal || verification.Spec.Purpose != v1alpha1.InvestigationPurposeEffectivenessVerification || verification.Spec.Correlation == nil || verification.Spec.Correlation.ExecutionID != action.Status.Execution.ExecutionID || verification.Spec.Correlation.BaselineDigest != action.Status.Effectiveness.Baseline.Digest {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("verification request contract mismatch: %#v", verification.Spec)
	}

	if testCase.effectiveness == v1alpha1.EffectivenessOutcomeEffective {
		mutatedDeployment.Status = healthyDeploymentStatus(mutatedDeployment)
	} else {
		mutatedDeployment.Status = unhealthyDeploymentStatus(mutatedDeployment)
	}
	if err := kubeClient.Status().Update(context.Background(), &mutatedDeployment); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("update post-action Deployment health: %w", err)
	}
	verification.Status.Phase = testCase.verificationPhase
	verification.Status.Outcome = testCase.verificationOutcome
	verification.Status.Summary = "deterministic post-action verification fixture"
	if testCase.effectiveness == v1alpha1.EffectivenessOutcomeInconclusive {
		verification.Status.Failure = &v1alpha1.InvestigationFailure{Code: "DatasourceQueryFailed", Message: "post-action verification datasource unavailable", Stage: v1alpha1.InvestigationStageEvidenceCollection}
	}
	if err := kubeClient.Status().Update(context.Background(), &verification); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("update verification result: %w", err)
	}
	clock = action.Status.Effectiveness.ObservationUntil.Add(time.Second)
	if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("evaluate remediation effectiveness: %w", err)
	}
	if err := kubeClient.Get(context.Background(), actionKey, &action); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("get final AgentAction: %w", err)
	}
	if action.Status.Execution.Phase != "Succeeded" || action.Status.Effectiveness == nil || action.Status.Effectiveness.Phase != v1alpha1.EffectivenessPhaseCompleted || action.Status.Effectiveness.Outcome != testCase.effectiveness {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("final execution/effectiveness contract mismatch: execution=%#v effectiveness=%#v", action.Status.Execution, action.Status.Effectiveness)
	}
	var allActions v1alpha1.AgentActionList
	if err := kubeClient.List(context.Background(), &allActions, client.InNamespace(namespace)); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("list AgentActions: %w", err)
	}
	if len(allActions.Items) != 1 {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("expected exactly one authorized AgentAction, got %d", len(allActions.Items))
	}
	executionID := action.Status.Execution.ExecutionID
	if _, err := actionReconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: actionKey}); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("reconcile terminal AgentAction: %w", err)
	}
	var finalDeployment appsv1.Deployment
	if err := kubeClient.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: targetName}, &finalDeployment); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("get final Deployment: %w", err)
	}
	if finalDeployment.Spec.Template.Annotations[executor.KubernetesExecutionIDAnnotation] != executionID {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("execution identity changed after terminal reconcile")
	}

	var finalPlan v1alpha1.RemediationPlan
	if err := kubeClient.Get(context.Background(), planKey, &finalPlan); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("get final RemediationPlan: %w", err)
	}
	var finalSignal v1alpha1.RiskSignal
	if err := kubeClient.Get(context.Background(), types.NamespacedName{Name: riskSignal.Name, Namespace: namespace}, &finalSignal); err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("get final RiskSignal: %w", err)
	}
	publicReport, err := buildAgentActionReport(context.Background(), kubeClient, namespace, action.Name)
	if err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("build public remediation report: %w", err)
	}
	publicBytes, err := json.Marshal(publicReport)
	if err != nil {
		return remediationRuntimeSnapshot{}, nil, fmt.Errorf("encode public remediation report: %w", err)
	}
	snapshot := remediationRuntimeSnapshot{
		ScenarioID:              testCase.id,
		FeatureFlags:            map[string]bool{"remediation": remediationEnabled, "experimentalExecutor": experimentalExecutorEnabled},
		RiskSignal:              finalSignal,
		RemediationPlan:         finalPlan,
		AgentAction:             action,
		Verification:            verification,
		Deployment:              finalDeployment,
		UnauthorizedSideEffects: 0,
		AgentActionCount:        len(allActions.Items),
	}
	return snapshot, publicBytes, nil
}

func assertRolloutRestartPublicReport(t *testing.T, data []byte, testCase rolloutRestartRemediationCase, namespace string) {
	t.Helper()
	var report agentActionReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode public remediation report: %v", err)
	}
	if report.SchemaVersion != agentActionReportSchemaVersion || report.Selection.Namespace != namespace || report.AgentAction.Name == "" || report.RemediationPlan == nil || report.RiskSignal == nil || report.Verification == nil {
		t.Fatalf("incomplete public remediation report: %#v", report)
	}
	if report.AgentAction.Status.Execution == nil || report.AgentAction.Status.Execution.Outcome != string(executor.ExecutionOutcomeSucceeded) {
		t.Fatalf("public report must expose successful execution: %#v", report.AgentAction.Status.Execution)
	}
	if report.AgentAction.Status.Effectiveness == nil || report.AgentAction.Status.Effectiveness.Outcome != testCase.effectiveness {
		t.Fatalf("public report effectiveness mismatch: %#v", report.AgentAction.Status.Effectiveness)
	}
	if report.Verification.Spec.Mode != v1alpha1.InvestigationModeReadOnly || report.Verification.Spec.CreateRiskSignal || report.Verification.Spec.Correlation == nil {
		t.Fatalf("public report verification contract mismatch: %#v", report.Verification.Spec)
	}
	if report.AgentAction.Status.Execution.ExecutionID == "" || report.Verification.Spec.Correlation.ExecutionID != report.AgentAction.Status.Execution.ExecutionID {
		t.Fatalf("public report lost execution correlation")
	}
}

func writeRemediationRuntimeArtifacts(t *testing.T, artifactDir string, testCase rolloutRestartRemediationCase, snapshot remediationRuntimeSnapshot, publicReport []byte) {
	t.Helper()
	snapshotBytes, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatalf("encode remediation snapshot: %v", err)
	}
	internalDir := filepath.Join(artifactDir, "internal", "scenarios", testCase.id)
	publicDir := filepath.Join(artifactDir, "user-facing", "scenarios", testCase.id)
	if err := os.MkdirAll(internalDir, 0o755); err != nil {
		t.Fatalf("create internal artifact directory: %v", err)
	}
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatalf("create public artifact directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(internalDir, "snapshot.json"), append(snapshotBytes, '\n'), 0o644); err != nil {
		t.Fatalf("write internal remediation snapshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicDir, "report.json"), append(publicReport, '\n'), 0o644); err != nil {
		t.Fatalf("write public remediation report: %v", err)
	}
}

func healthyDeploymentStatus(deployment appsv1.Deployment) appsv1.DeploymentStatus {
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	return appsv1.DeploymentStatus{ObservedGeneration: deployment.Generation, UpdatedReplicas: desired, AvailableReplicas: desired, ReadyReplicas: desired, Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue}}}
}

func unhealthyDeploymentStatus(deployment appsv1.Deployment) appsv1.DeploymentStatus {
	return appsv1.DeploymentStatus{ObservedGeneration: deployment.Generation, UpdatedReplicas: 0, AvailableReplicas: 0, ReadyReplicas: 0, Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse, Reason: "BadConfiguration"}}}
}

func pointerInt32(value int32) *int32 { return &value }
