package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type serverState struct {
	mu            sync.Mutex
	faultedApps   map[string]bool
	webhookEvents []map[string]any
}

func main() {
	state := &serverState{
		faultedApps: map[string]bool{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/v1/query_range", state.handlePrometheus)
	mux.HandleFunc("/loki/api/v1/query_range", state.handleLoki)
	mux.HandleFunc("/demo/webhook", state.handleWebhook)
	mux.HandleFunc("/demo/state", state.handleState)
	mux.HandleFunc("/demo/fault/", state.handleFault(true))
	mux.HandleFunc("/demo/recover/", state.handleFault(false))

	addr := ":8080"
	if value := os.Getenv("PORT"); value != "" {
		addr = ":" + value
	}

	log.Printf("demo observability listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func (s *serverState) handlePrometheus(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	app := inferApp(query)
	value := "0.01"
	if s.isFaulted(app) {
		value = "0.92"
	}

	writeJSON(w, map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "matrix",
			"result": []map[string]any{
				{
					"metric": map[string]string{
						"__name__": "http_requests_total",
						"app":      app,
					},
					"values": [][]any{
						{time.Now().Add(-time.Minute).Unix(), value},
						{time.Now().Unix(), value},
					},
				},
			},
		},
	})
}

func (s *serverState) handleLoki(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	app := inferApp(query)
	result := []map[string]any{}
	if s.isFaulted(app) {
		result = append(result, map[string]any{
			"stream": map[string]string{
				"app": app,
			},
			"values": [][]string{
				{strconvNano(time.Now()), "error timeout while calling upstream"},
				{strconvNano(time.Now().Add(-30 * time.Second)), "error request failed with 503"},
			},
		})
	}

	writeJSON(w, map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "streams",
			"result":     result,
		},
	})
}

func (s *serverState) handleWebhook(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.webhookEvents = append(s.webhookEvents, payload)
		s.mu.Unlock()
		writeJSON(w, map[string]any{"status": "received"})
	case http.MethodGet:
		s.mu.Lock()
		events := append([]map[string]any(nil), s.webhookEvents...)
		s.mu.Unlock()
		writeJSON(w, map[string]any{"events": events})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *serverState) handleState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]any{
		"faultedApps":   s.faultedApps,
		"webhookEvents": s.webhookEvents,
	})
}

func (s *serverState) handleFault(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app := strings.TrimPrefix(r.URL.Path, "/demo/fault/")
		if !enabled {
			app = strings.TrimPrefix(r.URL.Path, "/demo/recover/")
		}
		app = strings.TrimSpace(app)
		if app == "" {
			http.Error(w, "app is required", http.StatusBadRequest)
			return
		}

		s.mu.Lock()
		s.faultedApps[app] = enabled
		s.mu.Unlock()
		writeJSON(w, map[string]any{"app": app, "faulted": enabled})
	}
}

func (s *serverState) isFaulted(app string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.faultedApps[app]
}

func inferApp(query string) string {
	for _, candidate := range []string{"fluxagent-sample", "payments-api"} {
		if strings.Contains(query, candidate) {
			return candidate
		}
	}
	return "unknown-app"
}

func strconvNano(ts time.Time) string {
	return strconv.FormatInt(ts.UnixNano(), 10)
}

func writeJSON(w http.ResponseWriter, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
