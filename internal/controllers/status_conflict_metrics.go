package controllers

import (
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"fluxseer/internal/rcametrics"
)

func recordStatusUpdateConflict(resource string, err error) bool {
	if !apierrors.IsConflict(err) {
		return false
	}
	rcametrics.RecordStatusUpdateConflict(resource)
	return true
}
