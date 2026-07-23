package datasource

import (
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
	defaultDatasourceMaxAttempts = 3
	defaultDatasourceRetryDelay  = 100 * time.Millisecond
)

type QueryError struct {
	Reason  string
	Message string
}

func (e *QueryError) Error() string {
	return e.Message
}

func DefaultDo(client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := defaultHTTPClient(client).Do(req)
	if err != nil {
		return nil, fmt.Errorf("perform request: %w", err)
	}
	return resp, nil
}

func DoRequestWithRetry(ctx context.Context, source string, client *http.Client, buildRequest func(context.Context) (*http.Request, error)) ([]byte, error) {
	httpClient := defaultHTTPClient(client)
	attempts := 0
	for attempts < defaultDatasourceMaxAttempts {
		attempts++
		req, err := buildRequest(ctx)
		if err != nil {
			return nil, &QueryError{
				Reason:  "DatasourceRequestInvalid",
				Message: fmt.Sprintf("%s datasource create request: %v", source, err),
			}
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			queryErr, retry := classifyTransportError(source, err)
			if !retry || attempts >= defaultDatasourceMaxAttempts {
				return nil, withAttemptContext(queryErr, attempts)
			}
			if sleepErr := sleepForRetry(ctx, retryDelay(attempts, nil)); sleepErr != nil {
				return nil, withAttemptContext(queryErr, attempts)
			}
			continue
		}

		body, queryErr, retry := readResponse(resp, source)
		_ = resp.Body.Close()
		if queryErr == nil {
			return body, nil
		}
		if !retry || attempts >= defaultDatasourceMaxAttempts {
			return nil, withAttemptContext(queryErr, attempts)
		}
		if sleepErr := sleepForRetry(ctx, retryDelay(attempts, resp)); sleepErr != nil {
			return nil, withAttemptContext(queryErr, attempts)
		}
	}

	return nil, &QueryError{
		Reason:  "DatasourceUnavailable",
		Message: fmt.Sprintf("%s datasource request exhausted retry budget", source),
	}
}

func QueryErrorReason(err error, fallback string) string {
	var queryErr *QueryError
	if errors.As(err, &queryErr) && strings.TrimSpace(queryErr.Reason) != "" {
		return queryErr.Reason
	}
	return fallback
}

func readResponse(resp *http.Response, source string) ([]byte, *QueryError, bool) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &QueryError{
			Reason:  "DatasourceUnavailable",
			Message: fmt.Sprintf("%s datasource read response: %v", source, err),
		}, true
	}
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return body, nil, false
	}
	queryErr, retry := classifyHTTPStatus(source, resp.StatusCode, body)
	return nil, queryErr, retry
}

func classifyTransportError(source string, err error) (*QueryError, bool) {
	if errors.Is(err, context.Canceled) {
		return &QueryError{
			Reason:  "DatasourceUnavailable",
			Message: fmt.Sprintf("%s datasource request was canceled: %v", source, err),
		}, false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &QueryError{
			Reason:  "DatasourceUnavailable",
			Message: fmt.Sprintf("%s datasource request timed out: %v", source, err),
		}, true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return &QueryError{
			Reason:  "DatasourceUnavailable",
			Message: fmt.Sprintf("%s datasource transport error: %v", source, err),
		}, true
	}

	return &QueryError{
		Reason:  "DatasourceUnavailable",
		Message: fmt.Sprintf("%s datasource transport error: %v", source, err),
	}, true
}

func classifyHTTPStatus(source string, statusCode int, body []byte) (*QueryError, bool) {
	detail := strings.TrimSpace(string(body))
	message := fmt.Sprintf("%s datasource returned %d", source, statusCode)
	if detail != "" {
		message = fmt.Sprintf("%s: %s", message, detail)
	}

	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return &QueryError{Reason: "DatasourceAuthFailed", Message: message}, false
	case statusCode == http.StatusTooManyRequests:
		return &QueryError{Reason: "DatasourceRateLimited", Message: message}, true
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooEarly:
		return &QueryError{Reason: "DatasourceUnavailable", Message: message}, true
	case statusCode >= 500 && statusCode <= 599:
		return &QueryError{Reason: "DatasourceUnavailable", Message: message}, true
	case statusCode >= 400 && statusCode <= 499:
		return &QueryError{Reason: "DatasourceRequestInvalid", Message: message}, false
	default:
		return &QueryError{Reason: "DatasourceUnavailable", Message: message}, false
	}
}

func withAttemptContext(err *QueryError, attempts int) *QueryError {
	if err == nil || attempts <= 1 {
		return err
	}
	return &QueryError{
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
	return time.Duration(attempt) * defaultDatasourceRetryDelay
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
