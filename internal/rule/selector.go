package rule

import (
	"context"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/domain"
)

type Target struct {
	Resource domain.ResourceRef
	Labels   map[string]string
	UID      string
}

func DiscoverTargets(ctx context.Context, kubeClient client.Client, selector v1alpha1.TargetSelector) ([]Target, error) {
	if !supportsDeployment(selector.WorkloadSelector.Kinds) {
		return nil, nil
	}

	namespaces := selector.NamespaceSelector.MatchNames
	if len(namespaces) == 0 {
		namespaces = []string{""}
	}

	var deployments []appsv1.Deployment
	for _, namespace := range namespaces {
		var deploymentList appsv1.DeploymentList
		options := []client.ListOption{}
		if namespace != "" {
			options = append(options, client.InNamespace(namespace))
		}
		if err := kubeClient.List(ctx, &deploymentList, options...); err != nil {
			return nil, err
		}
		deployments = append(deployments, deploymentList.Items...)
	}

	targets := make([]Target, 0, len(deployments))
	for _, deployment := range deployments {
		labels := deploymentLabels(deployment)
		if !matchesLabels(labels, selector.WorkloadSelector.MatchLabels) {
			continue
		}
		targets = append(targets, Target{
			Resource: deploymentToResource(deployment),
			Labels:   labels,
			UID:      string(deployment.UID),
		})
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Resource.Namespace == targets[j].Resource.Namespace {
			return targets[i].Resource.Name < targets[j].Resource.Name
		}
		return targets[i].Resource.Namespace < targets[j].Resource.Namespace
	})
	return targets, nil
}

func supportsDeployment(kinds []string) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, kind := range kinds {
		if strings.EqualFold(kind, "Deployment") {
			return true
		}
	}
	return false
}

func matchesLabels(labels, required map[string]string) bool {
	for key, value := range required {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func deploymentLabels(deployment appsv1.Deployment) map[string]string {
	labels := make(map[string]string, len(deployment.Labels)+len(deployment.Spec.Template.Labels))
	for key, value := range deployment.Labels {
		labels[key] = value
	}
	for key, value := range deployment.Spec.Template.Labels {
		labels[key] = value
	}
	return labels
}

func deploymentToResource(deployment appsv1.Deployment) domain.ResourceRef {
	service := deployment.Labels["app"]
	if service == "" {
		service = deployment.Spec.Template.Labels["app"]
	}
	if service == "" {
		service = deployment.Name
	}
	return domain.ResourceRef{
		Cluster:    "in-cluster",
		Namespace:  deployment.Namespace,
		Kind:       "Deployment",
		Name:       deployment.Name,
		APIVersion: "apps/v1",
		Service:    service,
	}
}
