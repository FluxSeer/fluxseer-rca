package dataclassification

import (
	"testing"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/domain"
)

func TestDefaultForObservationClassifiesLogSamplesAsConfidential(t *testing.T) {
	classification := DefaultForObservation(domain.QueryTypeLog, "timeout")
	if classification.Level != v1alpha1.DataClassificationLevelConfidential {
		t.Fatalf("expected confidential log classification, got %#v", classification)
	}
	if !contains(classification.SensitivityTags, v1alpha1.SensitivityTagInfrastructureMetadata) {
		t.Fatalf("expected infrastructure metadata tag, got %#v", classification)
	}
}

func TestDefaultForObservationDetectsCredentialLikeContent(t *testing.T) {
	classification := DefaultForObservation(domain.QueryTypeLog, "request failed token=secret-one")
	if classification.Level != v1alpha1.DataClassificationLevelRestricted {
		t.Fatalf("expected restricted credential-like classification, got %#v", classification)
	}
	if classification.Source != v1alpha1.DataClassificationSourceContentDetection {
		t.Fatalf("expected content detection source, got %#v", classification)
	}
	if !contains(classification.SensitivityTags, v1alpha1.SensitivityTagCredentialLike) {
		t.Fatalf("expected credential-like tag, got %#v", classification)
	}
}

func TestBundleClassificationUsesHighestLevelAndTagUnion(t *testing.T) {
	classification := BundleClassification([]v1alpha1.EvidenceRef{
		{
			Kind: "metric",
			Classification: &v1alpha1.DataClassification{
				Level:           v1alpha1.DataClassificationLevelInternal,
				SensitivityTags: []string{v1alpha1.SensitivityTagInfrastructureMetadata},
			},
		},
		{
			Kind: "log",
			Classification: &v1alpha1.DataClassification{
				Level:           v1alpha1.DataClassificationLevelRestricted,
				SensitivityTags: []string{v1alpha1.SensitivityTagCredentialLike},
			},
		},
	})
	if classification.Level != v1alpha1.DataClassificationLevelRestricted {
		t.Fatalf("expected restricted bundle, got %#v", classification)
	}
	if !contains(classification.SensitivityTags, v1alpha1.SensitivityTagCredentialLike) || !contains(classification.SensitivityTags, v1alpha1.SensitivityTagInfrastructureMetadata) {
		t.Fatalf("expected union sensitivity tags, got %#v", classification)
	}
}

func TestEvaluateProviderPolicyRejectsClassificationAndTags(t *testing.T) {
	refs := []v1alpha1.EvidenceRef{
		{
			Kind:             "log",
			RedactionProfile: "default-v1",
			Classification: &v1alpha1.DataClassification{
				Level:           v1alpha1.DataClassificationLevelRestricted,
				SensitivityTags: []string{v1alpha1.SensitivityTagCredentialLike},
			},
		},
	}

	classificationDecision := EvaluateProviderPolicy(v1alpha1.ModelProviderDataPolicy{MaximumClassification: "Internal"}, refs)
	if classificationDecision.Decision != DecisionRejected || classificationDecision.Reason != ReasonClassificationExceeded {
		t.Fatalf("expected classification rejection, got %#v", classificationDecision)
	}

	tagDecision := EvaluateProviderPolicy(v1alpha1.ModelProviderDataPolicy{MaximumClassification: "Restricted", DeniedSensitivityTags: []string{"CredentialLike"}}, refs)
	if tagDecision.Decision != DecisionRejected || tagDecision.Reason != ReasonSensitivityTagDenied {
		t.Fatalf("expected tag rejection, got %#v", tagDecision)
	}
}

func TestEvaluateProviderPolicyTreatsUnknownLevelAsRestricted(t *testing.T) {
	refs := []v1alpha1.EvidenceRef{
		{
			Kind: "metric",
			Classification: &v1alpha1.DataClassification{
				Level: "UnknownFutureLevel",
			},
		},
	}

	decision := EvaluateProviderPolicy(v1alpha1.ModelProviderDataPolicy{MaximumClassification: "Internal"}, refs)
	if decision.Decision != DecisionRejected || decision.Reason != ReasonClassificationExceeded {
		t.Fatalf("expected unknown level to be rejected conservatively, got %#v", decision)
	}
}

func TestEvaluateProviderPolicyRequiresRedactionWithoutDowngradingClassification(t *testing.T) {
	refs := []v1alpha1.EvidenceRef{
		{
			Kind: "log",
			Classification: &v1alpha1.DataClassification{
				Level:           v1alpha1.DataClassificationLevelConfidential,
				SensitivityTags: []string{v1alpha1.SensitivityTagInfrastructureMetadata},
			},
		},
	}
	decision := EvaluateProviderPolicy(v1alpha1.ModelProviderDataPolicy{MaximumClassification: "Confidential", RequireRedaction: true}, refs)
	if decision.Decision != DecisionRejected || decision.Reason != ReasonRedactionRequired {
		t.Fatalf("expected redaction requirement rejection, got %#v", decision)
	}
	if decision.MaximumObserved != v1alpha1.DataClassificationLevelConfidential {
		t.Fatalf("expected redaction not to downgrade classification, got %#v", decision)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
