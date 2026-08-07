package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/FluxSeer/fluxseer-rca/internal/audit"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
	"github.com/FluxSeer/fluxseer-rca/internal/executor"
	"github.com/FluxSeer/fluxseer-rca/internal/guardrails"
	"github.com/FluxSeer/fluxseer-rca/internal/ingestion"
	"github.com/FluxSeer/fluxseer-rca/internal/knowledge"
	"github.com/FluxSeer/fluxseer-rca/internal/model/heuristic"
	"github.com/FluxSeer/fluxseer-rca/internal/reasoning"
)

func TestOrchestratorProducesManualApprovalForHighRisk(t *testing.T) {
	now := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	ingestionPipeline := ingestion.NewPipeline()
	ingestionPipeline.Now = func() time.Time { return now }

	orchestrator := &Orchestrator{
		Ingestion: ingestionPipeline,
		Reasoning: reasoning.NewEngine(knowledge.NewBase(), heuristic.Provider{}),
		Guardrails: guardrails.NewEngine(guardrails.Policy{
			AllowedActionTypes:       []string{"kubernetes.rolloutPause", "kubernetes.scaleDeployment"},
			ProtectedNamespaces:      []string{"payments"},
			AutoApproveMaxSeverity:   domain.SeverityLow,
			RequireApprovalAtOrAbove: domain.SeverityMedium,
		}),
		Executor: executor.NewRouter(executor.KubernetesExecutor{}, executor.GitOpsExecutor{}, executor.RunbookExecutor{}, executor.NotificationExecutor{}),
		Audit:    audit.NewStore(),
		Now:      func() time.Time { return now },
	}

	result, err := orchestrator.Run(context.Background(), ingestion.Request{
		ID: "incident-1",
		Signals: []domain.Signal{
			{
				ID:       "1",
				Kind:     "event",
				Source:   "kubernetes-events",
				Severity: domain.SeverityHigh,
				Message:  "Pod entered OOMKilled after deployment rollout",
				Resource: domain.ResourceRef{
					Cluster:    "prod",
					Namespace:  "payments",
					Kind:       "Deployment",
					Name:       "payments-api",
					APIVersion: "apps/v1",
					Service:    "payments-api",
				},
				Timestamp: now.Add(-time.Minute),
			},
		},
		Metadata: map[string]string{"environment": "production"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Approval.Action != domain.ApprovalManual {
		t.Fatalf("expected manual approval, got %s", result.Approval.Action)
	}
	if result.Execution != nil {
		t.Fatalf("expected no execution for manual approval flow")
	}
}

func TestOrchestratorExecutesAutoApprovedAction(t *testing.T) {
	now := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	ingestionPipeline := ingestion.NewPipeline()
	ingestionPipeline.Now = func() time.Time { return now }

	orchestrator := &Orchestrator{
		Ingestion: ingestionPipeline,
		Reasoning: reasoning.NewEngine(knowledge.NewBase(), heuristic.Provider{}),
		Guardrails: guardrails.NewEngine(guardrails.Policy{
			AllowedActionTypes:       []string{"kubernetes.scaleDeployment"},
			AutoApproveMaxSeverity:   domain.SeverityLow,
			RequireApprovalAtOrAbove: domain.SeverityMedium,
		}),
		Executor: executor.NewRouter(
			executor.KubernetesExecutor{Now: func() time.Time { return now }},
			executor.GitOpsExecutor{},
			executor.RunbookExecutor{},
			executor.NotificationExecutor{},
		),
		Audit: audit.NewStore(),
		Now:   func() time.Time { return now },
	}

	result, err := orchestrator.Run(context.Background(), ingestion.Request{
		ID: "incident-2",
		Signals: []domain.Signal{
			{
				ID:       "1",
				Kind:     "metric",
				Source:   "prometheus",
				Severity: domain.SeverityLow,
				Message:  "replica lag detected",
				Resource: domain.ResourceRef{
					Cluster:    "staging",
					Namespace:  "payments",
					Kind:       "Deployment",
					Name:       "payments-api",
					APIVersion: "apps/v1",
					Service:    "payments-api",
				},
				Attributes: map[string]string{
					"metric": "http_request_inflight",
					"value":  "210",
				},
				Timestamp: now.Add(-time.Minute),
			},
		},
		Metadata: map[string]string{"environment": "staging"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Approval.Action != domain.ApprovalAuto {
		t.Fatalf("expected auto approval, got %s", result.Approval.Action)
	}
	if result.Execution == nil {
		t.Fatalf("expected execution result")
	}
}
