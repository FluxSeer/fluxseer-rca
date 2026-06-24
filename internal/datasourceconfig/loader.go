package datasourceconfig

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	k8sadapter "fluxagent/internal/datasource/kubernetes"
	lokiadapter "fluxagent/internal/datasource/loki"
	promadapter "fluxagent/internal/datasource/prometheus"
)

const (
	CanonicalPrometheusName      = "prometheus"
	CanonicalLokiName            = "loki"
	CanonicalKubernetesEventName = "kubernetes-events"
)

type ValidationError struct {
	Reason  string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func RegisterFromResources(ctx context.Context, reader client.Reader, registry *datasource.Registry, kubeReader client.Reader) error {
	if reader == nil || registry == nil {
		return nil
	}

	var items v1alpha1.DataSourceList
	if err := reader.List(ctx, &items); err != nil {
		return fmt.Errorf("list datasource resources: %w", err)
	}

	logger := logf.Log.WithName("datasource-loader")
	for _, item := range items.Items {
		source, err := BuildSourceFromResource(ctx, reader, item, kubeReader)
		if err != nil {
			logger.Error(err, "skip datasource resource", "name", item.Name, "namespace", item.Namespace, "type", item.Spec.Type)
			continue
		}
		registry.RegisterNamed(item.Name, source)
		logger.Info("registered datasource resource", "name", item.Name, "namespace", item.Namespace, "type", item.Spec.Type, "adapter", source.Name())
	}
	return nil
}

func BuildSourceFromResource(ctx context.Context, reader client.Reader, item v1alpha1.DataSource, kubeReader client.Reader) (datasource.DataSource, error) {
	switch normalizeResourceType(item.Spec.Type) {
	case CanonicalPrometheusName:
		httpClient, err := buildHTTPClient(ctx, reader, item)
		if err != nil {
			return nil, err
		}
		return promadapter.Adapter{BaseURL: item.Spec.Endpoint, Client: httpClient}, nil
	case CanonicalLokiName:
		httpClient, err := buildHTTPClient(ctx, reader, item)
		if err != nil {
			return nil, err
		}
		return lokiadapter.Adapter{BaseURL: item.Spec.Endpoint, Client: httpClient}, nil
	case CanonicalKubernetesEventName:
		if kubeReader == nil {
			return nil, &ValidationError{
				Reason:  "KubernetesClientUnavailable",
				Message: "kubernetes datasource requires an in-cluster client",
			}
		}
		return k8sadapter.Adapter{Client: kubeReader}, nil
	default:
		return nil, &ValidationError{
			Reason:  "AdapterNotRegistered",
			Message: fmt.Sprintf("unsupported datasource type %q", item.Spec.Type),
		}
	}
}

func normalizeResourceType(value string) string {
	switch normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "-", ""), "_", "")); normalized {
	case "prometheus":
		return CanonicalPrometheusName
	case "loki":
		return CanonicalLokiName
	case "kubernetesevents", "kubernetesevent":
		return CanonicalKubernetesEventName
	default:
		return strings.TrimSpace(value)
	}
}

func buildHTTPClient(ctx context.Context, reader client.Reader, item v1alpha1.DataSource) (*http.Client, error) {
	if strings.TrimSpace(item.Spec.Endpoint) == "" {
		return nil, &ValidationError{
			Reason:  "EndpointMissing",
			Message: "endpoint is empty",
		}
	}

	timeout := 10 * time.Second
	if item.Spec.Timeout.Duration > 0 {
		timeout = item.Spec.Timeout.Duration
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if item.Spec.TLS != nil && item.Spec.TLS.InsecureSkipVerify {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	rt := http.RoundTripper(transport)
	if item.Spec.Auth != nil && strings.EqualFold(item.Spec.Auth.Type, "bearerToken") {
		token, err := resolveBearerToken(ctx, reader, item)
		if err != nil {
			return nil, err
		}
		rt = bearerRoundTripper{base: rt, token: token}
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: rt,
	}, nil
}

func resolveBearerToken(ctx context.Context, reader client.Reader, item v1alpha1.DataSource) (string, error) {
	if reader == nil {
		return "", &ValidationError{
			Reason:  "SecretReaderUnavailable",
			Message: "secret reader is nil",
		}
	}
	if item.Spec.Auth == nil || item.Spec.Auth.SecretRef == nil {
		return "", &ValidationError{
			Reason:  "SecretRefMissing",
			Message: "bearerToken auth requires secretRef",
		}
	}

	var secret corev1.Secret
	key := client.ObjectKey{Namespace: item.Namespace, Name: item.Spec.Auth.SecretRef.Name}
	if err := reader.Get(ctx, key, &secret); err != nil {
		return "", &ValidationError{
			Reason:  "SecretNotFound",
			Message: fmt.Sprintf("get secret %s/%s: %v", key.Namespace, key.Name, err),
		}
	}

	value, ok := secret.Data[item.Spec.Auth.SecretRef.Key]
	if !ok {
		return "", &ValidationError{
			Reason:  "SecretKeyMissing",
			Message: fmt.Sprintf("secret %s/%s missing key %q", key.Namespace, key.Name, item.Spec.Auth.SecretRef.Key),
		}
	}
	if strings.TrimSpace(string(value)) == "" {
		return "", &ValidationError{
			Reason:  "SecretValueEmpty",
			Message: fmt.Sprintf("secret %s/%s key %q is empty", key.Namespace, key.Name, item.Spec.Auth.SecretRef.Key),
		}
	}
	return string(value), nil
}

type bearerRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (r bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	if cloned.Header.Get("Authorization") == "" {
		cloned.Header.Set("Authorization", "Bearer "+r.token)
	}
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(cloned)
}
