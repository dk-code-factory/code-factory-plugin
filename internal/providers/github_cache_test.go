package providers

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubCacheExpiryInvalidationLiveWriteThroughAndRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var calls atomic.Int32
	value := "one"
	token := "principal-one"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprintf(w, `{"login":%q}`, value) }))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	newCache := func() *CachedGitHub {
		source := managedFixture(t, server, db, &now, 100, 0)
		source.Token = func(context.Context) (string, error) { return token, nil }
		g := NewCachedGitHub(source)
		g.ConfigureUsage(db, func(context.Context) (GitHubPolicy, error) { return GitHubPolicy{RequestsPerHour: 100}, nil })
		g.clock = func() time.Time { return now }
		return g
	}
	g := newCache()
	for n := 0; n < 2; n++ {
		if v, err := g.User(ctx); err != nil || v != "one" {
			t.Fatal(v, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("normal lookup bypassed cache", calls.Load())
	}
	g = newCache()
	if v, err := g.User(ctx); err != nil || v != "one" || calls.Load() != 1 {
		t.Fatal("restart lost query cache", v, err, calls.Load())
	}
	value = "two"
	if v, err := g.User(WithGitHubRead(ctx, RequireLive)); err != nil || v != "two" || calls.Load() != 2 {
		t.Fatal("direct read reused TTL data", v, err, calls.Load())
	}
	if v, err := g.User(ctx); err != nil || v != "two" || calls.Load() != 2 {
		t.Fatal("live result did not update cache", v, err, calls.Load())
	}
	if err := g.Invalidate(ctx, "identity"); err != nil {
		t.Fatal(err)
	}
	value = "three"
	if v, err := g.User(ctx); err != nil || v != "three" || calls.Load() != 3 {
		t.Fatal("invalidated entry or underlying REST TTL was reused", v, err, calls.Load())
	}
	now = now.Add(6 * time.Minute)
	value = "four"
	if v, err := g.User(ctx); err != nil || v != "four" || calls.Load() != 4 {
		t.Fatal("expired entry reused", v, err, calls.Load())
	}
	token = "principal-two"
	value = "other"
	if v, err := g.User(ctx); err != nil || v != "other" || calls.Load() != 5 {
		t.Fatal("principal reused another scope", v, err, calls.Load())
	}
}

func TestGitHubCacheLiveFailureNeverFallsBackAndHitsDoNotWaitForNetwork(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	var fail atomic.Bool
	var block atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if block.Load() {
			close(entered)
			<-release
		}
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"login":"saved"}`)
	}))
	defer server.Close()
	g := NewCachedGitHub(&GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }})
	if _, err := g.User(ctx); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	if _, err := g.User(WithGitHubRead(ctx, RequireLive)); err == nil {
		t.Fatal("direct failure silently returned cached data")
	}
	fail.Store(false)
	block.Store(true)
	done := make(chan error, 1)
	go func() { _, err := g.User(WithGitHubRead(ctx, RequireLive)); done <- err }()
	<-entered
	lookup, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	v, err := g.User(lookup)
	close(release)
	if err != nil || v != "saved" {
		t.Fatal("cache hit waited on an unrelated live fetch", v, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("cache hit made an HTTP request", calls.Load())
	}
}

func TestGitHubCacheDirectGraphReadUpdatesSharedHTTPState(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var calls atomic.Int32
	value := "old"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprintf(w, `{"data":{"value":%q}}`, value)
	}))
	defer server.Close()
	g := managedFixture(t, server, nil, &now, 100, 0)
	var out struct{ Value string }
	if err := g.graph(ctx, "query { value }", nil, &out); err != nil {
		t.Fatal(err)
	}
	value = "new"
	if err := g.graph(ctx, "query { value }", nil, &out); err != nil || out.Value != "old" || calls.Load() != 1 {
		t.Fatal("ordinary GraphQL cache failed", out, err)
	}
	if err := g.graph(WithGitHubRead(ctx, RequireLive), "query { value }", nil, &out); err != nil || out.Value != "new" || calls.Load() != 2 {
		t.Fatal("GraphQL ignored direct mode", out, err)
	}
	if err := g.graph(ctx, "query { value }", nil, &out); err != nil || out.Value != "new" || calls.Load() != 2 {
		t.Fatal("direct GraphQL result was not retained", out, err)
	}
}

type cacheSourceFixture struct {
	GitHubSource
	calls int
	scope string
}

func (s *cacheSourceFixture) CacheScope(context.Context) (string, error) { return s.scope, nil }
func (s *cacheSourceFixture) SyncIndex(ctx context.Context, r domain.Repository, full bool) ([]domain.Item, time.Time, error) {
	s.calls++
	return []domain.Item{{ID: r.ID + "#1", Kind: "pr", SummaryOnly: true, Title: "original"}}, time.Unix(100, 0), nil
}
func (s *cacheSourceFixture) CurrentWorkItem(ctx context.Context, i domain.Item) (domain.Item, error) {
	s.calls++
	i.Title = "current"
	return i, nil
}

func (s *cacheSourceFixture) ItemDetail(ctx context.Context, i domain.Item) (domain.Item, error) {
	s.calls++
	return i, nil
}

func TestGitHubCacheNewHeadOrUpdateCannotReuseOldDetail(t *testing.T) {
	ctx := context.Background()
	source := &cacheSourceFixture{scope: "fixture"}
	g := NewCachedGitHub(source)
	i := domain.Item{ID: "owner/repo#1", Repository: "owner/repo", Number: 1, Kind: "pr", Head: "old"}
	if _, err := g.ItemDetail(ctx, i); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ItemDetail(ctx, i); err != nil || source.calls != 1 {
		t.Fatal("same detail missed cache", err)
	}
	i.Head = "new"
	if v, err := g.ItemDetail(ctx, i); err != nil || v.Head != "new" || source.calls != 2 {
		t.Fatal("changed head inherited old detail", v, err)
	}
	i.Updated = time.Now().UTC()
	i.Body = "changed scope"
	if v, err := g.ItemDetail(ctx, i); err != nil || v.Body != "changed scope" || source.calls != 3 {
		t.Fatal("changed source inherited old detail", v, err)
	}
}

func TestGitHubCachePreservesSummarySemanticsCopiesAndRelatedInvalidation(t *testing.T) {
	ctx := context.Background()
	source := &cacheSourceFixture{scope: "fixture"}
	g := NewCachedGitHub(source)
	repo := domain.Repository{ID: "owner/repo"}
	other := domain.Repository{ID: "other/repo"}
	first, _, err := g.SyncIndex(ctx, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	first[0].Title = "caller edit"
	second, observed, err := g.SyncIndex(ctx, repo, true)
	if err != nil || !second[0].SummaryOnly || second[0].Title != "original" || !observed.Equal(time.Unix(100, 0)) || source.calls != 1 {
		t.Fatal("cached summary became a full import, changed its observation time or shared mutable data", second, err)
	}
	if _, _, err := g.SyncIndex(ctx, other, true); err != nil {
		t.Fatal(err)
	}
	i := domain.Item{ID: "owner/repo#1", Repository: repo.ID, Kind: "pr", Number: 1}
	if _, err := g.CurrentWorkItem(WithGitHubRead(ctx, RequireLive), i); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.SyncIndex(ctx, repo, true); err != nil {
		t.Fatal(err)
	}
	if source.calls != 4 {
		t.Fatal("live item did not invalidate its cached repository index", source.calls)
	}
	if _, _, err := g.SyncIndex(ctx, other, true); err != nil || source.calls != 4 {
		t.Fatal("scoped invalidation expired another repository", err)
	}
	if v, err := g.CurrentWorkItem(ctx, i); err != nil || v.Title != "current" || source.calls != 4 {
		t.Fatal("direct source result was not reusable", v, err, source.calls)
	}
}
