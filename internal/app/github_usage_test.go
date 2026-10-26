package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
)

func TestNotificationReviewHintsAreScopedReadOnlyAndRestartSafe(t *testing.T) {
	a, ctx := fixture(t)
	var requests atomic.Int32
	var deny atomic.Bool
	updated := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" {
			t.Error("notifications modified read/subscription state", r.Method)
		}
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"me"}`)
		case "/notifications":
			if r.URL.Query().Get("all") != "true" || r.URL.Query().Get("participating") != "false" || r.URL.Query().Get("since") == "" {
				t.Error("notification query lost read threads or cursor")
			}
			if deny.Load() {
				w.WriteHeader(503)
				return
			}
			fmt.Fprintf(w, `[{"reason":"review_requested","updated_at":%q,"repository":{"full_name":"owner/repo"},"subject":{"type":"PullRequest","url":%q}},{"reason":"review_requested","updated_at":%q,"repository":{"full_name":"unconfigured/repo"},"subject":{"type":"PullRequest","url":%q}}]`, updated.Format(time.RFC3339), server.URL+"/repos/owner/repo/pulls/7", updated.Format(time.RFC3339), server.URL+"/repos/unconfigured/repo/pulls/9")
		case "/repos/owner/repo/pulls/7":
			fmt.Fprint(w, `{"number":7,"title":"Requested PR","state":"open","head":{"sha":"abc"},"requested_reviewers":[{"login":"me"}]}`)
		default:
			t.Error("notification fanned out to unconfigured repository or enrichment", r.URL.Path)
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	a.GitHub.ConfigureUsage(a.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 60}, nil
	})
	if err := a.SaveRepository(ctx, domain.Repository{ID: "owner/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SyncNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := a.Inbox(ctx, "open")
	if err != nil || len(rows) != 1 || !ContainsQueue(rows[0], "review") || rows[0].Item.Head != "abc" {
		t.Fatal("notification review missing", rows, err)
	}
	var repo domain.Repository
	_ = a.Store.Get(ctx, "repository", "owner/repo", &repo)
	if !repo.LastSync.IsZero() {
		t.Fatal("notification advanced repository checkpoint")
	}
	var cursor notificationSync
	_ = a.Store.Get(ctx, "github-notifications", "cursor", &cursor)
	if !cursor.Since.Equal(updated) {
		t.Fatal("notification cursor did not advance", cursor)
	}
	before := requests.Load()
	if err := a.SyncNotifications(ctx); err != nil || requests.Load() != before {
		t.Fatal("repeated poll ignored cadence", err)
	}
	cursor.LastAttempt = time.Now().Add(-time.Hour)
	_ = a.Store.Put(ctx, "github-notifications", "cursor", cursor, "")
	before = requests.Load()
	if err := a.SyncNotifications(ctx); err != nil || requests.Load() != before+1 {
		t.Fatal("unchanged notification refetched its PR", requests.Load()-before, err)
	}
	cursor.LastAttempt = time.Now().Add(-time.Hour)
	_ = a.Store.Put(ctx, "github-notifications", "cursor", cursor, "")
	deny.Store(true)
	if err := a.SyncNotifications(ctx); err == nil {
		t.Fatal("failed notifications succeeded")
	}
	var failed notificationSync
	_ = a.Store.Get(ctx, "github-notifications", "cursor", &failed)
	if !failed.Since.Equal(cursor.Since) {
		t.Fatal("failed notification advanced cursor")
	}
	runs, _ := store.List[domain.Run](ctx, a.Store, "run")
	if len(runs) != 0 {
		t.Fatal("queue-only notification started work")
	}
}
func TestIndexPreservesHistoryInvalidatesChangedHeadAndBudgetEditsKeepAssessmentPolicy(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	i.Kind = "pr"
	i.Head = "old"
	i.Activity = []domain.Activity{{ID: "comment:1", Body: "retained"}}
	i.DetailSyncedAt = time.Now()
	i.PR = &domain.PullRequest{Head: i.Head, CI: "success", ReviewDecision: "approved", SyncedAt: time.Now()}
	importOne(t, a, ctx, i)
	summary := i
	summary.SummaryOnly = true
	summary.Activity = nil
	summary.PR = nil
	summary.Head = "new"
	summary.ReviewRequests = []string{"me"}
	var repo domain.Repository
	_ = a.Store.Get(ctx, "repository", i.Repository, &repo)
	if err := a.Import(ctx, repo, []domain.Item{summary}, "me", time.Now(), false); err != nil {
		t.Fatal(err)
	}
	var saved domain.Item
	_ = a.Store.Get(ctx, "item", i.ID, &saved)
	if len(saved.Activity) != 1 || saved.Activity[0].Body != "retained" || saved.PR.CI != "unknown" || saved.PR.Head != "new" || !saved.DetailSyncedAt.IsZero() {
		t.Fatal("cheap index lost history or reused old-head status", saved)
	}
	c, _ := a.Config(ctx)
	before := c.PolicyVersion
	c.GitHubRequestsPerHour = 120
	c.GitHubPollSeconds = 300
	if err := a.SaveConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	c, _ = a.Config(ctx)
	if c.PolicyVersion != before {
		t.Fatal("budget-only edit invalidated all model assessments")
	}
}

func TestIncidentalReferencesDoNotScanUntilSelectedAndErrorsBackOff(t *testing.T) {
	a, ctx := fixture(t)
	source := item()
	source.Body = ""
	importOne(t, a, ctx, source)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(404) }))
	defer server.Close()
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	link := domain.Relationship{ID: "discovered", Source: source.ID, Target: "external/repo#2", Evidence: "https://github.com/external/repo/pull/2"}
	if err := a.Store.Put(ctx, "relationship", link.ID, link, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.RefreshPullRequests(ctx); err != nil || calls.Load() != 0 {
		t.Fatal("incidental cross-reference scanned GitHub", calls.Load(), err)
	}
	if err := a.Store.Put(ctx, "github-detail-focus", source.ID, detailFocus{ID: source.ID, SeenAt: time.Now()}, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.RefreshPullRequests(ctx); err != nil || calls.Load() != 1 {
		t.Fatal("selected source did not refresh linked PR", calls.Load(), err)
	}
	if err := a.RefreshPullRequests(ctx); err != nil || calls.Load() != 1 {
		t.Fatal("inaccessible reference ignored cooldown", calls.Load(), err)
	}
	repos, _ := store.List[domain.Repository](ctx, a.Store, "repository")
	if len(repos) != 1 || repos[0].ID != source.Repository {
		t.Fatal("reference added repository monitoring")
	}
}
func TestInvalidAPIBudgetCannotOverwriteSavedPolicy(t *testing.T) {
	a, ctx := fixture(t)
	c, _ := a.Config(ctx)
	c.GitHubRequestsPerHour = 120
	c.GitHubPollSeconds = 300
	if err := a.SaveConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	before, _ := a.Config(ctx)
	for _, budget := range []int{-1, 59, 5001} {
		bad := before
		bad.GitHubRequestsPerHour = budget
		if err := a.SaveConfig(ctx, bad); err == nil {
			t.Fatal("invalid budget accepted", budget)
		}
	}
	bad := before
	reserve := -1
	bad.GitHubReserve = &reserve
	if err := a.SaveConfig(ctx, bad); err == nil {
		t.Fatal("negative reserve accepted")
	}
	after, _ := a.Config(ctx)
	if after.GitHubRequestsPerHour != before.GitHubRequestsPerHour || after.GitHubPollSeconds != before.GitHubPollSeconds || after.PolicyVersion != before.PolicyVersion {
		t.Fatal("rejected limits changed saved policy")
	}
}

func TestCachedPRObservationCannotBorrowFreshRepositoryAuthority(t *testing.T) {
	repo := domain.Repository{LastSync: time.Now()}
	i := domain.Item{ReviewObservedAt: time.Now().Add(-2 * time.Minute)}
	if reviewFresh(domain.Defaults(), repo, i) {
		t.Fatal("old cached PR borrowed a new repository checkpoint")
	}
	i.ReviewObservedAt = time.Now()
	if !reviewFresh(domain.Defaults(), repo, i) {
		t.Fatal("fresh PR observation was rejected")
	}
}
