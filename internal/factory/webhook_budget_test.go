package factory

import (
	"codefactory/internal/httpbudget"
	"codefactory/internal/store"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebhookFiniteResponseForDelayedSecretAndStoreContention(t *testing.T) {
	for _, scenario := range []string{"secret", "writer"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _ := fixture(t)
			release := make(chan struct{})
			exited := make(chan struct{})
			var releaseOnce sync.Once
			releaseWork := func() { releaseOnce.Do(func() { close(release) }) }
			defer func() {
				releaseWork()
				select {
				case <-exited:
				case <-time.After(2 * time.Second):
					t.Error("dependency cleanup timed out")
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := httpbudget.Drain(ctx); err != nil {
					t.Error("webhook cleanup failed", err)
				}
			}()
			if scenario == "writer" {
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
				if scenario == "secret" {
					defer close(exited)
					<-release
				}
				return "fixture-private-key", nil
			}
			body := `{"action":"opened"}`
			mac := hmac.New(sha256.New, []byte("fixture-private-key"))
			mac.Write([]byte(body))
			request := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/github/webhook", strings.NewReader(body))
				r.Header.Set("X-GitHub-Delivery", "bounded-delivery")
				r.Header.Set("X-GitHub-Event", "issues")
				r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
				w := httptest.NewRecorder()
				s.Webhook(key).ServeHTTP(w, r)
				return w
			}
			started := time.Now()
			w := request()
			if time.Since(started) > 100*time.Millisecond || w.Code != 503 || !strings.Contains(w.Body.String(), "outcome_unknown") {
				t.Fatal("blocked durable acknowledgement", w.Code, time.Since(started))
			}
			// Keep dependency stalled125ms and prove a rejected acknowledgement did not
			// claim the delivery was durably accepted. Cancellation suppresses late write.
			time.Sleep(time.Until(started.Add(125 * time.Millisecond)))
			releaseWork()
			select {
			case <-exited:
			case <-time.After(2 * time.Second):
				t.Fatal("delayed dependency exit timed out")
			}
			// The dependency signal precedes the HTTP handler's final store work.
			// Drain actual admitted workers before checking absence or redelivering.
			drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
			err := httpbudget.Drain(drainCtx)
			cancelDrain()
			if err != nil {
				t.Fatal("rejected webhook worker did not exit", err)
			}
			var d delivery
			if err := s.Store.Get(context.Background(), "factory-delivery", "bounded-delivery", &d); err == nil {
				t.Fatal("timed-out delivery unexpectedly committed")
			}
			key = func(context.Context) (string, error) { return "fixture-private-key", nil }
			started = time.Now()
			if retry := request(); retry.Code != 202 || time.Since(started) > 100*time.Millisecond {
				t.Fatal("deliberate exact-id redelivery failed", retry.Code)
			}
			if err := s.Store.Get(context.Background(), "factory-delivery", "bounded-delivery", &d); err != nil {
				t.Fatal("202 without durable delivery", err)
			}
		})
	}
}
