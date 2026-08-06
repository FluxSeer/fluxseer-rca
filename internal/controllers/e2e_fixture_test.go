package controllers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"fluxseer/api/v1alpha1"
	"fluxseer/internal/datasource"
	"fluxseer/internal/domain"
	"fluxseer/internal/investigation"
	"fluxseer/internal/knowledge"
	"fluxseer/internal/model"
	"fluxseer/internal/modelgateway"
)

type rawToFinalFixture struct {
	Name             string                `json:"name"`
	Profile          string                `json:"profile"`
	Target           fixtureTarget         `json:"target"`
	Datasources      []fixtureDatasource   `json:"datasources"`
	ProviderResponse map[string]any        `json:"providerResponse"`
	Expected         rawToFinalExpectation `json:"expected"`
}

type fixtureTarget struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
}

type fixtureDatasource struct {
	Name      string           `json:"name"`
	QueryType string           `json:"queryType"`
	Records   []map[string]any `json:"records"`
}

type rawToFinalExpectation struct {
	Phase             string `json:"phase"`
	Outcome           string `json:"outcome"`
	RootCauseType     string `json:"rootCauseType"`
	ClaimVerification string `json:"claimVerification"`
	ProviderCalls     int    `json:"providerCalls"`
}

type fixtureRCAProvider struct {
	output map[string]any
	calls  int
}

func (p *fixtureRCAProvider) Name() string {
	return "fixture"
}

func (p *fixtureRCAProvider) Complete(context.Context, domain.ModelRequest) (domain.ModelResponse, error) {
	p.calls++
	return domain.ModelResponse{
		Provider:   p.Name(),
		Model:      "fixture-model",
		Structured: true,
		Output:     p.output,
	}, nil
}

func TestRawToFinalE2EFixtureReplay(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "e2e", "*.json"))
	if err != nil {
		t.Fatalf("glob e2e fixtures: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected e2e fixtures")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var fixture rawToFinalFixture
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			reconciler, request, provider := fixtureReconciler(t, fixture)
			if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: request.Name, Namespace: request.Namespace},
			}); err != nil {
				t.Fatalf("reconcile fixture: %v", err)
			}

			var stored v1alpha1.InvestigationRequest
			if err := reconciler.Client.Get(context.Background(), types.NamespacedName{Name: request.Name, Namespace: request.Namespace}, &stored); err != nil {
				t.Fatalf("get stored request: %v", err)
			}
			if stored.Status.Phase != fixture.Expected.Phase || stored.Status.Outcome != fixture.Expected.Outcome {
				t.Fatalf("expected phase=%s outcome=%s, got phase=%s outcome=%s", fixture.Expected.Phase, fixture.Expected.Outcome, stored.Status.Phase, stored.Status.Outcome)
			}
			if stored.Status.Verdict == nil || stored.Status.Verdict.RootCauseType != fixture.Expected.RootCauseType {
				t.Fatalf("expected rootCauseType=%s, got %#v", fixture.Expected.RootCauseType, stored.Status.Verdict)
			}
			if !hasClaimVerification(stored.Status.Claims, fixture.Expected.ClaimVerification) {
				t.Fatalf("expected claim verification %s, got %#v", fixture.Expected.ClaimVerification, stored.Status.Claims)
			}
			if provider.calls != fixture.Expected.ProviderCalls {
				t.Fatalf("expected provider to be called %d times, got %d", fixture.Expected.ProviderCalls, provider.calls)
			}
		})
	}
}

func fixtureReconciler(t *testing.T, fixture rawToFinalFixture) (*InvestigationRequestReconciler, *v1alpha1.InvestigationRequest, *fixtureRCAProvider) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add batch scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add aiops scheme: %v", err)
	}
	target := fixtureTargetOrDefault(fixture.Target)
	request := &v1alpha1.InvestigationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: fixture.Name, Namespace: "fluxagent-system", Generation: 1},
		Spec: v1alpha1.InvestigationRequestSpec{
			Target: v1alpha1.TargetRef{
				Namespace:  target.Namespace,
				Kind:       target.Kind,
				Name:       target.Name,
				APIVersion: target.APIVersion,
			},
			DataSources:          fixtureDatasourceRefs(fixture.Datasources),
			ModelProviderRef:     v1alpha1.LocalObjectReference{Name: "fixture-provider"},
			Mode:                 v1alpha1.InvestigationModeReadOnly,
			EvidenceRequirements: v1alpha1.EvidenceRequirements{Profile: fixture.Profile},
		},
	}
	providerObj := &v1alpha1.ModelProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "fixture-provider", Namespace: "fluxagent-system"},
		Spec:       v1alpha1.ModelProviderSpec{Provider: "fixture", Model: "fixture-model"},
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.InvestigationRequest{}, &v1alpha1.RiskSignal{}).
		WithObjects(append(fixtureTargetObjects(target), request, providerObj)...).
		Build()
	provider := &fixtureRCAProvider{output: fixture.ProviderResponse}
	return &InvestigationRequestReconciler{
		Client: client,
		Scheme: scheme,
		Service: &investigation.Service{
			Client: client,
			Registry: datasource.NewRegistry(
				fixtureDataSources(fixture.Datasources)...,
			),
			Resolver: modelgateway.KubeResolver{Client: client},
			Gateway: &modelgateway.Gateway{
				Base:      knowledge.NewBase(),
				Providers: model.NewRegistry(provider),
			},
		},
		Now: func() time.Time { return time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC) },
	}, request, provider
}

func fixtureTargetOrDefault(target fixtureTarget) fixtureTarget {
	if target.Kind == "" {
		return fixtureTarget{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "prod", Name: "open-api"}
	}
	return target
}

func fixtureTargetObjects(target fixtureTarget) []client.Object {
	switch target.Kind {
	case "StatefulSet":
		return []client.Object{
			&appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: target.Namespace, Labels: map[string]string{"app": target.Name}},
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": target.Name}}},
				},
			},
		}
	case "DaemonSet":
		return []client.Object{
			&appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: target.Namespace, Labels: map[string]string{"app": target.Name}},
				Spec: appsv1.DaemonSetSpec{
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": target.Name}}},
				},
			},
		}
	case "Job":
		return []client.Object{
			&batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: target.Namespace, Labels: map[string]string{"app": target.Name}},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": target.Name}}},
				},
			},
		}
	case "CronJob":
		return []client.Object{
			&batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: target.Namespace, Labels: map[string]string{"app": target.Name}},
				Spec: batchv1.CronJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": target.Name}}},
					}},
				},
			},
		}
	case "Pod":
		return []client.Object{
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: target.Name, Namespace: target.Namespace, Labels: map[string]string{"app": target.Name}},
			},
		}
	default:
		return []client.Object{evidenceRequirementDeployment()}
	}
}

func fixtureDatasourceRefs(items []fixtureDatasource) []v1alpha1.LocalObjectReference {
	refs := make([]v1alpha1.LocalObjectReference, 0, len(items))
	for _, item := range items {
		refs = append(refs, v1alpha1.LocalObjectReference{Name: item.Name})
	}
	return refs
}

func fixtureDataSources(items []fixtureDatasource) []datasource.DataSource {
	sources := make([]datasource.DataSource, 0, len(items))
	for _, item := range items {
		sources = append(sources, fakeInvestigationDataSource{
			name:      item.Name,
			queryType: domain.QueryType(item.QueryType),
			records:   item.Records,
		})
	}
	return sources
}

func hasClaimVerification(claims []v1alpha1.RCAClaim, verification string) bool {
	if verification == "" {
		return true
	}
	for _, claim := range claims {
		if claim.Verification == verification {
			return true
		}
	}
	return false
}
