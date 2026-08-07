package executor

import (
	"context"
	"testing"
	"time"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

func TestRouterRoutesToKubernetesExecutor(t *testing.T) {
	router := NewRouter(
		KubernetesExecutor{Now: func() time.Time { return time.Unix(0, 0) }},
		GitOpsExecutor{},
		RunbookExecutor{},
		NotificationExecutor{},
	)

	result, err := router.Execute(context.Background(), ApprovedAction{
		Resource:   domain.ResourceRef{Namespace: "default", Name: "api"},
		ActionType: "kubernetes.scaleDeployment",
		ApprovedBy: "tester",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Executor != "kubernetes-executor" {
		t.Fatalf("expected kubernetes executor, got %s", result.Executor)
	}
}
