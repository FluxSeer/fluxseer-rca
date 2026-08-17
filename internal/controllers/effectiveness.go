package controllers

import (
	"strings"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/executor"
)

type effectivenessEvaluation struct {
	Outcome string
	Message string
}

func evaluateEffectiveness(baseline *v1alpha1.EffectivenessBaseline, post executor.HealthSnapshot, observed bool, verification *v1alpha1.InvestigationRequest) effectivenessEvaluation {
	if baseline == nil || baseline.Health == nil {
		return effectivenessEvaluation{Outcome: v1alpha1.EffectivenessOutcomeInconclusive, Message: "pre-action health baseline is missing"}
	}
	if verification == nil {
		return effectivenessEvaluation{Outcome: v1alpha1.EffectivenessOutcomeInconclusive, Message: "verification investigation is missing"}
	}
	if !observed {
		return effectivenessEvaluation{Outcome: v1alpha1.EffectivenessOutcomeInconclusive, Message: "post-action health observation is unavailable"}
	}
	if post.DesiredReplicas != baseline.Health.DesiredReplicas {
		return effectivenessEvaluation{Outcome: v1alpha1.EffectivenessOutcomeInconclusive, Message: "target replica intent changed during the verification window"}
	}
	if materiallyRegressed(*baseline.Health, post) {
		return effectivenessEvaluation{Outcome: v1alpha1.EffectivenessOutcomeRegressed, Message: "post-action workload health is objectively worse than the pre-action baseline"}
	}

	switch verification.Status.Outcome {
	case v1alpha1.InvestigationOutcomeNoIssueFound:
		if !healthIsHealthy(post) {
			return effectivenessEvaluation{Outcome: v1alpha1.EffectivenessOutcomeIneffective, Message: "verification found no new event but the target health is still below the desired state"}
		}
		if !healthImproved(*baseline.Health, post) {
			return effectivenessEvaluation{Outcome: v1alpha1.EffectivenessOutcomeInconclusive, Message: "verification found no issue but the post-action health did not improve over baseline"}
		}
		return effectivenessEvaluation{Outcome: v1alpha1.EffectivenessOutcomeEffective, Message: "target health improved and the original incident condition was not observed in the verification window"}
	case v1alpha1.InvestigationOutcomeConfirmed:
		return effectivenessEvaluation{Outcome: v1alpha1.EffectivenessOutcomeIneffective, Message: "the original incident condition remains observed after the action"}
	case v1alpha1.InvestigationOutcomeInconclusive, v1alpha1.InvestigationOutcomeUnknown:
		return effectivenessEvaluation{Outcome: v1alpha1.EffectivenessOutcomeInconclusive, Message: "verification evidence was insufficient to establish remediation effectiveness"}
	default:
		return effectivenessEvaluation{Outcome: v1alpha1.EffectivenessOutcomeInconclusive, Message: "verification returned an unsupported outcome"}
	}
}

func healthIsHealthy(snapshot executor.HealthSnapshot) bool {
	desired := snapshot.DesiredReplicas
	if desired <= 0 {
		return true
	}
	if snapshot.AvailableReplicas < desired || snapshot.ReadyReplicas < desired || snapshot.UpdatedReplicas < desired {
		return false
	}
	if snapshot.Generation > 0 && snapshot.ObservedGeneration < snapshot.Generation {
		return false
	}
	for _, condition := range snapshot.Conditions {
		if condition.Type == "Available" && condition.Status == "False" {
			return false
		}
		if condition.Type == "Progressing" && condition.Status == "False" {
			return false
		}
	}
	return true
}

func healthImproved(baseline v1alpha1.EffectivenessHealthSnapshot, post executor.HealthSnapshot) bool {
	if healthIsHealthy(post) && !healthSnapshotIsHealthy(baseline) {
		return true
	}
	return post.AvailableReplicas > baseline.AvailableReplicas || post.ReadyReplicas > baseline.ReadyReplicas || post.UpdatedReplicas > baseline.UpdatedReplicas
}

func healthSnapshotIsHealthy(snapshot v1alpha1.EffectivenessHealthSnapshot) bool {
	conditions := make([]executor.HealthCondition, 0, len(snapshot.Conditions))
	for _, condition := range snapshot.Conditions {
		conditions = append(conditions, executor.HealthCondition{Type: condition.Type, Status: condition.Status, Reason: condition.Reason, Message: condition.Message})
	}
	return healthIsHealthy(executor.HealthSnapshot{
		Generation:         snapshot.Generation,
		ObservedGeneration: snapshot.ObservedGeneration,
		DesiredReplicas:    snapshot.DesiredReplicas,
		UpdatedReplicas:    snapshot.UpdatedReplicas,
		AvailableReplicas:  snapshot.AvailableReplicas,
		ReadyReplicas:      snapshot.ReadyReplicas,
		Conditions:         conditions,
	})
}

func materiallyRegressed(baseline v1alpha1.EffectivenessHealthSnapshot, post executor.HealthSnapshot) bool {
	if post.AvailableReplicas < baseline.AvailableReplicas || post.ReadyReplicas < baseline.ReadyReplicas || post.UpdatedReplicas < baseline.UpdatedReplicas {
		return true
	}
	baselineConditions := map[string]executor.HealthCondition{}
	for _, condition := range baseline.Conditions {
		baselineConditions[condition.Type] = executor.HealthCondition{Type: condition.Type, Status: condition.Status, Reason: condition.Reason, Message: condition.Message}
	}
	for _, condition := range post.Conditions {
		if condition.Type == "Available" && condition.Status == "False" && baselineConditions[condition.Type].Status != "False" {
			return true
		}
		if condition.Type == "Progressing" && condition.Status == "False" && strings.EqualFold(condition.Reason, "ProgressDeadlineExceeded") && !strings.EqualFold(baselineConditions[condition.Type].Reason, "ProgressDeadlineExceeded") {
			return true
		}
	}
	return false
}
