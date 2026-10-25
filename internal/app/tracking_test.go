package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
)

func TestTrackingResolvesTargetsPaginatesHistoryAndSeparatesReceiptHeads(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	i.Kind = "pr"
	i.Head = "current-head"
	i.Body = ""
	i.URL = "https://github.com/owner/repo/pull/1"
	importOne(t, a, ctx, i)
	now := time.Now().UTC()
	for n, status := range []string{"idle", "failed", "cleaned", "prepared"} {
		r := domain.Run{ID: fmt.Sprintf("run%d", n), ItemID: i.ID, Status: status, Created: now.Add(time.Duration(n) * time.Minute), Profile: domain.Profile{ID: "review", Name: "Reviewer"}, Skills: []domain.Skill{{Files: map[string]string{"large": strings.Repeat("private bundle", 10000)}}}}
		if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
			t.Fatal(err)
		}
	}
	question := domain.Attention{ID: "ask", RunID: "run1", Origin: "text", Status: "pending", Created: now, Params: raw(map[string]any{"questions": []map[string]any{{"id": "scope", "question": "Choose a scope", "options": []map[string]string{{"label": "Continue"}, {"label": "Stop"}}}}})}
	if err := a.Store.Put(ctx, "attention", question.ID, question, ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ask2", "ask3"} {
		q := question
		q.ID = id
		if err := a.Store.Put(ctx, "attention", id, q, ""); err != nil {
			t.Fatal(err)
		}
	}
	reviews := domain.PRReviews{ItemID: i.ID, SyncedAt: now, Reviews: []domain.PublishedReview{{ID: "old", URL: "https://github.com/owner/repo/pull/1#pullrequestreview-1", Head: "old-head", Body: strings.Repeat("large review", 10000)}, {ID: "new", Head: i.Head}}}
	if err := a.Store.Put(ctx, "pr-reviews", i.ID, reviews, ""); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{i.ID, "repo#1", i.URL, "run1"} {
		out, err := a.TrackWork(ctx, TrackInput{Target: target, Limit: 1})
		if err != nil {
			t.Fatal(target, err)
		}
		if out["currentRun"].(map[string]any)["id"] != "run1" {
			t.Fatal("UI current-run preference changed", out)
		}
		if out["latestOutput"].(store.Progress).State != "unavailable" {
			t.Fatal("missing projection was fabricated")
		}
		ats := out["attention"].(AttentionPage)
		if len(ats.Requests) != 1 || ats.NextCursor == "" || string(ats.Requests[0].Request.Params) != string(question.Params) {
			t.Fatal("question altered or pagination lost", ats)
		}
		next, err := a.TrackWork(ctx, TrackInput{Target: target, Limit: 1, Cursor: out["nextCursor"].(string), AttentionCursor: ats.NextCursor, ReceiptCursor: out["nextReceiptCursor"].(string)})
		if err != nil {
			t.Fatal(err)
		}
		if next["runs"].([]map[string]any)[0]["id"] != "run2" {
			t.Fatal("retained history not paginated", next)
		}
		if next["publication"].(map[string]any)["receipts"].([]map[string]any)[0]["headState"] != "current" {
			t.Fatal("current receipt lost", next)
		}
		if out["publication"].(map[string]any)["receipts"].([]map[string]any)[0]["headState"] != "earlier" {
			t.Fatal("earlier head claimed current")
		}
		b, _ := json.Marshal(out)
		if len(b) > 32<<10 || strings.Contains(string(b), "private bundle") || strings.Contains(string(b), "large review") {
			t.Fatal("tracking leaked heavy detail", len(b))
		}
	}
	other := i
	other.ID = "else/repo#1"
	other.Repository = "else/repo"
	importOne(t, a, ctx, other)
	resolution, err := a.ResolveTarget(ctx, "repo#1")
	if err != nil || resolution.Status != "ambiguous" || len(resolution.Candidates) != 2 {
		t.Fatal(resolution, err)
	}
	if _, err := a.Runs.PreparePRReview(ctx, "repo#1"); err == nil {
		t.Fatal("ambiguous target prepared work")
	}
	i.Author = "other"
	if err := a.Store.Put(ctx, "item", i.ID, i, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "identity", "github", domain.GitHubViewer{Login: "me"}, ""); err != nil {
		t.Fatal(err)
	}
	r, err := a.Runs.PreparePRReview(ctx, i.URL)
	if err != nil || r.ItemID != i.ID || r.Status != "prepared" {
		t.Fatal(r, err)
	}
	page, err := a.TrackWork(ctx, TrackInput{Queue: "all", State: "all", Limit: 1})
	if err != nil || page["total"] != 2 || page["nextCursor"] == nil {
		t.Fatal(page, err)
	}
	if _, err := a.TrackWork(ctx, TrackInput{Queue: "issues", Cursor: page["nextCursor"].(string)}); err == nil {
		t.Fatal("cursor accepted under different filters")
	}
	for _, in := range []TrackInput{{Limit: 21}, {Target: i.ID, Cursor: "broken"}, {Target: i.ID, ReceiptCursor: "broken"}, {Target: i.ID, AttentionCursor: "broken"}} {
		if _, err := a.TrackWork(ctx, in); err == nil {
			t.Fatal("invalid paging accepted", in)
		}
	}
}

func TestSavedReadsDoNotWaitForSynchronizationOrBlockedGitHubAndDeduplicate(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	i.Kind = "pr"
	i.Head = "h"
	i.Body = ""
	importOne(t, a, ctx, i)
	var calls atomic.Int32
	entered := make(chan struct{}, 20)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		w.WriteHeader(503)
	}))
	defer server.Close()
	defer close(release)
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	a.GitHub.ConfigureUsage(a.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 600}, nil
	})
	// Synchronization owns the provider lock, but local reads must still finish.
	a.providerMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := a.ItemDetail(ctx, i.ID)
		if err == nil {
			var r domain.PRReviews
			r, err = a.ReadPRReviews(ctx, i.ID)
			if err == nil && r.Refresh != "pending" {
				err = fmt.Errorf("missing observation not scheduled: %s", r.Refresh)
			}
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			a.providerMu.Unlock()
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		a.providerMu.Unlock()
		t.Fatal("saved read waited on sync")
	}
	a.providerMu.Unlock()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no background refresh")
	}
	before := calls.Load()
	for n := 0; n < 8; n++ {
		out, err := a.TrackWork(ctx, TrackInput{Target: i.ID, Freshness: fmt.Sprint(n)})
		if err != nil || out["observation"].(Observation).Refresh != "pending" {
			t.Fatal(out, err)
		}
	}
	if calls.Load() != before {
		t.Fatal("duplicate observation workers bypassed provider serialization")
	}
}

func TestObservationPauseCooldownAndFailureRetainSavedState(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	i.Kind = "pr"
	i.Body = ""
	importOne(t, a, ctx, i)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	a.GitHub.ConfigureUsage(a.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 600}, nil
	})
	saved := domain.PRReviews{ItemID: i.ID, SyncedAt: time.Now().Add(-time.Hour), Reviews: []domain.PublishedReview{{ID: "receipt", Head: "old"}}}
	_ = a.Store.Put(ctx, "pr-reviews", i.ID, saved, "")
	_, err := a.ReadPRReviews(ctx, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	var failed domain.PRReviews
	for time.Now().Before(deadline) {
		failed, _ = a.SavedPRReviews(ctx, i.ID)
		if failed.Error != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if failed.Error == "" || !failed.SyncedAt.Equal(saved.SyncedAt) || len(failed.Reviews) != 1 || !failed.RetryAt.After(time.Now()) {
		t.Fatal("failed refresh lost evidence", failed)
	}
	before := calls.Load()
	out, err := a.ReadPRReviews(ctx, i.ID)
	if err != nil || out.Refresh != "cooldown" || calls.Load() != before {
		t.Fatal(out, err)
	}
	var repo domain.Repository
	_ = a.Store.Get(ctx, "repository", i.Repository, &repo)
	repo.Paused = true
	_ = a.SaveRepository(ctx, repo)
	out, err = a.ReadPRReviews(ctx, i.ID)
	if err != nil || out.Refresh != "paused" || calls.Load() != before {
		t.Fatal(out, err)
	}
	detail, err := a.ItemDetail(ctx, i.ID)
	if err != nil || detail["observation"].(Observation).Refresh != "paused" {
		t.Fatal(detail, err)
	}
	repo.Paused = false
	repo.RetryAt = time.Now().Add(time.Hour)
	_ = a.Store.Put(ctx, "repository", repo.ID, repo, "")
	detail, err = a.ItemDetail(ctx, i.ID)
	if err != nil || detail["observation"].(Observation).Refresh != "cooldown" || calls.Load() != before {
		t.Fatal(detail, err)
	}
}

func TestFreshDetailKeepsMonitoringFocusWithoutProviderRefresh(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	i.DetailSyncedAt = time.Now().UTC()
	i.Body = ""
	importOne(t, a, ctx, i)
	a.GitHub.ConfigureUsage(a.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 600}, nil
	})
	out, err := a.ItemDetail(ctx, i.ID)
	if err != nil || out["observation"].(Observation).Refresh != "fresh" {
		t.Fatal(out, err)
	}
	var focus detailFocus
	waitFor(t, func() bool { return a.Store.Get(ctx, "github-detail-focus", i.ID, &focus) == nil })
	if time.Since(focus.SeenAt) > time.Second {
		t.Fatal("cache hit lost selected-item monitoring focus", focus)
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if a.refreshing["detail:"+i.ID] || a.refreshing["reviews:"+i.ID] {
		t.Fatal("fresh detail scheduled network refresh")
	}
}

func TestSavedTrackingReadsDuringUncommittedSynchronizationWrite(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	i.Kind = "pr"
	i.Body = ""
	i.DetailSyncedAt = time.Now().UTC()
	importOne(t, a, ctx, i)
	a.GitHub.ConfigureUsage(a.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 600}, nil
	})
	_ = a.Store.Put(ctx, "pr-reviews", i.ID, domain.PRReviews{ItemID: i.ID, SyncedAt: time.Now().UTC(), Reviews: []domain.PublishedReview{}}, "")
	entered := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	writer := make(chan error, 1)
	go func() {
		writer <- a.Store.Write(ctx, func(tx *store.Tx) error {
			if err := tx.Put("fixture", "in-progress", map[string]string{"state": "uncommitted"}); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-writer:
		t.Fatal("synchronization writer exited before entering", err)
	case <-time.After(5 * time.Second):
		t.Fatal("synchronization writer did not enter")
	}
	done := make(chan error, 1)
	go func() { _, err := a.TrackWork(ctx, TrackInput{Target: i.ID}); done <- err }()
	var err error
	select {
	case err = <-done:
	case <-time.After(500 * time.Millisecond):
		err = fmt.Errorf("saved tracking read waited on synchronization write")
	}
	unblock()
	select {
	case writeErr := <-writer:
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("synchronization writer did not finish after release")
	}
	if err != nil {
		t.Fatal(err)
	}
}
