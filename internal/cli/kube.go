package cli

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
)

func buildScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	return scheme
}

func buildTypeMeta(kind string) metav1.TypeMeta {
	return metav1.TypeMeta{
		APIVersion: v1alpha1.SchemeGroupVersion.String(),
		Kind:       kind,
	}
}

func buildObjectMeta(name, namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
	}
}

func metav1Duration(duration time.Duration) metav1.Duration {
	return metav1.Duration{Duration: duration}
}
