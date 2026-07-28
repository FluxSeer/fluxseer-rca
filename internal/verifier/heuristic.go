package verifier

import "strings"

const (
	VerificationSupported  = "Supported"
	VerificationInferred   = "Inferred"
	VerificationUnverified = "Unverified"

	MethodHeuristicEvidenceCoverageV1 = "HeuristicEvidenceCoverageV1"
)

type Claim struct {
	ID        string
	Statement string
}

type EvidenceRef struct {
	ID      string
	Kind    string
	Summary string
	Source  string
}

type ClaimResult struct {
	ID           string
	EvidenceRefs []string
	Verification string
}

type Result struct {
	Claims        []ClaimResult
	CoverageScore float64
	Method        string
}

func VerifyClaims(claims []Claim, evidence []EvidenceRef) Result {
	result := Result{
		Claims: make([]ClaimResult, 0, len(claims)),
		Method: MethodHeuristicEvidenceCoverageV1,
	}
	if len(claims) == 0 {
		return result
	}

	supported := 0
	for _, claim := range claims {
		matches := matchingEvidenceIDs(claim.Statement, evidence)
		verification := VerificationInferred
		if len(evidence) == 0 {
			verification = VerificationUnverified
		}
		if len(matches) > 0 {
			verification = VerificationSupported
			supported++
		}
		result.Claims = append(result.Claims, ClaimResult{
			ID:           claim.ID,
			EvidenceRefs: matches,
			Verification: verification,
		})
	}
	result.CoverageScore = float64(supported) / float64(len(claims))
	return result
}

func matchingEvidenceIDs(statement string, evidence []EvidenceRef) []string {
	statement = normalizeText(statement)
	if statement == "" {
		return nil
	}

	ids := make([]string, 0, len(evidence))
	for _, ref := range evidence {
		id := strings.TrimSpace(ref.ID)
		if id == "" {
			continue
		}
		if evidenceSupportsStatement(statement, ref) {
			ids = append(ids, id)
		}
	}
	return ids
}

func evidenceSupportsStatement(statement string, ref EvidenceRef) bool {
	evidenceText := normalizeText(ref.Kind + " " + ref.Source + " " + ref.Summary)
	if evidenceText == "" {
		return false
	}
	for _, token := range evidenceTerms(statement) {
		if strings.Contains(evidenceText, token) {
			return true
		}
	}
	return false
}

func evidenceTerms(statement string) []string {
	switch {
	case containsAny(statement, "crash", "restart", "backoff", "oom", "pod", "startup"):
		return []string{"event", "log", "deploymentcondition", "crash", "restart", "backoff", "oom", "pod", "startup"}
	case containsAny(statement, "latency", "timeout", "traffic", "http", "5xx", "error"):
		return []string{"metric", "log", "latency", "timeout", "traffic", "http", "5xx", "error"}
	case containsAny(statement, "cpu", "memory", "resource", "pressure"):
		return []string{"metric", "event", "cpu", "memory", "resource", "pressure", "oom"}
	case containsAny(statement, "rollout", "release", "image", "config", "configuration"):
		return []string{"event", "deploymentcondition", "rollout", "release", "image", "config", "configuration"}
	default:
		return importantTokens(statement)
	}
}

func importantTokens(text string) []string {
	parts := strings.Fields(text)
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) >= 5 {
			tokens = append(tokens, part)
		}
	}
	return tokens
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func normalizeText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer("_", " ", "-", " ", ".", " ", "/", " ")
	return strings.Join(strings.Fields(replacer.Replace(text)), " ")
}
