package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostedReviewsRefreshAfterTurnAndRetainReceiptsOnFailure(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	i.Kind = "pr"
	i.Head = "head"
	importOne(t, a, ctx, i)
	var posted, deny atomic.Bool
	var reads atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != "GET" {
			t.Error("publication reader mutated GitHub")
		}
		if deny.Load() {
			w.WriteHeader(503)
			return
		}
		if posted.Load() && r.URL.Path == "/repos/owner/repo/pulls/1/reviews" {
			fmt.Fprint(w, `[{"id":9,"state":"COMMENTED","body":"Posted finding","commit_id":"head","submitted_at":"2026-10-24T01:00:00Z","html_url":"https://github.com/owner/repo/pull/1#pullrequestreview-9","user":{"login":"me"}}]`)
		} else {
			fmt.Fprint(w, `[]`)
		}
	}))
	defer s.Close()
	a.GitHub = &providers.GitHub{BaseURL: s.URL, Client: s.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	a.GitHub.ConfigureUsage(a.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 60}, nil
	})
	first, err := a.ReadPRReviews(ctx, i.ID)
	if err != nil || len(first.Reviews) != 0 || first.SyncedAt.IsZero() {
		t.Fatal(first, err)
	}
	before := reads.Load()
	posted.Store(true)
	_, _ = a.ReadPRReviews(ctx, i.ID)
	if reads.Load() != before {
		t.Fatal("repeated read bypassed cadence")
	}
	run := domain.Run{ID: "review-run", ItemID: i.ID, Status: "running"}
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	live := &session{run: run, completed: map[string]bool{}, pending: map[string]domain.Attention{}}
	a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]string{"id": "review-turn", "status": "completed"}})})
	deadline := time.Now().Add(3 * time.Second)
	var saved domain.PRReviews
	for time.Now().Before(deadline) {
		saved, err = a.SavedPRReviews(ctx, i.ID)
		if len(saved.Reviews) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || len(saved.Reviews) != 1 || saved.Reviews[0].Body != "Posted finding" || saved.Reviews[0].State != "COMMENTED" {
		t.Fatal("completed turn did not refresh GitHub receipt", saved, err)
	}
	deny.Store(true)
	failed, err := a.refreshPRReviews(ctx, i.ID, true)
	if err != nil || failed.Error == "" || !failed.SyncedAt.Equal(saved.SyncedAt) || len(failed.Reviews) != 1 {
		t.Fatal("failed read lost receipt or claimed success", failed, err)
	}
	before = reads.Load()
	repo := domain.Repository{ID: i.Repository, Paused: true}
	_ = a.SaveRepository(ctx, repo)
	paused, err := a.refreshPRReviews(ctx, i.ID, true)
	if err != nil || paused.Error == "" || reads.Load() != before || len(paused.Reviews) != 1 {
		t.Fatal("paused monitor fetched or lost evidence", paused, err)
	}
	i.Kind = "issue"
	_ = a.Store.Put(ctx, "item", i.ID, i, "")
	if _, err := a.ReadPRReviews(ctx, i.ID); err == nil {
		t.Fatal("issue accepted as PR")
	}
}
