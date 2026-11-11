package gateway

import (
	"bytes"
	"codefactory/internal/httpbudget"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRequestAuditExcludesCredentialsAndPreservesStreaming(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	oldWriter, oldFlags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer func() { slog.SetDefault(previous); log.SetOutput(oldWriter); log.SetFlags(oldFlags) }()
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
	oldWriter, oldFlags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer func() { slog.SetDefault(previous); log.SetOutput(oldWriter); log.SetFlags(oldFlags) }()
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

func TestCapacityRejectionsReachProductionGatewayAuditOnce(t *testing.T) {
	g := budgetGateway(t, "")
	var output capacityAuditBuffer
	previous := slog.Default()
	oldWriter, oldFlags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous); log.SetOutput(oldWriter); log.SetFlags(oldFlags) })
	release, entered := make(chan struct{}), make(chan struct{}, httpbudget.Capacity)
	var once sync.Once
	releaseWork := func() { once.Do(func() { close(release) }) }
	var wg sync.WaitGroup
	t.Cleanup(func() {
		releaseWork()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		awaitGatewaySignal(t, done, "capacity fixture responses")
		waitBudgetWorkers(t)
	})
	blocker := httpbudget.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { entered <- struct{}{}; <-release }))
	for i := 0; i < httpbudget.Capacity; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			blocker.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		}()
	}
	for i := 0; i < httpbudget.Capacity; i++ {
		awaitGatewaySignal(t, entered, "capacity fixture admission")
	}
	for _, row := range []struct {
		handler http.Handler
		path    string
	}{
		{httpbudget.WrapContext(context.Background(), g.Public()), "/token?code=private-code"},
		{httpbudget.WrapContext(context.Background(), g.Private()), "/approve?request=private-request"},
	} {
		w := httptest.NewRecorder()
		started := time.Now()
		row.handler.ServeHTTP(w, httptest.NewRequest("POST", row.path, strings.NewReader("private-body")))
		if w.Code != 503 || !strings.Contains(w.Body.String(), "request_not_started") || time.Since(started) > 100*time.Millisecond {
			t.Fatal("invalid capacity rejection", w.Code)
		}
	}
	releaseWork()
	waitBudgetWorkers(t)
	data := output.String()
	lines := strings.Split(strings.TrimSpace(data), "\n")
	if len(lines) != 2 {
		t.Fatal("capacity rejection missing/duplicated", data)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		seen[record["route"].(string)] = true
		if record["status"] != float64(503) || record["rejection_reason"] != "request_not_started" || record["rejected_count"] != float64(1) || record["coalesced"] != false {
			t.Fatal("missing rejection outcome", record)
		}
	}
	if !seen["oauth_token"] || !seen["owner_approval"] {
		t.Fatal("missing safe route identities", data)
	}
	for _, secret := range []string{"private-code", "private-request", "private-body"} {
		if strings.Contains(data, secret) {
			t.Fatal("private request retained", data)
		}
	}
}

type capacityAuditBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *capacityAuditBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *capacityAuditBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
