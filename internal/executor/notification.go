package executor

import (
	"context"
	"fmt"
	"time"

	"fluxseer/internal/domain"
	"fluxseer/internal/notifier"
	webhooknotifier "fluxseer/internal/notifier/webhook"
)

type NotificationExecutor struct {
	Now        func() time.Time
	WebhookURL string
}

func (e NotificationExecutor) Name() string {
	return "notification-executor"
}

func (e NotificationExecutor) Execute(ctx context.Context, action ApprovedAction) (domain.ExecutionResult, error) {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}

	channel := action.Parameters["channel"]
	if channel == "" {
		channel = "webhook"
	}

	summary := fmt.Sprintf("Simulated notification sent to %s for %s", channel, action.Resource.Name)
	if e.WebhookURL != "" {
		client := webhooknotifier.Notifier{URL: e.WebhookURL}
		if err := client.Notify(ctx, notifier.Message{
			Title:   "FluxSeer RCA Notification",
			Summary: summary,
			Body:    action.DryRunResult,
			Fields: map[string]any{
				"target":     action.Resource.Name,
				"namespace":  action.Resource.Namespace,
				"actionType": action.ActionType,
			},
		}); err != nil {
			return domain.ExecutionResult{}, err
		}
		summary = fmt.Sprintf("Webhook notification sent to %s for %s", channel, action.Resource.Name)
	}

	return domain.ExecutionResult{
		Executor:   e.Name(),
		Status:     "succeeded",
		Summary:    summary,
		Outputs:    map[string]string{"channel": channel, "actionType": action.ActionType},
		FinishedAt: now(),
	}, nil
}
