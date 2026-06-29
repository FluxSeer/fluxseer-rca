package modelgateway

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
)

func TestKubeSecretResolverResolvesAPIKey(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-secret", Namespace: "fluxagent-system"},
		Data:       map[string][]byte{"api-key": []byte("secret-token")},
	}
	provider := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "openai-provider", Namespace: "fluxagent-system"},
		Spec: v1alpha1.ModelProviderSpec{
			Provider: "openai",
			APIKeySecretRef: &v1alpha1.SecretKeyRef{
				Name: "openai-secret",
				Key:  "api-key",
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	resolver := KubeSecretResolver{Client: client}

	token, err := resolver.ResolveAPIKey(context.Background(), provider)
	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	if token != "secret-token" {
		t.Fatalf("expected secret-token, got %q", token)
	}
}
