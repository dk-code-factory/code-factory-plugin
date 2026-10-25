package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
)

func TestMessageQueueCommitsBeforeBlockedAgentAndSurvivesCallerCancellation(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
	s, _ := a.Runs.get(r.ID)
	s.mu.Lock() // A provider/session unavailable to the delivery worker.
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	caller, cancel := context.WithCancel(ctx)
	type result struct {
		d   MessageDelivery
		err error
	}
	accepted := make(chan result, 1)
	const text = "private message body saved before delivery"
	go func() { d, err := a.Runs.QueueMessage(caller, r.ID, text, r.TurnID); accepted <- result{d, err} }()
	var d MessageDelivery
	select {
	case got := <-accepted:
		if got.err != nil {
			t.Fatal(got.err)
		}
		d = got.d
	case <-time.After(500 * time.Millisecond):
		t.Fatal("acceptance waited for the agent")
	}
	cancel()
	if d.State != "queued" || d.ID == "" {
		t.Fatal(d)
	}
	if _, err := a.Runs.QueueMessage(ctx, r.ID, "duplicate while delivery is blocked", r.TurnID); err == nil {
		t.Fatal("concurrent pending message was accepted twice")
	}
	stored, err := a.Vault.Get(ctx, "message:"+d.ID)
	if err != nil || stored != text {
		t.Fatal("payload not committed", err)
	}
	rows, _ := a.Store.List(ctx, "message")
	events, _ := a.Store.Events(ctx, 0, 1000)
	b, _ := json.Marshal([]any{rows, events})
	if strings.Contains(string(b), text) {
		t.Fatal("plaintext payload leaked into journal or metadata")
	}
	s.mu.Unlock()
	locked = false
	waitFor(t, func() bool {
		current, _ := a.MessageDelivery(ctx, r.ID)
		return current != nil && current.State == "delivered"
	})
	tracked, err := a.TrackWork(ctx, TrackInput{Target: r.ID})
	if err != nil || tracked["messageDelivery"].(*MessageDelivery).ID != d.ID {
		t.Fatal(tracked, err)
	}
}

func TestQueuedFollowUpRecoversSameThreadWithoutAnyGitHubPreflight(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	r.ThreadID, r.Status, r.ResumePending = "thread-1", "interrupted", true
	if err := a.Runs.workspace(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	thread, worktree := r.ThreadID, r.Worktree
	a.Runs.Close()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	a.GitHub.ConfigureUsage(a.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 100}, nil
	})
	// Commit while the manager is stopped: recovery must deliver this message,
	// never a generic startup continuation followed by the submitted message.
	d, err := a.Runs.QueueMessage(ctx, r.ID, "Act on this saved follow-up", "")
	if err != nil || d.State != "queued" {
		t.Fatal(d, err)
	}
	a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		state, _ := a.MessageDelivery(ctx, r.ID)
		if state != nil && state.State == "uncertain" {
			var failed domain.Run
			_ = a.Store.Get(ctx, "run", r.ID, &failed)
			t.Fatalf("delivery failed: %+v; run: %+v", state, failed)
		}
		return state != nil && state.State == "delivered"
	})
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Get(ctx, "run", r.ID, &r)
	if r.ThreadID != thread || r.Worktree != worktree || calls.Load() != 0 {
		t.Fatal("follow-up revalidated GitHub or replaced retained session", r, calls.Load())
	}
	requests := recordedCalls(t, record)
	if len(requests) != 2 || requests[0].Method != "thread/resume" || requests[1].Method != "turn/start" {
		t.Fatal("duplicate or substitute delivery", requests)
	}
	var instructions string
	_ = json.Unmarshal(requests[0].Params["developerInstructions"], &instructions)
	if !strings.Contains(instructions, "GitHub has not been revalidated") || strings.Contains(instructions, "Current source check") {
		t.Fatal("saved context claimed fresh verification", instructions)
	}
}

func TestInterruptedMessageDeliveryIsUncertainAndNeverReplayed(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	record := filepath.Join(t.TempDir(), "provider.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	r.Status, r.ThreadID, r.ResumePending = "interrupted", "retained-thread", true
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	d := MessageDelivery{ID: ID(), RunID: r.ID, State: "delivering", Created: time.Now().UTC()}
	if err := a.Store.Write(ctx, func(tx *store.Tx) error { return saveMessage(tx, d) }); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		if err := a.Runs.Recover(ctx); err != nil {
			t.Fatal(err)
		}
	}
	state, err := a.MessageDelivery(ctx, r.ID)
	if err != nil || state.State != "uncertain" || !strings.Contains(state.Error, "not replayed") {
		t.Fatal(state, err)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("uncertain message launched a provider", err)
	}
	_ = a.Store.Get(ctx, "run", r.ID, &r)
	if r.Status != "interrupted" {
		t.Fatal("generic continuation bypassed uncertain delivery", r)
	}
}

func TestQueueMessageRejectsStaleAndUnstartedTargetsBeforeAcceptance(t *testing.T) {
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.QueueMessage(ctx, r.ID, "do not start proposal", ""); err == nil {
		t.Fatal("prepared proposal accepted")
	}
	r.Status, r.ThreadID, r.TurnID = "running", "thread", "new-turn"
	_ = a.Store.Put(ctx, "run", r.ID, r, "")
	if _, err := a.Runs.QueueMessage(ctx, r.ID, "stale steering", "old-turn"); err == nil {
		t.Fatal("stale steering accepted")
	}
	rows, _ := store.List[MessageDelivery](ctx, a.Store, "message")
	if len(rows) != 0 {
		t.Fatal("rejection left accepted messages", rows)
	}
}

func TestAcceptedMessageRemainsSavedWhenAgentRestorationFails(t *testing.T) {
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	r.ThreadID, r.Status = "retained-thread", "interrupted"
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	request := domain.Attention{ID: "unsettled", RunID: r.ID, Origin: "async", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "q1", Question: "Which contract?", IsOther: true}}})}
	if err := a.Store.Put(ctx, "attention", request.ID, request, ""); err != nil {
		t.Fatal(err)
	}
	const text = "This clarification must survive an unavailable workspace."
	d, err := a.Runs.QueueMessage(ctx, r.ID, text, "")
	if err != nil || d.State != "queued" {
		t.Fatal("local acceptance depended on restoration", d, err)
	}
	waitFor(t, func() bool {
		state, _ := a.MessageDelivery(ctx, r.ID)
		return state != nil && state.State == "uncertain"
	})
	saved, err := a.Vault.Get(ctx, "message:"+d.ID)
	if err != nil || saved != text {
		t.Fatal("accepted message was lost after restoration failure", err)
	}
	var after domain.Run
	_ = a.Store.Get(ctx, "run", r.ID, &after)
	if after.ThreadID != r.ThreadID || after.Status != "failed" || !strings.Contains(after.Error, "retained worktree is unavailable") {
		t.Fatal("failed delivery replaced the session", after)
	}
	if err := a.Store.Get(ctx, "attention", request.ID, &request); err != nil || request.Status != "pending" || len(request.Resolutions) != 0 {
		t.Fatal("uncertain delivery discarded an unanswered question", err)
	}
}
