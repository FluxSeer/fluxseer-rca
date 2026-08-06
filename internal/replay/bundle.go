package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"fluxseer/api/v1alpha1"
)

const (
	BundleSchemaVersion = "fluxagent-rca-replay-bundle-v1"
)

type Bundle struct {
	SchemaVersion string                            `json:"schemaVersion"`
	Source        Source                            `json:"source"`
	Spec          v1alpha1.InvestigationRequestSpec `json:"spec"`
	Status        Status                            `json:"status"`
	Identity      Identity                          `json:"identity"`
}

type Source struct {
	APIVersion string      `json:"apiVersion,omitempty"`
	Kind       string      `json:"kind,omitempty"`
	Namespace  string      `json:"namespace,omitempty"`
	Name       string      `json:"name,omitempty"`
	UID        string      `json:"uid,omitempty"`
	Generation int64       `json:"generation,omitempty"`
	CreatedAt  metav1.Time `json:"createdAt,omitempty"`
}

type Status struct {
	Phase               string                              `json:"phase,omitempty"`
	Outcome             string                              `json:"outcome,omitempty"`
	Failure             *v1alpha1.InvestigationFailure      `json:"failure,omitempty"`
	Verdict             *v1alpha1.RCAVerdict                `json:"verdict,omitempty"`
	Claims              []v1alpha1.RCAClaim                 `json:"claims,omitempty"`
	MissingEvidence     []v1alpha1.RCAMissingEvidence       `json:"missingEvidence,omitempty"`
	Degradation         *v1alpha1.RCADegradation            `json:"degradation,omitempty"`
	Execution           *v1alpha1.RCAExecution              `json:"execution,omitempty"`
	EvidenceRefs        []v1alpha1.EvidenceRef              `json:"evidenceRefs,omitempty"`
	Lineage             *v1alpha1.InvestigationLineage      `json:"lineage,omitempty"`
	LinkedRiskSignalRef *v1alpha1.NamespacedObjectReference `json:"linkedRiskSignalRef,omitempty"`
}

type Identity struct {
	Digest string `json:"digest"`
}

func Export(request *v1alpha1.InvestigationRequest) (Bundle, error) {
	if request == nil {
		return Bundle{}, errors.New("investigation request is nil")
	}
	if request.Status.Phase != v1alpha1.PhaseCompleted && request.Status.Phase != v1alpha1.PhaseFailed {
		return Bundle{}, fmt.Errorf("investigation request %s/%s is not terminal: phase=%s", request.Namespace, request.Name, request.Status.Phase)
	}
	copied := request.DeepCopy()
	bundle := Bundle{
		SchemaVersion: BundleSchemaVersion,
		Source: Source{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "InvestigationRequest",
			Namespace:  request.Namespace,
			Name:       request.Name,
			UID:        string(request.UID),
			Generation: request.Generation,
			CreatedAt:  request.CreationTimestamp,
		},
		Spec: copied.Spec,
		Status: Status{
			Phase:               request.Status.Phase,
			Outcome:             request.Status.Outcome,
			Failure:             request.Status.Failure,
			Verdict:             request.Status.Verdict,
			Claims:              append([]v1alpha1.RCAClaim(nil), request.Status.Claims...),
			MissingEvidence:     append([]v1alpha1.RCAMissingEvidence(nil), request.Status.MissingEvidence...),
			Degradation:         request.Status.Degradation,
			Execution:           request.Status.Execution,
			EvidenceRefs:        append([]v1alpha1.EvidenceRef(nil), request.Status.EvidenceRefs...),
			Lineage:             request.Status.Lineage,
			LinkedRiskSignalRef: request.Status.LinkedRiskSignalRef,
		},
	}
	digest, err := Digest(bundle)
	if err != nil {
		return Bundle{}, err
	}
	bundle.Identity.Digest = digest
	return bundle, nil
}

func Digest(bundle Bundle) (string, error) {
	copy := bundle
	copy.Identity = Identity{}
	data, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("marshal replay bundle: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

type Evaluation struct {
	Passed      bool         `json:"passed"`
	Differences []Difference `json:"differences,omitempty"`
}

type Difference struct {
	Field string `json:"field"`
}

func Compare(expected, actual Bundle) Evaluation {
	out := Evaluation{Passed: true}
	compareField(&out, "status.outcome", expected.Status.Outcome, actual.Status.Outcome)
	compareField(&out, "status.verdict.outcome", verdictOutcome(expected.Status.Verdict), verdictOutcome(actual.Status.Verdict))
	compareField(&out, "status.verdict.rootCauseType", verdictRootCauseType(expected.Status.Verdict), verdictRootCauseType(actual.Status.Verdict))
	compareField(&out, "status.verdict.confidence", verdictConfidence(expected.Status.Verdict), verdictConfidence(actual.Status.Verdict))
	compareField(&out, "status.claims", claimSignatures(expected.Status.Claims), claimSignatures(actual.Status.Claims))
	compareField(&out, "status.missingEvidence", expected.Status.MissingEvidence, actual.Status.MissingEvidence)
	return out
}

func compareField(out *Evaluation, field string, expected, actual any) {
	if reflect.DeepEqual(expected, actual) {
		return
	}
	out.Passed = false
	out.Differences = append(out.Differences, Difference{Field: field})
}

func verdictOutcome(verdict *v1alpha1.RCAVerdict) string {
	if verdict == nil {
		return ""
	}
	return verdict.Outcome
}

func verdictRootCauseType(verdict *v1alpha1.RCAVerdict) string {
	if verdict == nil {
		return ""
	}
	return verdict.RootCauseType
}

func verdictConfidence(verdict *v1alpha1.RCAVerdict) float64 {
	if verdict == nil {
		return 0
	}
	return verdict.Confidence
}

type claimSignature struct {
	Statement    string   `json:"statement"`
	Verification string   `json:"verification"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

func claimSignatures(claims []v1alpha1.RCAClaim) []claimSignature {
	out := make([]claimSignature, 0, len(claims))
	for _, claim := range claims {
		out = append(out, claimSignature{
			Statement:    claim.Statement,
			Verification: claim.Verification,
			EvidenceRefs: append([]string(nil), claim.EvidenceRefs...),
		})
	}
	return out
}
