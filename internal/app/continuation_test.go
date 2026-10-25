package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func currentItemProvider(t *testing.T, a *App, body string, status int) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("preflight mutated GitHub", r.Method)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	a.GitHub.ConfigureUsage(nil, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 100}, nil
	})
}

func TestContinuationProvidesFreshChangesWithoutReplacingSession(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
	before := r
	a.Runs.Close()
	// Older installed runs predate the persisted source baseline.
	r.SourceSnapshot = nil
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	currentItemProvider(t, a, `{"number":1,"title":"Changed task","body":"New acceptance condition","state":"open","updated_at":"2026-10-25T03:00:00Z","labels":[{"name":"changed"}],"assignees":[{"login":"reviewer"}]}`, 200)
	a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
	if r.ThreadID != before.ThreadID || r.Worktree != before.Worktree || r.Prompt != before.Prompt || r.SourceSnapshot.Body != "New acceptance condition" {
		t.Fatal("changed context replaced task or was not retained", r)
	}
	calls := recordedCalls(t, record)
	if len(calls) != 4 || calls[2].Method != "thread/resume" {
		t.Fatal("did not resume original thread", calls)
	}
	var instructions string
	_ = json.Unmarshal(calls[2].Params["developerInstructions"], &instructions)
	for _, expected := range []string{"Current source check", "New acceptance condition", before.SourceSnapshot.Body, `"baselineUnavailable":true`, `"Body"`, `"Assignees"`, "untrusted repository data", "redo only invalidated work"} {
		if !strings.Contains(instructions, expected) {
			t.Fatal("agent did not receive deterministic source changes", expected, instructions)
		}
	}
}

func TestClosedOrMergedSourcePermanentlyTerminatesContinuation(t *testing.T) {
	for _, tc := range []struct {
		kind, state string
		merged      bool
	}{{"issue", "closed", false}, {"pr", "closed", false}, {"pr", "closed", true}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "hold")
			record := filepath.Join(t.TempDir(), "calls.jsonl")
			t.Setenv("CF_FAKE_RECORD", record)
			a, ctx := fixture(t)
			r := prepareRun(t, a, ctx)
			if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
			if err := os.WriteFile(filepath.Join(r.Worktree, "unsaved.txt"), []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			a.Runs.Close()
			var item domain.Item
			_ = a.Store.Get(ctx, "item", r.ItemID, &item)
			item.Kind = tc.kind
			if err := a.Store.Put(ctx, "item", item.ID, item, ""); err != nil {
				t.Fatal(err)
			}
			currentItemProvider(t, a, fmt.Sprintf(`{"number":1,"title":"Task","state":%q,"merged":%t,"head":{"sha":"abc"}}`, tc.state, tc.merged), 200)
			a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
			if err := a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "terminated" })
			if r.ResumePending || r.TurnID != "" || len(recordedCalls(t, record)) != 2 {
				t.Fatal("terminal source launched more work", r)
			}
			if _, err := a.Runs.Start(ctx, r.ID, true); err == nil {
				t.Fatal("terminated run resumed")
			}
			if err := a.Runs.Message(ctx, r.ID, "Continue", ""); err == nil {
				t.Fatal("terminated run accepted follow-up")
			}
			if err := a.Runs.Cleanup(ctx, r.ID); err == nil {
				t.Fatal("dirty cleanup removed files")
			}
			_ = a.Store.Get(ctx, "run", r.ID, &r)
			if r.Status != "terminated" {
				t.Fatal("dirty cleanup made a terminal run resumable", r)
			}
			if b, err := os.ReadFile(filepath.Join(r.Worktree, "unsaved.txt")); err != nil || string(b) != "preserve" {
				t.Fatal("termination lost work", err)
			}
		})
	}
}

func TestUnverifiedSourceNeverStartsProviderOrReplaysTask(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	currentItemProvider(t, a, `unavailable`, 503)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "failed" })
	if !strings.Contains(r.Error, "cannot verify current work item") {
		t.Fatal("failed source check invisible", r)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("unverified source invoked provider", err)
	}
	if _, err := os.Stat(r.Worktree); !os.IsNotExist(err) {
		t.Fatal("unverified source created workspace", err)
	}
}

func TestChangedPRHeadInvalidatesCheckpointsWithoutRetargetingWorkspace(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	var i domain.Item
	_ = a.Store.Get(ctx, "item", r.ItemID, &i)
	i.Kind, i.Head = "pr", "old-head"
	r.SourceSnapshot = sourceSnapshot(i)
	if err := a.Store.Put(ctx, "item", i.ID, i, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
	before := r
	a.Runs.Close()
	currentItemProvider(t, a, `{"number":1,"title":"Task","state":"open","head":{"sha":"new-head","ref":"topic"}}`, 200)
	a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
	var instructions string
	_ = json.Unmarshal(recordedCalls(t, record)[2].Params["developerInstructions"], &instructions)
	if !strings.Contains(instructions, `"Head"`) || !strings.Contains(instructions, "old-head") || !strings.Contains(instructions, "new-head") || !strings.Contains(instructions, "which saved reviewer/workflow checkpoints remain valid") {
		t.Fatal("changed head was hidden from the agent", instructions)
	}
	if r.Base != before.Base || r.Worktree != before.Worktree || r.ThreadID != before.ThreadID {
		t.Fatal("preflight silently retargeted work", r)
	}
}

func TestStartupTerminatesClosedWorkEvenWithPendingQuestion(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "question")
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	pendingQuestion(t, a, "rpc")
	a.Runs.Close()
	currentItemProvider(t, a, `{"number":1,"title":"Task","state":"closed"}`, 200)
	a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Get(ctx, "run", r.ID, &r)
	if r.Status != "terminated" {
		t.Fatal("pending question bypassed terminal lifecycle check", r)
	}
	page, err := a.ListAttention(ctx, AttentionInput{RunID: r.ID})
	if err != nil || len(page.Requests) != 0 {
		t.Fatal("terminal question remained actionable", page, err)
	}
}

func TestSyncedClosureStopsActiveWorkAndRetiresQuestions(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "question")
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	at := pendingQuestion(t, a, "rpc")
	var i domain.Item
	_ = a.Store.Get(ctx, "item", r.ItemID, &i)
	i.State = "closed"
	importOne(t, a, ctx, i)
	_ = a.Store.Get(ctx, "run", r.ID, &r)
	if r.Status != "terminated" || r.ResumePending {
		t.Fatal("sync did not terminate active work", r)
	}
	live, err := a.Runs.get(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	live.mu.Lock()
	retired := live.retired
	live.mu.Unlock()
	if !retired {
		t.Fatal("provider still owns terminated work")
	}
	if err := a.Runs.Answer(ctx, at.ID, json.RawMessage(`{"answers":{"scope":{"answers":["Focused"]}}}`)); err == nil {
		t.Fatal("terminated question accepted")
	}
	page, err := a.ListAttention(ctx, AttentionInput{RunID: r.ID})
	if err != nil || len(page.Requests) != 0 {
		t.Fatal("terminated ask still visible", page, err)
	}
	a.Runs.Close()
	a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.List[domain.Attention](ctx, a.Store, "attention")
	for _, row := range rows {
		if row.RunID == r.ID && row.Status == "pending" {
			t.Fatal("restart resurrected terminated question", row)
		}
	}
	i.State = "open"
	importOne(t, a, ctx, i)
	if _, err := a.Runs.Start(ctx, r.ID, true); err == nil {
		t.Fatal("reopening source resurrected terminated run")
	}
}
