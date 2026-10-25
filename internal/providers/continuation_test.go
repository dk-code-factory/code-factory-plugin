package providers

import (
	"codefactory/internal/domain"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCurrentWorkItemRevalidatesCachedLifecycleAndFailsClosed(t *testing.T) {
	var calls atomic.Int32
	var closed, unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/repos/owner/repo/pulls/7" {
			t.Error("unexpected preflight request", r.Method, r.URL.Path)
		}
		if unavailable.Load() {
			w.WriteHeader(503)
			return
		}
		state, merged := "open", false
		if closed.Load() {
			state, merged = "closed", true
		}
		w.Header().Set("ETag", `"item"`)
		fmt.Fprintf(w, `{"number":7,"title":"Task","body":"Current scope","state":%q,"merged":%t,"head":{"sha":"abc","ref":"topic"}}`, state, merged)
	}))
	defer server.Close()
	g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	g.ConfigureUsage(nil, func(context.Context) (GitHubPolicy, error) { return GitHubPolicy{RequestsPerHour: 100}, nil })
	i := domain.Item{Repository: "owner/repo", Number: 7, Kind: "pr"}
	if _, err := g.CurrentWorkItem(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	for key, entry := range g.cache {
		entry.Until = time.Now().Add(time.Hour)
		g.cache[key] = entry
	}
	var cached any
	if _, err := g.get(context.Background(), "/repos/owner/repo/pulls/7", &cached); err != nil || calls.Load() != 1 {
		t.Fatal("ordinary cache not populated", calls.Load(), err)
	}
	closed.Store(true)
	current, err := g.CurrentWorkItem(context.Background(), i)
	if err != nil || current.State != "merged" || calls.Load() != 2 {
		t.Fatal("continuation trusted cached open state", current, calls.Load(), err)
	}
	unavailable.Store(true)
	if _, err := g.CurrentWorkItem(context.Background(), i); err == nil || calls.Load() != 3 {
		t.Fatal("continuation fell back to stale cached data", err)
	}
}
