package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

type StructuredOutput struct {
	RiskTitle       string   `json:"riskTitle"`
	RiskSummary     string   `json:"riskSummary"`
	Severity        string   `json:"severity"`
	ConfidenceScore *int     `json:"confidenceScore"`
	Rationale       string   `json:"rationale"`
	RCAHypothesis   string   `json:"rcaHypothesis"`
	RCACauses       []string `json:"rcaCauses"`
	ActionType      string   `json:"actionType"`
}

func StructuredSystemPrompt(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "Transform observability signals into guarded SRE reasoning output."
	}
	return base + "\nReturn only valid JSON with keys riskTitle, riskSummary, severity, confidenceScore, rationale, rcaHypothesis, rcaCauses, actionType."
}

func StructuredUserPrompt(req domain.ModelRequest) (string, error) {
	payload, err := json.MarshalIndent(struct {
		Messages []domain.ModelMessage `json:"messages,omitempty"`
		Context  map[string]any        `json:"context,omitempty"`
	}{
		Messages: req.Messages,
		Context:  req.Context,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal provider prompt: %w", err)
	}
	return "Analyze this incident context and return JSON only.\n" + string(payload), nil
}

func ParseStructuredText(provider, modelName, text string) (domain.ModelResponse, error) {
	payload, err := decodeStructuredOutput(text)
	if err != nil {
		return domain.ModelResponse{}, err
	}
	return buildStructuredResponse(provider, modelName, payload), nil
}

func WithProviderRequestID(resp domain.ModelResponse, requestID string) domain.ModelResponse {
	resp.ProviderRequestID = strings.TrimSpace(requestID)
	return resp
}

func ValidateModelResponse(resp domain.ModelResponse) error {
	if !resp.Structured {
		return &ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: "provider returned unstructured response",
		}
	}
	_, err := normalizedStructuredOutput(resp.Output)
	return err
}

func buildStructuredResponse(provider, modelName string, payload StructuredOutput) domain.ModelResponse {
	return domain.ModelResponse{
		Provider:   provider,
		Model:      modelName,
		Structured: true,
		Output: map[string]any{
			"riskTitle":       payload.RiskTitle,
			"riskSummary":     payload.RiskSummary,
			"severity":        payload.Severity,
			"confidenceScore": *payload.ConfidenceScore,
			"rationale":       payload.Rationale,
			"rcaHypothesis":   payload.RCAHypothesis,
			"rcaCauses":       payload.RCACauses,
			"actionType":      payload.ActionType,
		},
		RawText: payload.RiskSummary,
	}
}

func decodeStructuredOutput(text string) (StructuredOutput, error) {
	jsonText := extractJSON(strings.TrimSpace(text))
	var payload StructuredOutput
	if err := json.Unmarshal([]byte(jsonText), &payload); err != nil {
		return StructuredOutput{}, &ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: fmt.Sprintf("decode structured provider response: %v", err),
		}
	}
	return normalizeStructuredOutput(payload)
}

func normalizedStructuredOutput(output map[string]any) (StructuredOutput, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return StructuredOutput{}, &ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: fmt.Sprintf("marshal structured provider output: %v", err),
		}
	}
	var payload StructuredOutput
	if err := json.Unmarshal(raw, &payload); err != nil {
		return StructuredOutput{}, &ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: fmt.Sprintf("decode structured provider output: %v", err),
		}
	}
	return normalizeStructuredOutput(payload)
}

func normalizeStructuredOutput(payload StructuredOutput) (StructuredOutput, error) {
	payload.RiskTitle = strings.TrimSpace(payload.RiskTitle)
	payload.RiskSummary = strings.TrimSpace(payload.RiskSummary)
	payload.Severity = strings.ToLower(strings.TrimSpace(payload.Severity))
	payload.Rationale = strings.TrimSpace(payload.Rationale)
	payload.RCAHypothesis = strings.TrimSpace(payload.RCAHypothesis)
	payload.ActionType = strings.TrimSpace(payload.ActionType)

	if payload.RiskTitle == "" || payload.RiskSummary == "" || payload.Rationale == "" || payload.RCAHypothesis == "" || payload.ActionType == "" {
		return StructuredOutput{}, &ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: "provider response is missing required RCA fields",
		}
	}
	if payload.ConfidenceScore == nil {
		return StructuredOutput{}, &ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: "provider response is missing confidenceScore",
		}
	}
	switch payload.Severity {
	case string(domain.SeverityLow), string(domain.SeverityMedium), string(domain.SeverityHigh), string(domain.SeverityUnsafe):
	default:
		return StructuredOutput{}, &ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: fmt.Sprintf("provider response used unsupported severity %q", payload.Severity),
		}
	}
	if *payload.ConfidenceScore < 0 || *payload.ConfidenceScore > 100 {
		return StructuredOutput{}, &ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: fmt.Sprintf("provider response confidenceScore %d is out of range", *payload.ConfidenceScore),
		}
	}
	causes := make([]string, 0, len(payload.RCACauses))
	for _, cause := range payload.RCACauses {
		if trimmed := strings.TrimSpace(cause); trimmed != "" {
			causes = append(causes, trimmed)
		}
	}
	if len(causes) == 0 {
		return StructuredOutput{}, &ProviderError{
			Reason:  "InvalidProviderResponse",
			Message: "provider response is missing rcaCauses",
		}
	}
	payload.RCACauses = causes
	return payload, nil
}

func extractJSON(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end >= start {
		return text[start : end+1]
	}
	return text
}
