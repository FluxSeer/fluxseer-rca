package modelgateway

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fluxseer/api/v1alpha1"
)

const DefaultSystemNamespace = "fluxseer-rca-system"

type ResolveError struct {
	Reason  string
	Message string
}

func (e *ResolveError) Error() string {
	return e.Message
}

type ProviderResolver interface {
	Resolve(ctx context.Context, namespace string, ref *v1alpha1.LocalObjectReference) (*v1alpha1.ModelProvider, error)
}

type KubeResolver struct {
	Client          client.Reader
	SystemNamespace string
}

func (r KubeResolver) Resolve(ctx context.Context, namespace string, ref *v1alpha1.LocalObjectReference) (*v1alpha1.ModelProvider, error) {
	if ref == nil || ref.Name == "" {
		return DefaultHeuristicProvider(namespace), nil
	}
	if r.Client == nil {
		return nil, &ResolveError{
			Reason:  "ResolverUnavailable",
			Message: "model provider resolver is not configured",
		}
	}

	namespaces := []string{namespace}
	systemNamespace := r.SystemNamespace
	if systemNamespace == "" {
		systemNamespace = DefaultSystemNamespace
	}
	if namespace != systemNamespace {
		namespaces = append(namespaces, systemNamespace)
	}

	for _, candidateNS := range namespaces {
		provider := &v1alpha1.ModelProvider{}
		err := r.Client.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: candidateNS}, provider)
		if err == nil {
			return provider, nil
		}
		if apierrors.IsNotFound(err) {
			continue
		}
		return nil, err
	}

	return nil, &ResolveError{
		Reason:  "ProviderNotFound",
		Message: fmt.Sprintf("ModelProvider %q was not found in namespaces %v", ref.Name, namespaces),
	}
}

func DefaultHeuristicProvider(namespace string) *v1alpha1.ModelProvider {
	return &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-heuristic",
			Namespace: namespace,
		},
		Spec: v1alpha1.ModelProviderSpec{
			Provider:  "heuristic",
			Model:     "built-in",
			MaxTokens: 512,
		},
	}
}
