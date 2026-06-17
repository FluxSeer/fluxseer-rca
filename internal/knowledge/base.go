package knowledge

import (
	"strings"

	"fluxagent/internal/domain"
)

type Base struct {
	Runbooks map[string][]string
	Docs     map[string][]string
}

func NewBase() *Base {
	return &Base{
		Runbooks: map[string][]string{
			"payments-api": {
				"runbook://payments-api/oom-restart-guide",
				"runbook://payments-api/scale-and-throttle",
			},
		},
		Docs: map[string][]string{
			"payments-api": {
				"doc://payments-api/topology",
				"doc://payments-api/release-playbook",
			},
		},
	}
}

func (b *Base) Lookup(resource domain.ResourceRef, summary string) ([]string, []string) {
	key := strings.ToLower(resource.Service)
	runbooks := append([]string(nil), b.Runbooks[key]...)
	docs := append([]string(nil), b.Docs[key]...)

	if strings.Contains(strings.ToLower(summary), "oom") && len(runbooks) == 0 {
		runbooks = append(runbooks, "runbook://generic/oomkilled")
	}

	return runbooks, docs
}
