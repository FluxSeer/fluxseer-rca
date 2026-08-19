package controllers

import (
	"reflect"
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func TestInvestigationProjectionOutcomeContract(t *testing.T) {
	target := domain.ResourceRef{
		Cluster:    "cluster-a",
		Namespace:  "prod",
		Kind:       "Pod",
		Name:       "failed-scheduling",
		APIVersion: "v1",
	}
	wantEntity := v1alpha1.TargetRef{
		Cluster:    target.Cluster,
		Namespace:  target.Namespace,
		Kind:       target.Kind,
		Name:       target.Name,
		APIVersion: target.APIVersion,
	}

	tests := []struct {
		name           string
		outcome        string
		wantEntity     v1alpha1.TargetRef
		wantProjection bool
	}{
		{
			name:           "Confirmed",
			outcome:        v1alpha1.InvestigationOutcomeConfirmed,
			wantEntity:     wantEntity,
			wantProjection: true,
		},
		{
			name:           "Inconclusive",
			outcome:        v1alpha1.InvestigationOutcomeInconclusive,
			wantEntity:     v1alpha1.TargetRef{},
			wantProjection: false,
		},
		{
			name:           "NoIssueFound",
			outcome:        v1alpha1.InvestigationOutcomeNoIssueFound,
			wantEntity:     v1alpha1.TargetRef{},
			wantProjection: false,
		},
		{
			name:           "Failed/Unknown",
			outcome:        v1alpha1.InvestigationOutcomeUnknown,
			wantEntity:     v1alpha1.TargetRef{},
			wantProjection: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEntity := rootCauseEntityForInvestigationOutcome(tt.outcome, target)
			if !reflect.DeepEqual(gotEntity, tt.wantEntity) {
				t.Fatalf("root cause entity mismatch: got %#v want %#v", gotEntity, tt.wantEntity)
			}
			if got := investigationOutcomeAllowsRiskSignalProjection(tt.outcome); got != tt.wantProjection {
				t.Fatalf("risk signal projection permission mismatch: got %v want %v", got, tt.wantProjection)
			}
		})
	}
}
