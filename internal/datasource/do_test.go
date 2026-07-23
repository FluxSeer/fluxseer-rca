package datasource

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDoRequestWithRetryRetriesTransportError(t *testing.T) {
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts < defaultDatasourceMaxAttempts {
				return nil, timeoutError{}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("ok")),
			}, nil
		}),
	}

	body, err := DoRequestWithRetry(context.Background(), "test", client, newTestRequest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != defaultDatasourceMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", defaultDatasourceMaxAttempts, attempts)
	}
	if string(body) != "ok" {
		t.Fatalf("expected ok body, got %q", string(body))
	}
}

func TestDoRequestWithRetryDoesNotRetryCanceledContext(t *testing.T) {
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return nil, context.Canceled
		}),
	}

	_, err := DoRequestWithRetry(context.Background(), "test", client, newTestRequest)
	queryErr := assertQueryError(t, err, "DatasourceUnavailable")
	if attempts != 1 {
		t.Fatalf("expected one attempt, got %d", attempts)
	}
	if !strings.Contains(queryErr.Message, "request was canceled") {
		t.Fatalf("expected canceled message, got %q", queryErr.Message)
	}
}

func TestDoRequestWithRetryMapsTimeoutAfterRetries(t *testing.T) {
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return nil, timeoutError{}
		}),
	}

	_, err := DoRequestWithRetry(context.Background(), "test", client, newTestRequest)
	queryErr := assertQueryError(t, err, "DatasourceUnavailable")
	if attempts != defaultDatasourceMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", defaultDatasourceMaxAttempts, attempts)
	}
	if !strings.Contains(queryErr.Message, "after 3 attempts") {
		t.Fatalf("expected attempt count in message, got %q", queryErr.Message)
	}
}

func newTestRequest(ctx context.Context) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test/query", nil)
}

func assertQueryError(t *testing.T, err error, reason string) *QueryError {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	var queryErr *QueryError
	if !errors.As(err, &queryErr) {
		t.Fatalf("expected QueryError, got %T", err)
	}
	if queryErr.Reason != reason {
		t.Fatalf("expected %s, got %s", reason, queryErr.Reason)
	}
	return queryErr
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type timeoutError struct{}

func (timeoutError) Error() string {
	return "timeout"
}

func (timeoutError) Timeout() bool {
	return true
}

func (timeoutError) Temporary() bool {
	return true
}
