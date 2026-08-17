package controllers

import (
	"fmt"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

const (
	labelManagedBy               = "fluxseer-rca.aiops.platform/managed-by"
	labelRiskRule                = "fluxseer-rca.aiops.platform/risk-rule"
	annotationTargetRef          = "fluxseer-rca.aiops.platform/target-ref"
	annotationDetectionSource    = "fluxseer-rca.aiops.platform/detection-source"
	annotationNotificationAt     = "fluxseer-rca.aiops.platform/notified-at"
	annotationNotificationSource = "fluxseer-rca.aiops.platform/notification-source"
	annotationLineageSource      = "fluxseer-rca.aiops.platform/lineage-source"
	annotationLineageSourceKind  = "fluxseer-rca.aiops.platform/lineage-source-kind"
	annotationLineageSourceAPI   = "fluxseer-rca.aiops.platform/lineage-source-api-version"
	annotationLineageSourceUID   = "fluxseer-rca.aiops.platform/lineage-source-uid"
	annotationLineageGeneration  = "fluxseer-rca.aiops.platform/lineage-generation"
	annotationTargetUID          = "fluxseer-rca.aiops.platform/target-uid"
	annotationRiskSignalRef      = "fluxseer-rca.aiops.platform/risk-signal-ref"
	annotationRiskSignalUID      = "fluxseer-rca.aiops.platform/risk-signal-uid"
	annotationFindingFingerprint = "fluxseer-rca.aiops.platform/finding-fingerprint"
	annotationFindingSchema      = "fluxseer-rca.aiops.platform/finding-schema"
	annotationFindingType        = "fluxseer-rca.aiops.platform/finding-type"
	annotationObjectFindingID    = "fluxseer-rca.aiops.platform/object-finding-identity"
	annotationLogicalFindingID   = "fluxseer-rca.aiops.platform/logical-finding-identity"
	annotationIncidentOccurrence = "fluxseer-rca.aiops.platform/incident-occurrence"
	annotationTargetGeneration   = "fluxseer-rca.aiops.platform/target-generation"
	annotationInvestigationDepth = "fluxseer-rca.aiops.platform/investigation-depth"
	annotationWindowBucket       = "fluxseer-rca.aiops.platform/window-bucket"
	annotationEscalationChainRef = "fluxseer-rca.aiops.platform/escalation-chain-ref"
	conditionReady               = "Ready"
	conditionDegraded            = "Degraded"
	conditionTargetResolved      = "TargetResolved"
	conditionDatasourceResolved  = "DatasourceResolved"
	conditionQueryTypeSupported  = "QueryTypeSupported"
	conditionQueryPolicyReady    = "QueryPolicyReady"
	conditionEvidenceReady       = "EvidenceCollectionReady"
	conditionFindingReady        = "FindingReady"
	conditionRCAReady            = "RCAReady"
	conditionRemediationReady    = "RemediationReady"
	conditionVerified            = "Verified"
	conditionUnsupported         = "Unsupported"
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

func setRiskSignalStatus(status *v1alpha1.RiskSignalStatus, phase string, message string, generation int64, now time.Time) {
	setResourceStatus(&status.ResourceStatus, phase, message, generation, now)
}

func setRiskRuleStatus(status *v1alpha1.RiskRuleStatus, phase string, message string, generation int64, now time.Time) {
	setResourceStatus(&status.ResourceStatus, phase, message, generation, now)
}

func setDataSourceStatus(status *v1alpha1.DataSourceStatus, phase string, message string, generation int64, now time.Time) {
	setResourceStatus(&status.ResourceStatus, phase, message, generation, now)
}

func setInvestigationRequestStatus(status *v1alpha1.InvestigationRequestStatus, phase string, message string, generation int64, now time.Time) {
	setResourceStatus(&status.ResourceStatus, phase, message, generation, now)
}

func setStatusCondition(conditions *[]metav1.Condition, conditionType string, status metav1.ConditionStatus, reason string, message string, generation int64, now time.Time) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
		LastTransitionTime: metav1.NewTime(now),
	})
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

func targetRefString(target v1alpha1.TargetRef) string {
	return fmt.Sprintf("%s/%s %s", target.Namespace, target.Name, target.Kind)
}

func defaultRollbackPlan(actionType string) []string {
	switch actionType {
	case "kubernetes.rolloutRestart":
		return []string{"verify the restarted Deployment rollout and revert the triggering workload change if health does not recover"}
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
