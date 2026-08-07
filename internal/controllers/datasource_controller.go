package controllers

import (
	"context"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/datasource"
	"github.com/FluxSeer/fluxseer-rca/internal/datasourceconfig"
)

type DataSourceReconciler struct {
	client.Client
	APIReader client.Reader
	Registry  *datasource.Registry
	Now       func() time.Time
}

func (r *DataSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var dataSource v1alpha1.DataSource
	if err := r.Get(ctx, req.NamespacedName, &dataSource); err != nil {
		if apierrors.IsNotFound(err) && r.Registry != nil {
			r.Registry.Unregister(req.Name)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	original := dataSource.DeepCopy()
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}

	source, err := datasourceconfig.BuildSourceFromResource(ctx, reader, dataSource, r.Client)
	if err != nil {
		if r.Registry != nil {
			r.Registry.Unregister(dataSource.Name)
		}
		reason, message, unsupported := classifyDataSourceError(err)
		setDataSourceStatus(&dataSource.Status, v1alpha1.PhaseFailed, message, dataSource.Generation, now())
		setStatusCondition(&dataSource.Status.Conditions, conditionReady, metav1.ConditionFalse, reason, message, dataSource.Generation, now())
		if unsupported {
			setStatusCondition(&dataSource.Status.Conditions, conditionUnsupported, metav1.ConditionTrue, reason, message, dataSource.Generation, now())
		} else {
			setStatusCondition(&dataSource.Status.Conditions, conditionUnsupported, metav1.ConditionFalse, "SupportedType", "datasource type is supported", dataSource.Generation, now())
		}
	} else {
		if r.Registry != nil && source != nil {
			r.Registry.RegisterNamed(dataSource.Name, source)
		}
		setDataSourceStatus(&dataSource.Status, v1alpha1.PhaseObserved, "datasource configuration validated", dataSource.Generation, now())
		setStatusCondition(&dataSource.Status.Conditions, conditionReady, metav1.ConditionTrue, "ConfigValidated", "datasource configuration validated", dataSource.Generation, now())
		setStatusCondition(&dataSource.Status.Conditions, conditionUnsupported, metav1.ConditionFalse, "SupportedType", "datasource type is supported", dataSource.Generation, now())
	}

	if !reflect.DeepEqual(original.Status, dataSource.Status) {
		if err := r.Status().Update(ctx, &dataSource); err != nil && !recordStatusUpdateConflict("DataSource", err) {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *DataSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.DataSource{}).
		Complete(r)
}

func classifyDataSourceError(err error) (string, string, bool) {
	if validationErr, ok := err.(*datasourceconfig.ValidationError); ok {
		return validationErr.Reason, validationErr.Message, validationErr.Reason == "AdapterNotRegistered"
	}
	return "ValidationFailed", err.Error(), false
}
