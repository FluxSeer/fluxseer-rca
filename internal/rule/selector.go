package rule

import (
	"context"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/domain"
)

type PodReference struct {
	Namespace string
	Name      string
	UID       string
}

type WorkloadStatus struct {
	ReadyReplicas     int32
	AvailableReplicas int32
	DesiredReplicas   int32
	Succeeded         int32
	Failed            int32
	Active            int32
}

type Target struct {
	Resource   domain.ResourceRef
	Labels     map[string]string
	UID        string
	Generation int64
	Pods       []PodReference
	Status     WorkloadStatus
	OwnerChain []domain.ResourceRef
}

func DiscoverTargets(ctx context.Context, kubeClient client.Client, selector v1alpha1.TargetSelector) ([]Target, error) {
	kinds := selectedKinds(selector.WorkloadSelector.Kinds)
	namespaces := selectedNamespaces(selector.NamespaceSelector.MatchNames)

	targets := make([]Target, 0)
	for _, kind := range kinds {
		var discovered []Target
		var err error
		switch normalizeKind(kind) {
		case "deployment":
			discovered, err = discoverDeployments(ctx, kubeClient, namespaces)
		case "statefulset":
			discovered, err = discoverStatefulSets(ctx, kubeClient, namespaces)
		case "daemonset":
			discovered, err = discoverDaemonSets(ctx, kubeClient, namespaces)
		case "job":
			discovered, err = discoverJobs(ctx, kubeClient, namespaces)
		case "cronjob":
			discovered, err = discoverCronJobs(ctx, kubeClient, namespaces)
		case "pod":
			discovered, err = discoverPods(ctx, kubeClient, namespaces)
		default:
			continue
		}
		if err != nil {
			return nil, err
		}
		targets = append(targets, filterTargets(discovered, selector.WorkloadSelector.MatchLabels)...)
	}

	targets = dedupeTargets(targets)
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Resource.Namespace == targets[j].Resource.Namespace {
			if targets[i].Resource.Kind == targets[j].Resource.Kind {
				return targets[i].Resource.Name < targets[j].Resource.Name
			}
			return targets[i].Resource.Kind < targets[j].Resource.Kind
		}
		return targets[i].Resource.Namespace < targets[j].Resource.Namespace
	})
	return targets, nil
}

type CoverageReport struct {
	SupportedTargetKinds       []string
	UnsupportedDiscoveredKinds map[string]int32
	Partial                    bool
}

func DiscoverCoverage(ctx context.Context, kubeClient client.Client, selector v1alpha1.TargetSelector) (CoverageReport, error) {
	kinds := selectedKinds(selector.WorkloadSelector.Kinds)
	namespaces := selectedNamespaces(selector.NamespaceSelector.MatchNames)
	report := CoverageReport{
		SupportedTargetKinds:       make([]string, 0, len(kinds)),
		UnsupportedDiscoveredKinds: map[string]int32{},
	}
	for _, kind := range kinds {
		canonical, supported := canonicalSupportedKind(kind)
		if supported {
			report.SupportedTargetKinds = appendStringIfMissing(report.SupportedTargetKinds, canonical)
			continue
		}
		count, err := countUnsupportedKind(ctx, kubeClient, kind, namespaces)
		if err != nil {
			return report, err
		}
		report.UnsupportedDiscoveredKinds[canonicalUnsupportedKind(kind)] = count
		report.Partial = true
	}
	sort.Strings(report.SupportedTargetKinds)
	return report, nil
}

func selectedKinds(kinds []string) []string {
	if len(kinds) == 0 {
		return []string{"Deployment"}
	}
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			continue
		}
		out = append(out, kind)
	}
	return out
}

func selectedNamespaces(namespaces []string) []string {
	if len(namespaces) == 0 {
		return []string{""}
	}
	return namespaces
}

func canonicalSupportedKind(kind string) (string, bool) {
	switch normalizeKind(kind) {
	case "deployment":
		return "Deployment", true
	case "statefulset":
		return "StatefulSet", true
	case "daemonset":
		return "DaemonSet", true
	case "job":
		return "Job", true
	case "cronjob":
		return "CronJob", true
	case "pod":
		return "Pod", true
	default:
		return "", false
	}
}

func canonicalUnsupportedKind(kind string) string {
	switch normalizeKind(kind) {
	case "node":
		return "Node"
	case "persistentvolumeclaim", "pvc":
		return "PersistentVolumeClaim"
	case "persistentvolume", "pv":
		return "PersistentVolume"
	case "service":
		return "Service"
	case "endpointslice":
		return "EndpointSlice"
	case "ingress":
		return "Ingress"
	case "hpa", "horizontalpodautoscaler":
		return "HorizontalPodAutoscaler"
	case "pdb", "poddisruptionbudget":
		return "PodDisruptionBudget"
	default:
		return strings.TrimSpace(kind)
	}
}

func countUnsupportedKind(ctx context.Context, kubeClient client.Client, kind string, namespaces []string) (int32, error) {
	switch normalizeKind(kind) {
	case "node":
		var list corev1.NodeList
		if err := kubeClient.List(ctx, &list); err != nil {
			return 0, err
		}
		return int32(len(list.Items)), nil
	case "persistentvolumeclaim", "pvc":
		return countNamespaced(ctx, kubeClient, namespaces, func() client.ObjectList { return &corev1.PersistentVolumeClaimList{} })
	case "persistentvolume", "pv":
		var list corev1.PersistentVolumeList
		if err := kubeClient.List(ctx, &list); err != nil {
			return 0, err
		}
		return int32(len(list.Items)), nil
	case "service":
		return countNamespaced(ctx, kubeClient, namespaces, func() client.ObjectList { return &corev1.ServiceList{} })
	default:
		return 0, nil
	}
}

func countNamespaced(ctx context.Context, kubeClient client.Client, namespaces []string, newList func() client.ObjectList) (int32, error) {
	var total int32
	for _, namespace := range namespaces {
		list := newList()
		if err := listInNamespace(ctx, kubeClient, namespace, list); err != nil {
			return 0, err
		}
		items, err := apimeta.ExtractList(list)
		if err != nil {
			return 0, err
		}
		total += int32(len(items))
	}
	return total, nil
}

func discoverDeployments(ctx context.Context, kubeClient client.Client, namespaces []string) ([]Target, error) {
	targets := make([]Target, 0)
	for _, namespace := range namespaces {
		var list appsv1.DeploymentList
		if err := listInNamespace(ctx, kubeClient, namespace, &list); err != nil {
			return nil, err
		}
		for _, item := range list.Items {
			targets = append(targets, Target{
				Resource:   workloadToResource("Deployment", "apps/v1", item.Name, item.Namespace, item.Labels, item.Spec.Template.Labels),
				Labels:     workloadLabels(item.Labels, item.Spec.Template.Labels),
				UID:        string(item.UID),
				Generation: item.Generation,
				Status: WorkloadStatus{
					ReadyReplicas:     item.Status.ReadyReplicas,
					AvailableReplicas: item.Status.AvailableReplicas,
					DesiredReplicas:   desiredReplicas(item.Spec.Replicas),
				},
			})
		}
	}
	return targets, nil
}

func discoverStatefulSets(ctx context.Context, kubeClient client.Client, namespaces []string) ([]Target, error) {
	targets := make([]Target, 0)
	for _, namespace := range namespaces {
		var list appsv1.StatefulSetList
		if err := listInNamespace(ctx, kubeClient, namespace, &list); err != nil {
			return nil, err
		}
		for _, item := range list.Items {
			targets = append(targets, Target{
				Resource:   workloadToResource("StatefulSet", "apps/v1", item.Name, item.Namespace, item.Labels, item.Spec.Template.Labels),
				Labels:     workloadLabels(item.Labels, item.Spec.Template.Labels),
				UID:        string(item.UID),
				Generation: item.Generation,
				Status: WorkloadStatus{
					ReadyReplicas:   item.Status.ReadyReplicas,
					DesiredReplicas: desiredReplicas(item.Spec.Replicas),
				},
			})
		}
	}
	return targets, nil
}

func discoverDaemonSets(ctx context.Context, kubeClient client.Client, namespaces []string) ([]Target, error) {
	targets := make([]Target, 0)
	for _, namespace := range namespaces {
		var list appsv1.DaemonSetList
		if err := listInNamespace(ctx, kubeClient, namespace, &list); err != nil {
			return nil, err
		}
		for _, item := range list.Items {
			targets = append(targets, Target{
				Resource:   workloadToResource("DaemonSet", "apps/v1", item.Name, item.Namespace, item.Labels, item.Spec.Template.Labels),
				Labels:     workloadLabels(item.Labels, item.Spec.Template.Labels),
				UID:        string(item.UID),
				Generation: item.Generation,
				Status: WorkloadStatus{
					ReadyReplicas:   item.Status.NumberReady,
					DesiredReplicas: item.Status.DesiredNumberScheduled,
				},
			})
		}
	}
	return targets, nil
}

func discoverJobs(ctx context.Context, kubeClient client.Client, namespaces []string) ([]Target, error) {
	targets := make([]Target, 0)
	for _, namespace := range namespaces {
		var list batchv1.JobList
		if err := listInNamespace(ctx, kubeClient, namespace, &list); err != nil {
			return nil, err
		}
		for _, item := range list.Items {
			labels := workloadLabels(item.Labels, item.Spec.Template.Labels)
			target := Target{
				Resource:   workloadToResource("Job", "batch/v1", item.Name, item.Namespace, item.Labels, item.Spec.Template.Labels),
				Labels:     labels,
				UID:        string(item.UID),
				Generation: item.Generation,
				Status: WorkloadStatus{
					Active:    item.Status.Active,
					Succeeded: item.Status.Succeeded,
					Failed:    item.Status.Failed,
				},
			}
			if owner := controllerOwner(item.OwnerReferences); owner != nil && owner.Kind == "CronJob" {
				target.OwnerChain = append(target.OwnerChain, domain.ResourceRef{
					Cluster:    "in-cluster",
					Namespace:  item.Namespace,
					Kind:       "CronJob",
					Name:       owner.Name,
					APIVersion: "batch/v1",
					Service:    firstNonEmpty(labels["app"], owner.Name),
				})
			}
			targets = append(targets, target)
		}
	}
	return targets, nil
}

func discoverCronJobs(ctx context.Context, kubeClient client.Client, namespaces []string) ([]Target, error) {
	targets := make([]Target, 0)
	for _, namespace := range namespaces {
		var list batchv1.CronJobList
		if err := listInNamespace(ctx, kubeClient, namespace, &list); err != nil {
			return nil, err
		}
		for _, item := range list.Items {
			labels := workloadLabels(item.Labels, item.Spec.JobTemplate.Spec.Template.Labels)
			targets = append(targets, Target{
				Resource:   workloadToResource("CronJob", "batch/v1", item.Name, item.Namespace, item.Labels, item.Spec.JobTemplate.Spec.Template.Labels),
				Labels:     labels,
				UID:        string(item.UID),
				Generation: item.Generation,
			})
		}
	}
	return targets, nil
}

func discoverPods(ctx context.Context, kubeClient client.Client, namespaces []string) ([]Target, error) {
	targets := make([]Target, 0)
	for _, namespace := range namespaces {
		var list corev1.PodList
		if err := listInNamespace(ctx, kubeClient, namespace, &list); err != nil {
			return nil, err
		}
		for _, item := range list.Items {
			target, err := podCanonicalTarget(ctx, kubeClient, item)
			if err != nil {
				return nil, err
			}
			targets = append(targets, target)
		}
	}
	return targets, nil
}

func podCanonicalTarget(ctx context.Context, kubeClient client.Client, pod corev1.Pod) (Target, error) {
	podRef := domain.ResourceRef{
		Cluster:    "in-cluster",
		Namespace:  pod.Namespace,
		Kind:       "Pod",
		Name:       pod.Name,
		APIVersion: "v1",
		Service:    firstNonEmpty(pod.Labels["app"], pod.Name),
	}
	base := Target{
		Resource:   podRef,
		Labels:     workloadLabels(pod.Labels, nil),
		UID:        string(pod.UID),
		Generation: pod.Generation,
		Pods:       []PodReference{{Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID)}},
	}
	owner := controllerOwner(pod.OwnerReferences)
	if owner == nil {
		return base, nil
	}
	switch owner.Kind {
	case "ReplicaSet":
		var rs appsv1.ReplicaSet
		if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, &rs); err != nil {
			return base, client.IgnoreNotFound(err)
		}
		if rsOwner := controllerOwner(rs.OwnerReferences); rsOwner != nil && rsOwner.Kind == "Deployment" {
			var deployment appsv1.Deployment
			if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: rsOwner.Name}, &deployment); err != nil {
				return replicaSetTarget(rs, base), client.IgnoreNotFound(err)
			}
			target := deploymentTarget(deployment)
			target.Pods = base.Pods
			target.OwnerChain = []domain.ResourceRef{podRef, replicaSetResource(rs)}
			return target, nil
		}
		return replicaSetTarget(rs, base), nil
	case "StatefulSet":
		var statefulSet appsv1.StatefulSet
		if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, &statefulSet); err != nil {
			return base, client.IgnoreNotFound(err)
		}
		target := statefulSetTarget(statefulSet)
		target.Pods = base.Pods
		target.OwnerChain = []domain.ResourceRef{podRef}
		return target, nil
	case "DaemonSet":
		var daemonSet appsv1.DaemonSet
		if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, &daemonSet); err != nil {
			return base, client.IgnoreNotFound(err)
		}
		target := daemonSetTarget(daemonSet)
		target.Pods = base.Pods
		target.OwnerChain = []domain.ResourceRef{podRef}
		return target, nil
	case "Job":
		var job batchv1.Job
		if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, &job); err != nil {
			return base, client.IgnoreNotFound(err)
		}
		target := jobTarget(job)
		target.Pods = base.Pods
		target.OwnerChain = []domain.ResourceRef{podRef}
		if jobOwner := controllerOwner(job.OwnerReferences); jobOwner != nil && jobOwner.Kind == "CronJob" {
			target.OwnerChain = append(target.OwnerChain, domain.ResourceRef{
				Cluster:    "in-cluster",
				Namespace:  job.Namespace,
				Kind:       "CronJob",
				Name:       jobOwner.Name,
				APIVersion: "batch/v1",
				Service:    firstNonEmpty(target.Labels["app"], jobOwner.Name),
			})
		}
		return target, nil
	default:
		base.OwnerChain = []domain.ResourceRef{podRef}
		return base, nil
	}
}

func deploymentTarget(item appsv1.Deployment) Target {
	return Target{
		Resource:   workloadToResource("Deployment", "apps/v1", item.Name, item.Namespace, item.Labels, item.Spec.Template.Labels),
		Labels:     workloadLabels(item.Labels, item.Spec.Template.Labels),
		UID:        string(item.UID),
		Generation: item.Generation,
		Status: WorkloadStatus{
			ReadyReplicas:     item.Status.ReadyReplicas,
			AvailableReplicas: item.Status.AvailableReplicas,
			DesiredReplicas:   desiredReplicas(item.Spec.Replicas),
		},
	}
}

func statefulSetTarget(item appsv1.StatefulSet) Target {
	return Target{
		Resource:   workloadToResource("StatefulSet", "apps/v1", item.Name, item.Namespace, item.Labels, item.Spec.Template.Labels),
		Labels:     workloadLabels(item.Labels, item.Spec.Template.Labels),
		UID:        string(item.UID),
		Generation: item.Generation,
		Status: WorkloadStatus{
			ReadyReplicas:   item.Status.ReadyReplicas,
			DesiredReplicas: desiredReplicas(item.Spec.Replicas),
		},
	}
}

func daemonSetTarget(item appsv1.DaemonSet) Target {
	return Target{
		Resource:   workloadToResource("DaemonSet", "apps/v1", item.Name, item.Namespace, item.Labels, item.Spec.Template.Labels),
		Labels:     workloadLabels(item.Labels, item.Spec.Template.Labels),
		UID:        string(item.UID),
		Generation: item.Generation,
		Status: WorkloadStatus{
			ReadyReplicas:   item.Status.NumberReady,
			DesiredReplicas: item.Status.DesiredNumberScheduled,
		},
	}
}

func jobTarget(item batchv1.Job) Target {
	return Target{
		Resource:   workloadToResource("Job", "batch/v1", item.Name, item.Namespace, item.Labels, item.Spec.Template.Labels),
		Labels:     workloadLabels(item.Labels, item.Spec.Template.Labels),
		UID:        string(item.UID),
		Generation: item.Generation,
		Status: WorkloadStatus{
			Active:    item.Status.Active,
			Succeeded: item.Status.Succeeded,
			Failed:    item.Status.Failed,
		},
	}
}

func replicaSetTarget(item appsv1.ReplicaSet, pod Target) Target {
	target := Target{
		Resource:   replicaSetResource(item),
		Labels:     workloadLabels(item.Labels, item.Spec.Template.Labels),
		UID:        string(item.UID),
		Generation: item.Generation,
		Pods:       pod.Pods,
		OwnerChain: []domain.ResourceRef{pod.Resource},
	}
	return target
}

func replicaSetResource(item appsv1.ReplicaSet) domain.ResourceRef {
	return workloadToResource("ReplicaSet", "apps/v1", item.Name, item.Namespace, item.Labels, item.Spec.Template.Labels)
}

func listInNamespace(ctx context.Context, kubeClient client.Client, namespace string, list client.ObjectList) error {
	options := []client.ListOption{}
	if namespace != "" {
		options = append(options, client.InNamespace(namespace))
	}
	return kubeClient.List(ctx, list, options...)
}

func filterTargets(targets []Target, requiredLabels map[string]string) []Target {
	out := make([]Target, 0, len(targets))
	for _, target := range targets {
		if !matchesLabels(target.Labels, requiredLabels) {
			continue
		}
		out = append(out, target)
	}
	return out
}

func dedupeTargets(targets []Target) []Target {
	seen := map[string]Target{}
	for _, target := range targets {
		key := fmt.Sprintf("%s/%s/%s/%s", target.Resource.APIVersion, target.Resource.Kind, target.Resource.Namespace, target.Resource.Name)
		if existing, ok := seen[key]; ok {
			existing.Pods = append(existing.Pods, target.Pods...)
			if existing.UID == "" {
				existing.UID = target.UID
			}
			seen[key] = existing
			continue
		}
		seen[key] = target
	}
	out := make([]Target, 0, len(seen))
	for _, target := range seen {
		out = append(out, target)
	}
	return out
}

func matchesLabels(labels, required map[string]string) bool {
	for key, value := range required {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func workloadLabels(objectLabels map[string]string, templateLabels map[string]string) map[string]string {
	labels := make(map[string]string, len(objectLabels)+len(templateLabels))
	for key, value := range objectLabels {
		labels[key] = value
	}
	for key, value := range templateLabels {
		labels[key] = value
	}
	return labels
}

func workloadToResource(kind string, apiVersion string, name string, namespace string, objectLabels map[string]string, templateLabels map[string]string) domain.ResourceRef {
	service := firstNonEmpty(objectLabels["app"], templateLabels["app"], name)
	return domain.ResourceRef{
		Cluster:    "in-cluster",
		Namespace:  namespace,
		Kind:       kind,
		Name:       name,
		APIVersion: apiVersion,
		Service:    service,
	}
}

func desiredReplicas(replicas *int32) int32 {
	if replicas == nil {
		return 1
	}
	return *replicas
}

func controllerOwner(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	if len(refs) > 0 {
		return &refs[0]
	}
	return nil
}

func normalizeKind(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "-", ""), "_", ""))
}

func appendStringIfMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
