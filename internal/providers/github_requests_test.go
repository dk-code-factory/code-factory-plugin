package providers

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInboxRequestBudgetAndIndependentFreshness(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 30, 0, time.UTC)
	var calls, tokens, details, commitReads int
	head, base, review, check := "head", "base", "approved", "success"
	var since []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-GitHub-Api-Version") != apiVersion {
			t.Error("missing API version")
		}
		switch {
		case r.URL.Path == "/user":
			fmt.Fprint(w, `{"login":"owner"}`)
		case r.URL.Path == "/repos/o/r/issues":
			since = append(since, r.URL.Query().Get("since"))
			fmt.Fprint(w, `[]`)
		case r.URL.Path == "/repos/o/r/pulls":
			rows := []any{}
			for i := 1; i <= 10; i++ {
				rows = append(rows, map[string]any{"number": i, "state": "open", "head": map[string]any{"sha": fmt.Sprint(head, i), "ref": "topic", "repo": map[string]string{"full_name": "fork/r"}}, "base": map[string]any{"sha": base, "ref": "main", "repo": map[string]string{"full_name": "o/r"}}})
			}
			_ = json.NewEncoder(w).Encode(rows)
		case strings.HasSuffix(r.URL.Path, "/reviews"):
			fmt.Fprintf(w, `[{"id":1,"state":%q}]`, review)
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			fmt.Fprintf(w, `{"total_count":1,"check_runs":[{"conclusion":%q}]}`, check)
		case strings.Contains(r.URL.Path, "/pulls/") && strings.HasSuffix(r.URL.Path, "/commits"):
			commitReads++
			fmt.Fprint(w, `[{"sha":"one","commit":{"message":"work"}}]`)
		case strings.Contains(r.URL.Path, "/pulls/") && len(strings.Split(r.URL.Path, "/")) == 6:
			details++
			http.Error(w, "unexpected details", 500)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	g := &GitHub{BaseURL: server.URL, Client: server.Client(), Now: func() time.Time { return now }, Token: func(context.Context) (string, error) { tokens++; return "token", nil }}
	repo := domain.Repository{ID: "o/r", Imported: true, LastSync: now}
	run := func(full bool) []domain.Item {
		t.Helper()
		ctx := g.Operation(context.Background(), nil, false)
		if _, e := g.User(ctx); e != nil {
			t.Fatal(e)
		}
		items, e := g.Sync(ctx, repo, full)
		if e != nil {
			t.Fatal(e)
		}
		return items
	}
	first := run(false)
	if calls != 73 || tokens != 1 || details != 0 || commitReads != 10 {
		t.Fatal("cold request budget", calls, tokens, details, commitReads)
	}
	calls = 0
	now = now.Add(30 * time.Second)
	repo.LastSync = now
	warm := run(false)
	if calls != 62 || tokens != 2 || commitReads != 10 || since[0] != since[1] {
		t.Fatal("warm request budget or bucket reuse", calls, tokens, commitReads, since)
	}
	revisions := map[int]string{}
	for _, i := range first {
		revisions[i.Number] = i.Revision
	}
	for _, i := range warm {
		if revisions[i.Number] != i.Revision {
			t.Fatal("unchanged content changed revision")
		}
	}
	review, check = "dismissed", "failure"
	fresh := run(false)
	for _, i := range fresh {
		if revisions[i.Number] == i.Revision {
			t.Fatal("unchanged issue timestamp suppressed reviews/CI")
		}
	}
	head = "new"
	run(false)
	if commitReads != 20 {
		t.Fatal("head change reused commits", commitReads)
	}
	base = "newbase"
	run(false)
	if commitReads != 30 {
		t.Fatal("base change reused commits", commitReads)
	}
	run(true)
	if commitReads != 40 {
		t.Fatal("hourly reconciliation reused commits", commitReads)
	}
	now = now.Add(5 * time.Minute)
	calls = 0
	run(false)
	if calls != 63 {
		t.Fatal("identity did not expire", calls)
	}
	for _, boundary := range []struct{ last, want string }{{"2026-10-26T10:01:59Z", "2026-10-26T09:58:00Z"}, {"2026-10-26T10:02:00Z", "2026-10-26T10:00:00Z"}} {
		repo.LastSync, _ = time.Parse(time.RFC3339, boundary.last)
		run(false)
		if since[len(since)-1] != boundary.want {
			t.Fatal("incremental bucket boundary", boundary, since[len(since)-1])
		}
	}

}

func TestSharedCacheLRUAccountingAndExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	g := &GitHub{Now: func() time.Time { return now }}
	g.cachePut("replace", cacheEntry{Body: make([]byte, 1024)})
	g.cachePut("replace", cacheEntry{Body: []byte("x")})
	if g.cacheBytes != len("replace")+1 {
		t.Fatal("replacement leaked accounting", g.cacheBytes)
	}
	for i := 0; i < cacheEntries-1; i++ {
		g.cachePut(fmt.Sprint(i), cacheEntry{Body: []byte("x")})
	}
	g.cacheGet("replace")
	g.cachePut("last", cacheEntry{Body: []byte("x")})
	if _, ok := g.cacheGet("replace"); !ok {
		t.Fatal("recent entry evicted")
	}
	if _, ok := g.cacheGet("0"); ok {
		t.Fatal("oldest entry retained")
	}
	for i := 0; i < 6; i++ {
		g.cachePut(fmt.Sprintf("big%d", i), cacheEntry{Body: make([]byte, 16<<20)})
	}
	if g.cacheBytes > cacheLimit || len(g.cache) >= cacheEntries {
		t.Fatal("cache bounds exceeded", g.cacheBytes, len(g.cache))
	}
	g.CacheBlob("expires", []byte("one"), 10*time.Minute)
	now = now.Add(10 * time.Minute)
	if _, ok := g.CachedBlob("expires"); ok {
		t.Fatal("expiry boundary retained data")
	}
	total := 0
	for _, c := range g.cache {
		total += c.Size
	}
	if total != g.cacheBytes {
		t.Fatal("accounting mismatch", total, g.cacheBytes)
	}
}

func TestSharedOperationCacheAuthenticationAndSerialization(t *testing.T) {
	var tokens, calls, active, maxActive atomic.Int32
	token := "one"
	now := time.Unix(1_800_000_000, 0)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > maxActive.Load() {
			maxActive.Store(n)
		}
		calls.Add(1)
		if r.URL.Path == "/revoked" {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("If-None-Match") != "" {
			w.Header().Set("X-Updated", "yes")
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"same"`)
		w.Header().Set("Link", `<`+r.Host+`>; rel="prev"`)
		fmt.Fprint(w, `{"login":"owner"}`)
	}))
	defer s.Close()
	g := &GitHub{BaseURL: s.URL, Client: s.Client(), Now: func() time.Time { return now }, Token: func(context.Context) (string, error) { tokens.Add(1); return token, nil }}
	ctx := g.Operation(context.Background(), nil, false)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			var v any
			if _, e := g.get(ctx, "/data", &v); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if tokens.Load() != 1 || maxActive.Load() != 1 || calls.Load() != 12 {
		t.Fatal("operation/serialization", tokens.Load(), calls.Load(), maxActive.Load())
	}
	b, h, e := g.Request(ctx, "GET", "/data", nil, "")
	if e != nil || len(b) == 0 || h.Get("Link") == "" || h.Get("X-Updated") != "yes" {
		t.Fatal("304 header merge", h, e)
	}
	before := len(g.cache)
	if _, _, e = g.Request(ctx, "GET", "/data", nil, "application/octet-stream"); e != nil || len(g.cache) != before+1 {
		t.Fatal("media cache isolation", e)
	}
	if _, e = g.User(ctx); e != nil {
		t.Fatal(e)
	}
	if _, _, e = g.Request(ctx, "GET", "/revoked", nil, ""); e == nil {
		t.Fatal("revocation accepted")
	}
	previous := calls.Load()
	if _, e = g.User(ctx); e == nil || calls.Load() != previous {
		t.Fatal("invalidated operation replayed", e)
	}
	token = "two"
	if _, e = g.User(context.Background()); e != nil || tokens.Load() != 2 {
		t.Fatal("new operation did not rotate credentials", e, tokens.Load())
	}
}

func TestSharedPersistedCooldownAndPagination(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	requests := 0
	token := "one"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Retry-After", now.Add(3*time.Minute).Format(http.TimeFormat))
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(now.Add(time.Minute).Unix()))
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer s.Close()
	newClient := func() *GitHub {
		return &GitHub{BaseURL: s.URL, Client: s.Client(), Now: func() time.Time { return now }, Token: func(context.Context) (string, error) { return token, nil }}
	}
	g := newClient()
	_, _, e = g.Request(g.Operation(context.Background(), db, false), "GET", "/data", nil, "")
	var rate *RateLimitError
	if !errors.As(e, &rate) || !rate.Until.Equal(now.Add(3*time.Minute)) {
		t.Fatal("retry deadlines", e)
	}
	g = newClient()
	_, _, e = g.Request(g.Operation(context.Background(), db, true), "POST", "/issue", nil, "")
	var outcome *RequestError
	if !errors.As(e, &outcome) || outcome.Phase != BeforeDispatch || requests != 1 {
		t.Fatal("restart lost cooldown", e, requests)
	}
	token = "two"
	if _, _, e = g.Request(g.Operation(context.Background(), db, true), "GET", "/data", nil, ""); e != nil || requests != 2 {
		t.Fatal("credential cooldown isolation", e)
	}
	for _, link := range []string{`<https://evil.test/page>; rel="next"`, `</data>; rel="next", </other>; rel="next"`} {
		if _, e = g.NextPage(http.Header{"Link": {link}}); e == nil {
			t.Fatal("unsafe link accepted", link)
		}
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if _, _, e = g.Request(g.Operation(context.Background(), db, false), "GET", "/data", nil, ""); e == nil || requests != 2 {
		t.Fatal("unreadable ledger dispatched", e)
	}
}

func TestCheckRunLinksCompletenessAndIncrementalExpiry(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	checksMode := "valid"
	validators := []string{}
	var g *GitHub
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues"):
			validators = append(validators, r.Header.Get("If-None-Match"))
			w.Header().Set("ETag", `"issues"`)
			fmt.Fprint(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			fmt.Fprint(w, `[{"number":1,"head":{"sha":"head","ref":"topic","repo":{"full_name":"o/r"}},"base":{"sha":"base","ref":"main","repo":{"full_name":"o/r"}}}]`)
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `{"total_count":2,"check_runs":[{"name":"second"}]}`)
				return
			}
			if checksMode == "valid" {
				w.Header().Set("Link", `<`+g.BaseURL+r.URL.Path+`?per_page=100&page=2>; rel="next"`)
			}
			if checksMode == "cycle" {
				w.Header().Set("Link", `<`+g.BaseURL+r.URL.String()+`>; rel="next"`)
			}
			fmt.Fprint(w, `{"total_count":2,"check_runs":[{"name":"first"}]}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer s.Close()
	g = &GitHub{BaseURL: s.URL, Client: s.Client(), Token: func(context.Context) (string, error) { return "token", nil }, Now: func() time.Time { return now }}
	repo := domain.Repository{ID: "o/r", Imported: true, LastSync: now}
	items, e := g.Sync(context.Background(), repo, false)
	if e != nil || len(items) != 1 || !strings.Contains(string(items[0].Checks), "second") {
		t.Fatal("multi-page checks lost", items, e)
	}
	checksMode = "incomplete"
	if _, e = g.Sync(context.Background(), repo, false); e == nil {
		t.Fatal("incomplete check listing accepted")
	}
	checksMode = "cycle"
	if _, e = g.Sync(context.Background(), repo, false); e == nil {
		t.Fatal("cyclic check listing accepted")
	}
	now = now.Add(10 * time.Minute)
	checksMode = "valid"
	if _, e = g.Sync(context.Background(), repo, false); e != nil {
		t.Fatal(e)
	}
	if validators[0] != "" || validators[1] == "" || validators[len(validators)-1] != "" {
		t.Fatal("incremental validators did not expire", validators)
	}
}
