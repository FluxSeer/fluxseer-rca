// Package authorization contains provider-neutral, pure authorization
// decisions for evidence provenance. It does not read Kubernetes or mutate
// runtime state.
package authorization

import (
	"sort"
	"strings"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

// Disposition is the result of evaluating one evidence-to-target relation.
// Unresolved is deliberately distinct from Rejected: missing trusted identity
// evidence is not proof that the target is invalid.
type Disposition string

const (
	DispositionAuthorized Disposition = "Authorized"
	DispositionRejected   Disposition = "Rejected"
	DispositionUnresolved Disposition = "Unresolved"
)

const (
	ReasonEvidenceUnauthorized      = "EvidenceUnauthorized"
	ReasonCrossScopeTarget          = "CrossScopeTarget"
	ReasonUnauthorizedCluster       = "UnauthorizedCluster"
	ReasonInvalidTargetRef          = "InvalidTargetRef"
	ReasonAuthorizationScopeMissing = "AuthorizationScopeMissing"
	ReasonTargetIdentityUnavailable = "TargetIdentityUnavailable"
	ReasonTargetUIDMismatch         = "TargetUIDMismatch"
	ReasonTargetAuthorized          = "TargetAuthorized"
)

// Scope is the trusted investigation boundary. Empty scope fields fail
// closed; callers must not infer scope from an untrusted TargetRef.
type Scope struct {
	Cluster           string
	AllowedNamespaces []string
}

// EvidenceTarget is the normalized relation emitted by a trusted collector.
// UID is the identity observed with the evidence, not a value supplied by a
// public RelatedTargets field.
type EvidenceTarget struct {
	Ref v1alpha1.TargetRef
	UID string
}

// EvidenceInput is the authorization context for one evidence reference.
// Evidence authorization is intentionally independent from target decisions.
type EvidenceInput struct {
	ID             string
	Authorized     bool
	RelatedTargets []EvidenceTarget
}

// ResourceSnapshot is a trusted Kubernetes identity snapshot. The snapshot is
// supplied by the runtime adapter; this package never performs API calls.
type ResourceSnapshot struct {
	Ref v1alpha1.TargetRef
	UID string
}

// Input is the complete pure validation input for one evidence reference.
type Input struct {
	Evidence  EvidenceInput
	Scope     Scope
	Snapshots []ResourceSnapshot
}

// Decision records the stable, explainable outcome for one related target.
// The TargetRef is retained for provenance; raw evidence payload is excluded.
type Decision struct {
	EvidenceID  string
	Target      v1alpha1.TargetRef
	TargetUID   string
	Disposition Disposition
	Reason      string
	Stage       string
}

// ValidateRelatedTargets evaluates all target relations without side effects.
// It is fail-closed: evidence is rejected before target checks, scope is
// checked before identity, and an absent UID never becomes Authorized.
func ValidateRelatedTargets(input Input) []Decision {
	decisions := make([]Decision, 0, len(input.Evidence.RelatedTargets))
	for _, target := range input.Evidence.RelatedTargets {
		decisions = append(decisions, validateTarget(input, target))
	}
	sort.SliceStable(decisions, func(i, j int) bool {
		left, right := decisions[i], decisions[j]
		if left.EvidenceID != right.EvidenceID {
			return left.EvidenceID < right.EvidenceID
		}
		leftKey, rightKey := targetKey(left.Target), targetKey(right.Target)
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		if left.TargetUID != right.TargetUID {
			return left.TargetUID < right.TargetUID
		}
		return left.Reason < right.Reason
	})
	return decisions
}

func validateTarget(input Input, target EvidenceTarget) Decision {
	decision := Decision{EvidenceID: strings.TrimSpace(input.Evidence.ID), Target: normalizeTarget(target.Ref), TargetUID: strings.TrimSpace(target.UID)}
	if !input.Evidence.Authorized {
		decision.Disposition = DispositionRejected
		decision.Reason = ReasonEvidenceUnauthorized
		decision.Stage = "EvidenceAuthorization"
		return decision
	}
	if !scopeAvailable(input.Scope) {
		decision.Disposition = DispositionUnresolved
		decision.Reason = ReasonAuthorizationScopeMissing
		decision.Stage = "ScopeAuthorization"
		return decision
	}
	if !targetReferenceComplete(decision.Target) {
		decision.Disposition = DispositionRejected
		decision.Reason = ReasonInvalidTargetRef
		decision.Stage = "TargetReferenceValidation"
		return decision
	}
	if decision.Target.Cluster != strings.TrimSpace(input.Scope.Cluster) {
		decision.Disposition = DispositionRejected
		decision.Reason = ReasonUnauthorizedCluster
		decision.Stage = "TargetAuthorization"
		return decision
	}
	if !namespaceAllowed(input.Scope, decision.Target.Namespace) {
		decision.Disposition = DispositionRejected
		decision.Reason = ReasonCrossScopeTarget
		decision.Stage = "TargetAuthorization"
		return decision
	}

	snapshot, found := findSnapshot(input.Snapshots, decision.Target)
	if !found || strings.TrimSpace(snapshot.UID) == "" || decision.TargetUID == "" {
		decision.Disposition = DispositionUnresolved
		decision.Reason = ReasonTargetIdentityUnavailable
		decision.Stage = "TargetIdentityVerification"
		return decision
	}
	if strings.TrimSpace(snapshot.UID) != decision.TargetUID {
		decision.Disposition = DispositionRejected
		decision.Reason = ReasonTargetUIDMismatch
		decision.Stage = "TargetIdentityVerification"
		return decision
	}
	decision.Disposition = DispositionAuthorized
	decision.Reason = ReasonTargetAuthorized
	decision.Stage = "TargetIdentityVerification"
	return decision
}

func scopeAvailable(scope Scope) bool {
	return strings.TrimSpace(scope.Cluster) != "" && len(scope.AllowedNamespaces) > 0
}

func namespaceAllowed(scope Scope, namespace string) bool {
	namespace = strings.TrimSpace(namespace)
	for _, allowed := range scope.AllowedNamespaces {
		if namespace == strings.TrimSpace(allowed) {
			return true
		}
	}
	return false
}

func targetReferenceComplete(target v1alpha1.TargetRef) bool {
	return strings.TrimSpace(target.Cluster) != "" &&
		strings.TrimSpace(target.Namespace) != "" &&
		strings.TrimSpace(target.Kind) != "" &&
		strings.TrimSpace(target.Name) != "" &&
		strings.TrimSpace(target.APIVersion) != ""
}

func normalizeTarget(target v1alpha1.TargetRef) v1alpha1.TargetRef {
	target.Cluster = strings.TrimSpace(target.Cluster)
	target.Namespace = strings.TrimSpace(target.Namespace)
	target.Kind = strings.TrimSpace(target.Kind)
	target.Name = strings.TrimSpace(target.Name)
	target.APIVersion = strings.TrimSpace(target.APIVersion)
	target.Service = strings.TrimSpace(target.Service)
	return target
}

func findSnapshot(snapshots []ResourceSnapshot, target v1alpha1.TargetRef) (ResourceSnapshot, bool) {
	for _, snapshot := range snapshots {
		if targetKey(snapshot.Ref) == targetKey(target) {
			return snapshot, true
		}
	}
	return ResourceSnapshot{}, false
}

func targetKey(target v1alpha1.TargetRef) string {
	target = normalizeTarget(target)
	return strings.Join([]string{target.Cluster, target.Namespace, target.APIVersion, target.Kind, target.Name}, "|")
}
