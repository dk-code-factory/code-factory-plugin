package app

import (
	"codefactory/internal/codex"
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
	a.GitHub = providers.NewCachedGitHub(&providers.GitHub{BaseURL: s.URL, Client: s.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }})
	a.GitHub.ConfigureUsage(a.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 60}, nil
	})
	first, err := a.ReadPRReviews(ctx, i.ID)
	if err != nil || len(first.Reviews) != 0 || first.Refresh != "pending" || !first.SyncedAt.IsZero() {
		t.Fatal(first, err)
	}
	deadlineFirst := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadlineFirst) {
		first, err = a.SavedPRReviews(ctx, i.ID)
		if !first.SyncedAt.IsZero() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || first.SyncedAt.IsZero() {
		t.Fatal("background observation missing", first, err)
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

func TestPostedReviewRefreshCoalescesPostTurnHintsDuringPendingRead(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	i.Kind = "pr"
	i.Head = "head"
	i.Body = ""
	importOne(t, a, ctx, i)
	entered := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/reviews") {
			n := reads.Add(1)
			if n == 1 {
				close(entered)
				<-release
			}
			if n > 1 {
				fmt.Fprint(w, `[{"id":9,"state":"COMMENTED","body":"Posted after pending read","commit_id":"head","submitted_at":"2026-10-24T01:00:00Z","html_url":"https://github.com/owner/repo/pull/1#pullrequestreview-9","user":{"login":"me"}}]`)
				return
			}
		}
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	defer unblock()
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	a.GitHub.ConfigureUsage(a.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 600}, nil
	})
	first, err := a.ReadPRReviews(ctx, i.ID)
	if err != nil || first.Refresh != "pending" {
		t.Fatal(first, err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no background read")
	}
	for n := 0; n < 10; n++ {
		a.queueReviewsAfterTurn(i.ID)
	}
	if reads.Load() != 1 {
		t.Fatal("pending observation duplicated HTTP work")
	}
	unblock()
	deadline := time.Now().Add(3 * time.Second)
	var saved domain.PRReviews
	for time.Now().Before(deadline) {
		saved, _ = a.SavedPRReviews(ctx, i.ID)
		if len(saved.Reviews) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(saved.Reviews) != 1 || reads.Load() != 2 {
		t.Fatal("completion hint was lost or duplicated", len(saved.Reviews), reads.Load())
	}
}
