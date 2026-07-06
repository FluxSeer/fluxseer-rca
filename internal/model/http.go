package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultProviderTimeout     = 15 * time.Second
	defaultProviderMaxAttempts = 3
	defaultProviderRetryDelay  = 100 * time.Millisecond
)

func HTTPClient(timeout time.Duration, client *http.Client) *http.Client {
	base := http.DefaultClient
	if client != nil {
		base = client
	}
	copy := *base
	copy.Timeout = normalizeProviderTimeout(timeout)
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
		providerErr, _ := classifyProviderHTTPStatus(provider, resp.StatusCode, body)
		return nil, providerErr
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

func DoRequestWithRetry(ctx context.Context, provider string, timeout time.Duration, client *http.Client, buildRequest func(context.Context) (*http.Request, error)) ([]byte, error) {
	httpClient := HTTPClient(timeout, client)
	attempts := 0
	for attempts < defaultProviderMaxAttempts {
		attempts++
		req, err := buildRequest(ctx)
		if err != nil {
			if providerErr, ok := err.(*ProviderError); ok {
				return nil, providerErr
			}
			return nil, &ProviderError{
				Reason:  "ProviderUnavailable",
				Message: err.Error(),
			}
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			providerErr, retry := classifyProviderTransportError(provider, err)
			if !retry || attempts >= defaultProviderMaxAttempts {
				return nil, withAttemptContext(providerErr, attempts)
			}
			if sleepErr := sleepForRetry(ctx, retryDelay(attempts, nil)); sleepErr != nil {
				return nil, withAttemptContext(providerErr, attempts)
			}
			continue
		}

		body, providerErr, retry := readProviderResponse(resp, provider)
		_ = resp.Body.Close()
		if providerErr == nil {
			return body, nil
		}
		if !retry || attempts >= defaultProviderMaxAttempts {
			return nil, withAttemptContext(providerErr, attempts)
		}
		if sleepErr := sleepForRetry(ctx, retryDelay(attempts, resp)); sleepErr != nil {
			return nil, withAttemptContext(providerErr, attempts)
		}
	}

	return nil, &ProviderError{
		Reason:  "ProviderUnavailable",
		Message: fmt.Sprintf("%s provider request exhausted retry budget", provider),
	}
}

func normalizeProviderTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return DefaultProviderTimeout
}

func readProviderResponse(resp *http.Response, provider string) ([]byte, *ProviderError, bool) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("%s provider read response: %v", provider, err),
		}, true
	}
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return body, nil, false
	}
	providerErr, retry := classifyProviderHTTPStatus(provider, resp.StatusCode, body)
	return nil, providerErr, retry
}

func classifyProviderTransportError(provider string, err error) (*ProviderError, bool) {
	if errors.Is(err, context.Canceled) {
		return &ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("%s provider request was canceled: %v", provider, err),
		}, false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("%s provider request timed out: %v", provider, err),
		}, true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return &ProviderError{
			Reason:  "ProviderUnavailable",
			Message: fmt.Sprintf("%s provider transport error: %v", provider, err),
		}, true
	}

	return &ProviderError{
		Reason:  "ProviderUnavailable",
		Message: fmt.Sprintf("%s provider transport error: %v", provider, err),
	}, true
}

func classifyProviderHTTPStatus(provider string, statusCode int, body []byte) (*ProviderError, bool) {
	detail := strings.TrimSpace(string(bytes.TrimSpace(body)))
	message := fmt.Sprintf("%s provider returned %d", provider, statusCode)
	if detail != "" {
		message = fmt.Sprintf("%s: %s", message, detail)
	}

	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return &ProviderError{
			Reason:  "ProviderAuthFailed",
			Message: message,
		}, false
	case statusCode == http.StatusTooManyRequests:
		return &ProviderError{
			Reason:  "ProviderRateLimited",
			Message: message,
		}, true
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooEarly:
		return &ProviderError{
			Reason:  "ProviderUnavailable",
			Message: message,
		}, true
	case statusCode >= 500 && statusCode <= 599:
		return &ProviderError{
			Reason:  "ProviderUnavailable",
			Message: message,
		}, true
	case statusCode >= 400 && statusCode <= 499:
		return &ProviderError{
			Reason:  "ProviderRequestInvalid",
			Message: message,
		}, false
	default:
		return &ProviderError{
			Reason:  "ProviderUnavailable",
			Message: message,
		}, false
	}
}

func withAttemptContext(err *ProviderError, attempts int) *ProviderError {
	if err == nil || attempts <= 1 {
		return err
	}
	return &ProviderError{
		Reason:  err.Reason,
		Message: fmt.Sprintf("%s after %d attempts", err.Message, attempts),
	}
}

func retryDelay(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After")); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
				return time.Duration(seconds) * time.Second
			}
			if when, err := http.ParseTime(retryAfter); err == nil {
				if wait := time.Until(when); wait > 0 {
					return wait
				}
			}
		}
	}
	if attempt <= 0 {
		attempt = 1
	}
	return time.Duration(attempt) * defaultProviderRetryDelay
}

func sleepForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
