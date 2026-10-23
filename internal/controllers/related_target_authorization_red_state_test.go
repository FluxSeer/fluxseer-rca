package controllers

import (
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/investigation"
	"github.com/FluxSeer/fluxseer-rca/internal/verifier"
)

//go:embed testdata/related-target-authorization/red-state-v1.json
var relatedTargetAuthorizationRedStateFixture []byte

type relatedTargetAuthorizationFixture struct {
	SchemaVersion string `json:"schemaVersion"`
	Baseline      struct {
		Release string `json:"release"`
		Commit  string `json:"commit"`
		Probe   string `json:"probe"`
	} `json:"baseline"`
	Cases []struct {
		Name                   string             `json:"name"`
		EvidenceID             string             `json:"evidenceID"`
		EvidenceAuthorized     bool               `json:"evidenceAuthorized"`
		TargetAuthorized       bool               `json:"targetAuthorized"`
		TargetIdentityVerified bool               `json:"targetIdentityVerified"`
		ExpectedReason         string             `json:"expectedReason"`
		RelatedTarget          v1alpha1.TargetRef `json:"relatedTarget"`
		Baseline               struct {
			CoverageAccepted        bool `json:"coverageAccepted"`
			RootCauseEntityAccepted bool `json:"rootCauseEntityAccepted"`
		} `json:"baseline"`
		AfterFix struct {
			Coverage        string `json:"coverage"`
			RootCauseEntity string `json:"rootCauseEntity"`
		} `json:"afterFix"`
	} `json:"cases"`
}

func TestRelatedTargetAuthorizationRedStateReplay(t *testing.T) {
	var fixture relatedTargetAuthorizationFixture
	if err := json.Unmarshal(relatedTargetAuthorizationRedStateFixture, &fixture); err != nil {
		t.Fatalf("decode related-target authorization fixture: %v", err)
	}
	if fixture.SchemaVersion != "fluxseer-related-target-authorization-fixture/v1" {
		t.Fatalf("unexpected fixture schema %q", fixture.SchemaVersion)
	}
	if fixture.Baseline.Release != "v0.5.0-alpha.3" || fixture.Baseline.Commit != "6ac8e92c333278de65f79e401db2fefc2c124a60" {
		t.Fatalf("fixture is not pinned to the released baseline: %#v", fixture.Baseline)
	}

	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			evidence := investigation.EvidenceCollectionResult{}
			if testCase.EvidenceID != "log-missing-from-inventory" {
				evidence.EvidenceRefs = []v1alpha1.EvidenceRef{{
					ID:             testCase.EvidenceID,
					Kind:           string(domain.QueryTypeLog),
					Source:         "loki",
					Summary:        "upstream inventory unavailable: connection refused",
					RelatedTargets: []v1alpha1.TargetRef{testCase.RelatedTarget},
				}}
			}

			coverageAccepted := causalDependencyLogEvidencePresent(evidence)
			entity := causalRootCauseEntity([]v1alpha1.RCAClaim{{
				Verification: verifier.VerificationSupported,
				EvidenceRefs: []string{testCase.EvidenceID},
			}}, evidence)
			entityAccepted := entity != nil

			if coverageAccepted != testCase.Baseline.CoverageAccepted || entityAccepted != testCase.Baseline.RootCauseEntityAccepted {
				t.Fatalf("baseline behavior changed unexpectedly: coverageAccepted=%t entityAccepted=%t want coverage=%t entity=%t", coverageAccepted, entityAccepted, testCase.Baseline.CoverageAccepted, testCase.Baseline.RootCauseEntityAccepted)
			}
			if testCase.EvidenceAuthorized && (!testCase.TargetAuthorized || !testCase.TargetIdentityVerified) {
				if !coverageAccepted || !entityAccepted {
					t.Fatalf("red-state probe did not reproduce baseline acceptance for unauthorized/unverified target: coverage=%t entity=%t", coverageAccepted, entityAccepted)
				}
				t.Logf("RED_STATE baseline accepted target: evidenceAuthorized=%t targetAuthorized=%t targetIdentityVerified=%t expectedPostFix=%s/%s reason=%s", testCase.EvidenceAuthorized, testCase.TargetAuthorized, testCase.TargetIdentityVerified, testCase.AfterFix.Coverage, testCase.AfterFix.RootCauseEntity, testCase.ExpectedReason)
			} else if !testCase.EvidenceAuthorized && (coverageAccepted || entityAccepted) {
				t.Fatalf("unauthorized evidence unexpectedly influenced baseline path: coverage=%t entity=%t", coverageAccepted, entityAccepted)
			}
		})
	}
}
