package guardrails

import (
	"context"
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

const (
	PolicyMatchResource  = "Resource"
	PolicyMatchNamespace = "Namespace"
	PolicyMatchCluster   = "Cluster"
	PolicySourceCRD      = "ApprovalPolicy"
	PolicySourceLegacy   = "LegacyGuardrails"
	policyScopeCluster   = "Cluster"
)

// PolicyReviewInput extends the legacy review input with labels used by selectors.
type PolicyReviewInput struct {
	ReviewInput
	ResourceLabels  map[string]string
	NamespaceLabels map[string]string
}

// PolicyReference identifies the policy version used for an evaluation audit.
type PolicyReference struct {
	APIVersion      string
	Kind            string
	Name            string
	Namespace       string
	UID             string
	ResourceVersion string
	Generation      int64
	Version         string
	MatchLevel      string
	Priority        int32
}

// PolicyEvaluation contains the approval decision and its policy provenance.
type PolicyEvaluation struct {
	Decision               domain.ApprovalDecision
	Policy                 *PolicyReference
	Source                 string
	ApprovalTimeoutSeconds int64
	Escalation             *v1alpha1.EscalationConfig
}

// PolicyEngine resolves ApprovalPolicy resources and falls back to legacy guardrails.
type PolicyEngine struct {
	Reader  client.Reader
	Legacy  *Engine
	Enabled bool
}

func NewPolicyEngine(reader client.Reader, legacy *Engine, enabled bool) *PolicyEngine {
	return &PolicyEngine{Reader: reader, Legacy: legacy, Enabled: enabled}
}

// Evaluate resolves the highest-priority matching policy and evaluates its rules.
func (e *PolicyEngine) Evaluate(ctx context.Context, input PolicyReviewInput) (PolicyEvaluation, error) {
	if !e.Enabled {
		return e.evaluateLegacy(input), nil
	}
	if e.Reader == nil {
		return PolicyEvaluation{}, fmt.Errorf("policy engine requires a Kubernetes reader when enabled")
	}

	var policies v1alpha1.ApprovalPolicyList
	if err := e.Reader.List(ctx, &policies); err != nil {
		return PolicyEvaluation{}, fmt.Errorf("list ApprovalPolicy resources: %w", err)
	}
	matched, err := selectApprovalPolicy(policies.Items, input)
	if err != nil {
		return PolicyEvaluation{}, err
	}
	if matched == nil {
		return e.evaluateLegacy(input), nil
	}

	decision, timeout := evaluatePolicy(*matched.policy, input)
	return PolicyEvaluation{
		Decision:               decision,
		Policy:                 policyReference(matched.policy, matched.level),
		Source:                 PolicySourceCRD,
		ApprovalTimeoutSeconds: timeout,
		Escalation:             matched.policy.Spec.Escalation.DeepCopy(),
	}, nil
}

func (e *PolicyEngine) evaluateLegacy(input PolicyReviewInput) PolicyEvaluation {
	if e.Legacy == nil {
		return PolicyEvaluation{
			Decision: domain.ApprovalDecision{
				Action: domain.ApprovalManual,
				Reason: "no policy engine fallback is configured",
			},
			Source: PolicySourceLegacy,
		}
	}
	return PolicyEvaluation{
		Decision: e.Legacy.Evaluate(input.ReviewInput),
		Source:   PolicySourceLegacy,
	}
}

type policyMatch struct {
	policy *v1alpha1.ApprovalPolicy
	level  string
	rank   int
}

func selectApprovalPolicy(policies []v1alpha1.ApprovalPolicy, input PolicyReviewInput) (*policyMatch, error) {
	matches := make([]policyMatch, 0, len(policies))
	for i := range policies {
		policy := &policies[i]
		if !policy.Spec.Enabled || policy.Status.Phase == "Invalid" || policy.Status.Phase == "Disabled" {
			continue
		}
		level, rank, matched, err := matchApprovalPolicy(policy, input)
		if err != nil {
			return nil, fmt.Errorf("match ApprovalPolicy %s/%s: %w", policy.Namespace, policy.Name, err)
		}
		if matched {
			matches = append(matches, policyMatch{policy: policy, level: level, rank: rank})
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank > matches[j].rank
		}
		if matches[i].policy.Spec.Priority != matches[j].policy.Spec.Priority {
			return matches[i].policy.Spec.Priority > matches[j].policy.Spec.Priority
		}
		left := matches[i].policy.Namespace + "/" + matches[i].policy.Name
		right := matches[j].policy.Namespace + "/" + matches[j].policy.Name
		return left < right
	})
	return &matches[0], nil
}

func matchApprovalPolicy(policy *v1alpha1.ApprovalPolicy, input PolicyReviewInput) (string, int, bool, error) {
	resourceMatch, err := selectorMatches(policy.Spec.ResourceSelector, input.ResourceLabels)
	if err != nil {
		return "", 0, false, err
	}
	if resourceMatch {
		namespaceMatch, err := selectorMatches(policy.Spec.NamespaceSelector, input.NamespaceLabels)
		if err != nil {
			return "", 0, false, err
		}
		if policy.Spec.Scope == policyScopeCluster || policy.Namespace == input.Resource.Namespace || namespaceMatch {
			return PolicyMatchResource, 3, true, nil
		}
	}

	namespaceMatch, err := selectorMatches(policy.Spec.NamespaceSelector, input.NamespaceLabels)
	if err != nil {
		return "", 0, false, err
	}
	if policy.Spec.Scope != policyScopeCluster && policy.Spec.NamespaceSelector == nil && policy.Namespace == input.Resource.Namespace {
		namespaceMatch = true
	}
	if namespaceMatch {
		return PolicyMatchNamespace, 2, true, nil
	}
	if policy.Spec.Scope == policyScopeCluster {
		return PolicyMatchCluster, 1, true, nil
	}
	return "", 0, false, nil
}

func selectorMatches(selector *metav1.LabelSelector, values map[string]string) (bool, error) {
	// Note: A nil selector in PolicyEngine means this selector condition does not apply.
	// It does not mean the policy is excluded; the policy may still match via namespace or cluster scope.
	if selector == nil {
		return false, nil
	}
	converted, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return false, err
	}
	return converted.Matches(labels.Set(values)), nil
}

func evaluatePolicy(policy v1alpha1.ApprovalPolicy, input PolicyReviewInput) (domain.ApprovalDecision, int64) {
	actionType := input.Reasoning.Remediation.ActionType
	for _, rule := range policy.Spec.ActionTypeRules {
		if rule.ActionType == actionType {
			return decisionFromRule(rule.Action, rule.Reason, "action type policy rule"), policy.Spec.DefaultApprovalTimeout
		}
	}

	severity := normalizeSeverity(string(input.Reasoning.Severity))
	for _, rule := range policy.Spec.SeverityRules {
		if severityInRange(severity, rule.MinSeverity, rule.MaxSeverity) {
			timeout := policy.Spec.DefaultApprovalTimeout
			if rule.TimeoutSec > 0 {
				timeout = rule.TimeoutSec
			}
			return decisionFromRule(rule.Action, rule.Reason, "severity policy rule"), timeout
		}
	}

	return domain.ApprovalDecision{
		Action:       domain.ApprovalManual,
		Reason:       "matching ApprovalPolicy has no rule for this action or severity",
		DryRunResult: "dry-run: policy match requires explicit review",
	}, policy.Spec.DefaultApprovalTimeout
}

func decisionFromRule(action, reason, fallbackReason string) domain.ApprovalDecision {
	decision := domain.ApprovalDecision{Reason: reason}
	if decision.Reason == "" {
		decision.Reason = fallbackReason
	}
	switch action {
	case v1alpha1.ApprovalActionAuto:
		decision.Action = domain.ApprovalAuto
		decision.ApprovedBy = "fluxseer-rca-policy-engine"
		decision.DryRunResult = "dry-run: policy approved for execution"
	case v1alpha1.ApprovalActionReject:
		decision.Action = domain.ApprovalReject
	case v1alpha1.ApprovalActionManual, "":
		decision.Action = domain.ApprovalManual
		decision.DryRunResult = "dry-run: policy requires human approval"
	default:
		decision.Action = domain.ApprovalReject
		decision.Reason = fmt.Sprintf("unsupported ApprovalPolicy action %q", action)
	}
	return decision
}

func severityInRange(value, minimum, maximum string) bool {
	rank := policySeverityRank(value)
	if rank == 0 {
		return false
	}
	if minimum != "" {
		minimumRank := policySeverityRank(minimum)
		if minimumRank == 0 || rank < minimumRank {
			return false
		}
	}
	if maximum != "" {
		maximumRank := policySeverityRank(maximum)
		if maximumRank == 0 || rank > maximumRank {
			return false
		}
	}
	return true
}

func normalizeSeverity(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func policySeverityRank(value string) int {
	switch normalizeSeverity(value) {
	case "info":
		return 1
	case "low":
		return 2
	case "medium":
		return 3
	case "high":
		return 4
	case "critical":
		return 5
	default:
		return 0
	}
}

func policyReference(policy *v1alpha1.ApprovalPolicy, level string) *PolicyReference {
	version := policy.ResourceVersion
	if version == "" {
		version = fmt.Sprintf("generation-%d", policy.Generation)
	}
	return &PolicyReference{
		APIVersion:      schema.GroupVersion{Group: v1alpha1.Group, Version: v1alpha1.Version}.String(),
		Kind:            "ApprovalPolicy",
		Name:            policy.Name,
		Namespace:       policy.Namespace,
		UID:             string(policy.UID),
		ResourceVersion: policy.ResourceVersion,
		Generation:      policy.Generation,
		Version:         version,
		MatchLevel:      level,
		Priority:        policy.Spec.Priority,
	}
}
