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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/FluxSeer/fluxseer-rca/internal/datasource"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

type Adapter struct {
	Client    client.Reader
	APIReader client.Reader
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
		ServiceConfiguration: true,
		ProbeConfiguration:   true,
		ContainerTermination: true,
	}
}

func (a Adapter) Query(ctx context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	if err := a.HealthCheck(ctx); err != nil {
		return nil, err
	}
	if req.QueryType == domain.QueryTypeDeploymentCondition {
		return a.queryDeploymentConditions(ctx, req)
	}
	if req.QueryType == domain.QueryTypeServiceConfiguration {
		return a.queryServiceConfiguration(ctx, req)
	}
	if req.QueryType == domain.QueryTypeProbeConfiguration {
		return a.queryProbeConfiguration(ctx, req)
	}
	if req.QueryType == domain.QueryTypeContainerTermination {
		return a.queryContainerTermination(ctx, req)
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
		if !eventWithinTimeRange(event, req.StartTime, req.EndTime) {
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
			"reason":             event.Reason,
			"message":            event.Message,
			"type":               event.Type,
			"object":             event.InvolvedObject.Name,
			"eventUID":           string(event.UID),
			"eventName":          event.Name,
			"eventNamespace":     event.Namespace,
			"involvedObjectKind": event.InvolvedObject.Kind,
			"involvedObjectName": event.InvolvedObject.Name,
			"involvedObjectUID":  string(event.InvolvedObject.UID),
			"firstTimestamp":     event.FirstTimestamp.Time.UTC().Format(time.RFC3339),
			"lastTimestamp":      event.LastTimestamp.Time.UTC().Format(time.RFC3339),
			"count":              event.Count,
			"reportingComponent": event.ReportingController,
			"reportingInstance":  event.ReportingInstance,
			"sourceComponent":    event.Source.Component,
			"sourceHost":         event.Source.Host,
			"resourceVersion":    event.ResourceVersion,
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

func eventWithinTimeRange(event corev1.Event, start, end time.Time) bool {
	if start.IsZero() && end.IsZero() {
		return true
	}

	eventTime := event.EventTime.Time
	if eventTime.IsZero() {
		eventTime = event.LastTimestamp.Time
	}
	if eventTime.IsZero() {
		eventTime = event.FirstTimestamp.Time
	}
	if eventTime.IsZero() {
		eventTime = event.CreationTimestamp.Time
	}
	if eventTime.IsZero() {
		return false
	}
	if !start.IsZero() && eventTime.Before(start) {
		return false
	}
	if !end.IsZero() && eventTime.After(end) {
		return false
	}
	return true
}

type workloadContainerPort struct {
	WorkloadKind      string
	WorkloadName      string
	ContainerName     string
	ContainerPortName string
	ContainerPort     int32
}

type workloadContainerSet struct {
	Kind       string
	Name       string
	Containers []corev1.Container
}

// queryProbeConfiguration returns declared HTTP probe configuration and its
// resolution against the selected container ports. An unresolved named probe
// port is evidence insufficiency, not a confirmed mismatch.
func (a Adapter) queryProbeConfiguration(ctx context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	workload, err := a.workloadContainers(ctx, req)
	if err != nil {
		return nil, err
	}
	records := make([]map[string]any, 0)
	for _, container := range workload.Containers {
		probes := []struct {
			name  string
			probe *corev1.Probe
		}{
			{name: "readiness", probe: container.ReadinessProbe},
			{name: "liveness", probe: container.LivenessProbe},
		}
		for _, item := range probes {
			if item.probe == nil {
				continue
			}
			if item.probe.HTTPGet == nil {
				records = append(records, probeConfigurationRecord(workload, container, item.name, nil, 0, false, "UnsupportedProbeHandler", false, "ProbeHandlerUnsupported"))
				continue
			}
			httpGet := item.probe.HTTPGet
			probePortNamed := httpGet.Port.Type == intstr.String
			resolved := int32(0)
			matched := make([]corev1.ContainerPort, 0)
			if !probePortNamed {
				resolved = httpGet.Port.IntVal
			}
			for _, port := range container.Ports {
				if probePortNamed && port.Name == httpGet.Port.StrVal {
					matched = append(matched, port)
				}
				if !probePortNamed && port.ContainerPort == resolved {
					matched = append(matched, port)
				}
			}
			if len(matched) == 0 {
				if probePortNamed {
					records = append(records, probeConfigurationRecord(workload, container, item.name, httpGet, resolved, probePortNamed, "UnresolvedNamedProbePort", false, "ProbePortUnresolved"))
					continue
				}
				records = append(records, probeConfigurationRecord(workload, container, item.name, httpGet, resolved, probePortNamed, "NumericProbePortDoesNotMatchContainerPort", true, "ProbeConfigurationMismatch"))
				continue
			}
			for _, port := range matched {
				if probePortNamed {
					resolved = port.ContainerPort
				}
				records = append(records, probeConfigurationRecord(workload, container, item.name, httpGet, resolved, probePortNamed, "Resolved", false, "ProbeConfigurationResolved"))
			}
		}
	}
	return &datasource.QueryResult{
		Source:       a.Name(),
		QueryType:    domain.QueryTypeProbeConfiguration,
		Summary:      fmt.Sprintf("Kubernetes inspected %d HTTP probe configurations for %s %s/%s", len(records), workload.Kind, workload.Name, req.Target.Namespace),
		Records:      records,
		NativeCounts: datasource.NativeResultCounts{ResultType: "probeConfiguration", Records: len(records)},
	}, nil
}

func probeConfigurationRecord(workload workloadContainerSet, container corev1.Container, probeType string, httpGet *corev1.HTTPGetAction, resolved int32, named bool, resolution string, mismatch bool, reason string) map[string]any {
	path, scheme, portRaw := "", "", ""
	if httpGet != nil {
		path = httpGet.Path
		scheme = string(httpGet.Scheme)
		portRaw = httpGet.Port.String()
	}
	return map[string]any{
		"workloadKind":      workload.Kind,
		"workloadName":      workload.Name,
		"containerName":     container.Name,
		"probeType":         probeType,
		"probeScheme":       scheme,
		"probePath":         path,
		"probePortRaw":      portRaw,
		"probePortResolved": resolved,
		"probePortNamed":    named,
		"containerPortName": firstContainerPortName(container.Ports),
		"containerPort":     firstContainerPort(container.Ports),
		"resolution":        resolution,
		"mismatchConfirmed": mismatch,
		"reason":            reason,
	}
}

const (
	maxTerminationPods         = 20
	maxTerminationReplicaSets  = 100
	maxTerminationContainers   = 10
	maxTerminationPodListPages = 10
)

func (a Adapter) queryContainerTermination(ctx context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	reader := a.APIReader
	if reader == nil {
		reader = a.Client
	}
	pods, listTruncated, err := a.targetPods(ctx, req)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(pods, func(i, j int) bool {
		iCrashLoop := podHasCrashLoopContainer(pods[i])
		jCrashLoop := podHasCrashLoopContainer(pods[j])
		if iCrashLoop != jCrashLoop {
			return iCrashLoop
		}
		if pods[i].Name != pods[j].Name {
			return pods[i].Name < pods[j].Name
		}
		return string(pods[i].UID) < string(pods[j].UID)
	})
	originalPodCount := len(pods)
	retainPodCount := min(originalPodCount, maxTerminationPods)
	selected := pods[:retainPodCount]
	records := make([]map[string]any, 0)
	for _, pod := range selected {
		statuses := append([]corev1.ContainerStatus(nil), pod.Status.ContainerStatuses...)
		sort.SliceStable(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
		if len(statuses) > maxTerminationContainers {
			statuses = statuses[:maxTerminationContainers]
		}
		for _, status := range statuses {
			if status.State.Waiting == nil && status.LastTerminationState.Terminated == nil {
				continue
			}
			record := map[string]any{
				"podName":       pod.Name,
				"podUID":        string(pod.UID),
				"containerName": status.Name,
				"restartCount":  status.RestartCount,
			}
			if status.State.Waiting != nil {
				record["waitingReason"] = status.State.Waiting.Reason
			}
			if terminated := status.LastTerminationState.Terminated; terminated != nil {
				record["lastTerminatedReason"] = terminated.Reason
				record["lastTerminatedExitCode"] = terminated.ExitCode
				record["lastTerminatedSignal"] = terminated.Signal
				if !terminated.FinishedAt.IsZero() {
					record["lastTerminatedFinishedAt"] = terminated.FinishedAt.UTC().Format(time.RFC3339)
				}
			}
			records = append(records, record)
		}
	}
	result := &datasource.QueryResult{
		Source:       a.Name(),
		QueryType:    domain.QueryTypeContainerTermination,
		Summary:      fmt.Sprintf("Kubernetes inspected termination status for %d containers across %d target Pods", len(records), len(selected)),
		Records:      records,
		NativeCounts: datasource.NativeResultCounts{ResultType: "containerTermination", Records: len(records)},
	}
	if originalPodCount > maxTerminationPods || listTruncated {
		result.Truncated = true
		result.TruncationReason = "TargetPodLimitExceeded"
		result.LimitDimension = "pods"
		result.Limit = maxTerminationPods
		result.OriginalRecordCount = originalPodCount
		result.RetainedRecordCount = len(selected)
		result.NativeLimit = &datasource.NativeResultLimit{
			Reason: "TargetPodLimitExceeded", Dimension: "pods", Limit: maxTerminationPods,
			OriginalCount: int64(originalPodCount), RetainedCount: int64(len(selected)),
		}
	}
	return result, nil
}

func (a Adapter) targetPods(ctx context.Context, req datasource.QueryRequest) ([]corev1.Pod, bool, error) {
	reader := a.APIReader
	if reader == nil {
		reader = a.Client
	}
	key := types.NamespacedName{Namespace: req.Target.Namespace, Name: req.Target.Name}
	switch normalizeKind(req.Target.Kind) {
	case "pod":
		var pod corev1.Pod
		if err := a.Client.Get(ctx, key, &pod); err != nil {
			return nil, false, fmt.Errorf("get target Pod termination status: %w", err)
		}
		return []corev1.Pod{pod}, false, nil
	case "deployment":
		var workload appsv1.Deployment
		if err := a.Client.Get(ctx, key, &workload); err != nil {
			return nil, false, fmt.Errorf("get target Deployment selector: %w", err)
		}
		if workload.Spec.Selector == nil || len(workload.Spec.Selector.MatchLabels) == 0 {
			return nil, false, fmt.Errorf("target Deployment %s/%s has no matchLabels selector", req.Target.Namespace, req.Target.Name)
		}
		var replicas appsv1.ReplicaSetList
		if err := reader.List(ctx, &replicas, client.InNamespace(req.Target.Namespace), client.MatchingLabels(workload.Spec.Selector.MatchLabels), client.Limit(maxTerminationReplicaSets+1)); err != nil {
			return nil, false, fmt.Errorf("list target Deployment ReplicaSets: %w", err)
		}
		replicaUIDs := map[types.UID]bool{}
		for _, replica := range replicas.Items {
			if hasControllerOwner(replica.OwnerReferences, "Deployment", workload.Name, workload.UID) {
				replicaUIDs[replica.UID] = true
			}
		}
		pods, podListTruncated, err := a.listOwnedPods(ctx, reader, req.Target.Namespace, workload.Spec.Selector.MatchLabels, func(pod corev1.Pod) bool {
			for _, owner := range pod.OwnerReferences {
				if owner.Controller != nil && *owner.Controller && owner.Kind == "ReplicaSet" && replicaUIDs[owner.UID] {
					return true
				}
			}
			return false
		})
		return pods, podListTruncated || replicas.Continue != "" || len(replicas.Items) > maxTerminationReplicaSets, err
	case "statefulset":
		var workload appsv1.StatefulSet
		if err := a.Client.Get(ctx, key, &workload); err != nil {
			return nil, false, fmt.Errorf("get target StatefulSet selector: %w", err)
		}
		if workload.Spec.Selector == nil || len(workload.Spec.Selector.MatchLabels) == 0 {
			return nil, false, fmt.Errorf("target StatefulSet %s/%s has no matchLabels selector", req.Target.Namespace, req.Target.Name)
		}
		return a.listOwnedPods(ctx, reader, req.Target.Namespace, workload.Spec.Selector.MatchLabels, func(pod corev1.Pod) bool {
			return hasControllerOwner(pod.OwnerReferences, "StatefulSet", workload.Name, workload.UID)
		})
	case "daemonset":
		var workload appsv1.DaemonSet
		if err := a.Client.Get(ctx, key, &workload); err != nil {
			return nil, false, fmt.Errorf("get target DaemonSet selector: %w", err)
		}
		if workload.Spec.Selector == nil || len(workload.Spec.Selector.MatchLabels) == 0 {
			return nil, false, fmt.Errorf("target DaemonSet %s/%s has no matchLabels selector", req.Target.Namespace, req.Target.Name)
		}
		return a.listOwnedPods(ctx, reader, req.Target.Namespace, workload.Spec.Selector.MatchLabels, func(pod corev1.Pod) bool {
			return hasControllerOwner(pod.OwnerReferences, "DaemonSet", workload.Name, workload.UID)
		})
	case "replicaset":
		var workload appsv1.ReplicaSet
		if err := a.Client.Get(ctx, key, &workload); err != nil {
			return nil, false, fmt.Errorf("get target ReplicaSet selector: %w", err)
		}
		if workload.Spec.Selector == nil || len(workload.Spec.Selector.MatchLabels) == 0 {
			return nil, false, fmt.Errorf("target ReplicaSet %s/%s has no matchLabels selector", req.Target.Namespace, req.Target.Name)
		}
		return a.listOwnedPods(ctx, reader, req.Target.Namespace, workload.Spec.Selector.MatchLabels, func(pod corev1.Pod) bool {
			return hasControllerOwner(pod.OwnerReferences, "ReplicaSet", workload.Name, workload.UID)
		})
	default:
		return nil, false, fmt.Errorf("target kind %q does not support container termination queries", req.Target.Kind)
	}
}

func (a Adapter) listOwnedPods(ctx context.Context, reader client.Reader, namespace string, selector map[string]string, owned func(corev1.Pod) bool) ([]corev1.Pod, bool, error) {
	result := make([]corev1.Pod, 0, maxTerminationPods)
	continueToken := ""
	for page := 0; page < maxTerminationPodListPages; page++ {
		var pods corev1.PodList
		listOptions := []client.ListOption{
			client.InNamespace(namespace),
			client.MatchingLabels(selector),
			client.Limit(maxTerminationPods + 1),
		}
		if continueToken != "" {
			listOptions = append(listOptions, client.Continue(continueToken))
		}
		if err := reader.List(ctx, &pods, listOptions...); err != nil {
			return nil, false, fmt.Errorf("list target-owned Pods: %w", err)
		}
		for _, pod := range pods.Items {
			if owned(pod) {
				result = append(result, pod)
				if len(result) > maxTerminationPods {
					return result, true, nil
				}
			}
		}
		continueToken = pods.Continue
		if continueToken == "" {
			return result, false, nil
		}
	}
	return result, continueToken != "", nil
}

func hasControllerOwner(owners []metav1.OwnerReference, kind, name string, uid types.UID) bool {
	for _, owner := range owners {
		if owner.Controller != nil && *owner.Controller && owner.Kind == kind && owner.Name == name && owner.UID == uid {
			return true
		}
	}
	return false
}

func podHasCrashLoopContainer(pod corev1.Pod) bool {
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting != nil && status.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
	}
	return false
}

func firstContainerPortName(ports []corev1.ContainerPort) string {
	if len(ports) == 0 {
		return ""
	}
	return ports[0].Name
}

func firstContainerPort(ports []corev1.ContainerPort) int32 {
	if len(ports) == 0 {
		return 0
	}
	return ports[0].ContainerPort
}

// queryServiceConfiguration returns the raw Service targetPort and its
// resolution against the selected workload's declared container ports. A
// named targetPort is resolved by name; an unresolved name is evidence
// insufficiency, not a confirmed mismatch.
func (a Adapter) queryServiceConfiguration(ctx context.Context, req datasource.QueryRequest) (*datasource.QueryResult, error) {
	service, err := a.findService(ctx, req)
	if err != nil {
		return nil, err
	}
	ports, err := a.workloadContainerPorts(ctx, req)
	if err != nil {
		return nil, err
	}
	records := make([]map[string]any, 0, len(service.Spec.Ports))
	for _, servicePort := range service.Spec.Ports {
		targetRaw := servicePort.TargetPort.String()
		targetNamed := servicePort.TargetPort.Type == intstr.String
		resolved := int32(0)
		if !targetNamed {
			resolved = servicePort.TargetPort.IntVal
		}
		matched := make([]workloadContainerPort, 0)
		for _, port := range ports {
			if targetNamed && port.ContainerPortName == servicePort.TargetPort.StrVal {
				matched = append(matched, port)
			}
			if !targetNamed && port.ContainerPort == resolved {
				matched = append(matched, port)
			}
		}
		if len(matched) == 0 {
			if targetNamed {
				records = append(records, serviceConfigurationRecord(service, servicePort, targetRaw, resolved, true, "UnresolvedNamedTargetPort", workloadContainerPort{}))
				continue
			}
			if len(ports) == 0 {
				records = append(records, serviceConfigurationRecord(service, servicePort, targetRaw, resolved, true, "NoDeclaredContainerPort", workloadContainerPort{}))
				continue
			}
			fallback := ports[0]
			records = append(records, serviceConfigurationRecord(service, servicePort, targetRaw, resolved, false, "NumericTargetPortDoesNotMatchContainerPort", fallback))
			continue
		}
		for _, port := range matched {
			if targetNamed {
				resolved = port.ContainerPort
			}
			records = append(records, serviceConfigurationRecord(service, servicePort, targetRaw, resolved, false, "Resolved", port))
		}
	}
	return &datasource.QueryResult{
		Source:       a.Name(),
		QueryType:    domain.QueryTypeServiceConfiguration,
		Summary:      fmt.Sprintf("Kubernetes resolved %d Service ports for %s %s/%s", len(records), req.Target.Kind, req.Target.Namespace, req.Target.Name),
		Records:      records,
		NativeCounts: datasource.NativeResultCounts{ResultType: "serviceConfiguration", Records: len(records)},
	}, nil
}

func (a Adapter) findService(ctx context.Context, req datasource.QueryRequest) (*corev1.Service, error) {
	serviceName := strings.TrimSpace(req.Target.Service)
	if serviceName == "" {
		serviceName = req.Target.Name
	}
	var service corev1.Service
	if err := a.Client.Get(ctx, types.NamespacedName{Namespace: req.Target.Namespace, Name: serviceName}, &service); err == nil {
		return &service, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get Service %s/%s: %w", req.Target.Namespace, serviceName, err)
	}
	var services corev1.ServiceList
	if err := a.Client.List(ctx, &services, client.InNamespace(req.Target.Namespace)); err != nil {
		return nil, fmt.Errorf("list Services in %s: %w", req.Target.Namespace, err)
	}
	for i := range services.Items {
		if len(services.Items[i].Spec.Selector) == 0 {
			continue
		}
		if labelsMatch(req.Labels, services.Items[i].Spec.Selector) {
			return &services.Items[i], nil
		}
	}
	return nil, fmt.Errorf("service %s/%s was not found for workload %s", req.Target.Namespace, serviceName, req.Target.Name)
}

func (a Adapter) workloadContainerPorts(ctx context.Context, req datasource.QueryRequest) ([]workloadContainerPort, error) {
	workload, err := a.workloadContainers(ctx, req)
	if err != nil {
		return nil, err
	}
	ports := make([]workloadContainerPort, 0)
	appendPorts := func(kind, name string, containers []corev1.Container) {
		for _, container := range containers {
			for _, port := range container.Ports {
				ports = append(ports, workloadContainerPort{WorkloadKind: kind, WorkloadName: name, ContainerName: container.Name, ContainerPortName: port.Name, ContainerPort: port.ContainerPort})
			}
		}
	}
	appendPorts(workload.Kind, workload.Name, workload.Containers)
	return ports, nil
}

func (a Adapter) workloadContainers(ctx context.Context, req datasource.QueryRequest) (workloadContainerSet, error) {
	key := types.NamespacedName{Namespace: req.Target.Namespace, Name: req.Target.Name}
	switch normalizeKind(req.Target.Kind) {
	case "deployment":
		var workload appsv1.Deployment
		if err := a.Client.Get(ctx, key, &workload); err != nil {
			return workloadContainerSet{}, fmt.Errorf("get Deployment configuration: %w", err)
		}
		return workloadContainerSet{Kind: "Deployment", Name: workload.Name, Containers: workload.Spec.Template.Spec.Containers}, nil
	case "statefulset":
		var workload appsv1.StatefulSet
		if err := a.Client.Get(ctx, key, &workload); err != nil {
			return workloadContainerSet{}, fmt.Errorf("get StatefulSet configuration: %w", err)
		}
		return workloadContainerSet{Kind: "StatefulSet", Name: workload.Name, Containers: workload.Spec.Template.Spec.Containers}, nil
	case "daemonset":
		var workload appsv1.DaemonSet
		if err := a.Client.Get(ctx, key, &workload); err != nil {
			return workloadContainerSet{}, fmt.Errorf("get DaemonSet configuration: %w", err)
		}
		return workloadContainerSet{Kind: "DaemonSet", Name: workload.Name, Containers: workload.Spec.Template.Spec.Containers}, nil
	case "pod":
		var workload corev1.Pod
		if err := a.Client.Get(ctx, key, &workload); err != nil {
			return workloadContainerSet{}, fmt.Errorf("get Pod configuration: %w", err)
		}
		return workloadContainerSet{Kind: "Pod", Name: workload.Name, Containers: workload.Spec.Containers}, nil
	default:
		return workloadContainerSet{}, fmt.Errorf("workload kind %q does not expose probe configuration", req.Target.Kind)
	}
}

func serviceConfigurationRecord(service *corev1.Service, servicePort corev1.ServicePort, targetRaw string, resolved int32, unresolved bool, resolution string, port workloadContainerPort) map[string]any {
	mismatch := !unresolved && resolution == "NumericTargetPortDoesNotMatchContainerPort"
	return map[string]any{
		"serviceName":        service.Name,
		"servicePortName":    servicePort.Name,
		"servicePort":        servicePort.Port,
		"targetPortRaw":      targetRaw,
		"targetPortResolved": resolved,
		"targetPortNamed":    servicePort.TargetPort.Type == intstr.String,
		"workloadKind":       port.WorkloadKind,
		"workloadName":       port.WorkloadName,
		"containerName":      port.ContainerName,
		"containerPortName":  port.ContainerPortName,
		"containerPort":      port.ContainerPort,
		"resolution":         resolution,
		"mismatchConfirmed":  mismatch,
		"reason":             servicePortReason(mismatch, unresolved),
	}
}

func servicePortReason(mismatch, unresolved bool) string {
	if mismatch {
		return "ServicePortMismatch"
	}
	if unresolved {
		return "TargetPortUnresolved"
	}
	return "ServicePortResolved"
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
