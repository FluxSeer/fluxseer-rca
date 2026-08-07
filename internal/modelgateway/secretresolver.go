package modelgateway

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

type SecretResolver interface {
	ResolveAPIKey(ctx context.Context, provider *v1alpha1.ModelProvider) (string, error)
}

type KubeSecretResolver struct {
	Client client.Reader
}

func (r KubeSecretResolver) ResolveAPIKey(ctx context.Context, provider *v1alpha1.ModelProvider) (string, error) {
	if provider == nil {
		return "", &AnalyzeError{
			Reason:  "ProviderUnavailable",
			Message: "model provider is nil",
		}
	}
	if provider.Spec.APIKeySecretRef == nil {
		return "", &AnalyzeError{
			Reason:  "SecretRefMissing",
			Message: fmt.Sprintf("ModelProvider %q requires apiKeySecretRef", provider.Name),
		}
	}
	if r.Client == nil {
		return "", &AnalyzeError{
			Reason:  "SecretReaderUnavailable",
			Message: "model provider secret resolver is not configured",
		}
	}

	key := client.ObjectKey{
		Namespace: provider.Namespace,
		Name:      provider.Spec.APIKeySecretRef.Name,
	}
	var secret corev1.Secret
	if err := r.Client.Get(ctx, key, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", &AnalyzeError{
				Reason:  "SecretNotFound",
				Message: fmt.Sprintf("secret %s/%s was not found for ModelProvider %q", key.Namespace, key.Name, provider.Name),
			}
		}
		return "", &AnalyzeError{
			Reason:  "SecretReadFailed",
			Message: fmt.Sprintf("read secret %s/%s for ModelProvider %q: %v", key.Namespace, key.Name, provider.Name, err),
		}
	}

	value, ok := secret.Data[provider.Spec.APIKeySecretRef.Key]
	if !ok {
		return "", &AnalyzeError{
			Reason:  "SecretKeyMissing",
			Message: fmt.Sprintf("secret %s/%s missing key %q for ModelProvider %q", key.Namespace, key.Name, provider.Spec.APIKeySecretRef.Key, provider.Name),
		}
	}
	if strings.TrimSpace(string(value)) == "" {
		return "", &AnalyzeError{
			Reason:  "SecretValueEmpty",
			Message: fmt.Sprintf("secret %s/%s key %q is empty for ModelProvider %q", key.Namespace, key.Name, provider.Spec.APIKeySecretRef.Key, provider.Name),
		}
	}
	return string(value), nil
}
