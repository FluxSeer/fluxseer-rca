package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"fluxagent/internal/audit"
	"fluxagent/internal/controlplane"
	"fluxagent/internal/domain"
	"fluxagent/internal/executor"
	"fluxagent/internal/guardrails"
	"fluxagent/internal/ingestion"
	"fluxagent/internal/knowledge"
	"fluxagent/internal/model/heuristic"
	"fluxagent/internal/reasoning"
)

func main() {
	now := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	ingestionPipeline := ingestion.NewPipeline()
	ingestionPipeline.Now = func() time.Time { return now }

	orchestrator := &controlplane.Orchestrator{
		Ingestion: ingestionPipeline,
		Reasoning: reasoning.NewEngine(knowledge.NewBase(), heuristic.Provider{}),
		Guardrails: guardrails.NewEngine(guardrails.Policy{
			AllowedActionTypes: []string{
				"kubernetes.scaleDeployment",
				"kubernetes.rolloutPause",
				"gitops.createPullRequest",
				"runbook.triggerWorkflow",
				"notification.sendSlack",
			},
			ProtectedNamespaces:      []string{"prod"},
			AutoApproveMaxSeverity:   domain.SeverityLow,
			RequireApprovalAtOrAbove: domain.SeverityMedium,
		}),
		Executor: executor.NewRouter(
			executor.KubernetesExecutor{Now: func() time.Time { return now }},
			executor.GitOpsExecutor{Now: func() time.Time { return now }},
			executor.RunbookExecutor{Now: func() time.Time { return now }},
			executor.NotificationExecutor{Now: func() time.Time { return now }},
		),
		Audit: audit.NewStore(),
		Now:   func() time.Time { return now },
	}

	signals := []domain.Signal{
		{
			ID:        "evt-1",
			Kind:      "event",
			Source:    "kubernetes-events",
			Severity:  domain.SeverityHigh,
			Message:   "Pod entered OOMKilled after deployment rollout",
			Resource:  sampleResource(),
			Timestamp: now.Add(-5 * time.Minute),
		},
		{
			ID:        "evt-2",
			Kind:      "event",
			Source:    "kubernetes-events",
			Severity:  domain.SeverityHigh,
			Message:   "BackOff restarting failed container",
			Resource:  sampleResource(),
			Timestamp: now.Add(-4 * time.Minute),
		},
		{
			ID:       "met-1",
			Kind:     "metric",
			Source:   "prometheus",
			Severity: domain.SeverityMedium,
			Message:  "memory usage exceeded threshold",
			Resource: sampleResource(),
			Attributes: map[string]string{
				"metric": "container_memory_working_set_bytes",
				"value":  "983.2",
			},
			Timestamp: now.Add(-3 * time.Minute),
		},
		{
			ID:        "log-1",
			Kind:      "log",
			Source:    "loki",
			Severity:  domain.SeverityMedium,
			Message:   "application terminated with fatal out of memory error",
			Resource:  sampleResource(),
			Timestamp: now.Add(-2 * time.Minute),
		},
	}

	result, err := orchestrator.Run(context.Background(), ingestion.Request{
		ID:      "incident-prod-payments-api-001",
		Signals: signals,
		Metadata: map[string]string{
			"environment": "production",
			"source":      "demo",
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(result)
}

func sampleResource() domain.ResourceRef {
	return domain.ResourceRef{
		Cluster:    "prod-cluster-apne1",
		Namespace:  "payments",
		Kind:       "Deployment",
		Name:       "payments-api",
		APIVersion: "apps/v1",
		Service:    "payments-api",
	}
}
