package diagnostic

import "sort"

// Planner owns the bounded registry of diagnostic capabilities. It only
// selects and evaluates deterministic diagnostics; evidence collection remains
// outside this package.
type Planner struct {
	diagnostics []Diagnostic
}

func NewPlanner(diagnostics ...Diagnostic) *Planner {
	registry := make([]Diagnostic, 0, len(diagnostics))
	seen := make(map[string]struct{}, len(diagnostics))
	for _, candidate := range diagnostics {
		if candidate == nil {
			continue
		}
		key := candidate.ID() + "@" + candidate.Version()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		registry = append(registry, candidate)
	}
	sort.SliceStable(registry, func(i, j int) bool {
		if registry[i].ID() != registry[j].ID() {
			return registry[i].ID() < registry[j].ID()
		}
		return registry[i].Version() < registry[j].Version()
	})
	return &Planner{diagnostics: registry}
}

// DefaultPlanner returns the first built-in diagnostic registry. The registry
// is intentionally small; adding a diagnostic does not add a new query path.
func DefaultPlanner() *Planner {
	return NewPlanner(ImagePullDiagnostic{})
}

// Candidates returns diagnostics whose Match method accepts the current
// bounded evidence. A diagnostic may still return an Inconclusive result.
func (p *Planner) Candidates(evidence EvidenceSet) []Diagnostic {
	if p == nil {
		return nil
	}
	result := make([]Diagnostic, 0, len(p.diagnostics))
	for _, candidate := range p.diagnostics {
		if candidate.Match(evidence).Applicable {
			result = append(result, candidate)
		}
	}
	return result
}

// Evaluate evaluates all matching diagnostics in deterministic registry order.
func (p *Planner) Evaluate(evidence EvidenceSet) []Result {
	candidates := p.Candidates(evidence)
	results := make([]Result, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, candidate.Evaluate(evidence))
	}
	return results
}
