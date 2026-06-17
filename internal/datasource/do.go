package datasource

import (
	"fmt"
	"net/http"
)

func DefaultDo(client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := defaultHTTPClient(client).Do(req)
	if err != nil {
		return nil, fmt.Errorf("perform request: %w", err)
	}
	return resp, nil
}
