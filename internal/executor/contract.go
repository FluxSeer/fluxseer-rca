package executor

import (
	"context"
	"strings"
	"time"

	"github.com/FluxSeer/fluxseer-rca/internal/canonicaldigest"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

const ExecutorIdentityJSONV1 = "fluxseer-rca-executor-identity-v1"

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
	ActionDigest           string             `json:"actionDigest,omitempty"`
	ActionType             string             `json:"actionType"`
	ActionIndex            int                `json:"actionIndex,omitempty"`
	Target                 domain.ResourceRef `json:"target"`
	TargetUID              string             `json:"targetUID,omitempty"`
	Parameters             map[string]any     `json:"parameters,omitempty"`
	ApprovedPolicySnapshot PolicySnapshot     `json:"approvedPolicySnapshot,omitempty"`
	ApprovedBy             string             `json:"approvedBy,omitempty"`
	DryRunResult           string             `json:"dryRunResult,omitempty"`
	RollbackPlan           []string           `json:"rollbackPlan,omitempty"`
	Timeout                time.Duration      `json:"timeout,omitempty"`
	Attempt                int                `json:"attempt,omitempty"`
}

// IdentityInput contains the immutable action material used to derive
// deterministic execution and idempotency identities.
type IdentityInput struct {
	ActionRef      string
	AgentActionUID string
	Generation     int64
	ActionIndex    int
	Target         domain.ResourceRef
	TargetUID      string
	ActionDigest   string
	ActionType     string
	Parameters     map[string]any
}

type ExecutionIdentity struct {
	ExecutionID    string
	IdempotencyKey string
}

// BuildExecutionIdentity derives stable identities from the desired action.
// ExecutionID and IdempotencyKey intentionally have different prefixes and
// semantics even though both are deterministic for the same action.
func BuildExecutionIdentity(input IdentityInput) ExecutionIdentity {
	digest := canonicaldigest.String(ExecutorIdentityJSONV1, input)
	hexDigest := strings.TrimPrefix(digest, canonicaldigest.AlgorithmSHA256+":")
	shortDigest := hexDigest
	if len(shortDigest) > 32 {
		shortDigest = shortDigest[:32]
	}

	return ExecutionIdentity{
		ExecutionID:    "exec-" + shortDigest,
		IdempotencyKey: digest,
	}
}

// ApprovedAction is retained as a source-compatible alias while callers
// migrate to the explicit ExecutorRequest name.
type ApprovedAction = ExecutorRequest

// ExecutorResult is the executor-facing name for the shared result contract.
type ExecutorResult = domain.ExecutionResult

// ExecutionResolver is an optional recovery contract for executors that can
// read back an external side effect after an uncertain result.
type ExecutionResolver interface {
	Resolve(ctx context.Context, request ExecutorRequest) (ExecutorResult, bool, error)
}

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
