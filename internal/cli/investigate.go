package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"fluxseer/api/v1alpha1"
)

type stringSliceFlag []string

func (f *stringSliceFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringSliceFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type investigateOptions struct {
	targetNamespace  string
	requestNamespace string
	requestName      string
	question         string
	lookback         time.Duration
	datasources      []string
	queryFile        string
	provider         string
	createRiskSignal bool
	wait             bool
	timeout          time.Duration
}

func runInvestigate(args []string, stdout, stderr io.Writer) error {
	opts, kind, name, err := parseInvestigateArgs(args, stderr)
	if err != nil {
		return err
	}

	req, err := buildInvestigationRequest(kind, name, opts)
	if err != nil {
		return err
	}

	scheme := buildScheme()
	cfg := ctrl.GetConfigOrDie()
	kubeClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("create kubernetes client: %w", err)
	}

	ctx := context.Background()
	if err := kubeClient.Create(ctx, req); err != nil {
		return fmt.Errorf("create InvestigationRequest %s/%s: %w", req.Namespace, req.Name, err)
	}
	_, _ = fmt.Fprintf(stdout, "created InvestigationRequest %s/%s\n", req.Namespace, req.Name)

	if !opts.wait {
		return nil
	}

	final, err := waitForInvestigationRequest(ctx, kubeClient, client.ObjectKeyFromObject(req), opts.timeout)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "phase: %s\n", final.Status.Phase)
	if final.Status.Provider != "" {
		_, _ = fmt.Fprintf(stdout, "provider: %s\n", final.Status.Provider)
	}
	if final.Status.Summary != "" {
		_, _ = fmt.Fprintf(stdout, "summary: %s\n", final.Status.Summary)
	}
	if final.Status.Hypothesis != "" {
		_, _ = fmt.Fprintf(stdout, "hypothesis: %s\n", final.Status.Hypothesis)
	}
	if final.Status.LinkedRiskSignalRef != nil {
		_, _ = fmt.Fprintf(stdout, "riskSignal: %s/%s\n", final.Status.LinkedRiskSignalRef.Namespace, final.Status.LinkedRiskSignalRef.Name)
	}

	if final.Status.Phase == v1alpha1.PhaseFailed {
		return errors.New(final.Status.Message)
	}
	return nil
}

func parseInvestigateArgs(args []string, stderr io.Writer) (investigateOptions, string, string, error) {
	opts := investigateOptions{
		targetNamespace:  "default",
		requestNamespace: "fluxagent-system",
		lookback:         15 * time.Minute,
		wait:             true,
		timeout:          90 * time.Second,
	}
	var datasourceFlags stringSliceFlag

	fs := flag.NewFlagSet("fluxagent investigate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.targetNamespace, "namespace", opts.targetNamespace, "target workload namespace")
	fs.StringVar(&opts.targetNamespace, "n", opts.targetNamespace, "target workload namespace")
	fs.StringVar(&opts.requestNamespace, "request-namespace", opts.requestNamespace, "InvestigationRequest namespace")
	fs.StringVar(&opts.requestName, "request-name", "", "InvestigationRequest name")
	fs.StringVar(&opts.question, "question", "", "investigation question")
	fs.DurationVar(&opts.lookback, "lookback", opts.lookback, "investigation lookback window")
	fs.Var(&datasourceFlags, "datasource", "datasource reference for default investigation plan; repeatable")
	fs.StringVar(&opts.queryFile, "query-file", "", "YAML or JSON file containing InvestigationRequest queries")
	fs.StringVar(&opts.provider, "provider", "", "ModelProvider name; defaults to heuristic fallback when empty")
	fs.BoolVar(&opts.createRiskSignal, "create-risk-signal", false, "promote successful investigation into a RiskSignal")
	fs.BoolVar(&opts.wait, "wait", opts.wait, "wait for investigation completion")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "wait timeout")
	parseArgs := args
	kind := ""
	name := ""
	if len(args) >= 2 && !strings.HasPrefix(args[0], "-") && !strings.HasPrefix(args[1], "-") {
		kind = args[0]
		name = args[1]
		parseArgs = args[2:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return opts, "", "", err
	}

	if kind == "" || name == "" {
		remaining := fs.Args()
		if len(remaining) != 2 {
			return opts, "", "", errors.New("usage: fluxagent investigate <kind> <name> [flags]")
		}
		kind = remaining[0]
		name = remaining[1]
	}
	if len(fs.Args()) != 0 && (kind != "" && name != "") && len(parseArgs) != len(args) {
		return opts, "", "", errors.New("usage: fluxagent investigate <kind> <name> [flags]")
	}
	opts.datasources = append([]string(nil), datasourceFlags...)
	return opts, kind, name, nil
}

func buildInvestigationRequest(kind, name string, opts investigateOptions) (*v1alpha1.InvestigationRequest, error) {
	queries, err := loadQueriesFile(opts.queryFile)
	if err != nil {
		return nil, err
	}
	if len(queries) == 0 && len(opts.datasources) == 0 {
		return nil, errors.New("at least one --datasource or --query-file is required")
	}

	requestName := opts.requestName
	if strings.TrimSpace(requestName) == "" {
		requestName = generatedInvestigationRequestName(kind, name)
	}

	spec := v1alpha1.InvestigationRequestSpec{
		Target: v1alpha1.TargetRef{
			Namespace:  opts.targetNamespace,
			Kind:       normalizeTargetKind(kind),
			Name:       name,
			APIVersion: apiVersionForKind(kind),
			Service:    name,
		},
		TimeRange: v1alpha1.InvestigationTimeRange{
			Lookback: metav1Duration(opts.lookback),
		},
		Question:         opts.question,
		Queries:          queries,
		ModelProviderRef: v1alpha1.LocalObjectReference{Name: opts.provider},
		Mode:             v1alpha1.InvestigationModeReadOnly,
		CreateRiskSignal: opts.createRiskSignal,
	}
	if len(queries) == 0 {
		spec.DataSources = make([]v1alpha1.LocalObjectReference, 0, len(opts.datasources))
		for _, name := range opts.datasources {
			spec.DataSources = append(spec.DataSources, v1alpha1.LocalObjectReference{Name: name})
		}
	}

	return &v1alpha1.InvestigationRequest{
		TypeMeta:   buildTypeMeta("InvestigationRequest"),
		ObjectMeta: buildObjectMeta(requestName, opts.requestNamespace),
		Spec:       spec,
	}, nil
}

type queryFilePayload struct {
	Queries []v1alpha1.InvestigationQuery `json:"queries,omitempty"`
}

func loadQueriesFile(path string) ([]v1alpha1.InvestigationQuery, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read query file %q: %w", path, err)
	}

	var wrapped queryFilePayload
	if err := yaml.Unmarshal(body, &wrapped); err == nil && len(wrapped.Queries) > 0 {
		return wrapped.Queries, nil
	}

	var list []v1alpha1.InvestigationQuery
	if err := yaml.Unmarshal(body, &list); err == nil && len(list) > 0 {
		return list, nil
	}

	return nil, fmt.Errorf("query file %q must contain either a queries: list or a top-level list", path)
}

func waitForInvestigationRequest(ctx context.Context, kubeClient client.Client, key client.ObjectKey, timeout time.Duration) (*v1alpha1.InvestigationRequest, error) {
	deadline := time.Now().Add(timeout)
	for {
		var request v1alpha1.InvestigationRequest
		if err := kubeClient.Get(ctx, key, &request); err != nil {
			return nil, fmt.Errorf("get InvestigationRequest %s/%s: %w", key.Namespace, key.Name, err)
		}
		switch request.Status.Phase {
		case v1alpha1.PhaseCompleted, v1alpha1.PhaseFailed:
			return &request, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for InvestigationRequest %s/%s", key.Namespace, key.Name)
		}
		time.Sleep(2 * time.Second)
	}
}

func normalizeTargetKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "deployment", "deploy":
		return "Deployment"
	case "statefulset", "sts":
		return "StatefulSet"
	case "daemonset", "ds":
		return "DaemonSet"
	case "replicaset", "rs":
		return "ReplicaSet"
	case "pod", "po":
		return "Pod"
	default:
		return kind
	}
}

func apiVersionForKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "deployment", "deploy":
		return "apps/v1"
	case "statefulset", "sts":
		return "apps/v1"
	case "daemonset", "ds":
		return "apps/v1"
	case "replicaset", "rs":
		return "apps/v1"
	case "pod", "po":
		return "v1"
	default:
		return ""
	}
}

func generatedInvestigationRequestName(kind, name string) string {
	base := strings.ToLower(fmt.Sprintf("investigate-%s-%s", normalizeTargetKind(kind), name))
	base = strings.ReplaceAll(base, "_", "-")
	base = strings.ReplaceAll(base, ".", "-")
	if len(base) > 50 {
		base = strings.TrimSuffix(base[:50], "-")
	}
	return fmt.Sprintf("%s-%d", base, time.Now().Unix())
}
