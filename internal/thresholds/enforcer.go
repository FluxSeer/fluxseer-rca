package thresholds

import (
	"context"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

const (
	ThresholdMatchDirect   = "DirectNamespace"
	ThresholdMatchSelector = "NamespaceSelector"
)

// ResolveRequest identifies the namespace and labels used for threshold matching.
type ResolveRequest struct {
	Namespace       string
	NamespaceLabels map[string]string
}

// Usage contains the current namespace resource usage.
type Usage struct {
	ActivePlans      int32
	PendingApprovals int32
}

// ThresholdReference identifies the threshold version used for an enforcement decision.
type ThresholdReference struct {
	APIVersion      string
	Kind            string
	Name            string
	Namespace       string
	UID             string
	ResourceVersion string
	Generation      int64
	Version         string
	Priority        int32
	MatchType       string
}

// Resolution contains the selected threshold snapshot and audit reference.
type Resolution struct {
	Threshold *v1alpha1.NamespaceThreshold
	Reference ThresholdReference
}

// Violation describes one usage limit exceeded by the namespace.
type Violation struct {
	Resource string
	Current  int32
	Limit    int32
	Reason   string
}

// EnforcementResult reports whether current usage complies with the selected threshold.
type EnforcementResult struct {
	Allowed    bool
	Violations []Violation
	Resolution *Resolution
}

// Enforcer resolves NamespaceThreshold resources and evaluates namespace usage.
type Enforcer struct {
	Reader client.Reader
}

func NewEnforcer(reader client.Reader) *Enforcer {
	return &Enforcer{Reader: reader}
}

// Resolve selects the highest-priority applicable threshold.
func (e *Enforcer) Resolve(ctx context.Context, request ResolveRequest) (*Resolution, error) {
	if e.Reader == nil {
		return nil, fmt.Errorf("threshold enforcer requires a Kubernetes reader")
	}

	var thresholds v1alpha1.NamespaceThresholdList
	if err := e.Reader.List(ctx, &thresholds); err != nil {
		return nil, fmt.Errorf("list NamespaceThreshold resources: %w", err)
	}

	matches := make([]thresholdMatch, 0, len(thresholds.Items))
	for i := range thresholds.Items {
		threshold := &thresholds.Items[i]
		if !threshold.Spec.Enabled || threshold.Status.Phase == "Invalid" || threshold.Status.Phase == "Disabled" {
			continue
		}
		matchType, matched, err := matchThreshold(threshold, request)
		if err != nil {
			return nil, fmt.Errorf("match NamespaceThreshold %s/%s: %w", threshold.Namespace, threshold.Name, err)
		}
		if matched {
			matches = append(matches, thresholdMatch{threshold: threshold, matchType: matchType})
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].threshold.Spec.Priority != matches[j].threshold.Spec.Priority {
			return matches[i].threshold.Spec.Priority > matches[j].threshold.Spec.Priority
		}
		if matches[i].matchType != matches[j].matchType {
			return matches[i].matchType == ThresholdMatchDirect
		}
		left := matches[i].threshold.Namespace + "/" + matches[i].threshold.Name
		right := matches[j].threshold.Namespace + "/" + matches[j].threshold.Name
		return left < right
	})

	selected := matches[0]
	return &Resolution{
		Threshold: selected.threshold.DeepCopy(),
		Reference: thresholdReference(selected.threshold, selected.matchType),
	}, nil
}

// Evaluate checks current usage against the selected namespace threshold.
func (e *Enforcer) Evaluate(ctx context.Context, request ResolveRequest, usage Usage) (EnforcementResult, error) {
	resolution, err := e.Resolve(ctx, request)
	if err != nil {
		return EnforcementResult{}, err
	}
	if resolution == nil {
		return EnforcementResult{Allowed: true}, nil
	}

	violations := make([]Violation, 0, 2)
	if resolution.Threshold.Spec.ActivePlansLimit > 0 && usage.ActivePlans > resolution.Threshold.Spec.ActivePlansLimit {
		violations = append(violations, Violation{
			Resource: "activePlans",
			Current:  usage.ActivePlans,
			Limit:    resolution.Threshold.Spec.ActivePlansLimit,
			Reason:   "active RemediationPlan count exceeds namespace threshold",
		})
	}
	if resolution.Threshold.Spec.PendingApprovalsLimit > 0 && usage.PendingApprovals > resolution.Threshold.Spec.PendingApprovalsLimit {
		violations = append(violations, Violation{
			Resource: "pendingApprovals",
			Current:  usage.PendingApprovals,
			Limit:    resolution.Threshold.Spec.PendingApprovalsLimit,
			Reason:   "pending approval count exceeds namespace threshold",
		})
	}
	return EnforcementResult{
		Allowed:    len(violations) == 0,
		Violations: violations,
		Resolution: resolution,
	}, nil
}

// Enforce is an alias for Evaluate used by admission and reconciliation callers.
func (e *Enforcer) Enforce(ctx context.Context, request ResolveRequest, usage Usage) (EnforcementResult, error) {
	return e.Evaluate(ctx, request, usage)
}

type thresholdMatch struct {
	threshold *v1alpha1.NamespaceThreshold
	matchType string
}

func matchThreshold(threshold *v1alpha1.NamespaceThreshold, request ResolveRequest) (string, bool, error) {
	if threshold.Spec.NamespaceSelector == nil {
		return ThresholdMatchDirect, threshold.Namespace == request.Namespace, nil
	}
	selector, err := metav1.LabelSelectorAsSelector(threshold.Spec.NamespaceSelector)
	if err != nil {
		return "", false, err
	}
	return ThresholdMatchSelector, selector.Matches(labels.Set(request.NamespaceLabels)), nil
}

func thresholdReference(threshold *v1alpha1.NamespaceThreshold, matchType string) ThresholdReference {
	version := threshold.ResourceVersion
	if version == "" {
		version = fmt.Sprintf("generation-%d", threshold.Generation)
	}
	return ThresholdReference{
		APIVersion:      schema.GroupVersion{Group: v1alpha1.Group, Version: v1alpha1.Version}.String(),
		Kind:            "NamespaceThreshold",
		Name:            threshold.Name,
		Namespace:       threshold.Namespace,
		UID:             string(threshold.UID),
		ResourceVersion: threshold.ResourceVersion,
		Generation:      threshold.Generation,
		Version:         version,
		Priority:        threshold.Spec.Priority,
		MatchType:       matchType,
	}
}
