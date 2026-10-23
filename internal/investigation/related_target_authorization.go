package investigation

import (
	"context"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/authorization"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

const maxRelatedTargetSnapshots = 32

const relatedTargetSnapshotTimeout = 2 * time.Second

// RelatedTargetAuthorizationContext is internal runtime state. It keeps
// source RelatedTargets available for audit while exposing only validated
// decisions to trusted consumers.
type RelatedTargetAuthorizationContext struct {
	Ready     bool
	Decisions []authorization.Decision
}

// AuthorizedEvidenceRef reports whether every related target on a source
// evidence reference passed the trusted authorization context. Evidence with
// no RelatedTargets is unaffected by this boundary.
func (c RelatedTargetAuthorizationContext) AuthorizedEvidenceRef(ref v1alpha1.EvidenceRef) bool {
	if len(ref.RelatedTargets) == 0 {
		return true
	}
	if !c.Ready {
		return false
	}
	for _, target := range ref.RelatedTargets {
		if !c.AuthorizedTarget(ref.ID, target) {
			return false
		}
	}
	return true
}

func (c RelatedTargetAuthorizationContext) AuthorizedTarget(evidenceID string, target v1alpha1.TargetRef) bool {
	for _, decision := range c.Decisions {
		if decision.EvidenceID == strings.TrimSpace(evidenceID) && targetKey(decision.Target) == targetKey(target) && decision.Disposition == authorization.DispositionAuthorized {
			return true
		}
	}
	return false
}

func (s *Service) authorizeRelatedTargets(ctx context.Context, preflight PreflightResult, observations []domain.Observation, refs []v1alpha1.EvidenceRef) RelatedTargetAuthorizationContext {
	result := RelatedTargetAuthorizationContext{Ready: true}
	assertions := make(map[string][]string, len(observations))
	for _, observation := range observations {
		assertions[observation.ID] = append([]string(nil), observation.RelatedTargetUIDAssertions...)
	}
	snapshotCount := 0
	for _, ref := range refs {
		if len(ref.RelatedTargets) == 0 {
			continue
		}
		uidAssertions := assertions[ref.ID]
		input := authorization.Input{
			// EvidenceRefs reach this adapter only after the collection layer has
			// accepted the datasource query. This is the evidence authorization
			// decision; target authorization and identity are evaluated separately
			// below and are never inferred from RelatedTargets.
			Evidence: authorization.EvidenceInput{ID: ref.ID, Authorized: true},
			Scope: authorization.Scope{
				Cluster:           preflight.Target.Cluster,
				AllowedNamespaces: []string{preflight.Target.Namespace},
			},
		}
		for index, target := range ref.RelatedTargets {
			uid := ""
			if index < len(uidAssertions) {
				uid = strings.TrimSpace(uidAssertions[index])
			}
			input.Evidence.RelatedTargets = append(input.Evidence.RelatedTargets, authorization.EvidenceTarget{Ref: target, UID: uid})
			if snapshotCount >= maxRelatedTargetSnapshots || !relatedTargetWithinScope(preflight.Target, target) || ctx.Err() != nil {
				continue
			}
			snapshotCount++
			snapshotCtx, cancel := context.WithTimeout(ctx, relatedTargetSnapshotTimeout)
			snapshot, ok := s.snapshotRelatedTarget(snapshotCtx, target)
			cancel()
			if ok {
				input.Snapshots = append(input.Snapshots, snapshot)
			}
		}
		result.Decisions = append(result.Decisions, authorization.ValidateRelatedTargets(input)...)
	}
	sort.SliceStable(result.Decisions, func(i, j int) bool {
		left, right := result.Decisions[i], result.Decisions[j]
		if left.EvidenceID != right.EvidenceID {
			return left.EvidenceID < right.EvidenceID
		}
		return targetKey(left.Target) < targetKey(right.Target)
	})
	return result
}

func relatedTargetWithinScope(scope domain.ResourceRef, target v1alpha1.TargetRef) bool {
	return strings.TrimSpace(scope.Cluster) != "" && strings.TrimSpace(target.Cluster) == strings.TrimSpace(scope.Cluster) && strings.TrimSpace(target.Namespace) == strings.TrimSpace(scope.Namespace)
}

func (s *Service) snapshotRelatedTarget(ctx context.Context, target v1alpha1.TargetRef) (authorization.ResourceSnapshot, bool) {
	if s == nil || s.Client == nil {
		return authorization.ResourceSnapshot{}, false
	}
	key := types.NamespacedName{Namespace: target.Namespace, Name: target.Name}
	snapshot := authorization.ResourceSnapshot{Ref: target}
	switch normalizeTargetKind(target.Kind) {
	case "service":
		var object corev1.Service
		if err := s.Client.Get(ctx, key, &object); err != nil {
			return authorization.ResourceSnapshot{}, false
		}
		snapshot.Ref.APIVersion = "v1"
		snapshot.Ref.Kind = "Service"
		snapshot.UID = string(object.UID)
	case "pod":
		var object corev1.Pod
		if err := s.Client.Get(ctx, key, &object); err != nil {
			return authorization.ResourceSnapshot{}, false
		}
		snapshot.Ref.APIVersion = "v1"
		snapshot.Ref.Kind = "Pod"
		snapshot.UID = string(object.UID)
	case "deployment":
		var object appsv1.Deployment
		if err := s.Client.Get(ctx, key, &object); err != nil {
			return authorization.ResourceSnapshot{}, false
		}
		snapshot.Ref.APIVersion = "apps/v1"
		snapshot.Ref.Kind = "Deployment"
		snapshot.UID = string(object.UID)
	case "statefulset":
		var object appsv1.StatefulSet
		if err := s.Client.Get(ctx, key, &object); err != nil {
			return authorization.ResourceSnapshot{}, false
		}
		snapshot.Ref.APIVersion = "apps/v1"
		snapshot.Ref.Kind = "StatefulSet"
		snapshot.UID = string(object.UID)
	case "daemonset":
		var object appsv1.DaemonSet
		if err := s.Client.Get(ctx, key, &object); err != nil {
			return authorization.ResourceSnapshot{}, false
		}
		snapshot.Ref.APIVersion = "apps/v1"
		snapshot.Ref.Kind = "DaemonSet"
		snapshot.UID = string(object.UID)
	case "replicaset":
		var object appsv1.ReplicaSet
		if err := s.Client.Get(ctx, key, &object); err != nil {
			return authorization.ResourceSnapshot{}, false
		}
		snapshot.Ref.APIVersion = "apps/v1"
		snapshot.Ref.Kind = "ReplicaSet"
		snapshot.UID = string(object.UID)
	case "job":
		var object batchv1.Job
		if err := s.Client.Get(ctx, key, &object); err != nil {
			return authorization.ResourceSnapshot{}, false
		}
		snapshot.Ref.APIVersion = "batch/v1"
		snapshot.Ref.Kind = "Job"
		snapshot.UID = string(object.UID)
	case "cronjob":
		var object batchv1.CronJob
		if err := s.Client.Get(ctx, key, &object); err != nil {
			return authorization.ResourceSnapshot{}, false
		}
		snapshot.Ref.APIVersion = "batch/v1"
		snapshot.Ref.Kind = "CronJob"
		snapshot.UID = string(object.UID)
	default:
		return authorization.ResourceSnapshot{}, false
	}
	return snapshot, true
}

func targetKey(target v1alpha1.TargetRef) string {
	return strings.Join([]string{
		strings.TrimSpace(target.Cluster),
		strings.TrimSpace(target.Namespace),
		strings.TrimSpace(target.APIVersion),
		strings.TrimSpace(target.Kind),
		strings.TrimSpace(target.Name),
	}, "|")
}
