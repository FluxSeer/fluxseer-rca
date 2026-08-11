package escalation

import (
	"context"
	"fmt"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

// ResolveRequest describes the resource for which an escalation chain is needed.
type ResolveRequest struct {
	Namespace          string
	EscalationChainRef string
	ResourceLabels     map[string]string
}

// ChainReference identifies the chain version selected for a route.
type ChainReference struct {
	APIVersion      string
	Kind            string
	Name            string
	Namespace       string
	UID             string
	ResourceVersion string
	Generation      int64
	Version         string
	Priority        int32
}

// Route contains the selected immutable chain snapshot and its audit reference.
type Route struct {
	Chain     *v1alpha1.EscalationChain
	Reference ChainReference
}

// Router resolves explicit or selector-based EscalationChain resources.
type Router struct {
	Reader client.Reader
}

func NewRouter(reader client.Reader) *Router {
	return &Router{Reader: reader}
}

// Resolve selects an explicit chain reference or the highest-priority matching chain.
func (r *Router) Resolve(ctx context.Context, request ResolveRequest) (*Route, error) {
	if r.Reader == nil {
		return nil, fmt.Errorf("escalation router requires a Kubernetes reader")
	}
	if request.EscalationChainRef != "" {
		return r.resolveReference(ctx, request.Namespace, request.EscalationChainRef)
	}

	var chains v1alpha1.EscalationChainList
	if err := r.Reader.List(ctx, &chains, client.InNamespace(request.Namespace)); err != nil {
		return nil, fmt.Errorf("list EscalationChain resources in namespace %q: %w", request.Namespace, err)
	}

	matches := make([]*v1alpha1.EscalationChain, 0, len(chains.Items))
	for i := range chains.Items {
		chain := &chains.Items[i]
		if !chain.Spec.Enabled || chain.Status.Phase == "Invalid" || chain.Status.Phase == "Disabled" {
			continue
		}
		matched, err := selectorMatches(chain.Spec.ResourceSelector, request.ResourceLabels)
		if err != nil {
			return nil, fmt.Errorf("match EscalationChain %s/%s: %w", chain.Namespace, chain.Name, err)
		}
		if matched {
			matches = append(matches, chain)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Spec.Priority != matches[j].Spec.Priority {
			return matches[i].Spec.Priority > matches[j].Spec.Priority
		}
		leftSpecific := matches[i].Spec.ResourceSelector != nil
		rightSpecific := matches[j].Spec.ResourceSelector != nil
		if leftSpecific != rightSpecific {
			return leftSpecific
		}
		return matches[i].Name < matches[j].Name
	})
	return routeForChain(matches[0]), nil
}

// ResolveForPolicy resolves a chain referenced by an ApprovalPolicy escalation block.
func (r *Router) ResolveForPolicy(ctx context.Context, namespace string, config *v1alpha1.EscalationConfig, resourceLabels map[string]string) (*Route, error) {
	if config == nil {
		return nil, nil
	}
	return r.Resolve(ctx, ResolveRequest{
		Namespace:          namespace,
		EscalationChainRef: config.EscalationChainRef,
		ResourceLabels:     resourceLabels,
	})
}

func (r *Router) resolveReference(ctx context.Context, namespace, name string) (*Route, error) {
	var chain v1alpha1.EscalationChain
	if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &chain); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("referenced EscalationChain %s/%s was not found", namespace, name)
		}
		return nil, fmt.Errorf("get EscalationChain %s/%s: %w", namespace, name, err)
	}
	if !chain.Spec.Enabled || chain.Status.Phase == "Invalid" || chain.Status.Phase == "Disabled" {
		return nil, fmt.Errorf("referenced EscalationChain %s/%s is disabled or invalid", namespace, name)
	}
	return routeForChain(&chain), nil
}

func routeForChain(chain *v1alpha1.EscalationChain) *Route {
	snapshot := chain.DeepCopy()
	version := snapshot.ResourceVersion
	if version == "" {
		version = fmt.Sprintf("generation-%d", snapshot.Generation)
	}
	return &Route{
		Chain: snapshot,
		Reference: ChainReference{
			APIVersion:      schema.GroupVersion{Group: v1alpha1.Group, Version: v1alpha1.Version}.String(),
			Kind:            "EscalationChain",
			Name:            snapshot.Name,
			Namespace:       snapshot.Namespace,
			UID:             string(snapshot.UID),
			ResourceVersion: snapshot.ResourceVersion,
			Generation:      snapshot.Generation,
			Version:         version,
			Priority:        snapshot.Spec.Priority,
		},
	}
}

func selectorMatches(selector *metav1.LabelSelector, values map[string]string) (bool, error) {
	if selector == nil {
		return true, nil
	}
	converted, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return false, err
	}
	return converted.Matches(labels.Set(values)), nil
}
