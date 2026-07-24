package agentexecutor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fluxagent/api/v1alpha1"
)

type options struct {
	executorType string
	command      string
	args         []string
	promptArg    bool
	timeout      time.Duration
	resultName   string
	namespace    string
	evidencePath string
	promptPath   string
}

type repeatedFlag []string

func (f *repeatedFlag) String() string {
	return strings.Join(*f, " ")
}

func (f *repeatedFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type analysisPayload struct {
	Summary         string   `json:"summary,omitempty"`
	RootCause       string   `json:"rootCause,omitempty"`
	Confidence      float64  `json:"confidence,omitempty"`
	ValidationSteps []string `json:"validationSteps,omitempty"`
	Recommendations []string `json:"recommendations,omitempty"`
	MissingEvidence []string `json:"missingEvidence,omitempty"`
}

func Run(args []string, stdout, stderr io.Writer) error {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		return err
	}
	if opts.resultName == "" {
		return errors.New("FLUXAGENT_ANALYSIS_RESULT_NAME or --result-name is required")
	}
	if opts.namespace == "" {
		body, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err == nil {
			opts.namespace = strings.TrimSpace(string(body))
		}
	}
	if opts.namespace == "" {
		return errors.New("POD_NAMESPACE, serviceaccount namespace file, or --namespace is required")
	}

	prompt, err := os.ReadFile(opts.promptPath)
	if err != nil {
		return fmt.Errorf("read prompt %q: %w", opts.promptPath, err)
	}
	evidence, err := os.ReadFile(opts.evidencePath)
	if err != nil {
		return fmt.Errorf("read evidence %q: %w", opts.evidencePath, err)
	}

	ctx := context.Background()
	if opts.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout)
		defer cancel()
	}

	output, runErr := runCLI(ctx, opts, prompt, evidence)
	if writeErr := os.WriteFile(envOrDefault("FLUXAGENT_RESULT_PATH", "/var/run/fluxagent/result/result.json"), output, 0o600); writeErr != nil {
		_, _ = fmt.Fprintf(stderr, "write raw agent output: %v\n", writeErr)
	}
	if _, err := stdout.Write(output); err != nil {
		return fmt.Errorf("write executor output: %w", err)
	}

	parsed, parseErr := parseAnalysisOutput(output)
	cfg := ctrl.GetConfigOrDie()
	scheme := runtimeScheme()
	kubeClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("create kubernetes client: %w", err)
	}

	now := metav1.Now()
	var result v1alpha1.AgentAnalysisResult
	key := client.ObjectKey{Name: opts.resultName, Namespace: opts.namespace}
	if err := kubeClient.Get(context.Background(), key, &result); err != nil {
		return fmt.Errorf("get AgentAnalysisResult %s/%s: %w", opts.namespace, opts.resultName, err)
	}
	if result.Status.StartedAt == nil {
		result.Status.StartedAt = &now
	}
	result.Status.CompletedAt = &now

	if runErr != nil {
		setFailedStatus(&result, fmt.Sprintf("agent CLI failed: %v", runErr), now)
	} else if parseErr != nil {
		setFailedStatus(&result, fmt.Sprintf("parse agent output: %v", parseErr), now)
	} else {
		result.Status.Summary = parsed.Summary
		result.Status.RootCause = parsed.RootCause
		result.Status.Confidence = parsed.Confidence
		result.Status.ValidationSteps = parsed.ValidationSteps
		result.Status.Recommendations = parsed.Recommendations
		result.Status.MissingEvidence = parsed.MissingEvidence
		setSucceededStatus(&result, "agent analysis result parsed", now)
	}
	if err := kubeClient.Status().Update(context.Background(), &result); err != nil && !apierrors.IsConflict(err) {
		return fmt.Errorf("update AgentAnalysisResult status: %w", err)
	}

	if runErr != nil {
		return runErr
	}
	return parseErr
}

func parseArgs(args []string, stderr io.Writer) (options, error) {
	opts := options{
		executorType: envOrDefault("FLUXAGENT_EXECUTOR_TYPE", v1alpha1.AgentExecutorTypeCodexCLI),
		command:      envOrDefault("FLUXAGENT_CLI_COMMAND", "codex"),
		resultName:   os.Getenv("FLUXAGENT_ANALYSIS_RESULT_NAME"),
		namespace:    os.Getenv("POD_NAMESPACE"),
		evidencePath: envOrDefault("FLUXAGENT_EVIDENCE_PATH", "/var/run/fluxagent/evidence/risk-signal.json"),
		promptPath:   envOrDefault("FLUXAGENT_PROMPT_PATH", "/var/run/fluxagent/evidence/prompt.txt"),
		timeout:      15 * time.Minute,
	}
	if raw := os.Getenv("FLUXAGENT_TIMEOUT_SECONDS"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			opts.timeout = time.Duration(seconds) * time.Second
		}
	}
	var repeated repeatedFlag
	fs := flag.NewFlagSet("fluxagent-agent-executor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.executorType, "type", opts.executorType, "agent executor type")
	fs.StringVar(&opts.command, "command", opts.command, "CLI command to run")
	fs.Var(&repeated, "arg", "CLI argument; repeatable")
	fs.BoolVar(&opts.promptArg, "prompt-arg", false, "append the generated prompt as the final CLI argument")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "CLI execution timeout")
	fs.StringVar(&opts.resultName, "result-name", opts.resultName, "AgentAnalysisResult name")
	fs.StringVar(&opts.namespace, "namespace", opts.namespace, "AgentAnalysisResult namespace")
	fs.StringVar(&opts.evidencePath, "evidence-path", opts.evidencePath, "RiskSignal evidence path")
	fs.StringVar(&opts.promptPath, "prompt-path", opts.promptPath, "prompt path")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	opts.args = append(opts.args, repeated...)
	if len(opts.args) == 0 {
		opts.args = defaultArgs(opts.executorType)
		opts.promptArg = true
	}
	return opts, nil
}

func runCLI(ctx context.Context, opts options, prompt, evidence []byte) ([]byte, error) {
	cliArgs := append([]string(nil), opts.args...)
	if opts.promptArg {
		cliArgs = append(cliArgs, string(prompt))
	}
	cmd := exec.CommandContext(ctx, opts.command, cliArgs...)
	cmd.Stdin = bytes.NewReader(evidence)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.Bytes(), fmt.Errorf("agent CLI timed out after %s", opts.timeout)
	}
	if err != nil {
		if stderr.Len() > 0 {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func parseAnalysisOutput(output []byte) (analysisPayload, error) {
	var parsed analysisPayload
	if err := json.Unmarshal(output, &parsed); err == nil && parsed.hasContent() {
		return parsed, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var candidates []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var direct analysisPayload
		if err := json.Unmarshal([]byte(line), &direct); err == nil && direct.hasContent() {
			parsed = direct
			continue
		}
		if text := extractTextFromJSONLine([]byte(line)); text != "" {
			candidates = append(candidates, text)
		}
	}
	if err := scanner.Err(); err != nil {
		return parsed, err
	}
	if parsed.hasContent() {
		return parsed, nil
	}
	for i := len(candidates) - 1; i >= 0; i-- {
		if candidate, ok := extractJSONPayload(candidates[i]); ok {
			if err := json.Unmarshal([]byte(candidate), &parsed); err == nil && parsed.hasContent() {
				return parsed, nil
			}
		}
	}
	if candidate, ok := extractJSONPayload(string(output)); ok {
		if err := json.Unmarshal([]byte(candidate), &parsed); err == nil && parsed.hasContent() {
			return parsed, nil
		}
	}
	return parsed, errors.New("no structured analysis JSON found")
}

func (p analysisPayload) hasContent() bool {
	return p.Summary != "" || p.RootCause != "" || p.Confidence != 0 || len(p.ValidationSteps) > 0 || len(p.Recommendations) > 0 || len(p.MissingEvidence) > 0
}

func extractTextFromJSONLine(line []byte) string {
	var payload any
	if err := json.Unmarshal(line, &payload); err != nil {
		return ""
	}
	values := collectStringValues(payload, nil)
	return strings.Join(values, "\n")
}

func collectStringValues(value any, out []string) []string {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			lower := strings.ToLower(key)
			if lower == "text" || lower == "content" || lower == "message" || lower == "output" || lower == "response" {
				out = collectStringValues(nested, out)
			}
		}
	case []any:
		for _, nested := range typed {
			out = collectStringValues(nested, out)
		}
	case string:
		if strings.TrimSpace(typed) != "" {
			out = append(out, typed)
		}
	}
	return out
}

func extractJSONPayload(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return "", false
	}
	return text[start : end+1], true
}

func setSucceededStatus(result *v1alpha1.AgentAnalysisResult, message string, now metav1.Time) {
	result.Status.Phase = v1alpha1.PhaseSucceeded
	result.Status.Message = message
	result.Status.ObservedGeneration = result.Generation
	result.Status.UpdatedAt = now
	setCondition(&result.Status.Conditions, "AgentOutputReady", metav1.ConditionTrue, "OutputParsed", message, result.Generation, now)
}

func setFailedStatus(result *v1alpha1.AgentAnalysisResult, message string, now metav1.Time) {
	result.Status.Phase = v1alpha1.PhaseFailed
	result.Status.Message = message
	result.Status.ObservedGeneration = result.Generation
	result.Status.UpdatedAt = now
	setCondition(&result.Status.Conditions, "AgentOutputReady", metav1.ConditionFalse, "OutputFailed", message, result.Generation, now)
}

func setCondition(conditions *[]metav1.Condition, conditionType string, status metav1.ConditionStatus, reason string, message string, generation int64, now metav1.Time) {
	for i := range *conditions {
		if (*conditions)[i].Type == conditionType {
			(*conditions)[i].Status = status
			(*conditions)[i].Reason = reason
			(*conditions)[i].Message = message
			(*conditions)[i].ObservedGeneration = generation
			(*conditions)[i].LastTransitionTime = now
			return
		}
	}
	*conditions = append(*conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
		LastTransitionTime: now,
	})
}

func defaultArgs(executorType string) []string {
	switch executorType {
	case v1alpha1.AgentExecutorTypeClaudeCLI:
		return []string{"-p", "--output-format", "json"}
	case v1alpha1.AgentExecutorTypeGeminiCLI:
		return []string{"-p", "--json"}
	default:
		return []string{"exec", "--json"}
	}
}

func runtimeScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	return scheme
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
