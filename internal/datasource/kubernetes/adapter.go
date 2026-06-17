package kubernetes

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
)

type Adapter struct {
	Client client.Reader
}

func (a Adapter) Name() string {
	return "kubernetes-events"
}

func (a Adapter) Type() domain.QueryType {
	return domain.QueryTypeEvent
}

func (a Adapter) Query(ctx context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	if err := a.HealthCheck(ctx); err != nil {
		return nil, err
	}

	var events corev1.EventList
	if err := a.Client.List(ctx, &events, client.InNamespace(req.Target.Namespace)); err != nil {
		return nil, fmt.Errorf("list kubernetes events: %w", err)
	}

	records := make([]map[string]any, 0)
	for _, event := range events.Items {
		if !eventMatchesTarget(event, req.Target) {
			continue
		}
		records = append(records, map[string]any{
			"reason":  event.Reason,
			"message": event.Message,
			"type":    event.Type,
			"object":  event.InvolvedObject.Name,
		})
	}

	return &datasource.QueryResult{
		Source:    a.Name(),
		QueryType: domain.QueryTypeEvent,
		Summary:   fmt.Sprintf("Kubernetes returned %d matching events for %s", len(records), req.Target.Name),
		Records:   records,
	}, nil
}

func (a Adapter) HealthCheck(_ context.Context) error {
	if a.Client == nil {
		return fmt.Errorf("kubernetes client is nil")
	}
	return nil
}

func eventMatchesTarget(event corev1.Event, target domain.ResourceRef) bool {
	involved := strings.ToLower(event.InvolvedObject.Name)
	name := strings.ToLower(target.Name)
	kind := strings.ToLower(event.InvolvedObject.Kind)

	return strings.Contains(involved, name) || kind == strings.ToLower(target.Kind)
}
