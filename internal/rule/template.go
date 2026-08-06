package rule

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"fluxseer/internal/domain"
)

func RenderQuery(raw string, target domain.ResourceRef, labels map[string]string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}

	tpl, err := template.New("query").Option("missingkey=zero").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse query template: %w", err)
	}

	data := map[string]any{
		"cluster":    target.Cluster,
		"namespace":  target.Namespace,
		"kind":       target.Kind,
		"name":       target.Name,
		"apiVersion": target.APIVersion,
		"service":    target.Service,
		"app":        firstNonEmpty(labels["app"], target.Service, target.Name),
		"labels":     labels,
	}

	var rendered bytes.Buffer
	if err := tpl.Execute(&rendered, data); err != nil {
		return "", fmt.Errorf("execute query template: %w", err)
	}
	return strings.TrimSpace(rendered.String()), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
