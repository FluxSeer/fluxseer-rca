package querypolicy

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"fluxagent/api/v1alpha1"
)

var promQLAggregators = map[string]struct{}{
	"sum":          {},
	"min":          {},
	"max":          {},
	"avg":          {},
	"group":        {},
	"stddev":       {},
	"stdvar":       {},
	"count":        {},
	"count_values": {},
	"bottomk":      {},
	"topk":         {},
	"quantile":     {},
}

var promQLNonFunctions = map[string]struct{}{
	"bool":        {},
	"by":          {},
	"group_left":  {},
	"group_right": {},
	"ignoring":    {},
	"offset":      {},
	"on":          {},
	"without":     {},
}

func validatePromQLPolicy(policy v1alpha1.PromQLPolicy, req Request) Decision {
	query := stripQuotedContent(req.Query)
	functions := promQLFunctions(query)
	if denied := firstDenied(functions, policy.DeniedFunctions); denied != "" {
		return Decision{Decision: DecisionRejected, Reason: ReasonFunctionDenied, Message: fmt.Sprintf("PromQL function %q is denied by datasource queryPolicy.prometheus", denied)}
	}
	if notAllowed := firstNotAllowed(functions, policy.AllowedFunctions); notAllowed != "" {
		return Decision{Decision: DecisionRejected, Reason: ReasonFunctionNotAllowed, Message: fmt.Sprintf("PromQL function %q is not allowed by datasource queryPolicy.prometheus.allowedFunctions", notAllowed)}
	}
	if explicitlyFalse(policy.AllowSubqueries) && hasPromQLSubquery(query) {
		return Decision{Decision: DecisionRejected, Reason: ReasonSubqueryDenied, Message: "PromQL subquery syntax is denied by datasource queryPolicy.prometheus.allowSubqueries=false"}
	}
	if explicitlyFalse(policy.AllowOffset) && hasWordToken(query, "offset") {
		return Decision{Decision: DecisionRejected, Reason: ReasonOffsetDenied, Message: "PromQL offset modifier is denied by datasource queryPolicy.prometheus.allowOffset=false"}
	}
	if explicitlyFalse(policy.AllowAtModifier) && hasPromQLAtModifier(query) {
		return Decision{Decision: DecisionRejected, Reason: ReasonAtModifierDenied, Message: "PromQL @ modifier is denied by datasource queryPolicy.prometheus.allowAtModifier=false"}
	}
	return Decision{Decision: DecisionAllowed, Reason: ReasonAllowed, Message: "PromQL syntax policy allowed query"}
}

func explicitlyFalse(value *bool) bool {
	return value != nil && !*value
}

func promQLFunctions(query string) []string {
	functions := make([]string, 0)
	seen := map[string]struct{}{}
	for i := 0; i < len(query); {
		r, size := rune(query[i]), 1
		if r >= utf8.RuneSelf {
			r, size = utf8.DecodeRuneInString(query[i:])
		}
		if !isIdentifierStart(r) {
			i += size
			continue
		}
		start := i
		i += size
		for i < len(query) {
			r, size = rune(query[i]), 1
			if r >= utf8.RuneSelf {
				r, size = utf8.DecodeRuneInString(query[i:])
			}
			if !isIdentifierPart(r) {
				break
			}
			i += size
		}
		identifier := strings.ToLower(query[start:i])
		if _, reserved := promQLNonFunctions[identifier]; reserved {
			continue
		}
		j := skipSpaces(query, i)
		isCall := j < len(query) && query[j] == '('
		if !isCall {
			if _, ok := promQLAggregators[identifier]; ok {
				isCall = hasPromQLAggregatorCall(query, j)
			}
		}
		if isCall {
			if _, ok := seen[identifier]; !ok {
				seen[identifier] = struct{}{}
				functions = append(functions, identifier)
			}
		}
	}
	return functions
}

func hasPromQLAggregatorCall(query string, index int) bool {
	j := skipSpaces(query, index)
	for _, modifier := range []string{"by", "without"} {
		if !strings.HasPrefix(strings.ToLower(query[j:]), modifier) {
			continue
		}
		after := j + len(modifier)
		if after < len(query) && isIdentifierPart(rune(query[after])) {
			continue
		}
		after = skipSpaces(query, after)
		if after >= len(query) || query[after] != '(' {
			return false
		}
		close := matchingParen(query, after)
		if close < 0 {
			return false
		}
		j = skipSpaces(query, close+1)
		break
	}
	return j < len(query) && query[j] == '('
}

func hasPromQLSubquery(query string) bool {
	for i := 0; i < len(query); i++ {
		if query[i] != '[' {
			continue
		}
		close := strings.IndexByte(query[i+1:], ']')
		if close < 0 {
			return false
		}
		if strings.Contains(query[i+1:i+1+close], ":") {
			return true
		}
		i += close + 1
	}
	return false
}

func hasPromQLAtModifier(query string) bool {
	for i := 0; i < len(query); i++ {
		if query[i] != '@' {
			continue
		}
		if i+1 < len(query) && query[i+1] == '@' {
			continue
		}
		return true
	}
	return false
}

func firstDenied(items []string, denied []string) string {
	deniedSet := normalizedSet(denied)
	for _, item := range items {
		if _, ok := deniedSet[strings.ToLower(strings.TrimSpace(item))]; ok {
			return strings.ToLower(strings.TrimSpace(item))
		}
	}
	return ""
}

func firstNotAllowed(items []string, allowed []string) string {
	if len(allowed) == 0 {
		return ""
	}
	allowedSet := normalizedSet(allowed)
	for _, item := range items {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if _, ok := allowedSet[normalized]; !ok {
			return normalized
		}
	}
	return ""
}

func normalizedSet(items []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range items {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized != "" {
			out[normalized] = struct{}{}
		}
	}
	return out
}

func hasWordToken(query string, token string) bool {
	token = strings.ToLower(token)
	lower := strings.ToLower(query)
	for i := 0; i <= len(lower)-len(token); i++ {
		if lower[i:i+len(token)] != token {
			continue
		}
		beforeOK := i == 0 || !isIdentifierPart(rune(lower[i-1]))
		afterIndex := i + len(token)
		afterOK := afterIndex == len(lower) || !isIdentifierPart(rune(lower[afterIndex]))
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func skipSpaces(value string, index int) int {
	for index < len(value) && unicode.IsSpace(rune(value[index])) {
		index++
	}
	return index
}

func matchingParen(value string, open int) int {
	depth := 0
	for i := open; i < len(value); i++ {
		switch value[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func isIdentifierStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentifierPart(r rune) bool {
	return r == '_' || r == ':' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
