package datasourceconfig

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"fluxseer/api/v1alpha1"
	"fluxseer/internal/datasource"
	k8sadapter "fluxseer/internal/datasource/kubernetes"
	lokiadapter "fluxseer/internal/datasource/loki"
	promadapter "fluxseer/internal/datasource/prometheus"
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

type ipLookupFunc func(context.Context, string) ([]net.IPAddr, error)
type dialContextFunc func(context.Context, string, string) (net.Conn, error)

var defaultDatasourceDialer = (&net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
}).DialContext

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
	if err := validateQueryPolicy(item.Spec.QueryPolicy); err != nil {
		return nil, err
	}
	var source datasource.DataSource
	switch normalizeResourceType(item.Spec.Type) {
	case CanonicalPrometheusName:
		httpClient, err := buildHTTPClient(ctx, reader, item)
		if err != nil {
			return nil, err
		}
		source = promadapter.Adapter{BaseURL: item.Spec.Endpoint, Client: httpClient}
	case CanonicalLokiName:
		httpClient, err := buildHTTPClient(ctx, reader, item)
		if err != nil {
			return nil, err
		}
		source = lokiadapter.Adapter{BaseURL: item.Spec.Endpoint, Client: httpClient}
	case CanonicalKubernetesEventName:
		if kubeReader == nil {
			return nil, &ValidationError{
				Reason:  "KubernetesClientUnavailable",
				Message: "kubernetes datasource requires an in-cluster client",
			}
		}
		source = k8sadapter.Adapter{Client: kubeReader}
	default:
		return nil, &ValidationError{
			Reason:  "AdapterNotRegistered",
			Message: fmt.Sprintf("unsupported datasource type %q", item.Spec.Type),
		}
	}
	return policyDataSource{DataSource: source, policy: item.Spec.QueryPolicy, classification: item.Spec.DataClassification}, nil
}

type policyDataSource struct {
	datasource.DataSource
	policy         v1alpha1.DataSourceQueryPolicy
	classification v1alpha1.DataClassification
}

func (p policyDataSource) QueryPolicy() v1alpha1.DataSourceQueryPolicy {
	return p.policy
}

func (p policyDataSource) DataClassification() v1alpha1.DataClassification {
	return p.classification
}

func validateQueryPolicy(policy v1alpha1.DataSourceQueryPolicy) error {
	mode := strings.TrimSpace(policy.Mode)
	switch mode {
	case "", v1alpha1.DataSourceQueryPolicyModeLegacyUnrestricted, v1alpha1.DataSourceQueryPolicyModeTemplatesOnly:
	default:
		return &ValidationError{
			Reason:  "QueryPolicyInvalid",
			Message: fmt.Sprintf("unsupported datasource queryPolicy.mode %q", policy.Mode),
		}
	}
	if policy.MaxRange.Duration < 0 {
		return &ValidationError{
			Reason:  "QueryPolicyInvalid",
			Message: "datasource queryPolicy.maxRange must not be negative",
		}
	}
	if overlap := firstNormalizedOverlap(policy.Prometheus.AllowedFunctions, policy.Prometheus.DeniedFunctions); overlap != "" {
		return &ValidationError{
			Reason:  "QueryPolicyInvalid",
			Message: fmt.Sprintf("datasource queryPolicy.prometheus function %q cannot be both allowed and denied", overlap),
		}
	}
	if overlap := firstNormalizedOverlap(policy.Loki.AllowedPipelineStages, policy.Loki.DeniedPipelineStages); overlap != "" {
		return &ValidationError{
			Reason:  "QueryPolicyInvalid",
			Message: fmt.Sprintf("datasource queryPolicy.loki pipeline stage %q cannot be both allowed and denied", overlap),
		}
	}
	return nil
}

func firstNormalizedOverlap(left []string, right []string) string {
	seen := map[string]struct{}{}
	for _, item := range left {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized == "" {
			continue
		}
		seen[normalized] = struct{}{}
	}
	for _, item := range right {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			return normalized
		}
	}
	return ""
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
	if err := validateDatasourceEndpoint(item.Spec.Endpoint, item.Spec.NetworkPolicy); err != nil {
		return nil, err
	}

	timeout := 10 * time.Second
	if item.Spec.Timeout.Duration > 0 {
		timeout = item.Spec.Timeout.Duration
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = datasourcePolicyDialContext(item.Spec.NetworkPolicy, net.DefaultResolver.LookupIPAddr, defaultDatasourceDialer)
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
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			if err := validateDatasourceURL(req.URL, item.Spec.NetworkPolicy); err != nil {
				if validationErr, ok := err.(*ValidationError); ok {
					return &ValidationError{
						Reason:  validationErr.Reason,
						Message: fmt.Sprintf("redirect target %q rejected by datasource network policy: %s", req.URL.Host, validationErr.Message),
					}
				}
				return err
			}
			return nil
		},
	}, nil
}

func validateDatasourceEndpoint(endpoint string, policy v1alpha1.DataSourceNetworkPolicy) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return &ValidationError{
			Reason:  "DatasourceNetworkPolicyDenied",
			Message: fmt.Sprintf("endpoint URL is invalid: %v", err),
		}
	}
	return validateDatasourceURL(parsed, policy)
}

func validateDatasourceURL(parsed *url.URL, policy v1alpha1.DataSourceNetworkPolicy) error {
	if parsed == nil || strings.TrimSpace(parsed.Hostname()) == "" {
		return &ValidationError{
			Reason:  "DatasourceNetworkPolicyDenied",
			Message: "endpoint host is empty",
		}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return &ValidationError{
			Reason:  "DatasourceNetworkPolicyDenied",
			Message: fmt.Sprintf("endpoint scheme %q is not allowed", parsed.Scheme),
		}
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if !hostAllowed(host, policy.AllowedHosts) {
		return &ValidationError{
			Reason:  "DatasourceNetworkPolicyDenied",
			Message: fmt.Sprintf("endpoint host %q is not allowed by datasource networkPolicy.allowedHosts", host),
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := validateDatasourceResolvedIP(host, ip, policy); err != nil {
			return err
		}
	}
	return nil
}

func datasourcePolicyDialContext(policy v1alpha1.DataSourceNetworkPolicy, lookup ipLookupFunc, dial dialContextFunc) dialContextFunc {
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	if dial == nil {
		dial = defaultDatasourceDialer
	}
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, &ValidationError{
				Reason:  "DatasourceNetworkPolicyDenied",
				Message: fmt.Sprintf("dial address %q is invalid: %v", address, err),
			}
		}
		host = strings.ToLower(strings.TrimSpace(strings.Trim(host, "[]")))
		if !hostAllowed(host, policy.AllowedHosts) {
			return nil, &ValidationError{
				Reason:  "DatasourceNetworkPolicyDenied",
				Message: fmt.Sprintf("dial host %q is not allowed by datasource networkPolicy.allowedHosts", host),
			}
		}
		if ip := net.ParseIP(host); ip != nil {
			if err := validateDatasourceResolvedIP(host, ip, policy); err != nil {
				return nil, err
			}
			return dial(ctx, network, net.JoinHostPort(ip.String(), port))
		}

		addrs, err := lookup(ctx, host)
		if err != nil {
			return nil, &ValidationError{
				Reason:  "DatasourceNetworkPolicyDenied",
				Message: fmt.Sprintf("resolve datasource host %q: %v", host, err),
			}
		}
		if len(addrs) == 0 {
			return nil, &ValidationError{
				Reason:  "DatasourceNetworkPolicyDenied",
				Message: fmt.Sprintf("resolve datasource host %q returned no IP addresses", host),
			}
		}
		for _, addr := range addrs {
			if err := validateDatasourceResolvedIP(host, addr.IP, policy); err != nil {
				return nil, err
			}
		}
		return dial(ctx, network, net.JoinHostPort(addrs[0].IP.String(), port))
	}
}

func validateDatasourceResolvedIP(host string, ip net.IP, policy v1alpha1.DataSourceNetworkPolicy) error {
	if strings.TrimSpace(host) == "" {
		host = "<empty>"
	}
	if isClusterServiceHost(host) {
		if err := validateDatasourceIP(ip, policy, true); err != nil {
			return wrapResolvedIPError(host, ip, err)
		}
		return nil
	}
	if err := validateDatasourceIP(ip, policy, false); err != nil {
		return wrapResolvedIPError(host, ip, err)
	}
	return nil
}

func wrapResolvedIPError(host string, ip net.IP, err error) error {
	if validationErr, ok := err.(*ValidationError); ok {
		return &ValidationError{
			Reason:  validationErr.Reason,
			Message: fmt.Sprintf("datasource host %q resolved to denied IP %s: %s", host, ip.String(), validationErr.Message),
		}
	}
	return err
}

func hostAllowed(host string, allowedHosts []string) bool {
	if len(allowedHosts) == 0 || isClusterServiceHost(host) {
		return true
	}
	for _, allowed := range allowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		switch {
		case allowed == "":
			continue
		case allowed == host:
			return true
		case strings.HasPrefix(allowed, "*.") && strings.HasSuffix(host, strings.TrimPrefix(allowed, "*")):
			return true
		}
	}
	return false
}

func isClusterServiceHost(host string) bool {
	return strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".svc.cluster.local")
}

func validateDatasourceIP(ip net.IP, policy v1alpha1.DataSourceNetworkPolicy, allowPrivate bool) error {
	policyIP := ipForNetworkPolicy(ip)
	metadataCIDRs := []string{"169.254.169.254/32", "fd00:ec2::254/128"}
	for _, cidr := range append(metadataCIDRs, policy.DeniedCIDRs...) {
		if ipInCIDR(policyIP, cidr) {
			return &ValidationError{
				Reason:  "DatasourceNetworkPolicyDenied",
				Message: fmt.Sprintf("endpoint IP %s is denied by datasource network policy", policyIP.String()),
			}
		}
	}
	if policyIP.IsLoopback() || policyIP.IsLinkLocalUnicast() || policyIP.IsLinkLocalMulticast() || policyIP.IsUnspecified() {
		return &ValidationError{
			Reason:  "DatasourceNetworkPolicyDenied",
			Message: fmt.Sprintf("endpoint IP %s is loopback, link-local, or unspecified", policyIP.String()),
		}
	}
	if policyIP.IsPrivate() && !allowPrivate && !ipAllowedByCIDR(policyIP, policy.AllowedCIDRs) {
		return &ValidationError{
			Reason:  "DatasourceNetworkPolicyDenied",
			Message: fmt.Sprintf("private endpoint IP %s requires datasource networkPolicy.allowedCIDRs", policyIP.String()),
		}
	}
	return nil
}

func ipForNetworkPolicy(ip net.IP) net.IP {
	if ip == nil {
		return ip
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return ipv4
	}
	return ip
}

func ipAllowedByCIDR(ip net.IP, cidrs []string) bool {
	for _, cidr := range cidrs {
		if ipInCIDR(ip, cidr) {
			return true
		}
	}
	return false
}

func ipInCIDR(ip net.IP, cidr string) bool {
	_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
	return err == nil && network.Contains(ip)
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
