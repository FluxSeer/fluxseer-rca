package controllers

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/executor"
)

func TestEvaluateEffectivenessOutcomes(t *testing.T) {
	tests := []struct {
		name         string
		verification string
		post         executor.HealthSnapshot
		observed     bool
		wantOutcome  string
	}{
		{
			name:         "effective requires improved health and cleared incident",
			verification: v1alpha1.InvestigationOutcomeNoIssueFound,
			post:         healthyDeployment(3),
			observed:     true,
			wantOutcome:  v1alpha1.EffectivenessOutcomeEffective,
		},
		{
			name:         "ineffective keeps original incident",
			verification: v1alpha1.InvestigationOutcomeConfirmed,
			post:         unhealthyDeployment(3, 0),
			observed:     true,
			wantOutcome:  v1alpha1.EffectivenessOutcomeIneffective,
		},
		{
			name:         "regressed has worse health even if evidence is clear",
			verification: v1alpha1.InvestigationOutcomeNoIssueFound,
			post:         unhealthyDeployment(3, 0),
			observed:     true,
			wantOutcome:  v1alpha1.EffectivenessOutcomeRegressed,
		},
		{
			name:         "inconclusive when evidence is unavailable",
			verification: v1alpha1.InvestigationOutcomeInconclusive,
			post:         healthyDeployment(3),
			observed:     false,
			wantOutcome:  v1alpha1.EffectivenessOutcomeInconclusive,
		},
		{
			name:         "no issue without improvement is not effective",
			verification: v1alpha1.InvestigationOutcomeNoIssueFound,
			post:         unhealthyDeployment(3, 0),
			observed:     true,
			wantOutcome:  v1alpha1.EffectivenessOutcomeIneffective,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verification := &v1alpha1.InvestigationRequest{Status: v1alpha1.InvestigationRequestStatus{
				ResourceStatus: v1alpha1.ResourceStatus{Phase: v1alpha1.PhaseCompleted},
				Outcome:        test.verification,
			}}
			baselineHealth := &v1alpha1.EffectivenessHealthSnapshot{
				DesiredReplicas:   3,
				UpdatedReplicas:   0,
				AvailableReplicas: 0,
				ReadyReplicas:     0,
				Conditions: []v1alpha1.EffectivenessHealthCondition{{
					Type:   "Available",
					Status: "False",
				}},
			}
			if test.wantOutcome == v1alpha1.EffectivenessOutcomeRegressed {
				baselineHealth.UpdatedReplicas = 2
				baselineHealth.AvailableReplicas = 2
				baselineHealth.ReadyReplicas = 2
				baselineHealth.Conditions[0].Status = "True"
			}
			baseline := &v1alpha1.EffectivenessBaseline{
				Digest: "sha256:baseline",
				Health: baselineHealth,
			}
			got := evaluateEffectiveness(baseline, test.post, test.observed, verification)
			if got.Outcome != test.wantOutcome {
				t.Fatalf("expected %s, got %#v", test.wantOutcome, got)
			}
		})
	}
}

func healthyDeployment(desired int32) executor.HealthSnapshot {
	return executor.HealthSnapshot{
		Generation:         2,
		ObservedGeneration: 2,
		DesiredReplicas:    desired,
		UpdatedReplicas:    desired,
		AvailableReplicas:  desired,
		ReadyReplicas:      desired,
		Conditions: []executor.HealthCondition{{
			Type:   "Available",
			Status: "True",
		}},
	}
}

func unhealthyDeployment(desired, available int32) executor.HealthSnapshot {
	return executor.HealthSnapshot{
		Generation:         2,
		ObservedGeneration: 2,
		DesiredReplicas:    desired,
		UpdatedReplicas:    available,
		AvailableReplicas:  available,
		ReadyReplicas:      available,
		Conditions: []executor.HealthCondition{{
			Type:   "Available",
			Status: "False",
		}},
	}
}
