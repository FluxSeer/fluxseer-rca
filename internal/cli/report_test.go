package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

func TestBuildRiskRuleReportUsesPublicCRsAndLinkedProjection(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 8, 11, 7, 0, 0, 0, time.UTC))
	rule := &v1alpha1.RiskRule{ObjectMeta: metav1.ObjectMeta{Name: "latency-rule", Namespace: "fluxseer-rca-test"}}
	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "latency-request", Namespace: rule.Namespace, CreationTimestamp: now, Labels: map[string]string{riskRuleLabelKey: rule.Name}},
		Status: v1alpha1.InvestigationRequestStatus{
			ResourceStatus:      v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseCompleted},
			Outcome:             v1alpha1.InvestigationOutcomeConfirmed,
			LinkedRiskSignalRef: &v1alpha1.NamespacedObjectReference{Namespace: "database-test", Name: "latency-projection"},
		},
	}
	direct := &v1alpha1.RiskSignal{ObjectMeta: metav1.ObjectMeta{Name: "latency-direct", Namespace: "database-test", Labels: map[string]string{riskRuleLabelKey: rule.Name}}}
	projection := &v1alpha1.RiskSignal{ObjectMeta: metav1.ObjectMeta{Name: "latency-projection", Namespace: "database-test"}}
	kubeClient := fake.NewClientBuilder().WithScheme(buildScheme()).WithObjects(rule, request, direct, projection).Build()

	report, err := buildRiskRuleReport(context.Background(), kubeClient, rule.Namespace, rule.Name)
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	if report.SchemaVersion != riskRuleReportSchemaVersion || len(report.InvestigationRequests) != 1 || len(report.RiskSignals) != 2 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.RiskRule.Kind != "RiskRule" || report.InvestigationRequests[0].Kind != "InvestigationRequest" || report.RiskSignals[0].Kind != "RiskSignal" {
		t.Fatalf("expected exportable public resource identities, got %#v", report)
	}
	if report.InvestigationRequests[0].Status.Outcome != v1alpha1.InvestigationOutcomeConfirmed {
		t.Fatalf("expected public InvestigationRequest status, got %#v", report.InvestigationRequests[0].Status)
	}
}

func TestCrashLoopBackOffV2ReportExplainsMissingCausalLog(t *testing.T) {
	request := v1alpha1.InvestigationRequest{
		Spec: v1alpha1.InvestigationRequestSpec{
			EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "CrashLoopBackOffV2"},
		},
		Status: v1alpha1.InvestigationRequestStatus{
			ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseCompleted},
			Outcome:        v1alpha1.InvestigationOutcomeInconclusive,
			EvidenceRefs: []v1alpha1.EvidenceRef{
				{Kind: "event", Summary: "Back-off restarting container"},
				{Kind: "containerTermination", Summary: "lastTerminatedReason=Error exitCode=1"},
			},
		},
	}
	var markdown strings.Builder
	writeInvestigationMarkdown(&markdown, &request)
	if !strings.Contains(markdown.String(), "termination status alone does not explain why the process exited") || !strings.Contains(markdown.String(), "policy-allowed Loki query") {
		t.Fatalf("V2 report did not explain the missing causal log evidence:\n%s", markdown.String())
	}
}

func TestCrashLoopBackOffV2ReportExplainsTerminationEvidenceGaps(t *testing.T) {
	tests := []struct {
		name, reason, want string
	}{
		{name: "stale", reason: "ContainerTerminationEvidenceStale", want: "outside the requested window"},
		{name: "truncated", reason: "ContainerTerminationEvidenceTruncated", want: "bounded termination status scan"},
		{name: "missing", reason: "ContainerTerminationEvidenceMissing", want: "still has in-scope Pods"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := v1alpha1.InvestigationRequest{
				Spec: v1alpha1.InvestigationRequestSpec{EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "CrashLoopBackOffV2"}},
				Status: v1alpha1.InvestigationRequestStatus{
					ResourceStatus:  v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseCompleted},
					Outcome:         v1alpha1.InvestigationOutcomeInconclusive,
					MissingEvidence: []v1alpha1.RCAMissingEvidence{{Source: "containerTermination", Reason: test.reason}},
				},
			}
			var markdown strings.Builder
			writeInvestigationMarkdown(&markdown, &request)
			if !strings.Contains(markdown.String(), test.want) {
				t.Fatalf("report did not explain %s termination evidence gap:\n%s", test.name, markdown.String())
			}
		})
	}
}

func TestCrashLoopBackOffV2ReportExplainsKubernetesReadFailure(t *testing.T) {
	request := v1alpha1.InvestigationRequest{
		Spec: v1alpha1.InvestigationRequestSpec{
			EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "CrashLoopBackOffV2"},
		},
		Status: v1alpha1.InvestigationRequestStatus{
			ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseFailed},
			Outcome:        v1alpha1.InvestigationOutcomeUnknown,
			Failure:        &v1alpha1.InvestigationFailure{Code: "DatasourceQueryFailed"},
		},
	}
	var markdown strings.Builder
	writeInvestigationMarkdown(&markdown, &request)
	if !strings.Contains(markdown.String(), "read access to the target Kubernetes Pods") {
		t.Fatalf("V2 report did not explain the Kubernetes evidence read failure:\n%s", markdown.String())
	}
}

func TestWriteRiskRuleReportJSON(t *testing.T) {
	report := riskRuleReport{SchemaVersion: riskRuleReportSchemaVersion, InvestigationRequests: []v1alpha1.InvestigationRequest{}, RiskSignals: []v1alpha1.RiskSignal{}}
	var out bytes.Buffer
	if err := writeRiskRuleReport(&out, report, "json"); err != nil {
		t.Fatalf("write report: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if decoded["schemaVersion"] != riskRuleReportSchemaVersion {
		t.Fatalf("unexpected schema version: %#v", decoded)
	}
}

func TestWriteRiskRuleReportMarkdownCrashLoopEvidenceStates(t *testing.T) {
	tests := []struct {
		name       string
		request    v1alpha1.InvestigationRequest
		want       []string
		wantAbsent []string
	}{
		{
			name: "event only",
			request: v1alpha1.InvestigationRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "crashloop-event-only"},
				Spec: v1alpha1.InvestigationRequestSpec{
					Target:               v1alpha1.TargetRef{Kind: "Deployment", Namespace: "prod", Name: "checkout"},
					EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "CrashLoopBackOff"},
					Queries:              []v1alpha1.InvestigationQuery{{DatasourceRef: v1alpha1.LocalObjectReference{Name: "loki"}, QueryType: "log"}},
				},
				Status: v1alpha1.InvestigationRequestStatus{
					ResourceStatus:  v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseCompleted},
					Outcome:         v1alpha1.InvestigationOutcomeInconclusive,
					Summary:         "Collected evidence is insufficient to confirm the proposed root cause.",
					EvidenceRefs:    []v1alpha1.EvidenceRef{{ID: "event-1", Kind: "event", Source: "kubernetes-events", Reason: "BackOff", Summary: "Container is restarting"}},
					MissingEvidence: []v1alpha1.RCAMissingEvidence{{Source: "log", Reason: "CrashLoopApplicationEvidenceMissing"}},
					EvidenceCoverage: &v1alpha1.RCAEvidenceCoverage{
						Profile:          "CrashLoopBackOff",
						RequiredChecks:   []string{"event:CrashLoopBackOff", "log:ApplicationFailure"},
						CompletedChecks:  []string{"event:CrashLoopBackOff"},
						IncompleteChecks: []string{"log:ApplicationFailure"},
					},
				},
			},
			want:       []string{"**Not confirmed**", "kubernetes-events", "CrashLoopApplicationEvidenceMissing", "Check that the configured log query covers this workload", "do not confirm a root cause"},
			wantAbsent: []string{"Root cause: **Confirmed**"},
		},
		{
			name: "Loki datasource not found",
			request: v1alpha1.InvestigationRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "crashloop-no-loki"},
				Spec: v1alpha1.InvestigationRequestSpec{
					Target:               v1alpha1.TargetRef{Kind: "Deployment", Namespace: "prod", Name: "checkout"},
					EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "CrashLoopBackOff"},
					Queries:              []v1alpha1.InvestigationQuery{{DatasourceRef: v1alpha1.LocalObjectReference{Name: "loki"}, QueryType: "log"}},
				},
				Status: v1alpha1.InvestigationRequestStatus{
					ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseFailed},
					Outcome:        v1alpha1.InvestigationOutcomeUnknown,
					Failure:        &v1alpha1.InvestigationFailure{Code: "DataSourceNotFound", Message: "DataSource loki was not found"},
				},
			},
			want:       []string{"DataSourceNotFound", "Collection stopped: `DataSourceNotFound`", "Configure the referenced Loki datasource", "No root cause was inferred"},
			wantAbsent: []string{"DataSource loki was not found", "No evidence gaps are recorded"},
		},
		{
			name: "Loki query failed",
			request: v1alpha1.InvestigationRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "crashloop-loki-failed"},
				Spec: v1alpha1.InvestigationRequestSpec{
					Target:               v1alpha1.TargetRef{Kind: "Deployment", Namespace: "prod", Name: "checkout"},
					EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "CrashLoopBackOff"},
				},
				Status: v1alpha1.InvestigationRequestStatus{
					ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseFailed},
					Outcome:        v1alpha1.InvestigationOutcomeUnknown,
					Failure:        &v1alpha1.InvestigationFailure{Code: "DatasourceQueryFailed", Message: "query DataSource loki failed"},
					Degradation:    &v1alpha1.RCADegradation{Reasons: []v1alpha1.RCADegradationReason{{Code: "DatasourceUnavailable", Message: "upstream error included Authorization: Bearer secret-token"}}},
				},
			},
			want:       []string{"DatasourceQueryFailed", "Collection stopped: `DatasourceQueryFailed`", "Degradation: `DatasourceUnavailable`", "Restore access to the declared log datasource", "same target and time window"},
			wantAbsent: []string{"query DataSource loki failed", "Authorization: Bearer secret-token", "No evidence gaps are recorded"},
		},
		{
			name: "supporting application log",
			request: v1alpha1.InvestigationRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "crashloop-confirmed"},
				Spec: v1alpha1.InvestigationRequestSpec{
					Target:               v1alpha1.TargetRef{Kind: "Deployment", Namespace: "prod", Name: "checkout"},
					EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: "CrashLoopBackOff"},
				},
				Status: v1alpha1.InvestigationRequestStatus{
					ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseCompleted},
					Outcome:        v1alpha1.InvestigationOutcomeConfirmed,
					Verdict:        &v1alpha1.RCAVerdict{Outcome: v1alpha1.InvestigationOutcomeConfirmed, Summary: "Confirmed startup configuration failure", RootCauseType: "CrashLoop"},
					EvidenceRefs:   []v1alpha1.EvidenceRef{{ID: "log-1", Kind: "log", Source: "loki", Summary: "Invalid configuration prevented startup"}},
					Claims:         []v1alpha1.RCAClaim{{Statement: "Invalid configuration caused startup failure", Verification: "Supported", EvidenceRefs: []string{"log-1"}}},
				},
			},
			want:       []string{"Root cause: **Confirmed**", "Confirmed startup configuration failure", "Invalid configuration caused startup failure", "loki"},
			wantAbsent: []string{"Suggested next steps"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := writeRiskRuleReport(&out, riskRuleReport{
				SchemaVersion:         riskRuleReportSchemaVersion,
				RiskRule:              v1alpha1.RiskRule{ObjectMeta: metav1.ObjectMeta{Name: "crashloop", Namespace: "prod"}},
				InvestigationRequests: []v1alpha1.InvestigationRequest{tt.request},
			}, "markdown")
			if err != nil {
				t.Fatalf("write markdown report: %v", err)
			}
			got := out.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("markdown report missing %q:\n%s", want, got)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("markdown report unexpectedly contains %q:\n%s", absent, got)
				}
			}
		})
	}
}

func TestBuildAgentActionReportIncludesPublicRemediationChain(t *testing.T) {
	action := &v1alpha1.AgentAction{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "checkout-restart-plan-action",
			Namespace:       "prod",
			Annotations:     map[string]string{riskSignalRefAnnotation: "prod/checkout-risk"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "RemediationPlan", Name: "checkout-restart-plan"}},
		},
		Status: v1alpha1.AgentActionStatus{Effectiveness: &v1alpha1.AgentActionEffectivenessStatus{
			VerificationRef: &v1alpha1.NamespacedObjectReference{Name: "checkout-restart-verify", Namespace: "prod"},
		}},
	}
	plan := &v1alpha1.RemediationPlan{ObjectMeta: metav1.ObjectMeta{Name: "checkout-restart-plan", Namespace: "prod"}}
	signal := &v1alpha1.RiskSignal{ObjectMeta: metav1.ObjectMeta{Name: "checkout-risk", Namespace: "prod"}}
	verification := &v1alpha1.InvestigationRequest{ObjectMeta: metav1.ObjectMeta{Name: "checkout-restart-verify", Namespace: "prod"}}
	kubeClient := fake.NewClientBuilder().WithScheme(buildScheme()).WithObjects(action, plan, signal, verification).Build()

	report, err := buildAgentActionReport(context.Background(), kubeClient, "prod", action.Name)
	if err != nil {
		t.Fatalf("build AgentAction report: %v", err)
	}
	if report.SchemaVersion != agentActionReportSchemaVersion || report.RemediationPlan == nil || report.RiskSignal == nil || report.Verification == nil {
		t.Fatalf("expected complete public remediation chain, got %#v", report)
	}
	if report.AgentAction.Kind != "AgentAction" || report.RemediationPlan.Kind != "RemediationPlan" || report.RiskSignal.Kind != "RiskSignal" || report.Verification.Kind != "InvestigationRequest" {
		t.Fatalf("expected public Kubernetes type identities, got action=%#v plan=%#v signal=%#v verification=%#v", report.AgentAction.TypeMeta, report.RemediationPlan.TypeMeta, report.RiskSignal.TypeMeta, report.Verification.TypeMeta)
	}
}

func TestParseReportArgsRejectsUnsupportedOutput(t *testing.T) {
	_, _, _, err := parseReportArgs([]string{"riskrule", "latency", "--output=xml"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected unsupported output error")
	}
}

func TestParseReportArgsAcceptsMarkdownOutput(t *testing.T) {
	opts, resource, name, err := parseReportArgs([]string{"riskrule", "crashloop", "-o", "markdown"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse markdown report args: %v", err)
	}
	if opts.output != "markdown" || resource != "riskrule" || name != "crashloop" {
		t.Fatalf("unexpected parsed arguments: opts=%#v resource=%q name=%q", opts, resource, name)
	}
}
