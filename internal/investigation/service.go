package investigation

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	"fluxagent/internal/domain"
	"fluxagent/internal/modelgateway"
)

type Issue struct {
	Reason  string
	Message string
}

type PreflightResult struct {
	Target             domain.ResourceRef
	Labels             map[string]string
	DatasourceNames    []string
	Provider           *v1alpha1.ModelProvider
	TargetIssue        *Issue
	DatasourceIssue    *Issue
	ModelProviderIssue *Issue
}

func (r PreflightResult) FirstIssue() *Issue {
	if r.TargetIssue != nil {
		return r.TargetIssue
	}
	if r.DatasourceIssue != nil {
		return r.DatasourceIssue
	}
	if r.ModelProviderIssue != nil {
		return r.ModelProviderIssue
	}
	return nil
}

func (r PreflightResult) Successful() bool {
	return r.FirstIssue() == nil
}

type Service struct {
	Client   client.Reader
	Registry *datasource.Registry
	Resolver modelgateway.ProviderResolver
}

func (s *Service) Preflight(ctx context.Context, namespace string, spec v1alpha1.InvestigationRequestSpec) (PreflightResult, error) {
	result := PreflightResult{}

	target, labels, targetIssue, err := s.resolveTarget(ctx, spec.Target)
	if err != nil {
		return result, err
	}
	result.Target = target
	result.Labels = labels
	result.TargetIssue = targetIssue

	datasourceNames, datasourceIssue := s.resolveDatasources(spec)
	result.DatasourceNames = datasourceNames
	result.DatasourceIssue = datasourceIssue

	provider, providerIssue, err := s.resolveProvider(ctx, namespace, spec.ModelProviderRef)
	if err != nil {
		return result, err
	}
	result.Provider = provider
	result.ModelProviderIssue = providerIssue

	return result, nil
}

func (s *Service) resolveTarget(ctx context.Context, targetRef v1alpha1.TargetRef) (domain.ResourceRef, map[string]string, *Issue, error) {
	if s.Client == nil {
		return domain.ResourceRef{}, nil, &Issue{
			Reason:  "TargetResolverUnavailable",
			Message: "investigation service client is not configured",
		}, nil
	}

	if !strings.EqualFold(strings.TrimSpace(targetRef.Kind), "Deployment") {
		return domain.ResourceRef{}, nil, &Issue{
			Reason:  "UnsupportedTargetKind",
			Message: fmt.Sprintf("target kind %q is not supported yet; only Deployment is currently supported", targetRef.Kind),
		}, nil
	}

	var deployment appsv1.Deployment
	key := types.NamespacedName{
		Namespace: targetRef.Namespace,
		Name:      targetRef.Name,
	}
	if err := s.Client.Get(ctx, key, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return domain.ResourceRef{}, nil, &Issue{
				Reason:  "TargetNotFound",
				Message: fmt.Sprintf("target Deployment %s/%s was not found", key.Namespace, key.Name),
			}, nil
		}
		return domain.ResourceRef{}, nil, nil, err
	}

	return deploymentToResource(deployment), deploymentLabels(deployment), nil, nil
}

func (s *Service) resolveDatasources(spec v1alpha1.InvestigationRequestSpec) ([]string, *Issue) {
	if len(spec.DataSources) == 0 {
		return nil, &Issue{
			Reason:  "DataSourceNotSpecified",
			Message: "spec.dataSources must include at least one datasource reference",
		}
	}
	if s.Registry == nil {
		return nil, &Issue{
			Reason:  "DatasourceRegistryUnavailable",
			Message: "datasource registry is not configured",
		}
	}

	names := make([]string, 0, len(spec.DataSources))
	missing := make([]string, 0, len(spec.DataSources))
	for _, ref := range spec.DataSources {
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			missing = append(missing, "<empty>")
			continue
		}
		if _, ok := s.Registry.Get(name); !ok {
			missing = append(missing, name)
			continue
		}
		names = append(names, name)
	}
	if len(missing) > 0 {
		return names, &Issue{
			Reason:  "DataSourceNotFound",
			Message: fmt.Sprintf("datasource references were not found in the active registry: %s", strings.Join(missing, ", ")),
		}
	}
	return names, nil
}

func (s *Service) resolveProvider(ctx context.Context, namespace string, ref v1alpha1.LocalObjectReference) (*v1alpha1.ModelProvider, *Issue, error) {
	if s.Resolver == nil {
		return nil, &Issue{
			Reason:  "ResolverUnavailable",
			Message: "model provider resolver is not configured",
		}, nil
	}

	provider, err := s.Resolver.Resolve(ctx, namespace, localRefOrNil(ref))
	if err != nil {
		if resolveErr, ok := err.(*modelgateway.ResolveError); ok {
			return nil, &Issue{
				Reason:  resolveErr.Reason,
				Message: resolveErr.Message,
			}, nil
		}
		return nil, nil, err
	}
	return provider, nil, nil
}

func localRefOrNil(ref v1alpha1.LocalObjectReference) *v1alpha1.LocalObjectReference {
	if strings.TrimSpace(ref.Name) == "" {
		return nil
	}
	return &ref
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
