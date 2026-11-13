package transport

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTrackingReadPreparationWaitsForExactCurrentProducer(t *testing.T) {
	f := newTrackingFixture(t)
	args := map[string]any{"view": "issues", "search": "held producer"}
	key := projectionKey("factory_state", mustJSON(args))
	deps := projectionDependencies("factory_state", mustJSON(args))
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	f.server.projections.read(key, deps, func(ctx context.Context) (any, error) {
		close(started)
		select {
		case <-release:
			return map[string]any{"marker": "complete current projection"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("projection producer did not start")
	}
	result, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, "factory_state", args)
	if err != nil || result["isError"] == true {
		t.Fatal("initial HTTP read", result, err)
	}
	response := result["structuredContent"].(map[string]any)["data"].(map[string]any)["response"].(map[string]any)
	if response["state"] != "pending" {
		t.Fatal("blocked producer did not return real HTTP pending", response)
	}
	// A completed different key must not satisfy this read's producer handshake.
	f.server.projections.mu.Lock()
	f.server.projections.entries["different projection"] = projectionEntry{revision: f.a.Store.Revision(deps...), dependencies: deps}
	f.server.projections.mu.Unlock()
	wait := func() error {
		ctx, cancel := context.WithTimeout(f.ctx, 30*time.Millisecond)
		defer cancel()
		return f.awaitReadProjection(ctx, "factory_state", args)
	}
	if err := wait(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("preparation accepted an unfinished producer", err)
	}
	unblock()
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	if err := f.awaitReadProjection(ctx, "factory_state", args); err != nil {
		t.Fatal("completed producer was not observed", err)
	}
	value, failed := f.call(t, "factory_state", args)
	if failed || value["marker"] != "complete current projection" {
		t.Fatal("prepared HTTP call lost useful producer result", value, failed)
	}
	// Completion for this key at another revision is also insufficient.
	f.server.projections.mu.Lock()
	entry := f.server.projections.entries[key]
	entry.revision ^= 1
	f.server.projections.entries[key] = entry
	f.server.projections.mu.Unlock()
	if err := wait(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("preparation accepted a stale revision", err)
	}
}
