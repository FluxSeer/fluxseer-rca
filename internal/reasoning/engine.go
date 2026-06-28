package reasoning

import (
	"context"

	"fluxagent/internal/domain"
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model"
)

type Engine struct {
	Base     *knowledge.Base
	Provider model.Provider
}

func NewEngine(base *knowledge.Base, provider model.Provider) *Engine {
	return &Engine{
		Base:     base,
		Provider: provider,
	}
}

func (e *Engine) Analyze(ctx context.Context, input domain.IngestionOutput) (domain.ReasoningOutput, error) {
	runbooks, docs := e.Base.Lookup(input.Context.Resource, input.Context.Summary)
	response, err := e.Provider.Complete(ctx, domain.ModelRequest{
		ProviderHint: e.Provider.Name(),
		SystemPrompt: "Transform observability signals into guarded SRE reasoning output.",
		Messages: []domain.ModelMessage{
			{
				Role:    "user",
				Content: input.Context.Summary,
			},
		},
		Context: map[string]any{
			"service":  input.Context.Service,
			"resource": input.Context.Resource,
			"summary":  input.Context.Summary,
			"signals":  input.Signals,
			"evidence": input.Evidence,
			"timeline": input.Timeline.Events,
			"runbooks": runbooks,
			"docs":     docs,
		},
	})
	if err != nil {
		return domain.ReasoningOutput{}, err
	}

	output := response.Output
	severity := domain.Severity(asString(output["severity"], string(domain.SeverityLow)))
	actionType := asString(output["actionType"], "kubernetes.scaleDeployment")
	score := asInt(output["confidenceScore"], 35)
	riskTitle := asString(output["riskTitle"], "Workload degradation")
	riskSummary := asString(output["riskSummary"], "Service degradation detected from correlated signals.")
	rationale := asString(output["rationale"], "default heuristic")
	rcaHypothesis := asString(output["rcaHypothesis"], "Deployment change increased memory pressure and restart frequency.")

	remediation := domain.Remediation{
		ActionType:  actionType,
		Description: remediationDescription(actionType),
		Parameters:  remediationParameters(actionType),
		RollbackPlan: []string{
			"Scale deployment back to 4 replicas",
			"Revert deployment image if restart rate remains elevated",
		},
	}

	return domain.ReasoningOutput{
		RiskTitle:   riskTitle,
		RiskSummary: riskSummary,
		Severity:    severity,
		Confidence: domain.Confidence{
			Score:            score,
			Severity:         severity,
			Rationale:        rationale,
			EvidenceCoverage: "metrics+events+deployment-context",
		},
		RCA: domain.RCASummary{
			Hypothesis: rcaHypothesis,
			Causes: []string{
				"Recent rollout changed workload behavior",
				"Pod memory usage crossed safe threshold",
			},
			Evidence: append([]string{}, input.Evidence.Events...),
		},
		Remediation: remediation,
		RunbookRefs: runbooks,
		ServiceDocs: docs,
		Provider:    response.Provider,
	}, nil
}

func asString(value any, fallback string) string {
	text, ok := value.(string)
	if !ok || text == "" {
		return fallback
	}
	return text
}

func asInt(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return fallback
	}
}

func remediationDescription(actionType string) string {
	switch actionType {
	case "kubernetes.rolloutPause":
		return "Pause the rollout to stop additional unhealthy replicas."
	case "notification.sendSlack":
		return "Send incident notification to the response channel."
	default:
		return "Scale the deployment to stabilize traffic while investigating the new release."
	}
}

func remediationParameters(actionType string) map[string]string {
	switch actionType {
	case "kubernetes.rolloutPause":
		return map[string]string{"reason": "automatic-guardrail-pause"}
	case "notification.sendSlack":
		return map[string]string{"channel": "slack-webhook"}
	default:
		return map[string]string{"replicas": "6"}
	}
}
