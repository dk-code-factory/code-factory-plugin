package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/store"
)

func managedFixture(t *testing.T, server *httptest.Server, db *store.Store, now *time.Time, budget, reserve int) *GitHub {
	t.Helper()
	g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture-secret-token", nil }, clock: func() time.Time { return *now }}
	g.ConfigureUsage(db, func(context.Context) (GitHubPolicy, error) {
		return GitHubPolicy{RequestsPerHour: budget, Reserve: reserve}, nil
	})
	return g
}
func TestGitHubSharedBudgetSurvivesRestartSerializesAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var calls, active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := managedFixture(t, server, db, &now, 3, 0)
	var wg sync.WaitGroup
	for n := 0; n < 6; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); var out any; _, _ = g.get(ctx, "/item", &out) }()
	}
	wg.Wait()
	if calls.Load() != 3 || peak.Load() != 1 {
		t.Fatal("shared budget or serialization failed", calls.Load(), peak.Load())
	}
	restarted := managedFixture(t, server, db, &now, 3, 0)
	var out any
	if _, err = restarted.get(ctx, "/another", &out); err == nil || calls.Load() != 3 {
		t.Fatal("restart reset request budget", err)
	}
	now = now.Add(time.Hour + 2*time.Second)
	if _, err = restarted.get(ctx, "/another", &out); err != nil || calls.Load() != 4 {
		t.Fatal("rolling budget did not recover", err)
	}
	db.Close()
	if _, err = restarted.get(ctx, "/failed-state", &out); err == nil || calls.Load() != 4 {
		t.Fatal("failed accounting sent a request", err)
	}
}
func TestGitHubPersistentConditionalCacheAuthIsolationAnd304RateObservation(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-RateLimit-Remaining", "101")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(now.Add(time.Hour).Unix()))
		if r.Header.Get("If-None-Match") == `"one"` {
			w.Header().Set("X-RateLimit-Remaining", "99")
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"one"`)
		w.Header().Set("Set-Cookie", "secret-cookie-value")
		w.Header().Set("Link", "<"+r.URL.Path+"?page=2>; rel=\"next\"")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := managedFixture(t, server, db, &now, 20, 100)
	var out map[string]bool
	if _, err = g.get(ctx, "/item", &out); err != nil {
		t.Fatal(err)
	}
	g = managedFixture(t, server, db, &now, 20, 100)
	h, err := g.get(ctx, "/item", &out)
	if err != nil || !out["ok"] || h.Get("Link") == "" {
		t.Fatal("persistent 304 cache lost content or pagination", err)
	}
	if _, err = g.get(ctx, "/other", &out); err == nil || calls.Load() != 2 {
		t.Fatal("304 headers failed to protect reserve", err, calls.Load())
	}
	g.Token = func(context.Context) (string, error) { return "different-fixture-token", nil }
	if _, err = g.get(ctx, "/item", &out); err != nil || calls.Load() != 3 {
		t.Fatal("credential scope inherited cached body or rate state", err)
	}
	rows, _ := db.List(ctx, "github-cache")
	for _, row := range rows {
		if strings.Contains(string(row), "fixture-secret-token") || strings.Contains(string(row), "secret-cookie-value") {
			t.Fatal("cache retained credentials or cookies")
		}
	}
	status, err := g.UsageStatus(ctx)
	if err != nil || status.RequestsLastHour != 3 {
		t.Fatal("usage inspection changed the budget", status, err)
	}
}
func TestGitHubSecondaryCooldownBlocksRESTAndGraphQLAcrossRestart(t *testing.T) {
	for _, graph := range []bool{false, true} {
		t.Run(fmt.Sprint(graph), func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if graph {
					fmt.Fprint(w, `{"errors":[{"type":"RATE_LIMITED"}]}`)
				} else {
					w.WriteHeader(403)
					fmt.Fprint(w, `{"message":"You have exceeded a secondary rate limit."}`)
				}
			}))
			defer server.Close()
			db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			g := managedFixture(t, server, db, &now, 20, 0)
			var out any
			if graph {
				err = g.graph(ctx, "query{viewer{login}}", nil, &out)
			} else {
				_, err = g.get(ctx, "/item", &out)
			}
			if err == nil {
				t.Fatal("rate response accepted")
			}
			g = managedFixture(t, server, db, &now, 20, 0)
			_, _ = g.get(ctx, "/rest", &out)
			_ = g.graph(ctx, "query{viewer{login}}", nil, &out)
			if calls.Load() != 1 {
				t.Fatal("cooldown was not global or restart-safe", calls.Load())
			}
		})
	}
}
func TestIndexCallCountDoesNotGrowWithPRHistory(t *testing.T) {
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if strings.HasSuffix(r.URL.Path, "/issues") {
			fmt.Fprint(w, `[]`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/pulls") {
			fmt.Fprint(w, `[`)
			for n := 1; n <= 100; n++ {
				if n > 1 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `{"number":%d,"state":"open","head":{"sha":"abc"},"requested_reviewers":[{"login":"me"}]}`, n)
			}
			fmt.Fprint(w, `]`)
			return
		}
		t.Error("index requested per-PR enrichment", r.URL.Path)
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	items, _, err := g.SyncIndex(context.Background(), domain.Repository{ID: "o/r"}, true)
	if err != nil || len(items) != 100 || len(calls) != 2 {
		t.Fatal("index fan-out", len(items), calls, err)
	}
	for _, i := range items {
		if !i.SummaryOnly || i.Head != "abc" || len(i.ReviewRequests) != 1 {
			t.Fatal("index lost review request")
		}
	}
}

func TestNotificationConditionalReadsHonorPollIntervalAcrossCursorChanges(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/notifications" {
			t.Error("unexpected notification request")
		}
		w.Header().Set("Last-Modified", "Thu, 22 Oct 2026 00:00:00 GMT")
		w.Header().Set("X-Poll-Interval", "300")
		if r.Header.Get("If-Modified-Since") != "" {
			w.WriteHeader(304)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := managedFixture(t, server, db, &now, 20, 0)
	since := now.Add(-time.Minute)
	if _, _, err = g.ReviewNotifications(ctx, since); err != nil {
		t.Fatal(err)
	}
	if _, _, err = g.ReviewNotifications(ctx, since); err != nil || calls.Load() != 1 {
		t.Fatal("poll interval bypassed", err)
	}
	if _, _, err = g.ReviewNotifications(ctx, since.Add(time.Second)); err == nil || calls.Load() != 1 {
		t.Fatal("changed cursor bypassed endpoint poll interval", err)
	}
	now = now.Add(301 * time.Second)
	g = managedFixture(t, server, db, &now, 20, 0)
	if _, _, err = g.ReviewNotifications(ctx, since); err != nil || calls.Load() != 2 {
		t.Fatal("restart lost notification conditional header", err)
	}
}
func TestGitHubRedirectsAreBudgetedAndNeverForwardAuthenticationOffOrigin(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var external atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { external.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/old":
			w.Header().Set("Location", "/new")
			w.WriteHeader(301)
		case "/escape":
			w.Header().Set("Location", target.URL+"/leak")
			w.WriteHeader(302)
		default:
			fmt.Fprint(w, `{"ok":true}`)
		}
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := managedFixture(t, server, db, &now, 20, 0)
	var out map[string]bool
	if _, err = g.get(ctx, "/old", &out); err != nil || !out["ok"] {
		t.Fatal("safe redirect failed", err)
	}
	if _, err = g.get(ctx, "/escape", &out); err == nil || external.Load() != 0 {
		t.Fatal("redirect escaped authentication origin", err)
	}
	status, _ := g.UsageStatus(ctx)
	if status.RequestsLastHour != 3 {
		t.Fatal("redirect hops escaped budget", status)
	}
}

func TestRateHeadersStopRequestsEvenWhenErrorBodyIsTruncated(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "300")
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(429)
		fmt.Fprint(w, "{")
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := managedFixture(t, server, db, &now, 20, 0)
	var out any
	if _, err = g.get(ctx, "/rate", &out); err == nil {
		t.Fatal("rate response accepted")
	}
	if _, err = g.get(ctx, "/other", &out); err == nil || calls.Load() != 1 {
		t.Fatal("truncated response body bypassed header cooldown", err, calls.Load())
	}
}

func TestEmptyLocalCredentialCannotFallBackToAnonymousAPI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{}`) }))
	defer server.Close()
	g := NewGitHub()
	g.BaseURL = server.URL
	g.Client = server.Client()
	var out any
	if _, err := g.get(context.Background(), "/user", &out); err == nil || calls.Load() != 0 {
		t.Fatal("empty credential attempted an anonymous request", err)
	}
}
