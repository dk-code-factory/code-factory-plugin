package providers

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/json"
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

func sharedComponentFixture(t *testing.T) (*CachedGitHub, func() *CachedGitHub, *time.Time, *string, map[string]int) {
	t.Helper()
	now, head := time.Now().UTC(), "head"
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if r.URL.Path == "/graphql" {
			fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"headRefOid":%q,"reviewDecision":"APPROVED","statusCheckRollup":{"state":"SUCCESS"}}}}}`, head)
			return
		}
		etag := `"` + head + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		switch {
		case r.URL.Path == "/repos/o/r/pulls/1", r.URL.Path == "/repos/o/r/issues/1":
			fmt.Fprintf(w, `{"number":1,"title":"PR","state":"open","pull_request":{},"head":{"sha":%q,"ref":"topic","repo":{"full_name":"o/r"}},"base":{"sha":"base","ref":"main","repo":{"full_name":"o/r"}}}`, head)
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	t.Cleanup(server.Close)
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	newClient := func() *CachedGitHub {
		source := managedFixture(t, server, db, &now, 500, 0)
		g := NewCachedGitHub(source)
		g.ConfigureUsage(db, func(context.Context) (GitHubPolicy, error) { return GitHubPolicy{RequestsPerHour: 500}, nil })
		g.clock = func() time.Time { return now }
		return g
	}
	return newClient(), newClient, &now, &head, calls
}

func TestGitHubCompositeMissSharesComponentsWithoutRenewingTheirObservation(t *testing.T) {
	g, _, now, head, calls := sharedComponentFixture(t)
	ctx := context.Background()
	first, err := g.PullRequest(ctx, "o/r", 1)
	if err != nil || first.PR == nil || first.PR.Error != "" || calls["/graphql"] != 1 {
		t.Fatal("initial PR fetch", first, calls, err)
	}
	original := *now
	*now = now.Add(5 * time.Minute)
	input := domain.Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "pr", Head: "head"}
	detail, err := g.ItemDetail(ctx, input)
	if err != nil || detail.PR == nil || detail.PR.Error != "" || calls["/graphql"] != 1 {
		t.Fatal("another composite re-fetched fresh GraphQL metadata", detail, calls, err)
	}
	if !detail.DetailSyncedAt.Equal(original) || !detail.PR.SyncedAt.Equal(original) {
		t.Fatal("shared component observation was promoted to the new fetch time", detail.DetailSyncedAt, detail.PR.SyncedAt, original)
	}
	entries, err := store.List[githubQueryEntry](ctx, g.state, "github-query-cache")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		var saved domain.Item
		if json.Unmarshal(e.Body, &saved) == nil && saved.ID == input.ID && !saved.DetailSyncedAt.IsZero() {
			found = true
			if !e.CheckedAt.Equal(original) || !e.Until.Equal(original.Add(GitHubDetailTTL)) {
				t.Fatal("composite cache extended the reused component's freshness", e.CheckedAt, e.Until)
			}
		}
	}
	if !found {
		t.Fatal("detail composite was not retained")
	}
	*now = original.Add(GitHubDetailTTL + time.Second)
	if fresh, err := g.ItemDetail(ctx, input); err != nil || fresh.PR.Error != "" || calls["/graphql"] != 2 || !fresh.DetailSyncedAt.Equal(*now) {
		t.Fatal("expired shared metadata was not refreshed", fresh, calls, err)
	}
	*head = "next-head"
	input.Head = *head
	if fresh, err := g.ItemDetail(ctx, input); err != nil || fresh.PR.Error != "" || fresh.PR.Head != *head || calls["/graphql"] != 3 {
		t.Fatal("new head reused a different head's GraphQL snapshot", fresh, calls, err)
	}
}

func TestGitHubCompositeInvalidationFencesOverlappingComponentsAcrossRestart(t *testing.T) {
	g, restart, now, _, calls := sharedComponentFixture(t)
	ctx := context.Background()
	if _, err := g.PullRequest(ctx, "o/r", 1); err != nil {
		t.Fatal(err)
	}
	input := domain.Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "pr", Head: "head"}
	if _, err := g.ItemDetail(ctx, input); err != nil {
		t.Fatal(err)
	}
	if calls["/graphql"] != 1 {
		t.Fatal("ordinary overlapping reads did not share their component", calls)
	}
	if err := g.Invalidate(ctx, input.ID); err != nil {
		t.Fatal(err)
	}
	g = restart()
	if fresh, err := g.PullRequest(ctx, "o/r", 1); err != nil || fresh.PR.Error != "" || calls["/graphql"] != 2 {
		t.Fatal("restart lost component invalidation", fresh, calls, err)
	}
	if _, err := g.ItemDetail(ctx, input); err != nil || calls["/graphql"] != 2 {
		t.Fatal("another composite failed to reuse the revalidated component", calls, err)
	}
	*now = now.Add(time.Minute)
	if fresh, err := g.ItemDetail(WithGitHubRead(ctx, RequireLive), input); err != nil || fresh.PR.Error != "" || calls["/graphql"] != 3 || !fresh.DetailSyncedAt.Equal(*now) {
		t.Fatal("explicit live read reused an underlying component", fresh, calls, err)
	}
	if fresh, err := g.PullRequest(ctx, "o/r", 1); err != nil || fresh.PR.Error != "" || calls["/graphql"] != 3 {
		t.Fatal("successful live write-through immediately invalidated its components", fresh, calls, err)
	}
}

func TestGitHubCompositeBarrierPreservesGenericConditionalValidationAndMemoization(t *testing.T) {
	ctx := context.Background()
	now, calls, validations := time.Now().UTC(), 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("ETag", `"unchanged"`)
		if r.Header.Get("If-None-Match") == `"unchanged"` {
			validations++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		fmt.Fprint(w, `{"login":"me"}`)
	}))
	defer server.Close()
	source := managedFixture(t, server, nil, &now, 100, 0)
	g := NewCachedGitHub(source)
	g.clock = func() time.Time { return now }
	ctx = source.StartOperation(ctx, nil, false)
	// Give this batch a memo without requiring a durable publication ledger.
	ctx.Value(operationKey{}).(*operation).memoize = true
	read := func(name string) error {
		var out struct{ Login string }
		err := g.query(ctx, name, "identity", nil, &out, time.Minute, func(ctx context.Context) error {
			body, _, err := source.Request(ctx, http.MethodGet, "/user", nil, "")
			if err != nil {
				return err
			}
			return json.Unmarshal(body, &out)
		})
		if err == nil && out.Login != "me" {
			t.Fatal("conditional validation lost the cached body", out)
		}
		return err
	}
	if err := read("first"); err != nil {
		t.Fatal(err)
	}
	if err := g.Invalidate(ctx, "identity"); err != nil {
		t.Fatal(err)
	}
	if err := read("after-invalidation"); err != nil || calls != 2 || validations != 1 {
		t.Fatal("invalidation reused a batch memo or discarded its conditional validator", calls, validations, err)
	}
	if err := read("overlapping-query"); err != nil || calls != 2 {
		t.Fatal("generic revalidated component was not reusable", calls, err)
	}
}

func TestGitHubCompositeBarriersAreScopedAndBounded(t *testing.T) {
	ctx := context.Background()
	g := NewCachedGitHub(&cacheSourceFixture{scope: "principal"})
	barrier := func(scope, resource string) string {
		t.Helper()
		v, err := g.queryBarrier(ctx, scope, resource)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if err := g.Invalidate(ctx, "o/r#1"); err != nil {
		t.Fatal(err)
	}
	item := barrier("principal", "o/r#1")
	if item == "" || barrier("principal", "o/r") == "" || barrier("principal", "o/r#2") != "" || barrier("principal", "other/repo#1") != "" || barrier("other-principal", "o/r#1") != "" {
		t.Fatal("item invalidation escaped its principal/item and repository index")
	}
	for n := 0; n < githubQueryBarrierLimit; n++ {
		if err := g.Invalidate(ctx, fmt.Sprintf("extra/repo-%d", n)); err != nil {
			t.Fatal(err)
		}
	}
	if len(g.barriers.Resources) > githubQueryBarrierLimit || g.barriers.Global == "" || barrier("principal", "o/r#1") == item {
		t.Fatal("barrier rollover exceeded its bound or resurrected an old generation")
	}
}

func TestGitHubCompositeInvalidationRejectsConcurrentAndUnreadableGenerations(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		fmt.Fprint(w, `{"login":"me"}`)
	}))
	defer server.Close()
	defer unblock()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	newClient := func() *CachedGitHub {
		source := managedFixture(t, server, db, &now, 100, 0)
		g := NewCachedGitHub(source)
		g.ConfigureUsage(db, func(context.Context) (GitHubPolicy, error) { return GitHubPolicy{RequestsPerHour: 100}, nil })
		g.clock = func() time.Time { return now }
		return g
	}
	g, other := newClient(), newClient()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, err := g.User(ctx)
		done <- err
	}()
	defer func() {
		unblock()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("read worker did not finish during cleanup")
		}
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal("read exited before entering the provider", err)
	case <-time.After(5 * time.Second):
		t.Fatal("read did not reach the provider")
	}
	if err := other.Invalidate(ctx, "identity"); err != nil {
		t.Fatal(err)
	}
	unblock()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "invalidated during") {
			t.Fatal("concurrent invalidation promoted an obsolete component generation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read did not complete after release")
	}
	if user, err := g.User(ctx); err != nil || user != "me" || calls.Load() != 2 {
		t.Fatal("retry reused the invalidated in-flight response", user, calls.Load(), err)
	}
	if err := db.Put(ctx, "github-query-barriers", "application", map[string]any{"resources": true}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := g.User(ctx); err == nil || calls.Load() != 2 {
		t.Fatal("unreadable generation served a cached success or contacted GitHub", calls.Load(), err)
	}
}
