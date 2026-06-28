package evidence

import (
	"regexp"

	"fluxagent/internal/domain"
)

type Redactor interface {
	RedactIngestion(input domain.IngestionOutput) domain.IngestionOutput
	RedactText(input string) string
}

type PatternRedactor struct {
	rules []redactionRule
}

type redactionRule struct {
	pattern     *regexp.Regexp
	replacement string
}

func NewPatternRedactor() PatternRedactor {
	return PatternRedactor{
		rules: []redactionRule{
			{
				pattern:     regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)[^\s"']+`),
				replacement: `${1}[REDACTED]`,
			},
			{
				pattern:     regexp.MustCompile(`(?i)\b(api[-_ ]?key|token|password|secret)(\s*[=:]\s*|\s+)[^\s",;]+`),
				replacement: `${1}${2}[REDACTED]`,
			},
		},
	}
}

func (r PatternRedactor) RedactIngestion(input domain.IngestionOutput) domain.IngestionOutput {
	input.Context.Summary = r.RedactText(input.Context.Summary)
	for i := range input.Context.Signals {
		input.Context.Signals[i].Message = r.RedactText(input.Context.Signals[i].Message)
	}
	for i := range input.Signals {
		input.Signals[i].Message = r.RedactText(input.Signals[i].Message)
	}
	for i := range input.Timeline.Events {
		input.Timeline.Events[i].Summary = r.RedactText(input.Timeline.Events[i].Summary)
	}
	for i := range input.Evidence.Logs {
		input.Evidence.Logs[i] = r.RedactText(input.Evidence.Logs[i])
	}
	for i := range input.Evidence.Events {
		input.Evidence.Events[i] = r.RedactText(input.Evidence.Events[i])
	}
	for i := range input.Evidence.Traces {
		input.Evidence.Traces[i] = r.RedactText(input.Evidence.Traces[i])
	}
	for i := range input.Evidence.References {
		input.Evidence.References[i].Summary = r.RedactText(input.Evidence.References[i].Summary)
		input.Evidence.References[i].Query = r.RedactText(input.Evidence.References[i].Query)
		input.Evidence.References[i].Reason = r.RedactText(input.Evidence.References[i].Reason)
		input.Evidence.References[i].Link = r.RedactText(input.Evidence.References[i].Link)
	}
	for key, value := range input.Context.Metadata {
		input.Context.Metadata[key] = r.RedactText(value)
	}
	return input
}

func (r PatternRedactor) RedactText(input string) string {
	out := input
	for _, rule := range r.rules {
		out = rule.pattern.ReplaceAllString(out, rule.replacement)
	}
	return out
}
