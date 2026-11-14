package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
)

type retryGitHub struct {
	providers.GitHubSource
	until                  time.Time
	fail                   bool
	teamFail               bool
	reviewTeams            []string
	teams                  int
	indexes, notifications int
}

func (g *retryGitHub) Managed() bool                        { return true }
func (g *retryGitHub) User(context.Context) (string, error) { return "me", nil }
func (g *retryGitHub) SyncIndex(_ context.Context, r domain.Repository, _ bool) ([]domain.Item, time.Time, error) {
	g.indexes++
	if g.fail {
		return nil, time.Time{}, &providers.RateLimitError{Until: g.until, Reason: "GitHub secondary request limit reached"}
	}
	i := item()
	i.Repository, i.ID, i.Title = r.ID, r.ID+"#1", "Recovered source"
	i.ReviewTeams = g.reviewTeams
	return []domain.Item{i}, time.Now().UTC(), nil
}
func (g *retryGitHub) Teams(context.Context) ([]string, error) {
	g.teams++
	if g.teamFail {
		return nil, &providers.RateLimitError{Until: g.until, Reason: "GitHub secondary request limit reached"}
	}
	return g.reviewTeams, nil
}
func (g *retryGitHub) ReviewNotifications(_ context.Context, since time.Time) ([]providers.ReviewNotification, time.Time, error) {
	g.notifications++
	if g.fail {
		return nil, since, &providers.RateLimitError{Until: g.until, Reason: "GitHub secondary request limit reached"}
	}
	return nil, since, nil
}

func TestScanThrottleReturnsWaitingPreservesDataAndRetriesRecentRepository(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	importOne(t, a, ctx, i)
	var before domain.Repository
	if err := a.Store.Get(ctx, "repository", i.Repository, &before); err != nil {
		t.Fatal(err)
	}
	g := &retryGitHub{GitHubSource: a.GitHub, until: time.Now().UTC().Add(time.Minute), fail: true}
	a.GitHub = g
	var rate *providers.RateLimitError
	if err := a.Sync(ctx); !errors.As(err, &rate) || !rate.Until.Equal(g.until) {
		t.Fatal("scan swallowed throttle", err)
	}
	var waiting domain.Repository
	if err := a.Store.Get(ctx, "repository", i.Repository, &waiting); err != nil {
		t.Fatal(err)
	}
	if !waiting.SyncWaiting || !waiting.RetryAt.Equal(g.until) || !waiting.LastSync.Equal(before.LastSync) || !waiting.LastFull.Equal(before.LastFull) {
		t.Fatal("waiting lost deadline/checkpoints", waiting)
	}
	var saved domain.Item
	if err := a.Store.Get(ctx, "item", i.ID, &saved); err != nil || saved.Title != i.Title {
		t.Fatal("throttle changed saved source", saved, err)
	}
	if err := a.Sync(ctx); !errors.As(err, &rate) || g.indexes != 1 {
		t.Fatal("scan bypassed retained wait", err, g.indexes)
	}
	if err := a.sync(ctx, true); (err != nil && !errors.As(err, &rate)) || g.indexes != 1 {
		t.Fatal("background bypassed retained wait", err, g.indexes)
	}
	// Advance the retained repository deadline without waiting in the fixture.
	waiting.RetryAt = time.Now().UTC().Add(-time.Second)
	if err := a.Store.Put(ctx, "repository", i.Repository, waiting, ""); err != nil {
		t.Fatal(err)
	}
	g.fail = false
	if err := a.sync(ctx, true); err != nil || g.indexes != 2 {
		t.Fatal("recent successful checkpoint suppressed due retry", err, g.indexes)
	}
	waiting = domain.Repository{}
	if err := a.Store.Get(ctx, "repository", i.Repository, &waiting); err != nil {
		t.Fatal(err)
	}
	if waiting.SyncWaiting || waiting.Error != "" || !waiting.RetryAt.IsZero() || !waiting.LastSync.After(before.LastSync) {
		t.Fatal("successful retry retained waiting", waiting)
	}
	if err := a.Store.Get(ctx, "item", i.ID, &saved); err != nil || saved.Title != "Recovered source" {
		t.Fatal("successful retry did not import", saved, err)
	}
}

func TestScanTeamThrottlePreservesCheckpointsAndRetriesWhenDue(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	importOne(t, a, ctx, i)
	var before domain.Repository
	if err := a.Store.Get(ctx, "repository", i.Repository, &before); err != nil {
		t.Fatal(err)
	}
	previous := domain.GitHubViewer{Login: "me", Teams: []string{"owner/saved"}}
	if err := a.Store.Put(ctx, "identity", "github", previous, ""); err != nil {
		t.Fatal(err)
	}
	g := &retryGitHub{GitHubSource: a.GitHub, until: time.Now().UTC().Add(time.Minute), teamFail: true, reviewTeams: []string{"owner/reviewers"}}
	a.GitHub = g
	var rate *providers.RateLimitError
	if err := a.Sync(ctx); !errors.As(err, &rate) || !rate.Until.Equal(g.until) || g.indexes != 1 || g.teams != 1 {
		t.Fatal("post-index team throttle did not return waiting", err, g.indexes, g.teams)
	}
	var waiting domain.Repository
	if err := a.Store.Get(ctx, "repository", i.Repository, &waiting); err != nil {
		t.Fatal(err)
	}
	if !waiting.SyncWaiting || !waiting.RetryAt.Equal(g.until) || waiting.Error != rate.Error() || !waiting.LastSync.Equal(before.LastSync) || !waiting.LastFull.Equal(before.LastFull) {
		t.Fatal("team throttle changed checkpoints or lost provider deadline", waiting)
	}
	var saved domain.Item
	if err := a.Store.Get(ctx, "item", i.ID, &saved); err != nil || saved.Title != i.Title {
		t.Fatal("team throttle replaced saved PR data", saved, err)
	}
	var viewer domain.GitHubViewer
	if err := a.Store.Get(ctx, "identity", "github", &viewer); err != nil || len(viewer.Teams) != 1 || viewer.Teams[0] != "owner/saved" {
		t.Fatal("team throttle discarded saved membership", viewer, err)
	}
	if err := a.Sync(ctx); !errors.As(err, &rate) || g.indexes != 1 || g.teams != 1 {
		t.Fatal("repeated scan bypassed team cooldown", err, g.indexes, g.teams)
	}
	if err := a.sync(ctx, true); (err != nil && !errors.As(err, &rate)) || g.indexes != 1 || g.teams != 1 {
		t.Fatal("background read bypassed team cooldown", err, g.indexes, g.teams)
	}
	waiting.RetryAt = time.Now().UTC().Add(-time.Second)
	if err := a.Store.Put(ctx, "repository", i.Repository, waiting, ""); err != nil {
		t.Fatal(err)
	}
	g.teamFail = false
	if err := a.sync(ctx, true); err != nil || g.indexes != 2 || g.teams != 2 {
		t.Fatal("team retry suppressed by recent checkpoint", err, g.indexes, g.teams)
	}
	waiting = domain.Repository{}
	if err := a.Store.Get(ctx, "repository", i.Repository, &waiting); err != nil || waiting.SyncWaiting || waiting.Error != "" || !waiting.RetryAt.IsZero() || !waiting.LastSync.After(before.LastSync) {
		t.Fatal("team recovery did not complete checkpoint", waiting, err)
	}
	if err := a.Store.Get(ctx, "identity", "github", &viewer); err != nil || viewer.TeamError != "" || len(viewer.Teams) != 1 || viewer.Teams[0] != "owner/reviewers" {
		t.Fatal("team recovery did not publish membership", viewer, err)
	}
	if err := a.Store.Get(ctx, "item", i.ID, &saved); err != nil || saved.Title != "Recovered source" {
		t.Fatal("team recovery did not import refreshed source", saved, err)
	}
}

func TestNotificationThrottleRetainsCursorAndUsesProviderDeadline(t *testing.T) {
	a, ctx := fixture(t)
	importOne(t, a, ctx, item())
	since := time.Now().UTC().Add(-time.Hour)
	checkpoint := notificationSync{Login: "me", Since: since}
	if err := a.Store.Put(ctx, "github-notifications", "cursor", checkpoint, ""); err != nil {
		t.Fatal(err)
	}
	g := &retryGitHub{GitHubSource: a.GitHub, until: time.Now().UTC().Add(time.Minute), fail: true}
	a.GitHub = g
	var rate *providers.RateLimitError
	if err := a.SyncNotifications(ctx); !errors.As(err, &rate) {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "github-notifications", "cursor", &checkpoint); err != nil {
		t.Fatal(err)
	}
	if !checkpoint.Since.Equal(since) || !checkpoint.RetryAt.Equal(g.until) {
		t.Fatal("notification wait lost cursor/deadline", checkpoint)
	}
	if err := a.SyncNotifications(ctx); err != nil || g.notifications != 1 {
		t.Fatal("notification retried during cooldown", err, g.notifications)
	}
	checkpoint.RetryAt = time.Now().UTC().Add(-time.Second)
	if err := a.Store.Put(ctx, "github-notifications", "cursor", checkpoint, ""); err != nil {
		t.Fatal(err)
	}
	g.fail = false
	if err := a.SyncNotifications(ctx); err != nil || g.notifications != 2 {
		t.Fatal("normal cadence postponed due throttle recovery", err, g.notifications)
	}
	if err := a.Store.Get(ctx, "github-notifications", "cursor", &checkpoint); err != nil {
		t.Fatal(err)
	}
	if !checkpoint.RetryAt.IsZero() || checkpoint.Error != "" || !checkpoint.Since.Equal(since) {
		t.Fatal("notification recovery state", checkpoint)
	}
}
