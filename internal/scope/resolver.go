// Package scope resolves bounded Kubernetes workload ownership for reusable
// diagnostics. It returns resource identity and completeness metadata; it
// does not evaluate a root cause or perform any mutation.
package scope

import (
	"context"
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultMaxReplicaSets = 20
	defaultMaxPods        = 100
)

// Limits bounds the resources traversed by a scope resolution.
type Limits struct {
	MaxReplicaSets int
	MaxPods        int
}

func (l Limits) normalized() Limits {
	if l.MaxReplicaSets <= 0 {
		l.MaxReplicaSets = defaultMaxReplicaSets
	}
	if l.MaxPods <= 0 {
		l.MaxPods = defaultMaxPods
	}
	return l
}

// ResourceRef identifies one Kubernetes resource occurrence. UID is part of
// identity so a resource recreated with the same name is not conflated with
// an earlier occurrence.
type ResourceRef struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	UID        types.UID
}

// WarningCode describes why a scope result is incomplete or why a candidate
// was excluded. Warnings are safe to expose in diagnostics and reports.
type WarningCode string

const (
	WarningOwnerMismatch     WarningCode = "OwnerMismatch"
	WarningSelectorMismatch  WarningCode = "SelectorMismatch"
	WarningReplicaSetLimit   WarningCode = "ReplicaSetLimitExceeded"
	WarningPodLimit          WarningCode = "PodLimitExceeded"
	WarningReplicaSetList    WarningCode = "ReplicaSetListFailed"
	WarningPodList           WarningCode = "PodListFailed"
	WarningResourceNotFound  WarningCode = "ResourceNotFound"
	WarningNamespaceMismatch WarningCode = "NamespaceMismatch"
	WarningInvalidTarget     WarningCode = "InvalidTarget"
)

// ScopeWarning records a bounded, deterministic scope resolution warning.
type ScopeWarning struct {
	Code     WarningCode
	Message  string
	Resource *ResourceRef
}

// Result contains the validated Deployment ownership chain. Complete is
// false when the result may omit resources because of a query error or bound.
// Invalid owner references are excluded from the result and recorded as
// warnings without making valid scope incomplete.
type Result struct {
	Deployment  ResourceRef
	ReplicaSets []ResourceRef
	Pods        []ResourceRef
	Warnings    []ScopeWarning
	Complete    bool
}

// Resolver resolves bounded Deployment -> ReplicaSet -> Pod ownership.
type Resolver struct {
	reader client.Reader
	limits Limits
}

// NewResolver creates a read-only scope resolver. Zero limits use conservative
// defaults suitable for the first diagnostic slice.
func NewResolver(reader client.Reader, limits Limits) *Resolver {
	return &Resolver{reader: reader, limits: limits.normalized()}
}

// ResolveDeployment resolves the target Deployment and its controller-owned
// ReplicaSets and Pods. It uses selectors to constrain candidates but relies
// on controller OwnerReference UIDs as the ownership authority.
func (r *Resolver) ResolveDeployment(ctx context.Context, namespace, name string) (Result, error) {
	result := Result{Complete: true}
	if r == nil || r.reader == nil {
		return result, fmt.Errorf("scope resolver requires a Kubernetes reader")
	}
	if namespace == "" || name == "" {
		result.Complete = false
		result.Warnings = append(result.Warnings, ScopeWarning{
			Code:    WarningInvalidTarget,
			Message: "Deployment namespace and name are required",
		})
		return result, fmt.Errorf("Deployment namespace and name are required")
	}

	var deployment appsv1.Deployment
	if err := r.reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			result.Complete = false
			result.Warnings = append(result.Warnings, ScopeWarning{
				Code:    WarningResourceNotFound,
				Message: fmt.Sprintf("Deployment %s/%s was not found", namespace, name),
			})
		}
		return result, fmt.Errorf("get Deployment %s/%s: %w", namespace, name, err)
	}

	result.Deployment = resourceRef(&deployment, "apps/v1", "Deployment")
	if deployment.Namespace != namespace {
		result.Complete = false
		result.Warnings = append(result.Warnings, ScopeWarning{
			Code:     WarningNamespaceMismatch,
			Message:  "resolved Deployment namespace differs from requested namespace",
			Resource: &result.Deployment,
		})
		return result, fmt.Errorf("Deployment %s/%s resolved outside requested namespace", namespace, name)
	}
	if deployment.Spec.Selector == nil {
		result.Complete = false
		result.Warnings = append(result.Warnings, ScopeWarning{
			Code:     WarningInvalidTarget,
			Message:  "Deployment has no selector",
			Resource: &result.Deployment,
		})
		return result, fmt.Errorf("Deployment %s/%s has no selector", namespace, name)
	}
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		result.Complete = false
		result.Warnings = append(result.Warnings, ScopeWarning{
			Code:     WarningInvalidTarget,
			Message:  fmt.Sprintf("Deployment selector is invalid: %v", err),
			Resource: &result.Deployment,
		})
		return result, fmt.Errorf("parse Deployment %s/%s selector: %w", namespace, name, err)
	}

	var replicas appsv1.ReplicaSetList
	if err := r.reader.List(ctx, &replicas,
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: selector},
		client.Limit(int64(r.limits.MaxReplicaSets+1)),
	); err != nil {
		result.Complete = false
		result.Warnings = append(result.Warnings, ScopeWarning{
			Code:     WarningReplicaSetList,
			Message:  fmt.Sprintf("list ReplicaSets: %v", err),
			Resource: &result.Deployment,
		})
		return result, fmt.Errorf("list ReplicaSets for Deployment %s/%s: %w", namespace, name, err)
	}
	if len(replicas.Items) > r.limits.MaxReplicaSets || replicas.Continue != "" {
		result.Complete = false
		result.Warnings = append(result.Warnings, ScopeWarning{
			Code:     WarningReplicaSetLimit,
			Message:  fmt.Sprintf("ReplicaSet scope exceeded limit %d", r.limits.MaxReplicaSets),
			Resource: &result.Deployment,
		})
		replicas.Items = replicas.Items[:min(len(replicas.Items), r.limits.MaxReplicaSets)]
	}

	ownedReplicaSetUIDs := make(map[types.UID]struct{}, len(replicas.Items))
	for i := range replicas.Items {
		replica := replicas.Items[i]
		ref := resourceRef(&replica, "apps/v1", "ReplicaSet")
		if !controllerOwnerMatches(replica.OwnerReferences, "Deployment", deployment.Name, deployment.UID) {
			result.Warnings = append(result.Warnings, ScopeWarning{
				Code:     WarningOwnerMismatch,
				Message:  "ReplicaSet selector matched but controller OwnerReference does not match the target Deployment UID",
				Resource: &ref,
			})
			continue
		}
		if !selector.Matches(labels.Set(replica.Labels)) {
			result.Warnings = append(result.Warnings, ScopeWarning{
				Code:     WarningSelectorMismatch,
				Message:  "ReplicaSet controller owner matched but labels failed the Deployment selector",
				Resource: &ref,
			})
			continue
		}
		result.ReplicaSets = append(result.ReplicaSets, ref)
		ownedReplicaSetUIDs[replica.UID] = struct{}{}
	}

	var pods corev1.PodList
	if err := r.reader.List(ctx, &pods,
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: selector},
		client.Limit(int64(r.limits.MaxPods+1)),
	); err != nil {
		result.Complete = false
		result.Warnings = append(result.Warnings, ScopeWarning{
			Code:     WarningPodList,
			Message:  fmt.Sprintf("list Pods: %v", err),
			Resource: &result.Deployment,
		})
		return result, fmt.Errorf("list Pods for Deployment %s/%s: %w", namespace, name, err)
	}
	if len(pods.Items) > r.limits.MaxPods || pods.Continue != "" {
		result.Complete = false
		result.Warnings = append(result.Warnings, ScopeWarning{
			Code:     WarningPodLimit,
			Message:  fmt.Sprintf("Pod scope exceeded limit %d", r.limits.MaxPods),
			Resource: &result.Deployment,
		})
		pods.Items = pods.Items[:min(len(pods.Items), r.limits.MaxPods)]
	}
	for i := range pods.Items {
		pod := pods.Items[i]
		ref := resourceRef(&pod, "v1", "Pod")
		owner := controllerOwner(pod.OwnerReferences, "ReplicaSet")
		if owner == nil {
			result.Warnings = append(result.Warnings, ScopeWarning{
				Code:     WarningOwnerMismatch,
				Message:  "Pod selector matched but it has no controller ReplicaSet OwnerReference",
				Resource: &ref,
			})
			continue
		}
		if _, ok := ownedReplicaSetUIDs[owner.UID]; !ok {
			result.Warnings = append(result.Warnings, ScopeWarning{
				Code:     WarningOwnerMismatch,
				Message:  "Pod controller ReplicaSet UID is not in the validated Deployment scope",
				Resource: &ref,
			})
			continue
		}
		result.Pods = append(result.Pods, ref)
	}

	sortResourceRefs(result.ReplicaSets)
	sortResourceRefs(result.Pods)
	return result, nil
}

func resourceRef(obj metav1.Object, apiVersion, kind string) ResourceRef {
	return ResourceRef{
		APIVersion: apiVersion,
		Kind:       kind,
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		UID:        obj.GetUID(),
	}
}

func controllerOwner(refs []metav1.OwnerReference, kind string) *metav1.OwnerReference {
	for i := range refs {
		ref := refs[i]
		if ref.Controller != nil && *ref.Controller && ref.Kind == kind && ref.UID != "" {
			return &ref
		}
	}
	return nil
}

func controllerOwnerMatches(refs []metav1.OwnerReference, kind, name string, uid types.UID) bool {
	owner := controllerOwner(refs, kind)
	return owner != nil && owner.Name == name && owner.UID == uid
}

func sortResourceRefs(refs []ResourceRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Namespace != refs[j].Namespace {
			return refs[i].Namespace < refs[j].Namespace
		}
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].UID < refs[j].UID
	})
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
