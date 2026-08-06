package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"fluxseer/internal/notifier"
)

func TestNotifierPostsJSONPayload(t *testing.T) {
	received := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := Notifier{URL: server.URL}
	if err := client.Notify(context.Background(), notifier.Message{Title: "demo"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !received {
		t.Fatalf("expected webhook request")
	}
}
