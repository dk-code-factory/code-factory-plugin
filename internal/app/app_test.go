package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T) (*App, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	s, e := store.Open(filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	v, e := store.NewVault(s, dir)
	if e != nil {
		t.Fatal(e)
	}
	a := &App{Store: s, Vault: v, DataDir: dir, GitHub: providers.NewGitHub()}

	if e = a.InitCore(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cancel(); s.Close() })
	return a, ctx
}
func item() domain.Item {
	v := domain.Item{ID: "owner/repo#1", Repository: "owner/repo", Kind: "issue", Number: 1, Title: "Regression", Body: "See #2 and https://github.com/acme/api/pull/7 and APP-42", State: "open", Updated: time.Now().UTC(), Labels: []string{}, Activity: []domain.Activity{}}
	v.Revision = v.ContentRevision()
	return v
}
func importOne(t *testing.T, a *App, ctx context.Context, v domain.Item) {
	t.Helper()
	var r domain.Repository
	e := a.Store.Get(ctx, "repository", v.Repository, &r)
	if errors.Is(e, store.ErrNotFound) {
		r = domain.Repository{ID: v.Repository}
		if e = a.Store.Put(ctx, "repository", r.ID, r, ""); e != nil {
			t.Fatal(e)
		}
	} else if e != nil {
		t.Fatal(e)
	}
	if e = a.Import(ctx, r, []domain.Item{v}, "me", time.Now().UTC(), true); e != nil {
		t.Fatal(e)
	}
}
func TestInboxRevisionStatesPriorityAndLinks(t *testing.T) {
	a, ctx := fixture(t)
	v := item()
	importOne(t, a, ctx, v)
	entries, e := a.Inbox(ctx, "open")
	if e != nil {
		t.Fatal(e)
	}
	if len(entries) != 1 || !entries[0].Unread || entries[0].User.New {
		t.Fatal("first import must be unread baseline")
	}
	if e = a.UserAction(ctx, v.ID, "read", v.Revision, nil); e != nil {
		t.Fatal(e)
	}
	if e = a.UserAction(ctx, v.ID, "acted", "", nil); e != nil {
		t.Fatal(e)
	}
	entries, _ = a.Inbox(ctx, "open")
	if entries[0].Unread || len(entries[0].User.Actions) != 1 {
		t.Fatal("read or action state wrong")
	}
	old := v.Revision
	v.Body += " changed"
	v.Activity = append(v.Activity, domain.Activity{ID: "comment:1", Kind: "comment", Author: "me", Body: "worked on it"})
	v.Revision = v.ContentRevision()
	importOne(t, a, ctx, v)
	if e = a.UserAction(ctx, v.ID, "read", old, nil); e != nil {
		t.Fatal(e)
	}
	entries, _ = a.Inbox(ctx, "open")
	if !entries[0].Unread || len(entries[0].User.Actions) != 2 {
		t.Fatal("stale read suppressed new revision or action history missing")
	}
	v2 := v
	v2.ID = "owner/repo#3"
	v2.Number = 3
	v2.Revision = v2.ContentRevision()
	importOne(t, a, ctx, v2)
	entries, _ = a.Inbox(ctx, "open")
	var newer domain.InboxEntry
	for _, x := range entries {
		if x.Item.ID == v2.ID {
			newer = x
		}
	}
	if !newer.User.New {
		t.Fatal("subsequent creation missing new badge")
	}
	as := domain.Assessment{ID: v.ID, Revision: v.Revision, Policy: "1", Context: a.AssessmentContext(ctx, v), Status: "complete", Priority: 2, Rationale: "test", Confidence: .8}
	if e = a.Store.Put(ctx, "assessment", v.ID, as, ""); e != nil {
		t.Fatal(e)
	}
	entries, _ = a.Inbox(ctx, "open")
	if entries[0].PrioritySource != "inference" {
		t.Fatal("inference not resolved")
	}
	v.Labels = []string{"P1", "P0"}
	v.Revision = v.ContentRevision()
	importOne(t, a, ctx, v)
	p := 3
	if e = a.UserAction(ctx, v.ID, "priority", "", &p); e != nil {
		t.Fatal(e)
	}
	entries, _ = a.Inbox(ctx, "open")
	for _, x := range entries {
		if x.Item.ID == v.ID && (*x.Priority != 3 || x.PrioritySource != "override") {
			t.Fatal("override precedence")
		}
	}
	if e = a.UserAction(ctx, v.ID, "priority", "", nil); e != nil {
		t.Fatal(e)
	}
	entries, _ = a.Inbox(ctx, "open")
	if entries[0].Item.ID != v.ID || *entries[0].Priority != 0 {
		t.Fatal("label precedence")
	}
	links, _ := store.List[domain.Relationship](ctx, a.Store, "relationship")
	if len(links) < 3 {
		t.Fatal("references missing", links)
	}
	l := domain.Relationship{Source: v.ID, Target: "APP-42", Evidence: "my relationship"}
	if e = a.Link(ctx, l, false); e != nil {
		t.Fatal(e)
	}
	if e = a.Link(ctx, l, true); e != nil {
		t.Fatal(e)
	}
	links, _ = store.List[domain.Relationship](ctx, a.Store, "relationship")
	found := false
	for _, l := range links {
		found = found || l.Target == "APP-42" && !l.Manual
	}
	if !found {
		t.Fatal("local unlink deleted discovered evidence")
	}
}
func TestScheduledSyncRecoversCredentialsAndFindsNewItems(t *testing.T) {
	a, ctx := fixture(t)
	var revoked atomic.Bool
	revoked.Store(true)
	var number atomic.Int32
	number.Store(1)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			if revoked.Load() {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"login":"me"}`)
		case "/repos/owner/repo/issues":
			fmt.Fprintf(w, `[{"number":%d,"title":"Scheduled item","state":"open","updated_at":"2026-10-19T00:00:00Z","user":{"login":"alex"}}]`, number.Load())
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer httpServer.Close()
	a.GitHub = &providers.GitHub{BaseURL: httpServer.URL, Client: httpServer.Client(), Token: func(context.Context) (string, error) { return "test-token", nil }}
	if e := a.SaveRepository(ctx, domain.Repository{ID: "owner/repo"}); e != nil {
		t.Fatal(e)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	ticks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() { defer close(done); a.syncLoop(loopCtx, ticks) }()
	defer func() { cancel(); <-done }()
	var repo domain.Repository
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "repository", "owner/repo", &repo); return repo.Error != "" })
	if repo.Imported || !repo.LastSync.IsZero() {
		t.Fatal("revocation advanced checkpoint")
	}
	revoked.Store(false)
	repo.RetryAt = time.Now().Add(-time.Second)
	if err := a.Store.Put(ctx, "repository", repo.ID, repo, ""); err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "repository", "owner/repo", &repo)
		return repo.Imported && repo.Error == ""
	})
	entries, e := a.Inbox(ctx, "open")
	if e != nil || len(entries) != 1 || entries[0].User.New || !entries[0].Unread {
		t.Fatal("baseline state", entries, e)
	}
	number.Store(2)
	repo.LastSync = time.Now().Add(-16 * time.Minute)
	if err := a.Store.Put(ctx, "repository", repo.ID, repo, ""); err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	waitFor(t, func() bool { entries, _ = a.Inbox(ctx, "open"); return len(entries) == 2 })
	found := false
	for _, entry := range entries {
		if entry.Item.Number == 2 {
			found = entry.User.New
		}
	}
	if !found || SyncInterval != 30*time.Second {
		t.Fatal("scheduled creation or required interval missing")
	}
}

func TestSharedGitHubCooldownPreservesInboxCheckpoints(t *testing.T) {
	for _, limitedPath := range []string{"/user", "/repos/owner/a/issues"} {
		t.Run(limitedPath, func(t *testing.T) {
			a, ctx := fixture(t)
			prior := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
			for _, id := range []string{"owner/a", "owner/b"} {
				if e := a.Store.Put(ctx, "repository", id, domain.Repository{ID: id, Imported: true, LastSync: prior, LastFull: prior}, ""); e != nil {
					t.Fatal(e)
				}
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == limitedPath {
					w.Header().Set("Retry-After", "120")
					w.WriteHeader(429)
					return
				}
				if r.URL.Path == "/user" {
					fmt.Fprint(w, `{"login":"owner"}`)
					return
				}
				fmt.Fprint(w, `[]`)
			}))
			defer server.Close()
			tokens := 0
			a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { tokens++; return "token", nil }}
			a.GitHub.ConfigureUsage(a.Store, func(context.Context) (providers.GitHubPolicy, error) {
				return providers.GitHubPolicy{RequestsPerHour: 100}, nil
			})
			_ = a.Sync(ctx)
			for _, id := range []string{"owner/a", "owner/b"} {
				var r domain.Repository
				if e := a.Store.Get(ctx, "repository", id, &r); e != nil {
					t.Fatal(e)
				}
				if !r.Imported || !r.LastSync.Equal(prior) || !r.LastFull.Equal(prior) {
					t.Fatal("limited/skipped sync advanced checkpoint", r)
				}
				if id == "owner/a" && (r.Error == "" || r.RetryAt.IsZero()) {
					t.Fatal("rate limit lost repository diagnostics", r)
				}
			}
			want := 1
			if limitedPath != "/user" {
				want = 2
			}
			if calls != want || tokens != 1 {
				t.Fatal("rate limit did not stop batch", calls, tokens)
			}
			_ = a.Sync(ctx)
			if calls != want || tokens != 2 {
				t.Fatal("shared cooldown allowed another dispatch", calls, tokens)
			}
		})
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
