package verifier

import "strings"

const (
	VerificationSupported    = "Supported"
	VerificationInferred     = "Inferred"
	VerificationUnsupported  = "Unsupported"
	VerificationContradicted = "Contradicted"
	VerificationUnverified   = "Unverified"

	MethodHeuristicEvidenceCoverageV1 = "HeuristicEvidenceCoverageV1"
	MethodDomainEvidenceCoverageV2    = "DomainEvidenceCoverageV2"

	EvidenceRoleSupports     = "Supports"
	EvidenceRoleContradicts  = "Contradicts"
	EvidenceStrengthDirect   = "Direct"
	EvidenceStrengthIndirect = "Indirect"
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
	Reason  string
}

type ClaimResult struct {
	ID            string
	EvidenceRefs  []string
	EvidenceLinks []EvidenceLink
	Verification  string
}

type EvidenceLink struct {
	EvidenceRef string
	Role        string
	Strength    string
}

type Result struct {
	Claims        []ClaimResult
	CoverageScore float64
	Method        string
}

func VerifyClaims(claims []Claim, evidence []EvidenceRef) Result {
	result := Result{
		Claims: make([]ClaimResult, 0, len(claims)),
		Method: MethodDomainEvidenceCoverageV2,
	}
	if len(claims) == 0 {
		return result
	}

	supported := 0
	for _, claim := range claims {
		matches, domainClaim := domainSupportEvidenceIDs(claim.Statement, evidence)
		if !domainClaim {
			matches = matchingEvidenceIDs(claim.Statement, evidence)
		}
		contradictions := contradictoryEvidenceIDs(claim.Statement, evidence)
		verification := VerificationInferred
		if len(evidence) == 0 {
			verification = VerificationUnverified
		}
		if len(evidence) > 0 && len(matches) == 0 {
			verification = VerificationUnsupported
		}
		links := evidenceLinks(matches, EvidenceRoleSupports, EvidenceStrengthDirect)
		if len(matches) > 0 {
			verification = VerificationSupported
			if len(contradictions) == 0 {
				supported++
			}
		}
		if len(contradictions) > 0 {
			verification = VerificationContradicted
			links = evidenceLinks(contradictions, EvidenceRoleContradicts, EvidenceStrengthDirect)
		}
		result.Claims = append(result.Claims, ClaimResult{
			ID:            claim.ID,
			EvidenceRefs:  matches,
			EvidenceLinks: links,
			Verification:  verification,
		})
	}
	result.CoverageScore = float64(supported) / float64(len(claims))
	return result
}

func contradictoryEvidenceIDs(statement string, evidence []EvidenceRef) []string {
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
		if evidenceContradictsStatement(statement, ref) {
			ids = append(ids, id)
		}
	}
	return ids
}

func evidenceLinks(ids []string, role string, strength string) []EvidenceLink {
	links := make([]EvidenceLink, 0, len(ids))
	for _, id := range ids {
		links = append(links, EvidenceLink{
			EvidenceRef: id,
			Role:        role,
			Strength:    strength,
		})
	}
	return links
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
	evidenceText := evidenceText(ref)
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

func evidenceContradictsStatement(statement string, ref EvidenceRef) bool {
	evidenceText := evidenceText(ref)
	if evidenceText == "" {
		return false
	}
	if !containsContradictionMarker(evidenceText) {
		return false
	}
	for _, token := range evidenceTerms(statement) {
		if strings.Contains(evidenceText, token) {
			return true
		}
	}
	return false
}

func containsContradictionMarker(text string) bool {
	if containsAny(text, "below threshold", "no error", "no errors") {
		return true
	}
	tokens := tokenSet(text)
	markers := map[string][]string{
		"normal":    {"not normal"},
		"healthy":   {"not healthy"},
		"ready":     {"not ready", "never ready"},
		"available": {"not available"},
	}
	for marker, negations := range markers {
		if tokens[marker] && !containsAny(text, negations...) {
			return true
		}
	}
	return false
}

func domainSupportEvidenceIDs(statement string, evidence []EvidenceRef) ([]string, bool) {
	profile := domainProfile(statement)
	if profile == "" {
		return nil, false
	}
	ids := make([]string, 0, len(evidence))
	requiredKinds := requiredDomainKinds(profile)
	coveredKinds := map[string]bool{}
	for _, ref := range evidence {
		id := strings.TrimSpace(ref.ID)
		if id == "" || !domainEvidenceSupports(profile, ref) {
			continue
		}
		ids = append(ids, id)
		coveredKinds[strings.ToLower(strings.TrimSpace(ref.Kind))] = true
	}
	for _, kind := range requiredKinds {
		if !coveredKinds[kind] {
			return nil, true
		}
	}
	return ids, true
}

func domainProfile(statement string) string {
	statement = normalizeText(statement)
	switch {
	case containsAny(statement, "image", "registry", "pull") && containsAny(statement, "missing image", "missing container image", "image is missing", "image does not exist", "image not found", "repository does not exist", "manifest unknown"):
		return "imagepullmissing"
	case containsAny(statement, "image", "registry", "pull") && containsAny(statement, "authentication", "unauthorized", "access denied", "pull access denied", "credential"):
		return "registryauth"
	case containsAny(statement, "image", "registry", "pull") && containsAny(statement, "dns", "name resolution", "no such host", "host lookup"):
		return "registrydns"
	case containsAny(statement, "image", "registry", "pull") && containsAny(statement, "registry unavailable", "registry is unavailable", "registry was unavailable", "registry outage", "registry is down", "registry timeout", "registry connection refused"):
		return "registryunavailable"
	case containsAny(statement, "imagepullbackoff", "errimagepull", "image pull", "pull image", "failed to pull"):
		return "imagepullbackoff"
	case containsAny(statement, "crashloopbackoff", "crash loop", "crashloop", "backoff", "restarting", "restart"):
		return "crashloopbackoff"
	case containsAny(statement, "oomkilled", "out of memory", "memory limit", "memory pressure", "memory usage", "memory threshold", "safe threshold"):
		return "memorypressure"
	case containsAny(statement, "latency regression", "high latency", "p95 latency", "p99 latency", "timeout", "slow response"):
		return "latencyregression"
	case containsAny(statement, "rollout", "release", "image digest", "pod template", "replicaset", "generation changed", "deployment change"):
		return "rollout"
	default:
		return ""
	}
}

func requiredDomainKinds(profile string) []string {
	switch profile {
	case "imagepullbackoff", "imagepullmissing", "registryauth", "registrydns", "registryunavailable", "crashloopbackoff":
		return []string{"event"}
	case "memorypressure":
		return []string{"event", "metric"}
	case "latencyregression":
		return []string{"metric"}
	case "rollout":
		return []string{"deploymentcondition"}
	default:
		return nil
	}
}

func domainEvidenceSupports(profile string, ref EvidenceRef) bool {
	kind := strings.ToLower(strings.TrimSpace(ref.Kind))
	text := evidenceText(ref)
	switch profile {
	case "imagepullbackoff":
		return kind == "event" && containsAny(text, "imagepullbackoff", "errimagepull", "failed to pull image", "pull access denied")
	case "imagepullmissing":
		return kind == "event" && containsAny(text, "manifest unknown", "image not found", "image does not exist", "repository does not exist", "no such image")
	case "registryauth":
		return kind == "event" && containsAny(text, "pull access denied", "unauthorized", "authentication required", "access denied", "denied: requested access")
	case "registrydns":
		return kind == "event" && containsAny(text, "no such host", "dns", "name resolution", "temporary failure in name resolution", "host lookup")
	case "registryunavailable":
		return kind == "event" && containsAny(text, "connection refused", "i/o timeout", "registry unavailable", "service unavailable", "registry timeout")
	case "crashloopbackoff":
		return kind == "event" && containsAny(text, "crashloopbackoff", "backoff", "back off", "container crashed", "restarting failed container")
	case "memorypressure":
		switch kind {
		case "event":
			return containsAny(text, "oomkilled", "out of memory", "memory limit")
		case "metric":
			return containsAny(text, "memory", "working set", "rss", "container_memory")
		default:
			return false
		}
	case "latencyregression":
		return kind == "metric" && containsAny(text, "latency", "duration", "response time", "p95", "p99", "histogram")
	case "rollout":
		return kind == "deploymentcondition" && containsAny(text, "generation", "replicaset", "replica set", "pod template", "image digest", "rollout", "progressing")
	default:
		return false
	}
}

func evidenceTerms(statement string) []string {
	switch {
	case containsAny(statement, "crash", "restart", "backoff", "oom", "pod", "startup"):
		return []string{"crash", "restart", "backoff", "oom", "pod", "startup"}
	case containsAny(statement, "cpu", "memory", "resource", "pressure"):
		return []string{"cpu", "memory", "resource", "pressure", "oom"}
	case containsAny(statement, "latency", "timeout", "traffic", "http", "5xx", "error"):
		return []string{"latency", "timeout", "traffic", "http", "5xx", "error"}
	case containsAny(statement, "rollout", "release", "image", "config", "configuration"):
		return []string{"rollout", "release", "image", "config", "configuration"}
	default:
		return importantTokens(statement)
	}
}

func evidenceText(ref EvidenceRef) string {
	return normalizeText(ref.Kind + " " + ref.Source + " " + ref.Reason + " " + ref.Summary)
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

func tokenSet(text string) map[string]bool {
	parts := strings.Fields(text)
	tokens := make(map[string]bool, len(parts))
	for _, part := range parts {
		tokens[part] = true
	}
	return tokens
}

func normalizeText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer("_", " ", "-", " ", ".", " ", "/", " ")
	return strings.Join(strings.Fields(replacer.Replace(text)), " ")
}
