package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

const (
	riskRuleReportSchemaVersion = "fluxseer-riskrule-report/v1"
	riskRuleLabelKey            = "fluxseer-rca.aiops.platform/risk-rule"
)

type reportOptions struct {
	namespace string
	output    string
}

type riskRuleReportSelection struct {
	Namespace string `json:"namespace"`
	RiskRule  string `json:"riskRule"`
}

type riskRuleReport struct {
	SchemaVersion         string                          `json:"schemaVersion"`
	Selection             riskRuleReportSelection         `json:"selection"`
	RiskRule              v1alpha1.RiskRule               `json:"riskRule"`
	InvestigationRequests []v1alpha1.InvestigationRequest `json:"investigationRequests"`
	RiskSignals           []v1alpha1.RiskSignal           `json:"riskSignals"`
}

func runReport(args []string, stdout, stderr io.Writer) error {
	opts, resource, name, err := parseReportArgs(args, stderr)
	if err != nil {
		return err
	}
	if resource != "riskrule" {
		return fmt.Errorf("unsupported report resource %q; only riskrule is supported", resource)
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load kubernetes config: %w", err)
	}
	kubeClient, err := client.New(cfg, client.Options{Scheme: buildScheme()})
	if err != nil {
		return fmt.Errorf("create kubernetes client: %w", err)
	}
	report, err := buildRiskRuleReport(context.Background(), kubeClient, opts.namespace, name)
	if err != nil {
		return err
	}
	return writeRiskRuleReport(stdout, report, opts.output)
}

func parseReportArgs(args []string, stderr io.Writer) (reportOptions, string, string, error) {
	opts := reportOptions{namespace: "fluxseer-rca-system", output: "json"}
	if len(args) < 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		return opts, "", "", errors.New("usage: fluxseer report riskrule <name> [flags]")
	}
	resource := strings.ToLower(strings.TrimSpace(args[0]))
	name := strings.TrimSpace(args[1])
	fs := flag.NewFlagSet("fluxseer report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.namespace, "namespace", opts.namespace, "RiskRule namespace")
	fs.StringVar(&opts.namespace, "n", opts.namespace, "RiskRule namespace")
	fs.StringVar(&opts.output, "output", opts.output, "output format: json or yaml")
	fs.StringVar(&opts.output, "o", opts.output, "output format: json or yaml")
	if err := fs.Parse(args[2:]); err != nil {
		return opts, "", "", err
	}
	if len(fs.Args()) != 0 || name == "" {
		return opts, "", "", errors.New("usage: fluxseer report riskrule <name> [flags]")
	}
	opts.output = strings.ToLower(strings.TrimSpace(opts.output))
	if opts.output != "json" && opts.output != "yaml" {
		return opts, "", "", fmt.Errorf("unsupported output format %q; use json or yaml", opts.output)
	}
	return opts, resource, name, nil
}

func buildRiskRuleReport(ctx context.Context, kubeClient client.Client, namespace, name string) (riskRuleReport, error) {
	report := riskRuleReport{
		SchemaVersion:         riskRuleReportSchemaVersion,
		Selection:             riskRuleReportSelection{Namespace: namespace, RiskRule: name},
		InvestigationRequests: []v1alpha1.InvestigationRequest{},
		RiskSignals:           []v1alpha1.RiskSignal{},
	}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &report.RiskRule); err != nil {
		return report, fmt.Errorf("get RiskRule %s/%s: %w", namespace, name, err)
	}
	report.RiskRule.TypeMeta = metav1.TypeMeta{APIVersion: v1alpha1.SchemeGroupVersion.String(), Kind: "RiskRule"}

	var requests v1alpha1.InvestigationRequestList
	if err := kubeClient.List(ctx, &requests, client.InNamespace(namespace), client.MatchingLabels{riskRuleLabelKey: name}); err != nil {
		return report, fmt.Errorf("list InvestigationRequests for RiskRule %s/%s: %w", namespace, name, err)
	}
	report.InvestigationRequests = append(report.InvestigationRequests, requests.Items...)
	for i := range report.InvestigationRequests {
		report.InvestigationRequests[i].TypeMeta = metav1.TypeMeta{APIVersion: v1alpha1.SchemeGroupVersion.String(), Kind: "InvestigationRequest"}
	}
	sortInvestigationRequests(report.InvestigationRequests)

	var signals v1alpha1.RiskSignalList
	if err := kubeClient.List(ctx, &signals, client.MatchingLabels{riskRuleLabelKey: name}); err != nil {
		return report, fmt.Errorf("list RiskSignals for RiskRule %s/%s: %w", namespace, name, err)
	}
	report.RiskSignals = append(report.RiskSignals, signals.Items...)
	for i := range report.RiskSignals {
		report.RiskSignals[i].TypeMeta = metav1.TypeMeta{APIVersion: v1alpha1.SchemeGroupVersion.String(), Kind: "RiskSignal"}
	}
	for i := range report.InvestigationRequests {
		ref := report.InvestigationRequests[i].Status.LinkedRiskSignalRef
		if ref == nil || containsRiskSignal(report.RiskSignals, ref.Namespace, ref.Name) {
			continue
		}
		var signal v1alpha1.RiskSignal
		if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, &signal); err != nil {
			return report, fmt.Errorf("get linked RiskSignal %s/%s: %w", ref.Namespace, ref.Name, err)
		}
		signal.TypeMeta = metav1.TypeMeta{APIVersion: v1alpha1.SchemeGroupVersion.String(), Kind: "RiskSignal"}
		report.RiskSignals = append(report.RiskSignals, signal)
	}
	sortRiskSignals(report.RiskSignals)
	return report, nil
}

func sortInvestigationRequests(items []v1alpha1.InvestigationRequest) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreationTimestamp.Equal(&items[j].CreationTimestamp) {
			return items[i].Name < items[j].Name
		}
		return items[i].CreationTimestamp.Before(&items[j].CreationTimestamp)
	})
}

func sortRiskSignals(items []v1alpha1.RiskSignal) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreationTimestamp.Equal(&items[j].CreationTimestamp) {
			if items[i].Namespace == items[j].Namespace {
				return items[i].Name < items[j].Name
			}
			return items[i].Namespace < items[j].Namespace
		}
		return items[i].CreationTimestamp.Before(&items[j].CreationTimestamp)
	})
}

func containsRiskSignal(items []v1alpha1.RiskSignal, namespace, name string) bool {
	for i := range items {
		if items[i].Namespace == namespace && items[i].Name == name {
			return true
		}
	}
	return false
}

func writeRiskRuleReport(w io.Writer, report riskRuleReport, output string) error {
	var (
		data []byte
		err  error
	)
	if output == "yaml" {
		data, err = yaml.Marshal(report)
	} else {
		data, err = json.MarshalIndent(report, "", "  ")
	}
	if err != nil {
		return fmt.Errorf("encode RiskRule report: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}
