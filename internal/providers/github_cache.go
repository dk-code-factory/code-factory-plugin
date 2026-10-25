package providers

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type githubQueryEntry struct {
	ID        string          `json:"id"`
	Scope     string          `json:"scope"`
	Resource  string          `json:"resource"`
	Body      json.RawMessage `json:"body"`
	Size      int             `json:"size"`
	Until     time.Time       `json:"until"`
	CheckedAt time.Time       `json:"checkedAt"`
}

// CachedGitHub decorates the complete application read API. The underlying live
// adapter retains its HTTP validators and serialized request-budget accounting.
type CachedGitHub struct {
	GitHubSource
	mu      sync.Mutex
	entries map[string]githubQueryEntry
	state   *store.Store
	fetch   chan struct{}
	clock   func() time.Time
}

func NewCachedGitHub(source GitHubSource) *CachedGitHub {
	return &CachedGitHub{GitHubSource: source, entries: map[string]githubQueryEntry{}, fetch: make(chan struct{}, 1), clock: time.Now}
}

func (g *CachedGitHub) ConfigureUsage(s *store.Store, policy func(context.Context) (GitHubPolicy, error)) {
	g.GitHubSource.ConfigureUsage(s, policy)
	g.mu.Lock()
	g.state = s
	g.entries = map[string]githubQueryEntry{}
	g.mu.Unlock()
}

func (g *CachedGitHub) cachedQuery(ctx context.Context, id string, out any) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.entries[id]
	if !ok && g.state != nil {
		err := g.state.Get(ctx, "github-query-cache", id, &e)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return false, err
		}
		ok = err == nil
	}
	if !ok || !e.Until.After(g.clock()) {
		return false, nil
	}
	if err := json.Unmarshal(e.Body, out); err != nil {
		return false, fmt.Errorf("GitHub query cache is invalid")
	}
	noteRead(ctx, e.CheckedAt)
	return true, nil
}

func (g *CachedGitHub) rememberQuery(ctx context.Context, e githubQueryEntry) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Limits cover all credential scopes. Eviction affects cached copies only.
	entries := []githubQueryEntry{}
	if g.state != nil {
		var err error
		entries, err = store.ListWithout[githubQueryEntry](ctx, g.state, "github-query-cache", "$.body")
		if err != nil {
			return err
		}
	} else {
		for _, v := range g.entries {
			entries = append(entries, v)
		}
	}
	total, count := e.Size, 1
	for _, v := range entries {
		if v.ID != e.ID {
			total += v.Size
			count++
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].CheckedAt.Before(entries[j].CheckedAt) })
	remove := []string{}
	for _, v := range entries {
		if total <= 64<<20 && count <= 4096 {
			break
		}
		if v.ID == e.ID {
			continue
		}
		total -= v.Size
		count--
		remove = append(remove, v.ID)
	}
	if g.state != nil {
		if err := g.state.Write(ctx, func(tx *store.Tx) error {
			for _, id := range remove {
				if err := tx.Delete("github-query-cache", id); err != nil {
					return err
				}
			}
			return tx.Put("github-query-cache", e.ID, e)
		}); err != nil {
			return err
		}
	}
	for _, id := range remove {
		delete(g.entries, id)
	}
	// Persistent storage supplies cache hits without retaining a second full copy.
	if g.state == nil {
		g.entries[e.ID] = e
	}
	return nil
}

func (g *CachedGitHub) query(ctx context.Context, name, resource string, args, out any, ttl time.Duration, load func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	scope, err := g.CacheScope(ctx)
	if err != nil {
		return err
	}
	b, err := json.Marshal(args)
	if err != nil {
		return err
	}
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(scope+"\x00"+name+"\x00"+string(b))))
	direct := ctx.Value(requireFreshRead{}) == true
	if !direct {
		if hit, err := g.cachedQuery(ctx, id, out); err != nil || hit {
			return err
		}
	}
	// Cache hits remain available while a different query waits on GitHub.
	select {
	case g.fetch <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-g.fetch }()
	if !direct {
		if hit, err := g.cachedQuery(ctx, id, out); err != nil || hit {
			return err
		}
	}
	if err := load(WithGitHubRead(ctx, RequireLive)); err != nil {
		return err
	}
	currentScope, err := g.CacheScope(ctx)
	if err != nil {
		return err
	}
	if currentScope != scope {
		return fmt.Errorf("GitHub identity changed during the query; refresh before reading again")
	}
	// Enrichment may return partial data with its own visible error. Do not
	// promote that incomplete observation into a successful cached result.
	switch v := out.(type) {
	case *domain.Item:
		if v.PR != nil && v.PR.Error != "" {
			return nil
		}
	case *[]domain.Item:
		for _, i := range *v {
			if i.PR != nil && i.PR.Error != "" {
				return nil
			}
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if direct {
		if err := g.invalidateScope(ctx, scope, resource); err != nil {
			return err
		}
	}
	// Composite reads can exceed the per-entry bound; return their live data
	// without making an unbounded persistent or memory allocation for the cache.
	if len(body) > 64<<20 {
		return nil
	}
	now := g.clock().UTC()
	return g.rememberQuery(ctx, githubQueryEntry{ID: id, Scope: scope, Resource: resource, Body: body, Size: len(body), CheckedAt: now, Until: now.Add(ttl)})
}

// Invalidate removes this principal's cached queries for a repository/item.
// An item invalidation also expires repository indexes; other repositories stay.
// Empty resource explicitly invalidates all queries in this credential scope.
func (g *CachedGitHub) Invalidate(ctx context.Context, resource string) error {
	scope, err := g.CacheScope(ctx)
	if err != nil {
		return err
	}
	select {
	case g.fetch <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-g.fetch }()
	return g.invalidateScope(ctx, scope, resource)
}

func (g *CachedGitHub) invalidateScope(ctx context.Context, scope, resource string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	entries := []githubQueryEntry{}
	if g.state != nil {
		var err error
		entries, err = store.ListWithout[githubQueryEntry](ctx, g.state, "github-query-cache", "$.body")
		if err != nil {
			return err
		}
	} else {
		for _, e := range g.entries {
			entries = append(entries, e)
		}
	}
	repo := strings.SplitN(resource, "#", 2)[0]
	ids := []string{}
	for _, e := range entries {
		if e.Scope == scope && (resource == "" || e.Resource == resource || e.Resource == repo || !strings.Contains(resource, "#") && strings.HasPrefix(e.Resource, repo+"#")) {
			ids = append(ids, e.ID)
		}
	}
	if g.state != nil {
		if err := g.state.Write(ctx, func(tx *store.Tx) error {
			for _, id := range ids {
				if err := tx.Delete("github-query-cache", id); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, id := range ids {
		delete(g.entries, id)
	}
	return nil
}

func (g *CachedGitHub) User(ctx context.Context) (string, error) {
	var v string
	err := g.query(ctx, "user", "identity", nil, &v, 5*time.Minute, func(ctx context.Context) (err error) { v, err = g.GitHubSource.User(ctx); return })
	return v, err
}
func (g *CachedGitHub) Teams(ctx context.Context) ([]string, error) {
	var v []string
	err := g.query(ctx, "teams", "identity", nil, &v, 15*time.Minute, func(ctx context.Context) (err error) { v, err = g.GitHubSource.Teams(ctx); return })
	return v, err
}
func (g *CachedGitHub) Sync(ctx context.Context, r domain.Repository, full bool) ([]domain.Item, error) {
	var v []domain.Item
	err := g.query(ctx, "sync", r.ID, []any{r, full}, &v, time.Minute, func(ctx context.Context) (err error) { v, err = g.GitHubSource.Sync(ctx, r, full); return })
	return v, err
}
func (g *CachedGitHub) SyncIndex(ctx context.Context, r domain.Repository, full bool) ([]domain.Item, time.Time, error) {
	var v struct {
		Items    []domain.Item
		Observed time.Time
	}
	err := g.query(ctx, "index", r.ID, []any{r, full}, &v, time.Minute, func(ctx context.Context) (err error) {
		v.Items, v.Observed, err = g.GitHubSource.SyncIndex(ctx, r, full)
		return
	})
	for i := range v.Items {
		v.Items[i].SummaryOnly = true
	}
	return v.Items, v.Observed, err
}
func (g *CachedGitHub) ItemDetail(ctx context.Context, i domain.Item) (domain.Item, error) {
	var v domain.Item
	err := g.query(ctx, "detail", i.ID, []any{i.ID, i.Repository, i.Number, i.Kind, i.Head, i.Updated}, &v, GitHubDetailTTL, func(ctx context.Context) (err error) { v, err = g.GitHubSource.ItemDetail(ctx, i); return })
	return v, err
}
func (g *CachedGitHub) CurrentWorkItem(ctx context.Context, i domain.Item) (domain.Item, error) {
	var v domain.Item
	err := g.query(ctx, "current", i.ID, []any{i.ID, i.Repository, i.Kind, i.Number}, &v, time.Minute, func(ctx context.Context) (err error) { v, err = g.GitHubSource.CurrentWorkItem(ctx, i); return })
	return v, err
}
func (g *CachedGitHub) PullRequest(ctx context.Context, repo string, number int) (domain.Item, error) {
	var v domain.Item
	err := g.query(ctx, "pull", fmt.Sprintf("%s#%d", repo, number), []any{repo, number}, &v, GitHubDetailTTL, func(ctx context.Context) (err error) { v, err = g.GitHubSource.PullRequest(ctx, repo, number); return })
	return v, err
}
func (g *CachedGitHub) ReviewRequest(ctx context.Context, repo string, number int) (domain.Item, error) {
	var v domain.Item
	err := g.query(ctx, "review-request", fmt.Sprintf("%s#%d", repo, number), []any{repo, number}, &v, time.Minute, func(ctx context.Context) (err error) {
		v, err = g.GitHubSource.ReviewRequest(ctx, repo, number)
		return
	})
	return v, err
}
func (g *CachedGitHub) PublishedReviews(ctx context.Context, i domain.Item) ([]domain.PublishedReview, time.Time, error) {
	var v struct {
		Reviews  []domain.PublishedReview
		Observed time.Time
	}
	err := g.query(ctx, "reviews", i.ID, []any{i.ID, i.Head}, &v, time.Minute, func(ctx context.Context) (err error) {
		v.Reviews, v.Observed, err = g.GitHubSource.PublishedReviews(ctx, i)
		return
	})
	return v.Reviews, v.Observed, err
}
func (g *CachedGitHub) ReviewNotifications(ctx context.Context, since time.Time) ([]ReviewNotification, time.Time, error) {
	var v struct {
		Notices []ReviewNotification
		Next    time.Time
	}
	err := g.query(ctx, "notifications", "notifications", since, &v, 2*time.Minute, func(ctx context.Context) (err error) {
		v.Notices, v.Next, err = g.GitHubSource.ReviewNotifications(ctx, since)
		return
	})
	return v.Notices, v.Next, err
}
