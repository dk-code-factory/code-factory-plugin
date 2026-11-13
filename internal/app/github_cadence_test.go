package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubRepositoryCadenceSuppressesHTTPAndManualSyncRemainsLive(t *testing.T) {
	a, ctx := fixture(t)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"me"}`)
		case "/repos/owner/repo/issues", "/repos/owner/repo/pulls":
			fmt.Fprint(w, `[]`)
		default:
			t.Errorf("unexpected cadence request: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	a.GitHub = providers.NewCachedGitHub(&providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }})
	if err := a.EnableGitHubUsage(ctx); err != nil {
		t.Fatal(err)
	}
	config, err := a.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policy := config.PolicyVersion
	config.GitHubRepositoryPollSeconds = 3600
	if err = a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	config, err = a.Config(ctx)
	if err != nil || config.PolicyVersion != policy {
		t.Fatal("cadence edit changed assessment policy", config, err)
	}
	repo := domain.Repository{ID: "owner/repo", Imported: true, LastSync: time.Now().Add(-20 * time.Minute), LastFull: time.Now()}
	if err = a.Store.Put(ctx, "repository", repo.ID, repo, ""); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Put(ctx, "github-notifications", "cursor", notificationSync{LastAttempt: time.Now()}, ""); err != nil {
		t.Fatal(err)
	}
	if err = a.sync(ctx, true); err != nil || requests.Load() != 0 {
		t.Fatal("hourly cadence performed the old fifteen-minute poll", requests.Load(), err)
	}
	if err = a.Sync(ctx); err != nil || requests.Load() != 3 {
		t.Fatal("explicit repository synchronization was deferred", requests.Load(), err)
	}
	if err = a.Store.Get(ctx, "repository", repo.ID, &repo); err != nil {
		t.Fatal(err)
	}
	repo.LastSync = time.Now().Add(-61 * time.Minute)
	if err = a.Store.Put(ctx, "repository", repo.ID, repo, ""); err != nil {
		t.Fatal(err)
	}
	if err = a.sync(ctx, true); err != nil || requests.Load() != 5 {
		t.Fatal("due hourly poll did not fetch both repository lists", requests.Load(), err)
	}
}

func TestGitHubNotificationCadenceRunsWhileRepositoryScansStayDue(t *testing.T) {
	a, ctx := fixture(t)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var lists, notifications atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"me"}`)
		case "/repos/owner/repo/issues", "/repos/owner/repo/pulls":
			lists.Add(1)
			fmt.Fprint(w, `[]`)
		case "/notifications":
			notifications.Add(1)
			fmt.Fprintf(w, `[{"updated_at":%q}]`, time.Now().UTC().Format(time.RFC3339Nano))
		default:
			t.Errorf("unexpected scheduled request: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	a.GitHub = providers.NewCachedGitHub(&providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }})
	if err := a.EnableGitHubUsage(ctx); err != nil {
		t.Fatal(err)
	}
	config, err := a.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config.GitHubRepositoryPollSeconds = 60
	config.GitHubPollSeconds = 600
	if err = a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	repo := domain.Repository{ID: "owner/repo", Imported: true, LastFull: time.Now()}
	// Recreate the scan-start checkpoint left by a scan longer than its interval,
	// so every scheduled iteration has due repository work without sleeping a minute.
	makeRepositoryDue := func() {
		t.Helper()
		repo.LastSync = time.Now().Add(-61 * time.Second)
		if err := a.Store.Put(ctx, "repository", repo.ID, repo, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.Store.Put(ctx, "github-notifications", "cursor", notificationSync{LastAttempt: time.Now().Add(-11 * time.Minute)}, ""); err != nil {
		t.Fatal(err)
	}
	makeRepositoryDue()
	if err = a.sync(ctx, true); err != nil || lists.Load() != 2 || notifications.Load() != 1 {
		t.Fatal("due repository work starved overdue notifications", lists.Load(), notifications.Load(), err)
	}
	makeRepositoryDue()
	if err = a.sync(ctx, true); err != nil || lists.Load() != 4 || notifications.Load() != 1 {
		t.Fatal("repository work ignored the independent notification deadline", lists.Load(), notifications.Load(), err)
	}
	var checkpoint notificationSync
	if err = a.Store.Get(ctx, "github-notifications", "cursor", &checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint.LastAttempt = time.Now().Add(-11 * time.Minute)
	if err = a.Store.Put(ctx, "github-notifications", "cursor", checkpoint, ""); err != nil {
		t.Fatal(err)
	}
	makeRepositoryDue()
	if err = a.sync(ctx, true); err != nil || lists.Load() != 6 || notifications.Load() != 2 {
		t.Fatal("continuously due scans prevented the next notification poll", lists.Load(), notifications.Load(), err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err = a.sync(canceled, true); !errors.Is(err, context.Canceled) || lists.Load() != 6 || notifications.Load() != 2 {
		t.Fatal("canceled scheduling performed more upstream work", lists.Load(), notifications.Load(), err)
	}
}

func TestGitHubDetailCadenceSuppressesSelectedItemHTTP(t *testing.T) {
	a, ctx := fixture(t)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/repos/owner/repo/issues/1":
			fmt.Fprint(w, `{"number":1,"state":"open","title":"Current detail"}`)
		case "/repos/owner/repo/issues/1/comments", "/repos/owner/repo/issues/1/timeline":
			fmt.Fprint(w, `[]`)
		default:
			t.Errorf("unexpected detail request: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	a.GitHub = providers.NewCachedGitHub(&providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }})
	if err := a.EnableGitHubUsage(ctx); err != nil {
		t.Fatal(err)
	}
	config, err := a.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config.GitHubDetailPollSeconds = 3600
	if err = a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	item := item()
	item.DetailSyncedAt = time.Now().Add(-20 * time.Minute)
	importOne(t, a, ctx, item)
	if err = a.RefreshItemDetail(ctx, item.ID); err != nil || requests.Load() != 0 {
		t.Fatal("selected item ignored longer detail cadence", requests.Load(), err)
	}
	item.DetailSyncedAt = time.Now().Add(-61 * time.Minute)
	if err = a.Store.Put(ctx, "item", item.ID, item, ""); err != nil {
		t.Fatal(err)
	}
	if err = a.RefreshItemDetail(ctx, item.ID); err != nil || requests.Load() != 3 {
		t.Fatal("expired selected detail did not fetch source and history", requests.Load(), err)
	}
	var saved domain.Item
	if err = a.Store.Get(ctx, "item", item.ID, &saved); err != nil || saved.Title != "Current detail" || time.Since(saved.DetailSyncedAt) > time.Minute {
		t.Fatal("due detail was not saved", saved, err)
	}
}

func TestGitHubCadenceValidationPreservesSavedSettings(t *testing.T) {
	a, ctx := fixture(t)
	config, err := a.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if config.GitHubRepositoryPollInterval() != 15*time.Minute || config.GitHubDetailPollInterval() != 15*time.Minute || githubPollSeconds(config) != 120 {
		t.Fatal("legacy cadence defaults changed", config)
	}
	for _, invalid := range []int{-1, 1, 59, 86401} {
		for _, field := range []string{"repository", "detail"} {
			bad := config
			if field == "repository" {
				bad.GitHubRepositoryPollSeconds = invalid
			} else {
				bad.GitHubDetailPollSeconds = invalid
			}
			if err = a.SaveConfig(ctx, bad); err == nil {
				t.Fatalf("accepted invalid %s cadence %d", field, invalid)
			}
			current, err := a.Config(ctx)
			if err != nil || current.GitHubRepositoryPollSeconds != 0 || current.GitHubDetailPollSeconds != 0 || current.PolicyVersion != config.PolicyVersion {
				t.Fatal("invalid cadence replaced saved policy", current, err)
			}
		}
	}
}

func TestGitHubNotificationCadenceRemainsIndependent(t *testing.T) {
	a, ctx := fixture(t)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"me"}`)
		case "/notifications":
			fmt.Fprint(w, `[]`)
		default:
			t.Errorf("unexpected notification request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	if err := a.EnableGitHubUsage(ctx); err != nil {
		t.Fatal(err)
	}
	config, err := a.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config.GitHubPollSeconds = 600
	config.GitHubRepositoryPollSeconds = 3600
	config.GitHubDetailPollSeconds = 7200
	if err = a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	if err = a.SaveRepository(ctx, domain.Repository{ID: "owner/repo"}); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Put(ctx, "github-notifications", "cursor", notificationSync{LastAttempt: time.Now().Add(-3 * time.Minute)}, ""); err != nil {
		t.Fatal(err)
	}
	if err = a.SyncNotifications(ctx); err != nil || requests.Load() != 0 {
		t.Fatal("notification cadence ignored ten-minute interval", requests.Load(), err)
	}
	config.GitHubPollSeconds = 120
	if err = a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	if err = a.SyncNotifications(ctx); err != nil || requests.Load() != 2 {
		t.Fatal("repository/detail cadence delayed due notifications", requests.Load(), err)
	}
}

func TestGitHubReferencedPRCadenceSuppressesHTTPUntilDue(t *testing.T) {
	a, ctx := fixture(t)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/repos/owner/repo/pulls/1":
			fmt.Fprint(w, `{"number":1,"state":"open","title":"current referenced PR","head":{"sha":"head"}}`)
		case "/repos/owner/repo/commits/head/check-runs":
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		case "/repos/owner/repo/commits/head/statuses":
			fmt.Fprint(w, `[]`)
		case "/graphql":
			fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"headRefOid":"head","statusCheckRollup":{"state":"SUCCESS"}}}}}`)
		default:
			t.Errorf("unexpected referenced PR request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	g := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	g.ConfigureUsage(a.Store, func(ctx context.Context) (providers.GitHubPolicy, error) {
		c, err := a.Config(ctx)
		return providers.GitHubPolicy{RequestsPerHour: 100, DetailTTL: c.GitHubDetailPollInterval()}, err
	})
	a.GitHub = g
	config, err := a.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config.GitHubDetailPollSeconds = 3600
	if err = a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	item := item()
	item.Kind, item.Head = "pr", "head"
	item.PR = &domain.PullRequest{ID: item.ID, Head: item.Head, State: "open", SyncedAt: time.Now().Add(-20 * time.Minute)}
	importOne(t, a, ctx, item)
	link := domain.Relationship{ID: "manual-ref", Source: "owner/repo#2", Target: item.ID, Manual: true}
	if err = a.Store.Put(ctx, "relationship", link.ID, link, ""); err != nil {
		t.Fatal(err)
	}
	if err = a.RefreshPullRequests(ctx); err != nil || requests.Load() != 0 {
		t.Fatal("referenced PR ignored longer detail cadence", requests.Load(), err)
	}
	item.PR.SyncedAt = time.Now().Add(-61 * time.Minute)
	if err = a.Store.Put(ctx, "item", item.ID, item, ""); err != nil {
		t.Fatal(err)
	}
	if err = a.RefreshPullRequests(ctx); err != nil || requests.Load() != 4 {
		t.Fatal("due referenced PR did not refresh authoritative metadata", requests.Load(), err)
	}
	var current domain.Item
	if err = a.Store.Get(ctx, "item", item.ID, &current); err != nil || current.Title != "current referenced PR" || current.PR == nil || current.PR.CI != "success" {
		t.Fatal("referenced PR refresh not retained", current.PR, current.Title, err)
	}
}
