package executor

import (
	"time"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

// ExecutionOutcome is the backend-neutral execution outcome. Effectiveness
// is intentionally not represented here; it belongs to the follow-up
// verification workflow.
type ExecutionOutcome = domain.ExecutionOutcome

const (
	ExecutionOutcomeSucceeded          = domain.ExecutionOutcomeSucceeded
	ExecutionOutcomeFailed             = domain.ExecutionOutcomeFailed
	ExecutionOutcomeTimedOut           = domain.ExecutionOutcomeTimedOut
	ExecutionOutcomeUnknown            = domain.ExecutionOutcomeUnknown
	ExecutionOutcomePartiallySucceeded = domain.ExecutionOutcomePartiallySucceeded
)

// PolicySnapshot identifies the policy decision that authorized an action.
// The controller owns the snapshot and must revalidate it before dispatch.
type PolicySnapshot struct {
	Reference          string `json:"reference,omitempty"`
	Version            string `json:"version,omitempty"`
	Digest             string `json:"digest,omitempty"`
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
}

// ExecutorRequest is the backend-neutral request contract. It contains
// execution identity and policy context without granting the backend
// authority to approve or reinterpret the action.
type ExecutorRequest struct {
	ExecutionID            string             `json:"executionID,omitempty"`
	IdempotencyKey         string             `json:"idempotencyKey,omitempty"`
	ActionType             string             `json:"actionType"`
	Target                 domain.ResourceRef `json:"target"`
	Parameters             map[string]any     `json:"parameters,omitempty"`
	ApprovedPolicySnapshot PolicySnapshot     `json:"approvedPolicySnapshot,omitempty"`
	ApprovedBy             string             `json:"approvedBy,omitempty"`
	DryRunResult           string             `json:"dryRunResult,omitempty"`
	RollbackPlan           []string           `json:"rollbackPlan,omitempty"`
	Timeout                time.Duration      `json:"timeout,omitempty"`
	Attempt                int                `json:"attempt,omitempty"`
}

// ApprovedAction is retained as a source-compatible alias while callers
// migrate to the explicit ExecutorRequest name.
type ApprovedAction = ExecutorRequest

// ExecutorResult is the executor-facing name for the shared result contract.
type ExecutorResult = domain.ExecutionResult

// StringParameters converts the CRD's compatibility map[string]string
// representation into the executor contract's structured parameter map.
func StringParameters(values map[string]string) map[string]any {
	if len(values) == 0 {
		return nil
	}

	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
