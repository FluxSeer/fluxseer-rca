package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"fluxagent/internal/notifier"
)

type Notifier struct {
	URL    string
	Client *http.Client
}

func (n Notifier) Name() string {
	return "webhook"
}

func (n Notifier) Notify(ctx context.Context, message notifier.Message) error {
	if strings.TrimSpace(n.URL) == "" {
		return fmt.Errorf("webhook URL is empty")
	}

	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook returned %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	return nil
}
