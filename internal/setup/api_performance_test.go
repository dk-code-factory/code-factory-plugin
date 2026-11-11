package setup

import (
	"context"
	"encoding/json"
	"flag"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var onboardingPerformance = flag.Bool("api-performance", false, "run isolated API latency measurements")

func TestOnboardingAPILatency(t *testing.T) {
	if !*onboardingPerformance {
		t.Skip("isolated API performance gate")
	}
	rows := []map[string]any{}
	for _, slow := range []bool{false, true} {
		delay := time.Duration(0)
		if slow {
			delay = 125 * time.Millisecond
		}
		f := &credentialForm{origin: "http://127.0.0.1:8787", nonce: "latency-nonce", expires: time.Now().Add(time.Minute), done: make(chan error, 1), save: func(ctx context.Context, _ string) error {
			if slow {
				time.Sleep(delay)
				return ctx.Err()
			}
			return nil
		}}
		for _, route := range []struct {
			method, path string
			want         int
		}{{"GET", "/", 200}, {"POST", "/ready", 200}, {"POST", "/credential", map[bool]int{false: 200, true: 503}[slow]}} {
			r := httptest.NewRequest(route.method, f.origin+route.path, strings.NewReader("key=fixture-private-key"))
			r.Header.Set("Origin", f.origin)
			r.Header.Set("Authorization", "Bearer "+f.nonce)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			started := time.Now()
			f.ServeHTTP(w, r)
			elapsed := time.Since(started)
			outcome := "success"
			if w.Code == 503 {
				outcome = "truthful_rejection"
			}
			rows = append(rows, map[string]any{"method": route.method, "path": route.path, "status": w.Code, "outcome": outcome, "dependency_delay_ms": delay.Milliseconds(), "ms": float64(elapsed) / float64(time.Millisecond)})
			if elapsed > 100*time.Millisecond || w.Code != route.want {
				t.Errorf("%s %s: status%d elapsed%s", route.method, route.path, w.Code, elapsed)
			}
		}
		select {
		case <-f.done:
		case <-time.After(2 * time.Second):
			t.Fatal("credential completion timed out")
		}
	}
	path := filepath.Join("..", "..", "verification-results", "onboarding-latency.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(map[string]any{"limit_ms": 100, "samples": rows}, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
