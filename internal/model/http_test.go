package model

import (
	"net/http"
	"testing"
	"time"
)

func TestHTTPClientAppliesDefaultProviderTimeout(t *testing.T) {
	client := HTTPClient(0, nil)
	if client.Timeout != DefaultProviderTimeout {
		t.Fatalf("expected default provider timeout %s, got %s", DefaultProviderTimeout, client.Timeout)
	}
}

func TestHTTPClientPreservesProvidedTransportAndOverridesTimeout(t *testing.T) {
	base := &http.Client{Timeout: 45 * time.Second}
	client := HTTPClient(2*time.Second, base)
	if client.Timeout != 2*time.Second {
		t.Fatalf("expected timeout override 2s, got %s", client.Timeout)
	}
	if client == base {
		t.Fatal("expected copied client, got original pointer")
	}
}
