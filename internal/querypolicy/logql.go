package querypolicy

import (
	"fmt"
	"strings"
	"unicode"

	"fluxseer/api/v1alpha1"
)

func validateLogQLPolicy(policy v1alpha1.LogQLPolicy, req Request) Decision {
	stages := logQLPipelineStages(stripQuotedContent(req.Query))
	if denied := firstDenied(stages, policy.DeniedPipelineStages); denied != "" {
		return Decision{Decision: DecisionRejected, Reason: ReasonPipelineStageDenied, Message: fmt.Sprintf("LogQL pipeline stage %q is denied by datasource queryPolicy.loki", denied)}
	}
	if notAllowed := firstNotAllowed(stages, policy.AllowedPipelineStages); notAllowed != "" {
		return Decision{Decision: DecisionRejected, Reason: ReasonPipelineStageNotAllowed, Message: fmt.Sprintf("LogQL pipeline stage %q is not allowed by datasource queryPolicy.loki.allowedPipelineStages", notAllowed)}
	}
	return Decision{Decision: DecisionAllowed, Reason: ReasonAllowed, Message: "LogQL syntax policy allowed query"}
}

func logQLPipelineStages(query string) []string {
	stages := make([]string, 0)
	seen := map[string]struct{}{}
	for i := 0; i < len(query); i++ {
		stage, next, ok := nextLogQLStage(query, i)
		if !ok {
			continue
		}
		i = next
		if _, found := seen[stage]; found {
			continue
		}
		seen[stage] = struct{}{}
		stages = append(stages, stage)
	}
	return stages
}

func nextLogQLStage(query string, index int) (string, int, bool) {
	switch {
	case strings.HasPrefix(query[index:], "|="), strings.HasPrefix(query[index:], "!="):
		return "line_filter", index + 1, true
	case strings.HasPrefix(query[index:], "|~"), strings.HasPrefix(query[index:], "!~"):
		return "regex_filter", index + 1, true
	case query[index] != '|':
		return "", index, false
	}
	i := skipSpaces(query, index+1)
	if i >= len(query) || !isLogQLStageStart(rune(query[i])) {
		return "", index, false
	}
	start := i
	for i < len(query) && isLogQLStagePart(rune(query[i])) {
		i++
	}
	return strings.ToLower(query[start:i]), i, true
}

func isLogQLStageStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isLogQLStagePart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
