package canonicaldigest

import (
	"testing"
	"time"
)

func TestStringIgnoresObjectKeyOrder(t *testing.T) {
	first := String(ObservationJSONV1, map[string]any{"b": "two", "a": "one"})
	second := String(ObservationJSONV1, map[string]any{"a": "one", "b": "two"})

	if first != second {
		t.Fatalf("expected object key order to be canonical, got first=%s second=%s", first, second)
	}
}

func TestStringNormalizesUnicode(t *testing.T) {
	first := String(ObservationJSONV1, map[string]any{"message": "\u00e9"})
	second := String(ObservationJSONV1, map[string]any{"message": "e\u0301"})

	if first != second {
		t.Fatalf("expected NFC unicode normalization, got first=%s second=%s", first, second)
	}
}

func TestSHA256RecordsDigestMetadata(t *testing.T) {
	result := SHA256(ObservationJSONV1, map[string]any{"message": "stable"})

	if result.Algorithm != AlgorithmSHA256 {
		t.Fatalf("expected sha256 algorithm, got %#v", result)
	}
	if result.Canonicalization != ObservationJSONV1 {
		t.Fatalf("expected canonicalization version, got %#v", result)
	}
	if len(result.Value) != len("sha256:")+64 {
		t.Fatalf("expected sha256 value, got %#v", result)
	}
}

func TestCanonicalTimeUsesUTC(t *testing.T) {
	local := time.Date(2026, 7, 6, 20, 0, 0, 0, time.FixedZone("TST", 8*60*60))

	if got := CanonicalTime(local); got != "2026-07-06T12:00:00Z" {
		t.Fatalf("expected UTC canonical time, got %q", got)
	}
}
