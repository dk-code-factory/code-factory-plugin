package factory

import (
	"codefactory/internal/httpbudget"
	"codefactory/internal/store"
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
	"sync"
	"testing"
	"time"
)

var webhookPerformance = flag.Bool("api-performance", false, "run isolated API latency measurements")

func TestWebhookAPILatency(t *testing.T) {
	if !*webhookPerformance {
		t.Skip("isolated API performance gate")
	}
	rows := []map[string]any{}
	for _, mode := range []string{"durable_success", "secret_delay", "writer_contention"} {
		s, _, _ := fixture(t)
		release, exited := make(chan struct{}), make(chan struct{})
		var once sync.Once
		releaseWork := func() { once.Do(func() { close(release) }) }
		defer func() {
			releaseWork()
			if mode == "writer_contention" {
				select {
				case <-exited:
				case <-time.After(2 * time.Second):
					t.Error("writer cleanup timed out")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := httpbudget.Drain(ctx); err != nil {
				t.Error("webhook worker cleanup failed", err)
			}
		}()
		delay := time.Duration(0)
		if mode == "writer_contention" {
			delay = 125 * time.Millisecond
			entered := make(chan struct{})
			go func() {
				defer close(exited)
				_ = s.Store.Write(context.Background(), func(*store.Tx) error { close(entered); <-release; return nil })
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("writer readiness timed out")
			}
		}
		key := func(context.Context) (string, error) {
			if mode == "secret_delay" {
				<-release
			}
			return "fixture-key", nil
		}
		if mode == "secret_delay" {
			delay = 125 * time.Millisecond
		}
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
		outcome, want := "accepted_durable", 202
		if mode != "durable_success" {
			outcome, want = "truthful_rejection", 503
		}
		rows = append(rows, map[string]any{"method": "POST", "path": "/github/webhook", "case": mode, "status": w.Code, "outcome": outcome, "dependency_delay_ms": delay.Milliseconds(), "ms": float64(elapsed) / float64(time.Millisecond)})
		if w.Code != want || elapsed > 100*time.Millisecond {
			t.Errorf("%s status%d elapsed%s", mode, w.Code, elapsed)
		}
		if mode == "durable_success" {
			var d delivery
			if err := s.Store.Get(context.Background(), "factory-delivery", "latency-delivery", &d); err != nil {
				t.Error("202 before durable retention", err)
			}
		} else {
			time.Sleep(time.Until(started.Add(delay)))
			releaseWork()
			if mode == "writer_contention" {
				select {
				case <-exited:
				case <-time.After(2 * time.Second):
					t.Fatal("writer exit timed out")
				}
			}
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
	data, _ := json.MarshalIndent(map[string]any{"limit_ms": 100, "samples": rows}, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
