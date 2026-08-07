package controllers

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/detector"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

type DeploymentRiskReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Detector *detector.Service
	Interval time.Duration
	Now      func() time.Time
}

func (r *DeploymentRiskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var deployment appsv1.Deployment
	if err := r.Get(ctx, req.NamespacedName, &deployment); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	if deployment.Spec.Selector == nil {
		return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
	}

	labels := make(map[string]string, len(deployment.Spec.Template.Labels))
	for key, value := range deployment.Spec.Template.Labels {
		labels[key] = value
	}

	annotations := make(map[string]string, len(deployment.Annotations))
	for key, value := range deployment.Annotations {
		annotations[key] = value
	}

	finding, err := r.Detector.Detect(ctx, detector.Request{
		Target:      deploymentToResource(deployment),
		Labels:      labels,
		Annotations: annotations,
		Window:      r.requeueAfter(),
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if finding == nil {
		return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
	}

	riskSignal := &v1alpha1.RiskSignal{}
	riskSignal.Name = deployment.Name + "-observed-risk"
	riskSignal.Namespace = deployment.Namespace
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, riskSignal, func() error {
		if err := controllerutil.SetControllerReference(&deployment, riskSignal, r.Scheme); err != nil {
			return err
		}
		if riskSignal.Labels == nil {
			riskSignal.Labels = map[string]string{}
		}
		if riskSignal.Annotations == nil {
			riskSignal.Annotations = map[string]string{}
		}
		riskSignal.Labels[labelManagedBy] = "deployment-risk-controller"
		riskSignal.Annotations[annotationTargetRef] = fmt.Sprintf("%s/%s", deployment.Namespace, deployment.Name)
		riskSignal.Annotations[annotationDetectionSource] = "deployment-annotation"

		evidence := make([]v1alpha1.EvidenceRef, 0, len(finding.Evidence))
		for _, item := range finding.Evidence {
			evidence = append(evidence, v1alpha1.EvidenceRef{
				Kind:    item.Kind,
				Source:  item.Source,
				Summary: item.Summary,
				Query:   item.Query,
				Reason:  item.Reason,
				Link:    item.Link,
			})
		}
		sort.Slice(evidence, func(i, j int) bool {
			return evidence[i].Kind+evidence[i].Summary < evidence[j].Kind+evidence[j].Summary
		})

		target := deploymentToResource(deployment)
		riskSignal.Spec.Target = v1alpha1.TargetRef{
			Cluster:    target.Cluster,
			Namespace:  target.Namespace,
			Kind:       target.Kind,
			Name:       target.Name,
			APIVersion: target.APIVersion,
			Service:    target.Service,
		}
		riskSignal.Spec.SignalType = finding.SignalType
		riskSignal.Spec.Severity = string(finding.Severity)
		riskSignal.Spec.Confidence = finding.Confidence
		riskSignal.Spec.DryRun = true
		riskSignal.Spec.TTLSeconds = int64(r.requeueAfter().Seconds()) * 6
		riskSignal.Spec.Evidence = evidence
		riskSignal.Spec.ActionType = "notification.sendSlack"
		riskSignal.Spec.Parameters = map[string]string{
			"channel": "webhook",
			"mode":    "read-only",
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	original := riskSignal.DeepCopy()
	setRiskSignalStatus(&riskSignal.Status, v1alpha1.PhaseConfirmed, finding.Summary, riskSignal.Generation, now())
	if statusChangedRiskSignal(original, riskSignal) {
		if err := r.Status().Update(ctx, riskSignal); err != nil && !recordStatusUpdateConflict("RiskSignal", err) {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
}

func (r *DeploymentRiskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		Owns(&v1alpha1.RiskSignal{}).
		Complete(r)
}

func (r *DeploymentRiskReconciler) requeueAfter() time.Duration {
	if r.Interval > 0 {
		return r.Interval
	}
	return 30 * time.Second
}

func deploymentToResource(deployment appsv1.Deployment) domain.ResourceRef {
	service := deployment.Labels["app"]
	if service == "" {
		service = deployment.Spec.Template.Labels["app"]
	}
	if service == "" {
		service = deployment.Name
	}
	return domain.ResourceRef{
		Cluster:    "in-cluster",
		Namespace:  deployment.Namespace,
		Kind:       "Deployment",
		Name:       deployment.Name,
		APIVersion: "apps/v1",
		Service:    service,
	}
}
