package authorization

import (
	"testing"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

func TestValidateRelatedTargetsMatrix(t *testing.T) {
	scope := Scope{Cluster: "test-cluster", AllowedNamespaces: []string{"prod"}}
	validRef := v1alpha1.TargetRef{Cluster: "test-cluster", Namespace: "prod", Kind: "Service", Name: "inventory", APIVersion: "v1"}
	snapshot := ResourceSnapshot{Ref: validRef, UID: "service-inventory-uid"}

	tests := []struct {
		name         string
		target       EvidenceTarget
		evidenceAuth bool
		snapshots    []ResourceSnapshot
		want         Disposition
		wantReason   string
	}{
		{
			name:         "valid related target",
			target:       EvidenceTarget{Ref: validRef, UID: "service-inventory-uid"},
			evidenceAuth: true,
			snapshots:    []ResourceSnapshot{snapshot},
			want:         DispositionAuthorized,
			wantReason:   ReasonTargetAuthorized,
		},
		{
			name:         "cross namespace",
			target:       EvidenceTarget{Ref: v1alpha1.TargetRef{Cluster: "test-cluster", Namespace: "payments", Kind: "Service", Name: "inventory", APIVersion: "v1"}, UID: "service-inventory-uid"},
			evidenceAuth: true,
			snapshots:    []ResourceSnapshot{snapshot},
			want:         DispositionRejected,
			wantReason:   ReasonCrossScopeTarget,
		},
		{
			name:         "cross cluster",
			target:       EvidenceTarget{Ref: v1alpha1.TargetRef{Cluster: "other-cluster", Namespace: "prod", Kind: "Service", Name: "inventory", APIVersion: "v1"}, UID: "service-inventory-uid"},
			evidenceAuth: true,
			snapshots:    []ResourceSnapshot{snapshot},
			want:         DispositionRejected,
			wantReason:   ReasonUnauthorizedCluster,
		},
		{
			name:         "missing evidence UID",
			target:       EvidenceTarget{Ref: validRef},
			evidenceAuth: true,
			snapshots:    []ResourceSnapshot{snapshot},
			want:         DispositionUnresolved,
			wantReason:   ReasonTargetIdentityUnavailable,
		},
		{
			name:         "UID mismatch",
			target:       EvidenceTarget{Ref: validRef, UID: "service-recreated-uid"},
			evidenceAuth: true,
			snapshots:    []ResourceSnapshot{snapshot},
			want:         DispositionRejected,
			wantReason:   ReasonTargetUIDMismatch,
		},
		{
			name:         "conflicting snapshots",
			target:       EvidenceTarget{Ref: validRef, UID: "service-inventory-uid"},
			evidenceAuth: true,
			snapshots:    []ResourceSnapshot{snapshot, {Ref: validRef, UID: "service-recreated-uid"}},
			want:         DispositionRejected,
			wantReason:   ReasonTargetSnapshotConflict,
		},
		{
			name:         "stale or deleted target",
			target:       EvidenceTarget{Ref: validRef, UID: "service-inventory-uid"},
			evidenceAuth: true,
			want:         DispositionUnresolved,
			wantReason:   ReasonTargetIdentityUnavailable,
		},
		{
			name:         "evidence unauthorized",
			target:       EvidenceTarget{Ref: validRef, UID: "service-inventory-uid"},
			evidenceAuth: false,
			snapshots:    []ResourceSnapshot{snapshot},
			want:         DispositionRejected,
			wantReason:   ReasonEvidenceUnauthorized,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			decisions := ValidateRelatedTargets(Input{
				Evidence:  EvidenceInput{ID: "evidence-1", Authorized: testCase.evidenceAuth, RelatedTargets: []EvidenceTarget{testCase.target}},
				Scope:     scope,
				Snapshots: testCase.snapshots,
			})
			if len(decisions) != 1 {
				t.Fatalf("expected one decision, got %#v", decisions)
			}
			if decisions[0].Disposition != testCase.want || decisions[0].Reason != testCase.wantReason {
				t.Fatalf("unexpected decision: got=%#v want=%s/%s", decisions[0], testCase.want, testCase.wantReason)
			}
		})
	}
}

func TestValidateRelatedTargetsRejectsMissingScope(t *testing.T) {
	decisions := ValidateRelatedTargets(Input{
		Evidence: EvidenceInput{
			ID:         "evidence-1",
			Authorized: true,
			RelatedTargets: []EvidenceTarget{{
				Ref: v1alpha1.TargetRef{Cluster: "test-cluster", Namespace: "prod", Kind: "Service", Name: "inventory", APIVersion: "v1"},
				UID: "service-inventory-uid",
			}},
		},
	})
	if len(decisions) != 1 || decisions[0].Disposition != DispositionUnresolved || decisions[0].Reason != ReasonAuthorizationScopeMissing {
		t.Fatalf("expected fail-closed scope decision, got %#v", decisions)
	}
}

func TestValidateRelatedTargetsOutputIsStableAcrossInputOrder(t *testing.T) {
	first := v1alpha1.TargetRef{Cluster: "test-cluster", Namespace: "prod", Kind: "Service", Name: "zeta", APIVersion: "v1"}
	second := v1alpha1.TargetRef{Cluster: "test-cluster", Namespace: "prod", Kind: "Service", Name: "alpha", APIVersion: "v1"}
	snapshots := []ResourceSnapshot{{Ref: first, UID: "zeta-uid"}, {Ref: second, UID: "alpha-uid"}}
	left := ValidateRelatedTargets(Input{
		Evidence:  EvidenceInput{ID: "evidence-1", Authorized: true, RelatedTargets: []EvidenceTarget{{Ref: first, UID: "zeta-uid"}, {Ref: second, UID: "alpha-uid"}}},
		Scope:     Scope{Cluster: "test-cluster", AllowedNamespaces: []string{"prod"}},
		Snapshots: snapshots,
	})
	right := ValidateRelatedTargets(Input{
		Evidence:  EvidenceInput{ID: "evidence-1", Authorized: true, RelatedTargets: []EvidenceTarget{{Ref: second, UID: "alpha-uid"}, {Ref: first, UID: "zeta-uid"}}},
		Scope:     Scope{Cluster: "test-cluster", AllowedNamespaces: []string{"prod"}},
		Snapshots: snapshots,
	})
	if len(left) != len(right) || left[0].Target.Name != right[0].Target.Name || left[1].Target.Name != right[1].Target.Name {
		t.Fatalf("expected stable decision order, left=%#v right=%#v", left, right)
	}
}
