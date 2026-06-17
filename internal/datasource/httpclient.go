package datasource

import (
	"net/http"
	"time"
)

func defaultHTTPClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 10 * time.Second}
}
