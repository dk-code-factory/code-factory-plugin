package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
	if e = a.Init(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cancel(); a.Runs.Close(); s.Close() })
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
		if e = a.SaveRepository(ctx, r); e != nil {
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
func prepareRun(t *testing.T, a *App, ctx context.Context) domain.Run {
	t.Helper()
	checkout := t.TempDir()
	for _, args := range [][]string{{"init", "--initial-branch=main"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = checkout
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git: %s %v", out, e)
		}
	}
	v := item()
	importOne(t, a, ctx, v)
	if e := a.SaveRepository(ctx, domain.Repository{ID: v.Repository, Checkout: checkout}); e != nil {
		t.Fatal(e)
	}
	r, e := a.Runs.Prepare(ctx, LaunchInput{ItemID: v.ID, ProfileID: "investigate"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(r.Worktree); !os.IsNotExist(e) {
		t.Fatal("prepare created worktree")
	}
	return r
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
func TestAgentQuestionExactlyOnceAndRecovery(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "question")
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, e := a.Runs.Start(ctx, r.ID, false); e != nil {
		t.Fatal(e)
	}
	var pending domain.Attention
	waitFor(t, func() bool {
		atts, _ := store.List[domain.Attention](ctx, a.Store, "attention")
		for _, v := range atts {
			if v.RunID == r.ID && v.Status == "pending" {
				pending = v
				return true
			}
		}
		return false
	})
	var run domain.Run
	_ = a.Store.Get(ctx, "run", r.ID, &run)
	if run.Status != "waiting" {
		t.Fatal("blocking question must set waiting", run.Status)
	}
	if e := a.Runs.Message(ctx, r.ID, "Clarification", "wrong-turn"); e == nil {
		t.Fatal("accepted stale steering")
	}
	if e := a.Runs.Message(ctx, r.ID, "Clarification", run.TurnID); e != nil {
		t.Fatal(e)
	}
	events, e := a.Store.Events(ctx, 0, 200)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, event := range events {
		if event.Kind == "attention.required" {
			found = event.Entity == r.ID
		}
	}
	if !found {
		t.Fatal("attention filter must identify the run")
	}
	answer := json.RawMessage(`{"answers":{"scope":{"answers":["Focused"]}}}`)
	if e := a.Runs.Answer(ctx, pending.ID, answer); e != nil {
		t.Fatal(e)
	}
	if e := a.Runs.Answer(ctx, pending.ID, answer); e == nil {
		t.Fatal("duplicate answer accepted")
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &run); return run.Status == "idle" })
	if _, e := os.Stat(run.Worktree); e != nil {
		t.Fatal("worktree missing", e)
	}
	p, e := a.Store.RunEvents(ctx, r.ID, 0, 1)
	if e != nil || !p["hasMore"].(bool) {
		t.Fatal("transcript pagination missing", e)
	}
	if e = a.Runs.Recover(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestNonblockingApprovalsCrashAndResume(t *testing.T) {
	for _, mode := range []string{"nonblocking", "approval", "crash", "complete"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", mode)
			a, ctx := fixture(t)
			r := prepareRun(t, a, ctx)
			if _, e := a.Runs.Start(ctx, r.ID, false); e != nil {
				t.Fatal(e)
			}
			var got domain.Run
			waitFor(t, func() bool {
				_ = a.Store.Get(ctx, "run", r.ID, &got)
				switch mode {
				case "nonblocking":
					atts, _ := store.List[domain.Attention](ctx, a.Store, "attention")
					return len(atts) > 0
				case "approval":
					return got.Status == "waiting"
				case "crash":
					return got.Status == "interrupted" || got.Status == "failed"
				default:
					return got.Status == "idle"
				}
			})
			if mode == "nonblocking" && got.Status != "running" {
				t.Fatal("nonblocking stopped run", got)
			}
			if mode == "complete" && got.TurnID != "" {
				t.Fatal("fast completion left active turn", got)
			}
			if mode == "approval" {
				atts, _ := store.List[domain.Attention](ctx, a.Store, "attention")
				if e := a.Runs.Answer(ctx, atts[0].ID, json.RawMessage(`{"decision":"acceptForSession"}`)); e == nil {
					t.Fatal("unsupported approval accepted")
				}
				if e := a.Runs.Answer(ctx, atts[0].ID, json.RawMessage(`{"decision":"decline"}`)); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "crash" {
				t.Setenv("CF_FAKE_MODE", "complete")
				if _, e := a.Runs.Start(ctx, r.ID, true); e != nil {
					t.Fatal(e)
				}
				waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &got); return got.Status == "idle" })
			}
		})
	}
}
func TestAssessmentCacheAndInvalidation(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "inference")
	a, ctx := fixture(t)
	v := item()
	importOne(t, a, ctx, v)
	if e := a.AssessNext(ctx); e != nil {
		t.Fatal(e)
	}
	var as domain.Assessment
	if e := a.Store.Get(ctx, "assessment", v.ID, &as); e != nil {
		t.Fatal(e)
	}
	if as.Status != "complete" || as.Priority != 1 {
		t.Fatal(as)
	}
	seq := a.Store.Latest(ctx)
	if e := a.AssessNext(ctx); e != nil {
		t.Fatal(e)
	}
	if a.Store.Latest(ctx) != seq {
		t.Fatal("unchanged assessment reran")
	}
	config, _ := a.Config(ctx)
	config.AssessmentPrompt += " new policy"
	if e := a.SaveConfig(ctx, config); e != nil {
		t.Fatal(e)
	}
	if e := a.AssessNext(ctx); e != nil {
		t.Fatal(e)
	}
	entries, _ := a.Inbox(ctx, "open")
	if entries[0].PrioritySource != "inference" || strings.Contains(entries[0].Assessment.Rationale, "error") {
		t.Fatal(entries)
	}
}

func TestRecoveryStalesRequestsAndIdleRuns(t *testing.T) {
	a, ctx := fixture(t)
	r := domain.Run{ID: "recovery", Status: "idle", ThreadID: "dead-thread", Worktree: "/retained/worktree"}
	if e := a.Store.Put(ctx, "run", r.ID, r, ""); e != nil {
		t.Fatal(e)
	}
	at := domain.Attention{ID: "dead-request", RunID: r.ID, Status: "pending", Method: "item/tool/requestUserInput"}
	if e := a.Store.Put(ctx, "attention", at.ID, at, ""); e != nil {
		t.Fatal(e)
	}
	if e := a.Runs.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	_ = a.Store.Get(ctx, "run", r.ID, &r)
	_ = a.Store.Get(ctx, "attention", at.ID, &at)
	if r.Status != "interrupted" || r.Worktree != "/retained/worktree" || at.Status != "stale" {
		t.Fatal(r, at)
	}
	if e := a.Runs.Answer(ctx, at.ID, json.RawMessage(`{"answers":{}}`)); e == nil {
		t.Fatal("dead request accepted")
	}
}
func TestAttentionValidationPreservesPermissionAndFormBoundaries(t *testing.T) {
	permission := domain.Attention{Method: "item/permissions/requestApproval", Params: json.RawMessage(`{"permissions":{"network":{"enabled":true},"fileSystem":{"read":["/repo"]}}}`)}
	for _, b := range []string{`{"permissions":{},"scope":"turn"}`, `{"permissions":{"fileSystem":{"read":["/repo"]}},"scope":"turn"}`} {
		if e := validateAnswer(permission, json.RawMessage(b)); e != nil {
			t.Fatal(e)
		}
	}
	if e := validateAnswer(permission, json.RawMessage(`{"permissions":{"fileSystem":{"write":["/"]}}}`)); e == nil {
		t.Fatal("permission escalation accepted")
	}
	form := domain.Attention{Method: "mcpServer/elicitation/request", Params: json.RawMessage(`{"mode":"form","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}}`)}
	if e := validateAnswer(form, json.RawMessage(`{"action":"accept","content":{"name":"test"}}`)); e != nil {
		t.Fatal(e)
	}
	if e := validateAnswer(form, json.RawMessage(`{"action":"accept","content":{"name":42}}`)); e == nil {
		t.Fatal("invalid form accepted")
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

func TestStreamingServiceAndSecretAnswerRedactionBeforeJournal(t *testing.T) {
	a, ctx := fixture(t)
	if e := a.Vault.Set(ctx, "tunnel-key", "sk-service-credential"); e != nil {
		t.Fatal(e)
	}
	s := &session{run: domain.Run{ID: "redaction-run", Status: "running"}, secrets: []string{"secret-answer-value"}, pending: map[string]domain.Attention{}, completed: map[string]bool{}}
	for _, delta := range []string{"before sk-ser", "vice-cred", "ential and secret-", "answer-value after"} {
		a.Runs.onMessage(s, codex.Message{Method: "item/agentMessage/delta", Params: raw(map[string]string{"itemId": "output", "delta": delta})})
	}
	events, e := a.Store.Events(ctx, 0, 200)
	if e != nil {
		t.Fatal(e)
	}
	var joined string
	for _, event := range events {
		if event.Kind != "run.output" {
			continue
		}
		var output struct{ Params struct{ Delta string } }
		if e = json.Unmarshal(event.Data, &output); e != nil {
			t.Fatal(e)
		}
		joined += output.Params.Delta
	}
	if joined != "before [redacted] and [redacted] after" {
		t.Fatal("unsafe streamed journal", joined)
	}
}

func TestConcurrentPollingSteeringAndAnswersThenDirtyCleanup(t *testing.T) {
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, e := a.Runs.Start(ctx, r.ID, false); e != nil {
		t.Fatal(e)
	}
	var attention domain.Attention
	waitFor(t, func() bool {
		atts, _ := store.List[domain.Attention](ctx, a.Store, "attention")
		for _, at := range atts {
			if at.Status == "pending" {
				attention = at
				_ = a.Store.Get(ctx, "run", r.ID, &r)
				return r.Status == "waiting"
			}
		}
		return false
	})
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for range 8 {
		wg.Go(func() {
			for range 20 {
				if _, e := a.Store.RunEvents(ctx, r.ID, 0, 10); e != nil {
					t.Error(e)
				}
				if _, e := a.Inbox(ctx, "all"); e != nil {
					t.Error(e)
				}
			}
		})
	}
	for range 4 {
		wg.Go(func() { _ = a.Runs.Message(ctx, r.ID, "Concurrent clarification", r.TurnID) })
	}
	for range 8 {
		wg.Go(func() {
			if a.Runs.Answer(ctx, attention.ID, raw(map[string]any{"answers": map[string]any{"scope": map[string]any{"answers": []string{"Focused"}}}})) == nil {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("answer claim was not exactly once", accepted.Load())
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "idle" })
	if e := os.WriteFile(filepath.Join(r.Worktree, "unsaved.txt"), []byte("retain this change"), 0600); e != nil {
		t.Fatal(e)
	}
	if a.Runs.Cleanup(ctx, r.ID) == nil {
		t.Fatal("dirty worktree removed")
	}
	if _, e := os.Stat(filepath.Join(r.Worktree, "unsaved.txt")); e != nil {
		t.Fatal("dirty state lost", e)
	}
}

func TestSecretAnswerRedactionSurvivesMessageWake(t *testing.T) {
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, e := a.Runs.Start(ctx, r.ID, false); e != nil {
		t.Fatal(e)
	}
	var at domain.Attention
	waitFor(t, func() bool {
		atts, _ := store.List[domain.Attention](ctx, a.Store, "attention")
		for _, v := range atts {
			if v.Status == "pending" {
				at = v
				return true
			}
		}
		return false
	})
	s, e := a.Runs.get(r.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	var p map[string]any
	_ = json.Unmarshal(at.Params, &p)
	questions := p["questions"].([]any)
	questions[0].(map[string]any)["isSecret"] = true
	at.Params = raw(p)
	s.pending[at.ID] = at
	e = a.Runs.saveAttention(ctx, at, "")
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	secret := "secret-answer-after-resume"
	if e = a.Runs.Answer(ctx, at.ID, raw(map[string]any{"answers": map[string]any{"scope": map[string]any{"answers": []string{secret}}}})); e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "idle" })
	var ciphertext string
	if e = a.Store.Get(ctx, "secret", "run-redactions:"+r.ID, &ciphertext); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(ciphertext, secret) {
		t.Fatal("secret stored without encryption")
	}
	a.Runs.Close()
	if e = a.Runs.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	if e = a.Runs.Message(ctx, r.ID, "Continue without repeating the private answer.", ""); e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool {
		live, e := a.Runs.get(r.ID)
		if e != nil {
			return false
		}
		live.mu.Lock()
		defer live.mu.Unlock()
		return len(live.secrets) == 1 && live.secrets[0] == secret
	})
	evs, e := a.Store.Events(ctx, 0, 200)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(evs)
	if strings.Contains(string(b), secret) {
		t.Fatal("secret answer was journaled")
	}
}

func TestMessageRestoresRetainedSessionWithOneSubmittedTurn(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	record := filepath.Join(t.TempDir(), "requests.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
	original := r
	// Simulate daemon recovery: no live in-memory session remains.
	a.Runs.Close()
	a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Message(ctx, r.ID, "Do not accept a stale turn", original.TurnID); err == nil {
		t.Fatal("accepted stale turn")
	}
	const message = "Continue with the focused fix and report its status."
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Go(func() {
			if a.Runs.Message(ctx, r.ID, message, "") == nil {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted %d concurrent wake messages", accepted.Load())
	}
	if err := a.Store.Get(ctx, "run", r.ID, &r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "running" || r.ThreadID != original.ThreadID || r.Worktree != original.Worktree || r.Prompt != original.Prompt || string(raw(r.Profile)) != string(raw(original.Profile)) {
		t.Fatalf("retained session changed: %#v", r)
	}
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected original start and one resume/turn pair, got %d", len(lines))
	}
	var resume, turn struct {
		Method string
		Params map[string]json.RawMessage
	}
	if err = json.Unmarshal([]byte(lines[2]), &resume); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(lines[3]), &turn); err != nil {
		t.Fatal(err)
	}
	if resume.Method != "thread/resume" || string(resume.Params["threadId"]) != string(raw(original.ThreadID)) || string(resume.Params["cwd"]) != string(raw(original.Worktree)) || len(resume.Params["sandbox"]) != 0 {
		t.Fatalf("wrong resume parameters: %s", lines[2])
	}
	var input []struct{ Type, Text string }
	if err = json.Unmarshal(turn.Params["input"], &input); err != nil {
		t.Fatal(err)
	}
	if turn.Method != "turn/start" || len(input) == 0 || input[0].Text != message {
		t.Fatalf("wake did not use submitted message: %s", lines[3])
	}
}

func TestWakeMessageFailureKeepsRetainedRecordAndRejectsCleanedWork(t *testing.T) {
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if err := a.Runs.Message(ctx, r.ID, "Do not launch a prepared proposal", ""); err == nil {
		t.Fatal("prepared launch bypassed Start")
	}
	r.Status = "failed"
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Message(ctx, r.ID, "Do not create a different thread", ""); err == nil || !strings.Contains(err.Error(), "retained Codex session is unavailable") {
		t.Fatalf("missing-session error: %v", err)
	}
	r.Status = "interrupted"
	r.ThreadID = "retained-thread"
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Message(ctx, r.ID, "Retain this message on failure", ""); err == nil || !strings.Contains(err.Error(), "retained worktree is unavailable") {
		t.Fatalf("missing-worktree error: %v", err)
	}
	var after domain.Run
	if err := a.Store.Get(ctx, "run", r.ID, &after); err != nil {
		t.Fatal(err)
	}
	if after.Status != "failed" || after.ThreadID != r.ThreadID || after.Prompt != r.Prompt {
		t.Fatalf("failed wake lost retained state: %#v", after)
	}
	after.Status = "cleaned"
	if err := a.Store.Put(ctx, "run", r.ID, after, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Message(ctx, r.ID, "Do not revive cleaned work", ""); err == nil {
		t.Fatal("cleaned workspace accepted message")
	}
}

func TestInterruptThenMessageStalesOldQuestionAndRestoresSameSession(t *testing.T) {
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	var old domain.Attention
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", r.ID, &r)
		atts, _ := store.List[domain.Attention](ctx, a.Store, "attention")
		for _, at := range atts {
			if at.RunID == r.ID && at.Status == "pending" && r.Status == "waiting" {
				old = at
				return true
			}
		}
		return false
	})
	original := r
	if err := a.Runs.Interrupt(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "interrupted" })
	if err := a.Store.Get(ctx, "attention", old.ID, &old); err != nil {
		t.Fatal(err)
	}
	if old.Status != "stale" || r.TurnID != "" {
		t.Fatalf("interruption retained an active request: %#v %#v", old, r)
	}
	answer := json.RawMessage(`{"answers":{"scope":{"answers":["Focused"]}}}`)
	if err := a.Runs.Answer(ctx, old.ID, answer); err == nil {
		t.Fatal("old interrupted question accepted")
	}
	if err := a.Runs.Message(ctx, r.ID, "Ask about the focused fix again.", ""); err != nil {
		t.Fatal(err)
	}
	var fresh domain.Attention
	waitFor(t, func() bool {
		atts, _ := store.List[domain.Attention](ctx, a.Store, "attention")
		for _, at := range atts {
			if at.RunID == r.ID && at.Status == "pending" && at.ID != old.ID {
				fresh = at
				return true
			}
		}
		return false
	})
	if err := a.Runs.Answer(ctx, old.ID, answer); err == nil {
		t.Fatal("old request answered the newly restored question")
	}
	if err := a.Runs.Answer(ctx, fresh.ID, answer); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "idle" })
	if r.ThreadID != original.ThreadID || r.Worktree != original.Worktree {
		t.Fatal("interrupted conversation was replaced")
	}
}
