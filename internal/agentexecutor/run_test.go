package agentexecutor

import (
	"encoding/json"
	"testing"
)

func TestParseAnalysisOutputFromDirectJSON(t *testing.T) {
	output := []byte(`{"summary":"pods crash","rootCause":"bad image","confidence":0.82,"validationSteps":["check rollout"],"recommendations":["rollback"],"missingEvidence":["logs"]}`)
	parsed, err := parseAnalysisOutput(output)
	if err != nil {
		t.Fatalf("parse output: %v", err)
	}
	if parsed.Summary != "pods crash" || parsed.RootCause != "bad image" || parsed.Confidence != 0.82 {
		t.Fatalf("unexpected parsed payload: %#v", parsed)
	}
}

func TestParseAnalysisOutputFromJSONLTextPayload(t *testing.T) {
	inner := `{"summary":"latency increased","rootCause":"database saturation","confidence":0.71,"validationSteps":["inspect db metrics"]}`
	line, err := json.Marshal(map[string]any{
		"type": "message",
		"content": []any{
			map[string]any{"type": "output_text", "text": "analysis follows\n```json\n" + inner + "\n```"},
		},
	})
	if err != nil {
		t.Fatalf("marshal line: %v", err)
	}
	parsed, err := parseAnalysisOutput(append(line, '\n'))
	if err != nil {
		t.Fatalf("parse output: %v", err)
	}
	if parsed.RootCause != "database saturation" || len(parsed.ValidationSteps) != 1 {
		t.Fatalf("unexpected parsed payload: %#v", parsed)
	}
}

func TestParseAnalysisOutputRejectsUnstructuredText(t *testing.T) {
	if _, err := parseAnalysisOutput([]byte("root cause is probably bad")); err == nil {
		t.Fatal("expected parse error")
	}
}
