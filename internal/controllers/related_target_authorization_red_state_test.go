package controllers

import (
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/authorization"
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

func TestRelatedTargetAuthorizationEnforcesRedStateReplay(t *testing.T) {
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
			evidence.RelatedTargetAuthorization = investigation.RelatedTargetAuthorizationContext{
				Ready: true,
				Decisions: []authorization.Decision{{
					EvidenceID:  testCase.EvidenceID,
					Target:      testCase.RelatedTarget,
					Disposition: replayDisposition(testCase.AfterFix.Coverage),
				}},
			}

			coverageAccepted := causalDependencyLogEvidencePresent(evidence)
			entity := causalRootCauseEntity([]v1alpha1.RCAClaim{{
				Verification: verifier.VerificationSupported,
				EvidenceRefs: []string{testCase.EvidenceID},
			}}, evidence)
			entityAccepted := entity != nil

			wantCoverage := testCase.AfterFix.Coverage == "Authorized"
			wantEntity := testCase.AfterFix.RootCauseEntity == "Authorized"
			if coverageAccepted != wantCoverage || entityAccepted != wantEntity {
				t.Fatalf("authorization enforcement mismatch: coverageAccepted=%t entityAccepted=%t want coverage=%t entity=%t", coverageAccepted, entityAccepted, wantCoverage, wantEntity)
			}
			if testCase.EvidenceAuthorized && (!testCase.TargetAuthorized || !testCase.TargetIdentityVerified) {
				t.Logf("RED_STATE baseline was accepted; fixed path now returns %s/%s reason=%s", testCase.AfterFix.Coverage, testCase.AfterFix.RootCauseEntity, testCase.ExpectedReason)
			}
		})
	}
}

func replayDisposition(disposition string) authorization.Disposition {
	if disposition == "Authorized" {
		return authorization.DispositionAuthorized
	}
	if disposition == "Rejected" {
		return authorization.DispositionRejected
	}
	return authorization.DispositionUnresolved
}
