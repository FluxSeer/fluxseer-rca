package cli

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestParseReportArgsRejectsUnsupportedOutput(t *testing.T) {
	_, _, _, err := parseReportArgs([]string{"riskrule", "latency", "--output=xml"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected unsupported output error")
	}
}
