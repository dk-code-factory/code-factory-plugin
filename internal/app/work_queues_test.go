package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInboxDetectsPullRequestsInBothDirectionsAndExternalEvidence(t *testing.T) {
	a, ctx := fixture(t)
	issue := item()
	issue.Body = ""
	issue.Revision = issue.ContentRevision()
	importOne(t, a, ctx, issue)
	pr := issue
	pr.ID = "owner/repo#2"
	pr.Number = 2
	pr.Kind = "pr"
	pr.Revision = pr.ContentRevision()
	importOne(t, a, ctx, pr)
	for _, link := range []domain.Relationship{
		{ID: "forward", Source: issue.ID, Target: pr.ID, Manual: true},
		{ID: "reverse", Source: pr.ID, Target: issue.ID, Manual: true},
		{ID: "external", Source: issue.ID, Target: "external/repo#8", Evidence: "https://github.com/external/repo/pull/8", Manual: true},
	} {
		if err := a.Link(ctx, link, false); err != nil {
			t.Fatal(err)
		}
		rows, err := a.Inbox(ctx, "all")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.Item.ID == issue.ID {
				found = row.HasPullRequest
			}
		}
		if !found {
			t.Fatalf("link %s did not mark issue", link.ID)
		}
		if err := a.Link(ctx, link, true); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := a.Inbox(ctx, "all")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Item.ID == issue.ID && row.HasPullRequest {
			t.Fatal("removed links left stale issue membership")
		}
	}
}

func TestSyncPublishesAuthenticatedViewerAndTeamFailures(t *testing.T) {
	a, ctx := fixture(t)
	var denyTeams atomic.Bool
	var changedAccount atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			if changedAccount.Load() {
				fmt.Fprint(w, `{"login":"other"}`)
			} else {
				fmt.Fprint(w, `{"login":"me"}`)
			}
		case "/user/teams":
			if denyTeams.Load() {
				w.WriteHeader(403)
			} else {
				fmt.Fprint(w, `[{"slug":"core","organization":{"login":"org"}}]`)
			}
		case "/repos/org/repo/pulls":
			fmt.Fprint(w, `[{"number":1,"title":"Review","state":"open","updated_at":"2026-10-21T00:00:00Z","user":{"login":"author"}}]`)
		case "/repos/org/repo/pulls/1":
			fmt.Fprint(w, `{"head":{"sha":"abc","ref":"feature"},"requested_teams":[{"slug":"core"}]}`)
		case "/repos/org/repo/commits/abc/check-runs":
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	if err := a.SaveRepository(ctx, domain.Repository{ID: "org/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var viewer domain.GitHubViewer
	if err := a.Store.Get(ctx, "identity", "github", &viewer); err != nil {
		t.Fatal(err)
	}
	if viewer.Login != "me" || len(viewer.Teams) != 1 || viewer.Teams[0] != "org/core" || viewer.TeamError != "" {
		t.Fatal(viewer)
	}
	denyTeams.Store(true)
	if err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "identity", "github", &viewer); err != nil {
		t.Fatal(err)
	}
	if len(viewer.Teams) != 0 || viewer.TeamError == "" {
		t.Fatal("stale team membership or missing permission feedback", viewer)
	}
	changedAccount.Store(true)
	if err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "identity", "github", &viewer); err != nil {
		t.Fatal(err)
	}
	if viewer.Login != "other" || len(viewer.Teams) != 0 {
		t.Fatal("identity reused previous account", viewer)
	}
}

func TestPausedRepositorySkipsProviderPollingAndRetainsPolicy(t *testing.T) {
	a, ctx := fixture(t)
	var repoCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			fmt.Fprint(w, `{"login":"me"}`)
			return
		}
		repoCalls.Add(1)
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	repo := domain.Repository{ID: "org/repo", Paused: true, ReviewProfileID: "review", ReviewPrompt: "Check API compatibility"}
	if err := a.SaveRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if repoCalls.Load() != 0 {
		t.Fatal("paused repository was polled")
	}
	var retained domain.Repository
	_ = a.Store.Get(ctx, "repository", repo.ID, &retained)
	if !retained.Paused || retained.ReviewPrompt != repo.ReviewPrompt || !retained.LastSync.IsZero() {
		t.Fatal("paused repository policy or checkpoint changed")
	}
}

func TestFailedPollCannotOverwriteConcurrentRepositoryPolicyChanges(t *testing.T) {
	a, ctx := fixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			fmt.Fprint(w, `{"login":"me"}`)
			return
		}
		if r.URL.Path == "/repos/org/repo/issues" {
			enteredOnce.Do(func() { close(entered) })
			<-release
			w.WriteHeader(403)
			fmt.Fprint(w, `{"message":"Access denied"}`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	defer releaseOnce.Do(func() { close(release) })
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	repo := domain.Repository{ID: "org/repo"}
	if err := a.SaveRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Sync(ctx) }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal("provider poll exited before reaching fixture", err)
	case <-time.After(8 * time.Second):
		t.Fatal("provider poll did not reach fixture")
	}
	v := true
	repo.AutoReview = &v
	repo.Paused = true
	repo.ReviewPrompt = "Owner changed monitoring while the old poll was in flight."
	if err := a.SaveRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("synchronization did not finish after fixture release")
	}
	var current domain.Repository
	_ = a.Store.Get(ctx, "repository", repo.ID, &current)
	if !current.Paused || current.AutoReview == nil || !*current.AutoReview || current.ReviewPrompt != repo.ReviewPrompt || current.Error == "" || !current.LastSync.IsZero() {
		t.Fatal("failed poll overwrote owner policy or checkpoint", current)
	}
}

func TestSyncImportsOtherRepositoryWhileOneIsBlocked(t *testing.T) {
	a, ctx := fixture(t)
	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"me"}`)
		case "/repos/a/slow/issues":
			close(blocked)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `[]`)
		case "/repos/z/fast/pulls":
			fmt.Fprint(w, `[{"number":7,"state":"open","user":{"login":"other"}}]`)
		case "/repos/z/fast/pulls/7":
			fmt.Fprint(w, `{"head":{"sha":"abc","ref":"feature"},"requested_reviewers":[{"login":"me"}]}`)
		case "/repos/z/fast/commits/abc/check-runs":
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	defer once.Do(func() { close(release) })
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	for _, id := range []string{"a/slow", "z/fast"} {
		if err := a.SaveRepository(ctx, domain.Repository{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- a.Sync(ctx) }()
	select {
	case <-blocked:
	case err := <-done:
		t.Fatal("slow sync exited before reaching fixture", err)
	case <-time.After(5 * time.Second):
		t.Fatal("slow sync did not enter")
	}
	waitFor(t, func() bool {
		entries, err := a.Inbox(ctx, "open")
		if err != nil {
			return false
		}
		for _, e := range entries {
			if e.Item.ID == "z/fast#7" && ContainsQueue(e, "review") {
				return true
			}
		}
		return false
	})
	var slow domain.Repository
	if err := a.Store.Get(ctx, "repository", "a/slow", &slow); err != nil || slow.Imported {
		t.Fatal("blocked checkpoint advanced", err)
	}
	once.Do(func() { close(release) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("synchronization did not finish after fixture release")
	}
}

func TestReferencePollingCancellationYieldsAndRotatesWithoutFalseErrors(t *testing.T) {
	a, ctx := fixture(t)
	var block atomic.Bool
	block.Store(true)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var mu sync.Mutex
	order := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			mu.Lock()
			order = append(order, r.URL.Path)
			mu.Unlock()
			if strings.HasSuffix(r.URL.Path, "/1") && block.Load() {
				select {
				case entered <- struct{}{}:
				case <-release:
					return
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
				return
			}
			n := "1"
			if strings.HasSuffix(r.URL.Path, "/2") {
				n = "2"
			}
			fmt.Fprintf(w, `{"number":%s,"state":"open","head":{"sha":"abc"}}`, n)
		} else if strings.HasSuffix(r.URL.Path, "/check-runs") {
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		} else {
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	defer close(release)
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	old := domain.Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "pr", State: "open", PR: &domain.PullRequest{ID: "o/r#1", Head: "retained", SyncedAt: time.Now().Add(-time.Hour)}}
	if err := a.Store.Put(ctx, "linked-pr", old.ID, old, ""); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		link := domain.Relationship{Manual: true, ID: fmt.Sprint(n), Source: "owner/repo#1", Target: fmt.Sprintf("o/r#%d", n), Evidence: fmt.Sprintf("https://github.com/o/r/pull/%d", n)}
		if err := a.Store.Put(ctx, "relationship", link.ID, link, ""); err != nil {
			t.Fatal(err)
		}
	}
	limited, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.RefreshPullRequests(limited) }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal("reference fetch exited before entering", err)
	case <-time.After(5 * time.Second):
		t.Fatal("reference fetch did not enter")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal("cancellation did not yield", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reference fetch did not finish after cancellation")
	}
	var retained domain.Item
	if err := a.Store.Get(ctx, "linked-pr", old.ID, &retained); err != nil || retained.PR.Head != "retained" || retained.PR.Error != "" {
		t.Fatal("budget cancellation replaced successful metadata", err)
	}
	block.Store(false)
	if err := a.RefreshPullRequests(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 || order[1] != "/repos/o/r/pulls/2" {
		t.Fatal("next pass did not advance past blocked reference", order)
	}
}
