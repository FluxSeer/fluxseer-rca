package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
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

func (a Adapter) Type() string {
	return "kubernetesEvents"
}

func (a Adapter) Capabilities() datasource.Capabilities {
	return datasource.Capabilities{
		Events:               true,
		DeploymentConditions: true,
	}
}

func (a Adapter) Query(ctx context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	if err := a.HealthCheck(ctx); err != nil {
		return nil, err
	}
	if req.QueryType == domain.QueryTypeDeploymentCondition {
		return a.queryDeploymentConditions(ctx, req)
	}

	var events corev1.EventList
	if err := a.Client.List(ctx, &events, client.InNamespace(req.Target.Namespace)); err != nil {
		return nil, fmt.Errorf("list kubernetes events: %w", err)
	}

	matched := make([]corev1.Event, 0)
	for _, event := range events.Items {
		if !eventMatchesTarget(event, req.Target) {
			continue
		}
		if !eventReasonAllowed(event.Reason, req.Reasons) {
			continue
		}
		matched = append(matched, event)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		return eventSortKey(matched[i]) < eventSortKey(matched[j])
	})
	records := make([]map[string]any, 0, len(matched))
	limit := nativeRecordLimit("records", req.ResultLimits.Events.MaxRecords, int64(len(matched)))
	retain := len(matched)
	if limit != nil {
		retain = int(limit.RetainedCount)
	}
	for _, event := range matched[:retain] {
		records = append(records, map[string]any{
			"reason":  event.Reason,
			"message": event.Message,
			"type":    event.Type,
			"object":  event.InvolvedObject.Name,
		})
	}

	result := &datasource.QueryResult{
		Source:       a.Name(),
		QueryType:    domain.QueryTypeEvent,
		Summary:      fmt.Sprintf("Kubernetes returned %d matching events for %s", len(matched), req.Target.Name),
		Records:      records,
		NativeCounts: datasource.NativeResultCounts{ResultType: "events", Records: len(matched)},
	}
	if limit != nil {
		result.Truncated = true
		result.TruncationReason = limit.Reason
		result.LimitDimension = limit.Dimension
		result.Limit = limit.Limit
		result.OriginalRecordCount = int(limit.OriginalCount)
		result.RetainedRecordCount = int(limit.RetainedCount)
		result.NativeLimit = limit
		result.Summary = fmt.Sprintf("%s; native records limit retained %d of %d", result.Summary, limit.RetainedCount, limit.OriginalCount)
	}
	return result, nil
}

func (a Adapter) queryDeploymentConditions(ctx context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	if !strings.EqualFold(req.Target.Kind, "Deployment") {
		return &datasource.QueryResult{
			Source:    a.Name(),
			QueryType: domain.QueryTypeDeploymentCondition,
			Summary:   fmt.Sprintf("Kubernetes deployment conditions skipped for non-Deployment target %s/%s", req.Target.Namespace, req.Target.Name),
			Records:   nil,
		}, nil
	}

	var deployment appsv1.Deployment
	if err := a.Client.Get(ctx, types.NamespacedName{Namespace: req.Target.Namespace, Name: req.Target.Name}, &deployment); err != nil {
		return nil, fmt.Errorf("get deployment conditions: %w", err)
	}

	records := make([]map[string]any, 0, len(deployment.Status.Conditions))
	conditions := append([]appsv1.DeploymentCondition(nil), deployment.Status.Conditions...)
	sort.SliceStable(conditions, func(i, j int) bool {
		return string(conditions[i].Type) < string(conditions[j].Type)
	})
	limit := nativeRecordLimit("records", req.ResultLimits.Events.MaxRecords, int64(len(conditions)))
	retain := len(conditions)
	if limit != nil {
		retain = int(limit.RetainedCount)
	}
	for _, condition := range conditions[:retain] {
		records = append(records, map[string]any{
			"type":    string(condition.Type),
			"status":  string(condition.Status),
			"reason":  condition.Reason,
			"message": condition.Message,
		})
	}

	result := &datasource.QueryResult{
		Source:       a.Name(),
		QueryType:    domain.QueryTypeDeploymentCondition,
		Summary:      fmt.Sprintf("Kubernetes returned %d deployment conditions for %s", len(conditions), req.Target.Name),
		Records:      records,
		NativeCounts: datasource.NativeResultCounts{ResultType: "deploymentConditions", Records: len(conditions)},
	}
	if limit != nil {
		result.Truncated = true
		result.TruncationReason = limit.Reason
		result.LimitDimension = limit.Dimension
		result.Limit = limit.Limit
		result.OriginalRecordCount = int(limit.OriginalCount)
		result.RetainedRecordCount = int(limit.RetainedCount)
		result.NativeLimit = limit
		result.Summary = fmt.Sprintf("%s; native records limit retained %d of %d", result.Summary, limit.RetainedCount, limit.OriginalCount)
	}
	return result, nil
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

func eventReasonAllowed(reason string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	normalizedReason := strings.TrimSpace(reason)
	if normalizedReason == "" {
		return false
	}
	for _, expected := range allowed {
		if strings.EqualFold(normalizedReason, strings.TrimSpace(expected)) {
			return true
		}
	}
	return false
}

func eventSortKey(event corev1.Event) string {
	timestamp := event.EventTime.Time
	if timestamp.IsZero() {
		timestamp = event.LastTimestamp.Time
	}
	if timestamp.IsZero() {
		timestamp = event.FirstTimestamp.Time
	}
	if timestamp.IsZero() {
		timestamp = time.Time{}
	}
	return timestamp.UTC().Format(time.RFC3339Nano) + "\xff" + event.Namespace + "\xff" + event.Name
}

func nativeRecordLimit(dimension string, limit int64, original int64) *datasource.NativeResultLimit {
	if limit <= 0 || original <= limit {
		return nil
	}
	return &datasource.NativeResultLimit{
		Reason:        "NativeResultLimitExceeded",
		Dimension:     dimension,
		Limit:         limit,
		OriginalCount: original,
		RetainedCount: limit,
	}
}
