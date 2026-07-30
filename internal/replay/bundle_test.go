package replay

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"fluxagent/api/v1alpha1"
)

func TestExportCompletedInvestigationRequestBundle(t *testing.T) {
	request := replayRequest()
	bundle, err := Export(request)
	if err != nil {
		t.Fatalf("export bundle: %v", err)
	}
	if bundle.SchemaVersion != BundleSchemaVersion {
		t.Fatalf("unexpected schema version %q", bundle.SchemaVersion)
	}
	if bundle.Source.Namespace != "fluxagent-system" || bundle.Source.Name != "checkout-latency" {
		t.Fatalf("unexpected source %#v", bundle.Source)
	}
	if bundle.Status.Verdict == nil || bundle.Status.Verdict.RootCauseType != "LatencyRegression" {
		t.Fatalf("expected verdict to be exported, got %#v", bundle.Status.Verdict)
	}
	if len(bundle.Status.EvidenceRefs) != 1 || bundle.Status.EvidenceRefs[0].Summary == "" {
		t.Fatalf("expected compact evidence refs, got %#v", bundle.Status.EvidenceRefs)
	}
	if !strings.HasPrefix(bundle.Identity.Digest, "sha256:") {
		t.Fatalf("expected bundle digest, got %q", bundle.Identity.Digest)
	}

	again, err := Export(request)
	if err != nil {
		t.Fatalf("export bundle again: %v", err)
	}
	if again.Identity.Digest != bundle.Identity.Digest {
		t.Fatalf("expected deterministic bundle digest, got %s then %s", bundle.Identity.Digest, again.Identity.Digest)
	}
}

func TestExportRejectsNonTerminalInvestigationRequest(t *testing.T) {
	request := replayRequest()
	request.Status.Phase = v1alpha1.PhaseCollecting
	if _, err := Export(request); err == nil {
		t.Fatal("expected non-terminal request export to fail")
	}
}

func TestCompareReplayBundles(t *testing.T) {
	expected, err := Export(replayRequest())
	if err != nil {
		t.Fatalf("export expected: %v", err)
	}
	actual := expected
	actual.Status.Verdict = &v1alpha1.RCAVerdict{
		Outcome:       v1alpha1.InvestigationOutcomeConfirmed,
		RootCauseType: "ConfigurationMismatch",
		Confidence:    0.91,
	}
	report := Compare(expected, actual)
	if report.Passed {
		t.Fatal("expected replay comparison to fail")
	}
	if len(report.Differences) != 1 || report.Differences[0].Field != "status.verdict.rootCauseType" {
		t.Fatalf("unexpected differences %#v", report.Differences)
	}
}

func replayRequest() *v1alpha1.InvestigationRequest {
	return &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "checkout-latency",
			Namespace:  "fluxagent-system",
			UID:        types.UID("investigation-uid"),
			Generation: 3,
		},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Namespace:  "prod",
				Name:       "checkout",
			},
			DataSources: []v1alpha1.LocalObjectReference{{Name: "prometheus"}},
			Mode:        v1alpha1.InvestigationModeReadOnly,
		},
		Status: v1alpha1.InvestigationRequestStatus{
			ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseCompleted},
			Outcome:        v1alpha1.InvestigationOutcomeConfirmed,
			Verdict: &v1alpha1.RCAVerdict{
				Outcome:       v1alpha1.InvestigationOutcomeConfirmed,
				RootCauseType: "LatencyRegression",
				Confidence:    0.91,
			},
			Claims: []v1alpha1.RCAClaim{{
				Statement:    "checkout has a latency regression",
				Verification: "Supported",
				EvidenceRefs: []string{"evidence-001"},
			}},
			EvidenceRefs: []v1alpha1.EvidenceRef{{
				ID:      "evidence-001",
				Kind:    "MetricObservation",
				Source:  "prometheus",
				Summary: "p95 latency increased after rollout",
			}},
			Execution: &v1alpha1.RCAExecution{
				ID:                     "execution-001",
				Provider:               "heuristic-provider",
				RCASchemaVersion:       "fluxagent-rca-result-v1",
				VerifierVersion:        "fluxagent-verifier-v1",
				ReasoningPolicyVersion: "fluxagent-reasoning-policy-v1",
			},
		},
	}
}
