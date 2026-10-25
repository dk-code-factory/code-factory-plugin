package gateway

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestAuditExcludesCredentialsAndPreservesStreaming(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	handler := audit("public", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		if e := http.NewResponseController(w).Flush(); e != nil {
			t.Error("audit broke response streaming", e)
		}
	}))
	request := httptest.NewRequest("POST", "https://public.example/token?code=hidden-code", strings.NewReader("refresh_token=hidden-refresh"))
	request.Header.Set("Authorization", "Bearer hidden-access")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	log := output.String()
	for _, secret := range []string{"hidden-code", "hidden-refresh", "hidden-access", "public.example"} {
		if strings.Contains(log, secret) {
			t.Fatal("audit leaked credential", log)
		}
	}
	if !strings.Contains(log, `"route":"oauth_token"`) || !strings.Contains(log, `"status":401`) {
		t.Fatal("missing actionable audit", log)
	}
	if response.Code != 401 || !response.Flushed {
		t.Fatal("audit changed HTTP behavior")
	}
}

func TestRequestAuditIncludesUnknownRoutesWithoutRequestDetails(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	handler := audit("public", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error("audit broke unknown-route response streaming", err)
		}
	}))
	request := httptest.NewRequest("HIDDEN-METHOD", "https://hidden-host.example/.well-known/hidden-path?code=hidden-query", strings.NewReader("hidden-body"))
	request.Header.Set("X-Hidden-Header", "hidden-header-value")
	request.Header.Set("Authorization", "Bearer hidden-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	log := output.String()
	for _, secret := range []string{"HIDDEN-METHOD", "hidden-host", ".well-known", "hidden-path", "hidden-query", "hidden-body", "X-Hidden-Header", "hidden-header-value", "hidden-token"} {
		if strings.Contains(log, secret) {
			t.Fatal("unknown-route audit leaked request details", log)
		}
	}
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal("missing structured unknown-route audit", err)
	}
	if duration, ok := entry["duration_ms"].(float64); !ok || duration < 0 {
		t.Fatal("missing safe latency diagnostics", log)
	}
	if entry["route"] != "other" || entry["status"] != float64(http.StatusNotFound) || entry["surface"] != "public" {
		t.Fatal("missing actionable unknown-route audit", log)
	}
	if response.Code != http.StatusNotFound || !response.Flushed {
		t.Fatal("audit changed unknown-route HTTP behavior")
	}
}
