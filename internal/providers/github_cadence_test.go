package providers

import (
	"codefactory/internal/domain"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubDetailCadenceControlsHTTPQueryCacheAndExplicitLiveReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/repos/o/r/pulls/1":
			fmt.Fprint(w, `{"number":1,"state":"open","title":"current","head":{"sha":"head"}}`)
		case "/repos/o/r/commits/head/check-runs":
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		case "/repos/o/r/commits/head/statuses":
			fmt.Fprint(w, `[]`)
		case "/graphql":
			fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"headRefOid":"head","reviewDecision":"APPROVED","statusCheckRollup":{"state":"SUCCESS"}}}}}`)
		default:
			t.Errorf("unexpected detail request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	source := managedFixture(t, server, nil, &now, 100, 0)
	cache := NewCachedGitHub(source)
	cache.clock = func() time.Time { return now }
	ttl := time.Hour
	failPolicy := false
	cache.ConfigureUsage(nil, func(context.Context) (GitHubPolicy, error) {
		if failPolicy {
			return GitHubPolicy{}, errors.New("fixture policy failure")
		}
		return GitHubPolicy{RequestsPerHour: 100, DetailTTL: ttl}, nil
	})
	read := func(ctx context.Context, want int32) {
		t.Helper()
		item, err := cache.PullRequest(ctx, "o/r", 1)
		if err != nil || item.PR == nil || item.PR.Error != "" || item.PR.CI != "success" || calls.Load() != want {
			t.Fatal("incorrect PR freshness or request count", item.PR, calls.Load(), want, err)
		}
	}
	read(ctx, 4)
	now = now.Add(20 * time.Minute)
	read(ctx, 4)
	read(WithGitHubRead(ctx, RequireLive), 8)
	read(ctx, 8)
	now = now.Add(2 * time.Minute)
	ttl = time.Minute
	read(ctx, 12)
	failPolicy = true
	if _, err := cache.PullRequest(ctx, "o/r", 1); err == nil || calls.Load() != 12 {
		t.Fatal("policy failure returned a cached successful query", err, calls.Load())
	}
	failPolicy = false
	// Explicit lifecycle validation stays live even within the longer detail TTL.
	ttl = time.Hour
	if item, err := cache.CurrentWorkItem(WithGitHubRead(ctx, RequireLive), domain.Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "pr"}); err != nil || item.Head != "head" || calls.Load() != 13 {
		t.Fatal("lifecycle action reused detail TTL", item, calls.Load(), err)
	}
}

func TestGitHubDetailCadenceControlsGraphQLCacheAfterSettingChanges(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"data":{"value":"current"}}`)
	}))
	defer server.Close()
	g := managedFixture(t, server, nil, &now, 100, 0)
	ttl := time.Hour
	g.ConfigureUsage(nil, func(context.Context) (GitHubPolicy, error) {
		return GitHubPolicy{RequestsPerHour: 100, DetailTTL: ttl}, nil
	})
	var out struct{ Value string }
	read := func(ctx context.Context, want int32) {
		t.Helper()
		if err := g.graph(ctx, "query { value }", nil, &out); err != nil || out.Value != "current" || calls.Load() != want {
			t.Fatal("GraphQL detail cadence mismatch", calls.Load(), want, out, err)
		}
	}
	read(ctx, 1)
	now = now.Add(20 * time.Minute)
	read(ctx, 1)
	ttl = time.Minute
	read(ctx, 2)
	read(ctx, 2)
	read(WithGitHubRead(ctx, RequireLive), 3)
}
