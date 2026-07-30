package adaptive

import (
	"fmt"
	"strings"
)

type Config struct {
	Enabled          bool
	AllowedTemplates []string
	MaxSteps         int
	MaxQueries       int
}

type Request struct {
	DesiredTemplates []string
	Reason           string
}

type Plan struct {
	Enabled bool   `json:"enabled"`
	Steps   []Step `json:"steps,omitempty"`
	Issue   *Issue `json:"issue,omitempty"`
}

type Step struct {
	ID            string `json:"id"`
	QueryTemplate string `json:"queryTemplate"`
	Reason        string `json:"reason,omitempty"`
}

type Issue struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func PlanSteps(config Config, request Request) Plan {
	if !config.Enabled {
		return Plan{Enabled: false}
	}
	allowed := map[string]struct{}{}
	for _, name := range config.AllowedTemplates {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return Plan{Enabled: true, Issue: &Issue{Reason: "NoAllowedTemplates", Message: "adaptive investigation requires explicit allowed templates"}}
	}
	limit := config.MaxSteps
	if limit <= 0 || (config.MaxQueries > 0 && config.MaxQueries < limit) {
		limit = config.MaxQueries
	}
	if limit <= 0 {
		return Plan{Enabled: true, Issue: &Issue{Reason: "BudgetExhausted", Message: "adaptive investigation query budget is exhausted"}}
	}
	steps := []Step{}
	for _, template := range request.DesiredTemplates {
		template = strings.TrimSpace(template)
		if template == "" {
			continue
		}
		if _, ok := allowed[template]; !ok {
			return Plan{Enabled: true, Issue: &Issue{Reason: "TemplateNotAllowed", Message: fmt.Sprintf("adaptive query template %q is not allowlisted", template)}}
		}
		steps = append(steps, Step{
			ID:            fmt.Sprintf("adaptive-step-%03d", len(steps)+1),
			QueryTemplate: template,
			Reason:        strings.TrimSpace(request.Reason),
		})
		if len(steps) >= limit {
			break
		}
	}
	return Plan{Enabled: true, Steps: steps}
}
