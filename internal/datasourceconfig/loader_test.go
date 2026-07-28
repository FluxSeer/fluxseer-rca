package datasourceconfig

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/datasource"
	k8sadapter "fluxagent/internal/datasource/kubernetes"
	lokiadapter "fluxagent/internal/datasource/loki"
	promadapter "fluxagent/internal/datasource/prometheus"
)

func TestRegisterFromResourcesBuildsRegistry(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add aiops scheme: %v", err)
	}

	reader := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "prom-token",
					Namespace: "fluxagent-system",
				},
				Data: map[string][]byte{
					"token": []byte("secret-token"),
				},
			},
			&v1alpha1.DataSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "prometheus",
					Namespace: "fluxagent-system",
				},
				Spec: v1alpha1.DataSourceSpec{
					Type:     "prometheus",
					Endpoint: "http://prometheus.example",
					Timeout:  metav1.Duration{Duration: 3 * time.Second},
					Auth: &v1alpha1.DataSourceAuthSpec{
						Type: "bearerToken",
						SecretRef: &v1alpha1.SecretKeyRef{
							Name: "prom-token",
							Key:  "token",
						},
					},
				},
			},
			&v1alpha1.DataSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "loki",
					Namespace: "fluxagent-system",
				},
				Spec: v1alpha1.DataSourceSpec{
					Type:     "loki",
					Endpoint: "http://loki.example",
					Timeout:  metav1.Duration{Duration: 5 * time.Second},
				},
			},
			&v1alpha1.DataSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubernetes-events",
					Namespace: "fluxagent-system",
				},
				Spec: v1alpha1.DataSourceSpec{
					Type: "kubernetesEvents",
				},
			},
		).
		Build()

	registry := datasource.NewRegistry()
	if err := RegisterFromResources(context.Background(), reader, registry, reader); err != nil {
		t.Fatalf("register from resources failed: %v", err)
	}

	promSource, found := registry.Get(CanonicalPrometheusName)
	if !found {
		t.Fatal("expected prometheus datasource in registry")
	}
	promAdapter, ok := unwrapPolicySource(promSource).(promadapter.Adapter)
	if !ok {
		t.Fatalf("expected prometheus adapter, got %T", promSource)
	}
	if promAdapter.Client == nil {
		t.Fatal("expected prometheus client to be configured")
	}
	if promAdapter.Client.Timeout != 3*time.Second {
		t.Fatalf("expected prometheus timeout 3s, got %s", promAdapter.Client.Timeout)
	}
	rt, ok := promAdapter.Client.Transport.(bearerRoundTripper)
	if !ok {
		t.Fatalf("expected bearer transport, got %T", promAdapter.Client.Transport)
	}
	if rt.token != "secret-token" {
		t.Fatalf("expected bearer token to be loaded from secret, got %q", rt.token)
	}

	lokiSource, found := registry.Get(CanonicalLokiName)
	if !found {
		t.Fatal("expected loki datasource in registry")
	}
	lokiAdapter, ok := unwrapPolicySource(lokiSource).(lokiadapter.Adapter)
	if !ok {
		t.Fatalf("expected loki adapter, got %T", lokiSource)
	}
	if lokiAdapter.Client == nil || lokiAdapter.Client.Timeout != 5*time.Second {
		t.Fatalf("expected loki timeout 5s, got %#v", lokiAdapter.Client)
	}

	k8sSource, found := registry.Get(CanonicalKubernetesEventName)
	if !found {
		t.Fatal("expected kubernetes events datasource in registry")
	}
	if _, ok := unwrapPolicySource(k8sSource).(k8sadapter.Adapter); !ok {
		t.Fatalf("expected kubernetes adapter, got %T", k8sSource)
	}
}

func TestBuildSourceFromResourceValidatesQueryPolicy(t *testing.T) {
	_, err := BuildSourceFromResource(context.Background(), nil, v1alpha1.DataSource{
		Spec: v1alpha1.DataSourceSpec{
			Type:     "prometheus",
			Endpoint: "http://prometheus.example",
			QueryPolicy: v1alpha1.DataSourceQueryPolicy{
				Mode: "UnknownMode",
			},
		},
	}, nil)
	if validationErr, ok := err.(*ValidationError); !ok || validationErr.Reason != "QueryPolicyInvalid" {
		t.Fatalf("expected QueryPolicyInvalid, got %T %v", err, err)
	}

	_, err = BuildSourceFromResource(context.Background(), nil, v1alpha1.DataSource{
		Spec: v1alpha1.DataSourceSpec{
			Type:     "prometheus",
			Endpoint: "http://prometheus.example",
			QueryPolicy: v1alpha1.DataSourceQueryPolicy{
				MaxRange: metav1.Duration{Duration: -time.Second},
			},
		},
	}, nil)
	if validationErr, ok := err.(*ValidationError); !ok || validationErr.Reason != "QueryPolicyInvalid" {
		t.Fatalf("expected QueryPolicyInvalid, got %T %v", err, err)
	}

	_, err = BuildSourceFromResource(context.Background(), nil, v1alpha1.DataSource{
		Spec: v1alpha1.DataSourceSpec{
			Type:     "prometheus",
			Endpoint: "http://prometheus.example",
			QueryPolicy: v1alpha1.DataSourceQueryPolicy{
				Prometheus: v1alpha1.PromQLPolicy{
					AllowedFunctions: []string{"rate"},
					DeniedFunctions:  []string{"RATE"},
				},
			},
		},
	}, nil)
	if validationErr, ok := err.(*ValidationError); !ok || validationErr.Reason != "QueryPolicyInvalid" {
		t.Fatalf("expected QueryPolicyInvalid, got %T %v", err, err)
	}

	_, err = BuildSourceFromResource(context.Background(), nil, v1alpha1.DataSource{
		Spec: v1alpha1.DataSourceSpec{
			Type:     "loki",
			Endpoint: "http://loki.example",
			QueryPolicy: v1alpha1.DataSourceQueryPolicy{
				Loki: v1alpha1.LogQLPolicy{
					AllowedPipelineStages: []string{"json"},
					DeniedPipelineStages:  []string{"JSON"},
				},
			},
		},
	}, nil)
	if validationErr, ok := err.(*ValidationError); !ok || validationErr.Reason != "QueryPolicyInvalid" {
		t.Fatalf("expected QueryPolicyInvalid, got %T %v", err, err)
	}
}

func unwrapPolicySource(source datasource.DataSource) datasource.DataSource {
	if wrapped, ok := source.(policyDataSource); ok {
		return wrapped.DataSource
	}
	return source
}

func TestBuildHTTPClientRejectsDeniedDatasourceEndpoints(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
	}{
		{name: "metadata", endpoint: "http://169.254.169.254/latest/meta-data"},
		{name: "loopback", endpoint: "http://127.0.0.1:9090"},
		{name: "link-local", endpoint: "http://169.254.10.20:9090"},
		{name: "private-without-allowlist", endpoint: "http://10.32.0.15:9090"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildHTTPClient(context.Background(), nil, v1alpha1.DataSource{
				Spec: v1alpha1.DataSourceSpec{
					Type:     "prometheus",
					Endpoint: tc.endpoint,
				},
			})
			expectNetworkPolicyDenied(t, err)
		})
	}
}

func TestBuildHTTPClientAllowsConfiguredPrivateCIDRAndClusterService(t *testing.T) {
	if _, err := buildHTTPClient(context.Background(), nil, v1alpha1.DataSource{
		Spec: v1alpha1.DataSourceSpec{
			Type:     "prometheus",
			Endpoint: "http://10.32.0.15:9090",
			NetworkPolicy: v1alpha1.DataSourceNetworkPolicy{
				AllowedCIDRs: []string{"10.32.0.0/16"},
			},
		},
	}); err != nil {
		t.Fatalf("expected private endpoint to be allowed by CIDR, got %v", err)
	}
	if _, err := buildHTTPClient(context.Background(), nil, v1alpha1.DataSource{
		Spec: v1alpha1.DataSourceSpec{
			Type:     "prometheus",
			Endpoint: "http://prometheus-server.monitoring.svc:9090",
		},
	}); err != nil {
		t.Fatalf("expected cluster service endpoint to be allowed, got %v", err)
	}
}

func TestBuildHTTPClientRevalidatesRedirectTarget(t *testing.T) {
	client, err := buildHTTPClient(context.Background(), nil, v1alpha1.DataSource{
		Spec: v1alpha1.DataSourceSpec{
			Type:     "prometheus",
			Endpoint: "http://prometheus.example",
		},
	})
	if err != nil {
		t.Fatalf("expected client build to succeed, got %v", err)
	}
	redirectURL, err := url.Parse("http://169.254.169.254/latest/meta-data")
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}
	err = client.CheckRedirect(&http.Request{URL: redirectURL}, []*http.Request{{URL: &url.URL{Scheme: "http", Host: "prometheus.example"}}})
	expectNetworkPolicyDenied(t, err)
}

func TestDatasourcePolicyDialContextRejectsDeniedResolvedIP(t *testing.T) {
	dialCalled := false
	dial := datasourcePolicyDialContext(
		v1alpha1.DataSourceNetworkPolicy{},
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			dialCalled = true
			return nil, errors.New("unexpected dial")
		},
	)

	_, err := dial(context.Background(), "tcp", "prometheus.example:9090")
	expectNetworkPolicyDenied(t, err)
	if dialCalled {
		t.Fatal("expected denied resolved IP to block before dial")
	}
}

func TestDatasourcePolicyDialContextPinsAllowedResolvedIP(t *testing.T) {
	expectedErr := errors.New("dial stopped after policy validation")
	var dialAddress string
	dial := datasourcePolicyDialContext(
		v1alpha1.DataSourceNetworkPolicy{AllowedCIDRs: []string{"10.32.0.0/16"}},
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("10.32.0.15")}}, nil
		},
		func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialAddress = address
			return nil, expectedErr
		},
	)

	_, err := dial(context.Background(), "tcp", "prometheus.example:9090")
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected dial sentinel after policy validation, got %v", err)
	}
	if dialAddress != "10.32.0.15:9090" {
		t.Fatalf("expected dial to pinned IP address, got %q", dialAddress)
	}
}

func TestDatasourcePolicyDialContextRejectsWhenAnyResolvedIPDenied(t *testing.T) {
	dialCalled := false
	dial := datasourcePolicyDialContext(
		v1alpha1.DataSourceNetworkPolicy{AllowedCIDRs: []string{"10.32.0.0/16"}},
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{
				{IP: net.ParseIP("10.32.0.15")},
				{IP: net.ParseIP("169.254.169.254")},
			}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			dialCalled = true
			return nil, errors.New("unexpected dial")
		},
	)

	_, err := dial(context.Background(), "tcp", "prometheus.example:9090")
	expectNetworkPolicyDenied(t, err)
	if dialCalled {
		t.Fatal("expected one denied resolved IP to block before dial")
	}
}

func TestDatasourcePolicyDialContextAllowsClusterServicePrivateIP(t *testing.T) {
	expectedErr := errors.New("dial stopped after service policy validation")
	var dialAddress string
	dial := datasourcePolicyDialContext(
		v1alpha1.DataSourceNetworkPolicy{},
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("10.96.0.10")}}, nil
		},
		func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialAddress = address
			return nil, expectedErr
		},
	)

	_, err := dial(context.Background(), "tcp", "prometheus-server.monitoring.svc:9090")
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected dial sentinel after service policy validation, got %v", err)
	}
	if dialAddress != "10.96.0.10:9090" {
		t.Fatalf("expected dial to service ClusterIP, got %q", dialAddress)
	}
}

func expectNetworkPolicyDenied(t *testing.T, err error) {
	t.Helper()
	validationErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T %v", err, err)
	}
	if validationErr.Reason != "DatasourceNetworkPolicyDenied" {
		t.Fatalf("expected DatasourceNetworkPolicyDenied, got %#v", validationErr)
	}
}

func TestBearerRoundTripperAddsAuthorizationHeader(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	called := false
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("expected authorization header, got %q", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})

	_, err = bearerRoundTripper{base: base, token: "secret-token"}.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip failed: %v", err)
	}
	if !called {
		t.Fatal("expected base round tripper to be called")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
