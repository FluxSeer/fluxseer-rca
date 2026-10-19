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
	riskRuleReportSchemaVersion    = "fluxseer-riskrule-report/v1"
	agentActionReportSchemaVersion = "fluxseer-agentaction-report/v1"
	riskRuleLabelKey               = "fluxseer-rca.aiops.platform/risk-rule"
	riskSignalRefAnnotation        = "fluxseer-rca.aiops.platform/risk-signal-ref"
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

type agentActionReportSelection struct {
	Namespace   string `json:"namespace"`
	AgentAction string `json:"agentAction"`
}

type agentActionReport struct {
	SchemaVersion   string                         `json:"schemaVersion"`
	Selection       agentActionReportSelection     `json:"selection"`
	AgentAction     v1alpha1.AgentAction           `json:"agentAction"`
	RemediationPlan *v1alpha1.RemediationPlan      `json:"remediationPlan,omitempty"`
	RiskSignal      *v1alpha1.RiskSignal           `json:"riskSignal,omitempty"`
	Verification    *v1alpha1.InvestigationRequest `json:"verification,omitempty"`
}

func runReport(args []string, stdout, stderr io.Writer) error {
	opts, resource, name, err := parseReportArgs(args, stderr)
	if err != nil {
		return err
	}
	if resource != "riskrule" && resource != "agentaction" {
		return fmt.Errorf("unsupported report resource %q; use riskrule or agentaction", resource)
	}
	if opts.output == "markdown" && resource != "riskrule" {
		return errors.New("markdown output is currently supported for riskrule reports only")
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load kubernetes config: %w", err)
	}
	kubeClient, err := client.New(cfg, client.Options{Scheme: buildScheme()})
	if err != nil {
		return fmt.Errorf("create kubernetes client: %w", err)
	}
	var output any
	if resource == "agentaction" {
		report, reportErr := buildAgentActionReport(context.Background(), kubeClient, opts.namespace, name)
		if reportErr != nil {
			return reportErr
		}
		output = report
	} else {
		report, reportErr := buildRiskRuleReport(context.Background(), kubeClient, opts.namespace, name)
		if reportErr != nil {
			return reportErr
		}
		output = report
	}
	return writeReport(stdout, output, opts.output)
}

func parseReportArgs(args []string, stderr io.Writer) (reportOptions, string, string, error) {
	opts := reportOptions{namespace: "fluxseer-rca-system", output: "json"}
	if len(args) < 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		return opts, "", "", errors.New("usage: fluxseer report <riskrule|agentaction> <name> [flags]")
	}
	resource := strings.ToLower(strings.TrimSpace(args[0]))
	name := strings.TrimSpace(args[1])
	fs := flag.NewFlagSet("fluxseer report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.namespace, "namespace", opts.namespace, "RiskRule namespace")
	fs.StringVar(&opts.namespace, "n", opts.namespace, "RiskRule namespace")
	fs.StringVar(&opts.output, "output", opts.output, "output format: json, yaml, or markdown")
	fs.StringVar(&opts.output, "o", opts.output, "output format: json, yaml, or markdown")
	if err := fs.Parse(args[2:]); err != nil {
		return opts, "", "", err
	}
	if len(fs.Args()) != 0 || name == "" {
		return opts, "", "", errors.New("usage: fluxseer report <riskrule|agentaction> <name> [flags]")
	}
	opts.output = strings.ToLower(strings.TrimSpace(opts.output))
	if opts.output != "json" && opts.output != "yaml" && opts.output != "markdown" {
		return opts, "", "", fmt.Errorf("unsupported output format %q; use json, yaml, or markdown", opts.output)
	}
	return opts, resource, name, nil
}

func buildAgentActionReport(ctx context.Context, kubeClient client.Client, namespace, name string) (agentActionReport, error) {
	report := agentActionReport{
		SchemaVersion: agentActionReportSchemaVersion,
		Selection:     agentActionReportSelection{Namespace: namespace, AgentAction: name},
	}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &report.AgentAction); err != nil {
		return report, fmt.Errorf("get AgentAction %s/%s: %w", namespace, name, err)
	}
	report.AgentAction.TypeMeta = metav1.TypeMeta{APIVersion: v1alpha1.SchemeGroupVersion.String(), Kind: "AgentAction"}
	for _, owner := range report.AgentAction.OwnerReferences {
		if owner.Kind != "RemediationPlan" || owner.Name == "" {
			continue
		}
		var plan v1alpha1.RemediationPlan
		if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: owner.Name}, &plan); err != nil {
			return report, fmt.Errorf("get RemediationPlan %s/%s: %w", namespace, owner.Name, err)
		}
		plan.TypeMeta = metav1.TypeMeta{APIVersion: v1alpha1.SchemeGroupVersion.String(), Kind: "RemediationPlan"}
		report.RemediationPlan = &plan
		break
	}
	if ref := namespacedReportRef(report.AgentAction.Annotations[riskSignalRefAnnotation], namespace); ref != nil {
		var signal v1alpha1.RiskSignal
		if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, &signal); err != nil {
			return report, fmt.Errorf("get RiskSignal %s/%s: %w", ref.Namespace, ref.Name, err)
		}
		signal.TypeMeta = metav1.TypeMeta{APIVersion: v1alpha1.SchemeGroupVersion.String(), Kind: "RiskSignal"}
		report.RiskSignal = &signal
	}
	if ref := report.AgentAction.Status.Effectiveness; ref != nil && report.AgentAction.Status.Effectiveness.VerificationRef != nil {
		verificationRef := report.AgentAction.Status.Effectiveness.VerificationRef
		var verification v1alpha1.InvestigationRequest
		if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: verificationRef.Namespace, Name: verificationRef.Name}, &verification); err != nil {
			return report, fmt.Errorf("get verification InvestigationRequest %s/%s: %w", verificationRef.Namespace, verificationRef.Name, err)
		}
		verification.TypeMeta = metav1.TypeMeta{APIVersion: v1alpha1.SchemeGroupVersion.String(), Kind: "InvestigationRequest"}
		report.Verification = &verification
	}
	return report, nil
}

func namespacedReportRef(value, defaultNamespace string) *v1alpha1.NamespacedObjectReference {
	parts := strings.SplitN(strings.TrimSpace(value), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return nil
	}
	if len(parts) == 1 {
		return &v1alpha1.NamespacedObjectReference{Name: parts[0], Namespace: defaultNamespace}
	}
	return &v1alpha1.NamespacedObjectReference{Name: parts[1], Namespace: parts[0]}
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
	if output == "markdown" {
		return writeRiskRuleReportMarkdown(w, report)
	}
	return writeReport(w, report, output)
}

func writeRiskRuleReportMarkdown(w io.Writer, report riskRuleReport) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# RiskRule report: %s\n\n", markdownText(report.RiskRule.Name))
	fmt.Fprintf(&b, "- Namespace: `%s`\n- Report schema: `%s`\n\n", markdownText(report.RiskRule.Namespace), riskRuleReportSchemaVersion)
	if len(report.InvestigationRequests) == 0 {
		b.WriteString("No InvestigationRequest is linked to this RiskRule.\n")
	} else {
		for index := range report.InvestigationRequests {
			writeInvestigationMarkdown(&b, &report.InvestigationRequests[index])
		}
	}
	if len(report.RiskSignals) > 0 {
		b.WriteString("## RiskSignals\n\n")
		for i := range report.RiskSignals {
			signal := &report.RiskSignals[i]
			fmt.Fprintf(&b, "- `%s/%s`: phase `%s`, severity `%s`, type `%s`", markdownText(signal.Namespace), markdownText(signal.Name), markdownText(signal.Status.Phase), markdownText(signal.Spec.Severity), markdownText(signal.Spec.SignalType))
			if signal.Status.RCASummary != "" {
				fmt.Fprintf(&b, " — %s", markdownText(signal.Status.RCASummary))
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	if err != nil {
		return fmt.Errorf("write Markdown RiskRule report: %w", err)
	}
	return nil
}

func writeInvestigationMarkdown(b *strings.Builder, request *v1alpha1.InvestigationRequest) {
	fmt.Fprintf(b, "## InvestigationRequest: %s\n\n", markdownText(request.Name))
	target := request.Spec.Target
	fmt.Fprintf(b, "- Target: `%s/%s/%s`\n- Phase: `%s`\n- Outcome: `%s`\n", markdownText(target.Kind), markdownText(target.Namespace), markdownText(target.Name), markdownText(request.Status.Phase), markdownText(request.Status.Outcome))
	confirmed := request.Status.Outcome == v1alpha1.InvestigationOutcomeConfirmed &&
		request.Status.Verdict != nil && request.Status.Verdict.Outcome == v1alpha1.InvestigationOutcomeConfirmed
	if confirmed {
		fmt.Fprintf(b, "- Root cause: **Confirmed** — %s\n", markdownText(request.Status.Verdict.Summary))
		if request.Status.Verdict.RootCauseType != "" {
			fmt.Fprintf(b, "- Root cause type: `%s`\n", markdownText(request.Status.Verdict.RootCauseType))
		}
	} else {
		b.WriteString("- Root cause: **Not confirmed**\n")
		if request.Status.Summary != "" {
			fmt.Fprintf(b, "- Assessment: %s\n", markdownText(request.Status.Summary))
		}
	}
	if request.Status.Failure != nil {
		fmt.Fprintf(b, "- Investigation failure: `%s`", markdownText(request.Status.Failure.Code))
		if request.Status.Failure.Stage != "" {
			fmt.Fprintf(b, " (stage `%s`)", markdownText(request.Status.Failure.Stage))
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	b.WriteString("### Observed evidence\n\n")
	if len(request.Status.EvidenceRefs) == 0 {
		b.WriteString("No evidence references were retained.\n\n")
	} else {
		for _, ref := range request.Status.EvidenceRefs {
			label := strings.TrimSpace(strings.Join(nonEmptyReportStrings(ref.Kind, ref.Source, ref.Reason), " / "))
			if label == "" {
				label = "Evidence"
			}
			fmt.Fprintf(b, "- **%s**", markdownText(label))
			if ref.CollectedAt != nil {
				fmt.Fprintf(b, " (%s)", ref.CollectedAt.UTC().Format("2006-01-02T15:04:05Z"))
			}
			if ref.Summary != "" {
				fmt.Fprintf(b, ": %s", markdownText(ref.Summary))
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	if len(request.Status.Claims) > 0 {
		b.WriteString("### RCA claims\n\n")
		for _, claim := range request.Status.Claims {
			fmt.Fprintf(b, "- **%s** — %s", markdownText(claim.Verification), markdownText(claim.Statement))
			if len(claim.EvidenceRefs) > 0 {
				refs := make([]string, 0, len(claim.EvidenceRefs))
				for _, ref := range claim.EvidenceRefs {
					refs = append(refs, "`"+markdownText(ref)+"`")
				}
				fmt.Fprintf(b, " (evidence: %s)", strings.Join(refs, ", "))
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	if request.Status.Hypothesis != "" || len(request.Status.AlternativeHypotheses) > 0 {
		b.WriteString("### Hypotheses (unconfirmed)\n\n")
		if request.Status.Hypothesis != "" {
			fmt.Fprintf(b, "- %s\n", markdownText(request.Status.Hypothesis))
		}
		for _, hypothesis := range request.Status.AlternativeHypotheses {
			fmt.Fprintf(b, "- %s", markdownText(hypothesis.Statement))
			if hypothesis.Disposition != "" {
				fmt.Fprintf(b, " — `%s`", markdownText(hypothesis.Disposition))
			}
			if len(hypothesis.EvidenceRefs) > 0 {
				refs := make([]string, 0, len(hypothesis.EvidenceRefs))
				for _, ref := range hypothesis.EvidenceRefs {
					refs = append(refs, "`"+markdownText(ref)+"`")
				}
				fmt.Fprintf(b, " (evidence: %s)", strings.Join(refs, ", "))
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	writeEvidenceGapsMarkdown(b, request)
	writeCrashLoopNextStepsMarkdown(b, request)
}

func writeEvidenceGapsMarkdown(b *strings.Builder, request *v1alpha1.InvestigationRequest) {
	b.WriteString("### Evidence status\n\n")
	seen := map[string]bool{}
	if request.Status.Failure != nil {
		fmt.Fprintf(b, "- Collection stopped: `%s`\n", markdownText(request.Status.Failure.Code))
		seen[request.Status.Failure.Code] = true
	}
	if coverage := request.Status.EvidenceCoverage; coverage != nil && coverage.Profile != "" {
		fmt.Fprintf(b, "- Profile: `%s`\n", markdownText(coverage.Profile))
		for _, check := range coverage.CompletedChecks {
			seen[check] = true
			fmt.Fprintf(b, "- Completed check: `%s`\n", markdownText(check))
		}
	}
	for _, missing := range request.Status.MissingEvidence {
		line := strings.TrimSpace(strings.Join(nonEmptyReportStrings(missing.Source, missing.Reason), " — "))
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		fmt.Fprintf(b, "- Missing or insufficient: `%s`\n", markdownText(line))
	}
	if coverage := request.Status.EvidenceCoverage; coverage != nil {
		for _, check := range coverage.IncompleteChecks {
			if seen[check] {
				continue
			}
			seen[check] = true
			fmt.Fprintf(b, "- Incomplete check: `%s`\n", markdownText(check))
		}
	}
	if request.Status.Degradation != nil {
		for _, reason := range request.Status.Degradation.Reasons {
			if reason.Code == "" {
				continue
			}
			// Degradation messages may contain raw upstream responses or request
			// details. Keep the public Markdown projection to the stable reason code.
			fmt.Fprintf(b, "- Degradation: `%s`\n", markdownText(reason.Code))
		}
	}
	if len(seen) == 0 && (request.Status.Degradation == nil || len(request.Status.Degradation.Reasons) == 0) {
		b.WriteString("No evidence gaps are recorded.\n")
	}
	b.WriteByte('\n')
}

func writeCrashLoopNextStepsMarkdown(b *strings.Builder, request *v1alpha1.InvestigationRequest) {
	profile := strings.TrimSpace(request.Spec.EvidenceRequirements.Profile)
	if !strings.EqualFold(profile, "CrashLoopBackOff") && !strings.EqualFold(profile, "CrashLoopBackOffV2") {
		return
	}
	if request.Status.Outcome == v1alpha1.InvestigationOutcomeConfirmed && request.Status.Verdict != nil && request.Status.Verdict.Outcome == v1alpha1.InvestigationOutcomeConfirmed {
		return
	}
	b.WriteString("### Suggested next steps (read-only)\n\n")
	switch {
	case request.Status.Failure != nil && (request.Status.Failure.Code == "DataSourceNotFound" || request.Status.Failure.Code == "DataSourceNotSpecified"):
		if requestReferencesDatasource(request, "loki") {
			b.WriteString("- Configure the referenced Loki datasource, then rerun the investigation. No root cause was inferred from the datasource failure.\n")
		} else {
			b.WriteString("- Configure or correct the declared datasource, then rerun the investigation. No root cause was inferred from the datasource failure.\n")
		}
	case request.Status.Failure != nil && isDatasourceQueryFailure(request.Status.Failure.Code):
		if strings.EqualFold(profile, "CrashLoopBackOffV2") {
			b.WriteString("- Restore read access to the target Kubernetes Pods and configured Event source, then rerun the investigation for the same target and time window.\n")
		} else {
			b.WriteString("- Restore access to the declared log datasource and rerun the investigation for the same target and time window.\n")
		}
	case hasMissingEvidenceReason(request.Status.MissingEvidence, "CrashLoopApplicationEvidenceMissing"):
		if hasExplicitLogQuery(request.Spec.Queries) {
			b.WriteString("- Check that the configured log query covers this workload and incident time window and returns application failure context.\n")
		} else if len(request.Spec.DataSources) > 0 {
			b.WriteString("- Check that the controller-planned datasource set includes a log source such as Loki and returns application failure context for this workload and incident time window.\n")
		} else {
			b.WriteString("- Check that a Loki datasource is configured and included in this investigation; query application logs for the target workload and incident time window.\n")
		}
	case hasMissingEvidenceReason(request.Status.MissingEvidence, "CrashLoopApplicationLogRequired"):
		b.WriteString("- To confirm the process-level cause, add an explicitly scoped, policy-allowed Loki query for this workload and incident window; the retained termination status alone does not explain why the process exited.\n")
	case hasMissingEvidenceReason(request.Status.MissingEvidence, "ContainerTerminationEvidenceMissing"):
		b.WriteString("- Check that the target workload still has in-scope Pods and that the controller can read their container status; rerun within the incident time window.\n")
	case hasMissingEvidenceReason(request.Status.MissingEvidence, "ContainerTerminationEvidenceStale"):
		b.WriteString("- Collect a fresh container termination status and rerun with an incident window that includes its finish time; the retained status is outside the requested window.\n")
	case hasMissingEvidenceReason(request.Status.MissingEvidence, "ContainerTerminationEvidenceTruncated"):
		b.WriteString("- Narrow the target workload or reduce the number of matching Pods so the bounded termination status scan can cover the full target set.\n")
	case strings.EqualFold(profile, "CrashLoopBackOffV2") && !hasCrashLoopApplicationFailureLog(request.Status.EvidenceRefs):
		if hasExplicitLogQuery(request.Spec.Queries) {
			b.WriteString("- Check that the scoped Loki query returns application failure context for this workload and incident window; termination status alone does not explain why the process exited.\n")
		} else {
			b.WriteString("- To investigate a process-level cause, include an explicitly scoped, policy-allowed Loki query for this workload and incident window; termination status alone does not explain why the process exited.\n")
		}
	default:
		b.WriteString("- Review the retained Event and log evidence for startup failure context. If it does not support a causal claim, retain the Inconclusive outcome and collect additional permitted evidence.\n")
	}
	b.WriteString("- Treat these steps as diagnostics only; they do not confirm a root cause or authorize remediation.\n\n")
}

func hasCrashLoopApplicationFailureLog(refs []v1alpha1.EvidenceRef) bool {
	for _, ref := range refs {
		if !strings.EqualFold(strings.TrimSpace(ref.Kind), "log") {
			continue
		}
		summary := strings.ToLower(ref.Summary)
		for _, marker := range []string{"panic", "fatal", "startup error", "startup failure", "failed to initialize", "invalid configuration", "configuration error", "exception", "segmentation fault"} {
			if strings.Contains(summary, marker) {
				return true
			}
		}
	}
	return false
}

func requestReferencesDatasource(request *v1alpha1.InvestigationRequest, name string) bool {
	for _, source := range request.Spec.DataSources {
		if strings.EqualFold(strings.TrimSpace(source.Name), name) {
			return true
		}
	}
	for _, query := range request.Spec.Queries {
		if strings.EqualFold(strings.TrimSpace(query.DatasourceRef.Name), name) {
			return true
		}
	}
	return false
}

func hasMissingEvidenceReason(items []v1alpha1.RCAMissingEvidence, reason string) bool {
	for _, item := range items {
		if item.Reason == reason {
			return true
		}
	}
	return false
}

func hasExplicitLogQuery(queries []v1alpha1.InvestigationQuery) bool {
	for _, query := range queries {
		if strings.EqualFold(strings.TrimSpace(query.QueryType), "log") {
			return true
		}
	}
	return false
}

func isDatasourceQueryFailure(code string) bool {
	switch code {
	case "DatasourceQueryFailed", "DatasourceAuthFailed", "DatasourceRateLimited", "DatasourceUnavailable", "DatasourceRequestInvalid", "InvalidDatasourceResponse":
		return true
	default:
		return false
	}
}

func nonEmptyReportStrings(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func markdownText(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "`", "\\`")
	for _, marker := range []string{"*", "_", "[", "]", "<", ">", "|"} {
		value = strings.ReplaceAll(value, marker, "\\"+marker)
	}
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.TrimSpace(value)
}

func writeReport(w io.Writer, report any, output string) error {
	var (
		data []byte
		err  error
	)
	if output == "markdown" {
		if riskRule, ok := report.(riskRuleReport); ok {
			return writeRiskRuleReportMarkdown(w, riskRule)
		}
		return fmt.Errorf("Markdown output is unsupported for report type %T", report)
	} else if output == "yaml" {
		data, err = yaml.Marshal(report)
	} else {
		data, err = json.MarshalIndent(report, "", "  ")
	}
	if err != nil {
		return fmt.Errorf("encode public report: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}
