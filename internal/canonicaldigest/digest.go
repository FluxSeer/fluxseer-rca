package canonicaldigest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/text/unicode/norm"
)

const (
	AlgorithmSHA256   = "sha256"
	ObservationJSONV1 = "fluxseer-rca-observation-json-v1"
	RCAJSONV1         = "fluxseer-rca-json-v1"
)

type Result struct {
	Algorithm        string `json:"algorithm,omitempty"`
	Canonicalization string `json:"canonicalization,omitempty"`
	Value            string `json:"value,omitempty"`
}

func SHA256(canonicalization string, value any) Result {
	payload, err := CanonicalJSON(value)
	if err != nil {
		payload = []byte(fmt.Sprint(value))
	}
	sum := sha256.Sum256(payload)
	return Result{
		Algorithm:        AlgorithmSHA256,
		Canonicalization: canonicalization,
		Value:            AlgorithmSHA256 + ":" + hex.EncodeToString(sum[:]),
	}
}

func String(canonicalization string, value any) string {
	return SHA256(canonicalization, value).Value
}

func CanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return json.Marshal(normalize(decoded))
}

func CanonicalTime(value time.Time) string {
	return value.UTC().Round(0).Format(time.RFC3339Nano)
}

func normalize(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[norm.NFC.String(key)] = normalize(value)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = normalize(typed[i])
		}
		return out
	case string:
		return norm.NFC.String(typed)
	default:
		return typed
	}
}
