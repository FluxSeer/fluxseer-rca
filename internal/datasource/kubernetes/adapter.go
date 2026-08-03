package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
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
	relatedNames := a.relatedObjectNames(ctx, req)

	matched := make([]corev1.Event, 0)
	for _, event := range events.Items {
		if !eventMatchesTarget(event, req.Target, relatedNames) {
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
	records, resultType, err := a.queryWorkloadStatusRecords(ctx, req)
	if err != nil {
		return nil, err
	}
	limit := nativeRecordLimit("records", req.ResultLimits.Events.MaxRecords, int64(len(records)))
	retain := len(records)
	if limit != nil {
		retain = int(limit.RetainedCount)
	}

	result := &datasource.QueryResult{
		Source:       a.Name(),
		QueryType:    domain.QueryTypeDeploymentCondition,
		Summary:      fmt.Sprintf("Kubernetes returned %d workload status records for %s %s/%s", len(records), req.Target.Kind, req.Target.Namespace, req.Target.Name),
		Records:      records[:retain],
		NativeCounts: datasource.NativeResultCounts{ResultType: resultType, Records: len(records)},
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

func (a Adapter) queryWorkloadStatusRecords(ctx context.Context, req datasource.QueryRequest) ([]map[string]any, string, error) {
	key := types.NamespacedName{Namespace: req.Target.Namespace, Name: req.Target.Name}
	switch normalizeKind(req.Target.Kind) {
	case "deployment":
		var deployment appsv1.Deployment
		if err := a.Client.Get(ctx, key, &deployment); err != nil {
			return nil, "", fmt.Errorf("get deployment conditions: %w", err)
		}
		conditions := append([]appsv1.DeploymentCondition(nil), deployment.Status.Conditions...)
		sort.SliceStable(conditions, func(i, j int) bool {
			return string(conditions[i].Type) < string(conditions[j].Type)
		})
		records := make([]map[string]any, 0, len(conditions))
		for _, condition := range conditions {
			records = append(records, conditionRecord(string(condition.Type), string(condition.Status), condition.Reason, condition.Message))
		}
		return records, "deploymentConditions", nil
	case "statefulset":
		var statefulSet appsv1.StatefulSet
		if err := a.Client.Get(ctx, key, &statefulSet); err != nil {
			return nil, "", fmt.Errorf("get statefulset status: %w", err)
		}
		desired := int32(1)
		if statefulSet.Spec.Replicas != nil {
			desired = *statefulSet.Spec.Replicas
		}
		return []map[string]any{
			conditionRecord("Ready", statusBool(statefulSet.Status.ReadyReplicas >= desired), "ReadyReplicas", fmt.Sprintf("%d/%d StatefulSet replicas are ready", statefulSet.Status.ReadyReplicas, desired)),
		}, "statefulSetStatus", nil
	case "daemonset":
		var daemonSet appsv1.DaemonSet
		if err := a.Client.Get(ctx, key, &daemonSet); err != nil {
			return nil, "", fmt.Errorf("get daemonset status: %w", err)
		}
		return []map[string]any{
			conditionRecord("Ready", statusBool(daemonSet.Status.NumberReady >= daemonSet.Status.DesiredNumberScheduled), "NumberReady", fmt.Sprintf("%d/%d DaemonSet pods are ready", daemonSet.Status.NumberReady, daemonSet.Status.DesiredNumberScheduled)),
		}, "daemonSetStatus", nil
	case "job":
		var job batchv1.Job
		if err := a.Client.Get(ctx, key, &job); err != nil {
			return nil, "", fmt.Errorf("get job status: %w", err)
		}
		records := make([]map[string]any, 0, len(job.Status.Conditions)+1)
		conditions := append([]batchv1.JobCondition(nil), job.Status.Conditions...)
		sort.SliceStable(conditions, func(i, j int) bool {
			return string(conditions[i].Type) < string(conditions[j].Type)
		})
		for _, condition := range conditions {
			records = append(records, conditionRecord(string(condition.Type), string(condition.Status), condition.Reason, condition.Message))
		}
		records = append(records, conditionRecord("Failed", statusBool(job.Status.Failed > 0), "FailedPods", fmt.Sprintf("Job has active=%d succeeded=%d failed=%d pods", job.Status.Active, job.Status.Succeeded, job.Status.Failed)))
		return records, "jobStatus", nil
	case "cronjob":
		var cronJob batchv1.CronJob
		if err := a.Client.Get(ctx, key, &cronJob); err != nil {
			return nil, "", fmt.Errorf("get cronjob status: %w", err)
		}
		return []map[string]any{
			conditionRecord("Scheduled", statusBool(cronJob.Status.LastScheduleTime != nil), "LastScheduleTime", fmt.Sprintf("CronJob has %d active jobs", len(cronJob.Status.Active))),
		}, "cronJobStatus", nil
	default:
		return nil, "", nil
	}
}

func conditionRecord(conditionType string, status string, reason string, message string) map[string]any {
	return map[string]any{
		"type":    conditionType,
		"status":  status,
		"reason":  reason,
		"message": message,
	}
}

func statusBool(ok bool) string {
	if ok {
		return "True"
	}
	return "False"
}

func (a Adapter) HealthCheck(_ context.Context) error {
	if a.Client == nil {
		return fmt.Errorf("kubernetes client is nil")
	}
	return nil
}

func (a Adapter) relatedObjectNames(ctx context.Context, req datasource.QueryRequest) map[string]struct{} {
	names := map[string]struct{}{
		strings.ToLower(req.Target.Name): {},
	}
	if normalizeKind(req.Target.Kind) == "pod" || len(req.Labels) == 0 {
		return names
	}

	var pods corev1.PodList
	if err := a.Client.List(ctx, &pods, client.InNamespace(req.Target.Namespace)); err != nil {
		return names
	}
	for _, pod := range pods.Items {
		if !labelsMatch(pod.Labels, req.Labels) {
			continue
		}
		names[strings.ToLower(pod.Name)] = struct{}{}
	}
	return names
}

func eventMatchesTarget(event corev1.Event, target domain.ResourceRef, relatedNames map[string]struct{}) bool {
	involved := strings.ToLower(event.InvolvedObject.Name)
	name := strings.ToLower(target.Name)
	if strings.Contains(involved, name) {
		return true
	}
	for relatedName := range relatedNames {
		if relatedName != "" && strings.Contains(involved, relatedName) {
			return true
		}
	}
	return false
}

func labelsMatch(labels map[string]string, required map[string]string) bool {
	for key, value := range required {
		if labels[key] != value {
			return false
		}
	}
	return true
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

func normalizeKind(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "-", ""), "_", ""))
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
