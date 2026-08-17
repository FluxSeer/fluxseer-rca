package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/FluxSeer/fluxseer-rca/internal/notifier"
	webhooknotifier "github.com/FluxSeer/fluxseer-rca/internal/notifier/webhook"
)

type NotificationExecutor struct {
	Now        func() time.Time
	WebhookURL string
}

func (e NotificationExecutor) Name() string {
	return "notification-executor"
}

func (e NotificationExecutor) Execute(ctx context.Context, action ExecutorRequest) (ExecutorResult, error) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	startedAt := now()

	channel := ""
	if value, ok := action.Parameters["channel"].(string); ok {
		channel = value
	}
	if channel == "" {
		channel = "webhook"
	}

	summary := fmt.Sprintf("Simulated notification sent to %s for %s", channel, action.Target.Name)
	if e.WebhookURL != "" {
		client := webhooknotifier.Notifier{URL: e.WebhookURL}
		if err := client.Notify(ctx, notifier.Message{
			Title:   "FluxSeer RCA Notification",
			Summary: summary,
			Body:    action.DryRunResult,
			Fields: map[string]any{
				"target":     action.Target.Name,
				"namespace":  action.Target.Namespace,
				"actionType": action.ActionType,
			},
		}); err != nil {
			return ExecutorResult{}, err
		}
		summary = fmt.Sprintf("Webhook notification sent to %s for %s", channel, action.Target.Name)
	}

	return ExecutorResult{
		ExecutionID: action.ExecutionID,
		Outcome:     ExecutionOutcomeSucceeded,
		Executor:    e.Name(),
		Status:      "succeeded",
		Summary:     summary,
		Outputs:     map[string]string{"channel": channel, "actionType": action.ActionType},
		StartedAt:   startedAt,
		FinishedAt:  now(),
	}, nil
}
