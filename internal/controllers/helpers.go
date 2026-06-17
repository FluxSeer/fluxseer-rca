package controllers

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/domain"
)

func targetToResource(target v1alpha1.TargetRef) domain.ResourceRef {
	return domain.ResourceRef{
		Cluster:    target.Cluster,
		Namespace:  target.Namespace,
		Kind:       target.Kind,
		Name:       target.Name,
		APIVersion: target.APIVersion,
		Service:    target.Service,
	}
}

func setResourceStatus(status *v1alpha1.ResourceStatus, phase string, message string, generation int64, now time.Time) {
	status.Phase = phase
	status.Message = message
	status.ObservedGeneration = generation
	status.UpdatedAt = metav1.NewTime(now)
}

func remediationFromPlan(plan *v1alpha1.RemediationPlan) domain.Remediation {
	step := v1alpha1.RemediationStep{}
	if len(plan.Spec.Steps) > 0 {
		step = plan.Spec.Steps[0]
	}

	return domain.Remediation{
		ActionType:   step.ActionType,
		Description:  step.Description,
		Parameters:   step.Parameters,
		RollbackPlan: plan.Spec.RollbackPlan,
	}
}

func actionSummary(actionType string, target v1alpha1.TargetRef) string {
	return fmt.Sprintf("%s prepared for %s/%s", actionType, target.Namespace, target.Name)
}

func defaultRollbackPlan(actionType string) []string {
	switch actionType {
	case "kubernetes.rolloutPause":
		return []string{"resume rollout after validation", "revert deployment image if crash loop persists"}
	case "kubernetes.scaleDeployment":
		return []string{"restore previous replica count"}
	case "gitops.createPullRequest":
		return []string{"close PR if mitigation is not needed"}
	case "runbook.triggerWorkflow":
		return []string{"cancel workflow run if approval is revoked"}
	case "notification.sendSlack":
		return []string{"notify responders that the alert was informational only"}
	default:
		return []string{"manual rollback required"}
	}
}
