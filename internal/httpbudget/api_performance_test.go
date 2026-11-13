package httpbudget

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var boundaryPerformance = flag.Bool("api-performance", false, "run isolated API latency measurements")

func TestHTTPBoundaryAPILatency(t *testing.T) {
	if !*boundaryPerformance {
		t.Skip("isolated API performance gate")
	}
	rows := []map[string]any{}
	measure := func(name string, h http.Handler, want int, outcome string) {
		t.Helper()
		w := httptest.NewRecorder()
		started := time.Now()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/finite", nil))
		elapsed := time.Since(started)
		rows = append(rows, map[string]any{"case": name, "status": w.Code, "outcome": outcome, "ms": float64(elapsed) / float64(time.Millisecond)})
		checkResponseLatency(t, name, elapsed)
		if w.Code != want {
			t.Errorf("%s status%d elapsed%s", name, w.Code, elapsed)
		}
	}
	measure("normal", Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "saved state") })), 200, "success")
	measure("accepted", Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(202)
		io.WriteString(w, "accepted identity")
	})), 202, "accepted")
	measure("maximum_supported_response", Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, strings.Repeat("x", MaxResponseBytes)) })), 200, "success")
	measure("oversized_response", Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, strings.Repeat("x", MaxResponseBytes+1))
	})), 500, "terminal_response_limit")
	release := make(chan struct{})
	var once sync.Once
	releaseWork := func() { once.Do(func() { close(release) }) }
	entered := make(chan struct{}, Capacity)
	var wg sync.WaitGroup
	defer func() {
		releaseWork()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("admitted request cleanup timed out")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := Drain(ctx); err != nil {
			t.Error("HTTP worker cleanup failed", err)
		}
	}()
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		io.WriteString(w, "late")
	}))
	for range Capacity {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/held", nil))
		}()
	}
	for range Capacity {
		awaitBoundary(t, entered, "admission readiness")
	}
	measure("capacity_admission", h, 503, "request_not_started")
	// Admission remains occupied by actual work, regardless of elapsed time.
	time.Sleep(125 * time.Millisecond)
	measure("capacity_while_slow", h, 503, "request_not_started")
	releaseWork()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Drain(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "verification-results", "http-boundary-latency.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(map[string]any{"limit_ms": Duration.Milliseconds(), "finite_response_limit_bytes": MaxResponseBytes, "worker_capacity": Capacity, "samples": rows}, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func awaitBoundary(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(label + " timed out")
	}
}

func checkResponseLatency(t *testing.T, name string, elapsed time.Duration) {
	t.Helper()
	if elapsed > Duration {
		t.Errorf("%s exceeded 75 ms: %s", name, elapsed)
	}
}
