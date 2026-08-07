package rule

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/datasource"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func TestEvaluateDeploymentConditionSignalMatchesUnavailableDeployment(t *testing.T) {
	match := EvaluateSignal(
		v1alpha1.RiskRuleSignal{
			Name:      "deployment-unavailable",
			QueryType: "deploymentCondition",
			Reasons: []string{
				"Available=False",
				"MinimumReplicasUnavailable",
			},
			Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
		},
		domain.QueryTypeDeploymentCondition,
		&datasource.QueryResult{
			Source:    "kubernetes-events",
			QueryType: domain.QueryTypeDeploymentCondition,
			Records: []map[string]any{
				{
					"type":    "Available",
					"status":  "False",
					"reason":  "MinimumReplicasUnavailable",
					"message": "Deployment does not have minimum availability.",
				},
			},
		},
		domain.ResourceRef{Namespace: "demo", Kind: "Deployment", Name: "demo-app"},
		"warning",
	)

	if match == nil {
		t.Fatal("expected deployment condition match")
	}
	if len(match.Evidence) == 0 || match.Evidence[0].Kind != "deploymentCondition" {
		t.Fatalf("expected deploymentCondition evidence, got %#v", match.Evidence)
	}
}

func TestEvaluateKubernetesEventSignalMatchesReasonExactly(t *testing.T) {
	result := &datasource.QueryResult{
		Source:    "kubernetes-events",
		QueryType: domain.QueryTypeEvent,
		Records: []map[string]any{
			{
				"reason":  "ImagePullBackOff",
				"message": "Back-off pulling image",
			},
		},
	}
	target := domain.ResourceRef{Namespace: "demo", Kind: "Deployment", Name: "demo-app"}

	crashLoop := EvaluateSignal(
		v1alpha1.RiskRuleSignal{
			Name:      "crashloop-backoff",
			QueryType: "event",
			Reasons:   []string{"CrashLoopBackOff", "BackOff"},
			Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
		},
		domain.QueryTypeEvent,
		result,
		target,
		"warning",
	)
	if crashLoop != nil {
		t.Fatalf("expected ImagePullBackOff not to match BackOff signal, got %#v", crashLoop)
	}

	imagePull := EvaluateSignal(
		v1alpha1.RiskRuleSignal{
			Name:      "image-pull-failure",
			QueryType: "event",
			Reasons:   []string{"ImagePullBackOff", "ErrImagePull"},
			Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
		},
		domain.QueryTypeEvent,
		result,
		target,
		"warning",
	)
	if imagePull == nil {
		t.Fatal("expected ImagePullBackOff to match image pull signal")
	}
	if len(imagePull.Evidence) != 1 || imagePull.Evidence[0].Reason != "ImagePullBackOff" {
		t.Fatalf("expected one ImagePullBackOff evidence item, got %#v", imagePull.Evidence)
	}
	if imagePull.Summary != "image-pull-failure detected 1 matching event for demo-app" {
		t.Fatalf("unexpected image pull summary: %q", imagePull.Summary)
	}
}

func TestEvaluateKubernetesEventSignalDescribesBackOffAsRestartBackoff(t *testing.T) {
	match := EvaluateSignal(
		v1alpha1.RiskRuleSignal{
			Name:      "crashloop-backoff",
			QueryType: "event",
			Reasons:   []string{"CrashLoopBackOff", "BackOff"},
			Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
		},
		domain.QueryTypeEvent,
		&datasource.QueryResult{
			Source:    "kubernetes-events",
			QueryType: domain.QueryTypeEvent,
			Records: []map[string]any{
				{
					"reason":  "BackOff",
					"message": "Back-off restarting failed container",
				},
			},
		},
		domain.ResourceRef{Namespace: "demo", Kind: "Deployment", Name: "demo-app"},
		"warning",
	)
	if match == nil {
		t.Fatal("expected BackOff match")
	}
	if match.Summary != "container restart backoff detected 1 matching event for demo-app" {
		t.Fatalf("unexpected BackOff summary: %q", match.Summary)
	}
}

func TestParseDeploymentConditionQueryType(t *testing.T) {
	queryType, ok := ParseQueryType("deploymentCondition")
	if !ok {
		t.Fatal("expected deploymentCondition query type to parse")
	}
	if queryType != domain.QueryTypeDeploymentCondition {
		t.Fatalf("expected %s, got %s", domain.QueryTypeDeploymentCondition, queryType)
	}
}
