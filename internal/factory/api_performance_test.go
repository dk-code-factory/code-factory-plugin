package factory

import (
	"codefactory/internal/httpbudget"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var webhookPerformance = flag.Bool("api-performance", false, "run isolated API latency measurements")

func TestWebhookAPILatency(t *testing.T) {
	if !*webhookPerformance {
		t.Skip("isolated API performance gate")
	}
	rows := []map[string]any{}
	// Deliberate 125 ms stalls are correctness regressions: production must
	// preserve them. This gate measures useful, durably accepted responses.
	for sample := 0; sample < 20; sample++ {
		s, _, _ := fixture(t)
		key := func(context.Context) (string, error) { return "fixture-key", nil }
		body := `{"action":"opened"}`
		mac := hmac.New(sha256.New, []byte("fixture-key"))
		mac.Write([]byte(body))
		r := httptest.NewRequest("POST", "/github/webhook", strings.NewReader(body))
		r.Header.Set("X-GitHub-Delivery", "latency-delivery")
		r.Header.Set("X-GitHub-Event", "issues")
		r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		w := httptest.NewRecorder()
		started := time.Now()
		s.Webhook(key).ServeHTTP(w, r)
		elapsed := time.Since(started)
		rows = append(rows, map[string]any{"method": "POST", "path": "/github/webhook", "case": "durable_success", "status": w.Code, "outcome": "accepted_durable", "ms": float64(elapsed) / float64(time.Millisecond)})
		if w.Code != 202 || elapsed > httpbudget.Duration {
			t.Errorf("durable acknowledgement status%d elapsed%s", w.Code, elapsed)
		}
		var d delivery
		if err := s.Store.Get(context.Background(), "factory-delivery", "latency-delivery", &d); err != nil {
			t.Error("202 before durable retention", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := httpbudget.Drain(ctx); err != nil {
			t.Error(err)
		}
		cancel()
	}
	path := filepath.Join("..", "..", "verification-results", "webhook-latency.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(map[string]any{"limit_ms": httpbudget.Duration.Milliseconds(), "samples": rows}, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
