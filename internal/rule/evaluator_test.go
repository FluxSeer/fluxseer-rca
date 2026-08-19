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

func TestEvaluateServiceConfigurationSignalRequiresConfirmedMismatch(t *testing.T) {
	match := EvaluateSignal(
		v1alpha1.RiskRuleSignal{
			Name:      "service-port-mismatch",
			QueryType: "serviceConfiguration",
			Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
		},
		domain.QueryTypeServiceConfiguration,
		&datasource.QueryResult{
			Source:    "kubernetes-events",
			QueryType: domain.QueryTypeServiceConfiguration,
			Records: []map[string]any{{
				"serviceName":        "checkout",
				"targetPortRaw":      "8080",
				"targetPortResolved": int32(8080),
				"workloadKind":       "Deployment",
				"workloadName":       "checkout",
				"containerName":      "app",
				"containerPort":      int32(3000),
				"mismatchConfirmed":  true,
				"reason":             "ServicePortMismatch",
			}},
		},
		domain.ResourceRef{Namespace: "demo", Kind: "Deployment", Name: "checkout"},
		"warning",
	)
	if match == nil || len(match.Evidence) != 1 {
		t.Fatalf("expected confirmed service configuration match, got %#v", match)
	}
	if match.Evidence[0].Kind != "serviceConfiguration" || match.Evidence[0].Reason != "ServicePortMismatch" {
		t.Fatalf("expected normalized service configuration evidence, got %#v", match.Evidence[0])
	}

	noMatch := EvaluateSignal(
		v1alpha1.RiskRuleSignal{Name: "service-port-mismatch", QueryType: "serviceConfiguration", Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0}},
		domain.QueryTypeServiceConfiguration,
		&datasource.QueryResult{Source: "kubernetes-events", QueryType: domain.QueryTypeServiceConfiguration, Records: []map[string]any{{"mismatchConfirmed": false}}},
		domain.ResourceRef{Name: "checkout"},
		"warning",
	)
	if noMatch != nil {
		t.Fatalf("expected unresolved/resolved service evidence not to trigger mismatch, got %#v", noMatch)
	}
}

func TestParseServiceConfigurationQueryType(t *testing.T) {
	queryType, ok := ParseQueryType("serviceConfiguration")
	if !ok || queryType != domain.QueryTypeServiceConfiguration {
		t.Fatalf("expected serviceConfiguration query type, got %q, %t", queryType, ok)
	}
}

func TestEvaluateProbeConfigurationSignalRequiresConfirmedMismatch(t *testing.T) {
	signal := v1alpha1.RiskRuleSignal{
		Name:      "probe-configuration-mismatch",
		QueryType: "probeConfiguration",
		Threshold: v1alpha1.RiskThreshold{Operator: "count_gt", Value: 0},
	}
	match := EvaluateSignal(signal, domain.QueryTypeProbeConfiguration, &datasource.QueryResult{
		Source:    "kubernetes-events",
		QueryType: domain.QueryTypeProbeConfiguration,
		Records: []map[string]any{{
			"probeType":         "readiness",
			"probePath":         "/ready",
			"probeScheme":       "HTTP",
			"probePortRaw":      "8080",
			"containerPort":     int32(3000),
			"resolution":        "NumericProbePortDoesNotMatchContainerPort",
			"mismatchConfirmed": true,
			"reason":            "ProbeConfigurationMismatch",
		}},
	}, domain.ResourceRef{Namespace: "demo", Kind: "Deployment", Name: "checkout"}, "high")
	if match == nil || len(match.Evidence) != 1 || match.Evidence[0].Kind != "probeConfiguration" || match.Evidence[0].Reason != "ProbeConfigurationMismatch" {
		t.Fatalf("expected confirmed probe configuration match, got %#v", match)
	}

	noMatch := EvaluateSignal(signal, domain.QueryTypeProbeConfiguration, &datasource.QueryResult{
		Source:    "kubernetes-events",
		QueryType: domain.QueryTypeProbeConfiguration,
		Records:   []map[string]any{{"mismatchConfirmed": false, "reason": "ProbeConfigurationResolved"}},
	}, domain.ResourceRef{Namespace: "demo", Kind: "Deployment", Name: "checkout"}, "high")
	if noMatch != nil {
		t.Fatalf("expected resolved probe configuration not to trigger mismatch, got %#v", noMatch)
	}
}

func TestParseProbeConfigurationQueryType(t *testing.T) {
	queryType, ok := ParseQueryType("probeConfiguration")
	if !ok || queryType != domain.QueryTypeProbeConfiguration {
		t.Fatalf("expected probeConfiguration query type, got %q, %t", queryType, ok)
	}
}
