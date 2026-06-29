package model

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

func HTTPClient(timeout time.Duration, client *http.Client) *http.Client {
	if client == nil && timeout == 0 {
		return http.DefaultClient
	}

	base := http.DefaultClient
	if client != nil {
		base = client
	}
	copy := *base
	if timeout > 0 {
		copy.Timeout = timeout
	}
	return &copy
}

func ReadHTTPBody(resp *http.Response, provider string) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("%s provider read response: %v", provider, err),
		}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("%s provider returned %s: %s", provider, resp.Status, bytes.TrimSpace(body)),
		}
	}
	return body, nil
}

func NewJSONRequest(ctx context.Context, method string, endpoint string, payload []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
