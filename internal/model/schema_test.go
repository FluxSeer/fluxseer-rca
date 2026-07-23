package model

import (
	"strings"
	"testing"
)

func TestParseStructuredTextRejectsMissingConfidenceScore(t *testing.T) {
	_, err := ParseStructuredText("openai", "gpt-test", `{
		"riskTitle":"API degradation",
		"riskSummary":"error rate increased",
		"severity":"high",
		"rationale":"correlated telemetry",
		"rcaHypothesis":"recent deploy changed behavior",
		"rcaCauses":["rollout regression"],
		"actionType":"notification.sendSlack"
	}`)
	assertInvalidProviderResponse(t, err, "confidenceScore")
}

func TestParseStructuredTextRejectsEmptyRCACauses(t *testing.T) {
	_, err := ParseStructuredText("openai", "gpt-test", `{
		"riskTitle":"API degradation",
		"riskSummary":"error rate increased",
		"severity":"high",
		"confidenceScore":82,
		"rationale":"correlated telemetry",
		"rcaHypothesis":"recent deploy changed behavior",
		"rcaCauses":["  "],
		"actionType":"notification.sendSlack"
	}`)
	assertInvalidProviderResponse(t, err, "rcaCauses")
}

func TestParseStructuredTextNormalizesValidOutput(t *testing.T) {
	resp, err := ParseStructuredText("openai", "gpt-test", `{
		"riskTitle":" API degradation ",
		"riskSummary":" error rate increased ",
		"severity":"HIGH",
		"confidenceScore":82,
		"rationale":" correlated telemetry ",
		"rcaHypothesis":" recent deploy changed behavior ",
		"rcaCauses":[" rollout regression "],
		"actionType":" notification.sendSlack "
	}`)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if resp.Output["severity"] != "high" {
		t.Fatalf("expected normalized severity, got %#v", resp.Output["severity"])
	}
	causes, ok := resp.Output["rcaCauses"].([]string)
	if !ok || len(causes) != 1 || causes[0] != "rollout regression" {
		t.Fatalf("expected normalized rca causes, got %#v", resp.Output["rcaCauses"])
	}
}

func assertInvalidProviderResponse(t *testing.T, err error, messageFragment string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	providerErr, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected ProviderError, got %T", err)
	}
	if providerErr.Reason != "InvalidProviderResponse" {
		t.Fatalf("expected InvalidProviderResponse, got %q", providerErr.Reason)
	}
	if !strings.Contains(providerErr.Message, messageFragment) {
		t.Fatalf("expected message to contain %q, got %q", messageFragment, providerErr.Message)
	}
}
