package heuristic

import (
	"context"
	"strings"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

type Provider struct{}

func (p Provider) Name() string {
	return "heuristic"
}

func (p Provider) Complete(_ context.Context, req domain.ModelRequest) (domain.ModelResponse, error) {
	summary := ""
	if len(req.Messages) > 0 {
		summary = req.Messages[len(req.Messages)-1].Content
	}

	severity := string(domain.SeverityLow)
	actionType := "kubernetes.scaleDeployment"
	title := "Workload degradation"
	riskSummary := "Service degradation detected from correlated signals."
	rca := "Deployment change increased memory pressure and restart frequency."
	rationale := "default heuristic"
	score := 35

	if strings.Contains(strings.ToLower(summary), "oomkilled") || strings.Contains(strings.ToLower(summary), "backoff") {
		severity = string(domain.SeverityHigh)
		actionType = "kubernetes.rolloutPause"
		title = "Crash loop after rollout"
		riskSummary = "Pods are restarting and entering backoff after a deployment change."
		rca = "A recent release likely introduced elevated memory or startup failures."
		rationale = "OOMKilled and BackOff signals raise blast-radius risk"
		score = 86
	}

	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      "rule-based",
		Structured: true,
		Output: map[string]any{
			"riskTitle":       title,
			"riskSummary":     riskSummary,
			"severity":        severity,
			"confidenceScore": score,
			"rationale":       rationale,
			"rcaHypothesis":   rca,
			"rcaCauses":       []string{"Recent rollout changed workload behavior", "Pod memory usage crossed safe threshold"},
			"actionType":      actionType,
		},
		RawText: riskSummary,
	}, nil
}
