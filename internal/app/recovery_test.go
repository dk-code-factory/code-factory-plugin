package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/testutil"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recordedCalls(t *testing.T, path string) []struct {
	Method string
	Params map[string]json.RawMessage
} {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []struct {
		Method string
		Params map[string]json.RawMessage
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var call struct {
			Method string
			Params map[string]json.RawMessage
		}
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		out = append(out, call)
	}
	return out
}

func TestStartupContinuesPendingSessionFromRetainedProgress(t *testing.T) {
	for _, status := range []string{"running", "queued", "starting", "interrupted"} {
		t.Run(status, func(t *testing.T) {
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
			checkpoint := filepath.Join(r.Worktree, "completed-reviewer.json")
			if err := os.WriteFile(checkpoint, []byte(`{"reviewer":"completed","findings":[]}`), 0600); err != nil {
				t.Fatal(err)
			}
			a.Runs.Close()
			r.Status = status
			r.ResumePending = true
			if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
				t.Fatal(err)
			}
			a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
			if err := a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
			if err := a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			if r.ID != before.ID || r.ThreadID != before.ThreadID || r.Worktree != before.Worktree || r.Prompt != before.Prompt || string(raw(r.Profile)) != string(raw(before.Profile)) {
				t.Fatal("startup replaced the retained session or authority", r)
			}
			if b, err := os.ReadFile(checkpoint); err != nil || string(b) != `{"reviewer":"completed","findings":[]}` {
				t.Fatal("startup lost saved reviewer progress", err)
			}
			starts, resumes, turns := 0, 0, 0
			for _, call := range recordedCalls(t, record) {
				switch call.Method {
				case "thread/start":
					starts++
				case "thread/resume":
					resumes++
					if string(call.Params["threadId"]) != `"`+before.ThreadID+`"` {
						t.Fatal("resumed another thread")
					}
				case "turn/start":
					turns++
					if turns == 2 {
						input := string(call.Params["input"])
						if strings.Contains(input, before.Prompt) || !strings.Contains(input, "last unfinished step") || !strings.Contains(input, "workflow/reviewer checkpoints") || !strings.Contains(input, "receipts") {
							t.Fatal("startup replayed original task or lost continuation instructions", input)
						}
					}
				}
			}
			if starts != 1 || resumes != 1 || turns != 2 {
				t.Fatalf("startup launched duplicate or fresh work: starts=%d resumes=%d turns=%d", starts, resumes, turns)
			}
		})
	}
}

func TestStartupLeavesFinishedStoppedAndUnstartedWorkAlone(t *testing.T) {
	for _, status := range []string{"idle", "interrupted", "failed", "prepared", "cleaned"} {
		t.Run(status, func(t *testing.T) {
			record := filepath.Join(t.TempDir(), "calls.jsonl")
			t.Setenv("CF_FAKE_RECORD", record)
			a, ctx := fixture(t)
			r := prepareRun(t, a, ctx)
			r.Status = status
			if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
				t.Fatal(err)
			}
			before := string(raw(r))
			if err := a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			_ = a.Store.Get(ctx, "run", r.ID, &r)
			if string(raw(r)) != before {
				t.Fatal("startup changed inactive work", r)
			}
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatal("startup invoked provider for inactive work", err)
			}
			if _, err := os.Stat(r.Worktree); !os.IsNotExist(err) {
				t.Fatal("startup created an unstarted workspace", err)
			}
		})
	}
}

func TestStartupRetainsQuestionsAndNeverReplaysApprovalAnswers(t *testing.T) {
	for _, mode := range []string{"question", "approval"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", mode)
			record := filepath.Join(t.TempDir(), "calls.jsonl")
			t.Setenv("CF_FAKE_RECORD", record)
			a, ctx := fixture(t)
			r := prepareRun(t, a, ctx)
			if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "waiting" })
			a.Runs.Close()
			a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
			if err := a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			if mode == "question" {
				pendingQuestion(t, a, "text")
				_ = a.Store.Get(ctx, "run", r.ID, &r)
				if r.Status != "interrupted" || len(recordedCalls(t, record)) != 2 {
					t.Fatal("startup bypassed an unanswered question", r)
				}
			} else {
				waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "waiting" })
				var atts []domain.Attention
				rows, err := a.Store.List(ctx, "attention")
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range rows {
					var at domain.Attention
					_ = json.Unmarshal(row, &at)
					atts = append(atts, at)
				}
				stale, pending := 0, 0
				for _, at := range atts {
					if at.Origin == "text" {
						t.Fatal("lost approval became a text answer")
					}
					if at.Status == "stale" {
						stale++
					}
					if at.Status == "pending" {
						pending++
					}
				}
				if stale != 1 || pending != 1 {
					t.Fatal("approval was reused instead of requested afresh", atts)
				}
			}
		})
	}
}

func TestStartupReconcilesProviderCompletionWithoutAnotherTurn(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	t.Setenv("CF_FAKE_RESUME_STATUS", "completed")
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
	a.Runs.Close()
	a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "idle" })
	if r.ResumePending || r.TurnID != "" || len(recordedCalls(t, record)) != 3 {
		t.Fatal("durably completed turn was replayed", r)
	}
}

func TestStartupHonorsInterruptBeforeProviderAcknowledges(t *testing.T) {
	a, ctx := fixture(t)
	r := domain.Run{ID: "stopping", Status: "running", ResumePending: true, InterruptRequested: true}
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Get(ctx, "run", r.ID, &r)
	if r.Status != "interrupted" || r.ResumePending {
		t.Fatal("startup undid a user interrupt", r)
	}
	if _, err := a.Runs.get(r.ID); err == nil {
		t.Fatal("interrupted work was launched")
	}
}

func TestStartupIsolatesUnavailableWorkAndContinuesOtherSessions(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
	a.Runs.Close()
	broken := r
	broken.ID = "unavailable"
	broken.Worktree = filepath.Join(t.TempDir(), "missing")
	if err := a.Store.Put(ctx, "run", broken.ID, broken, ""); err != nil {
		t.Fatal(err)
	}
	a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", r.ID, &r)
		_ = a.Store.Get(ctx, "run", broken.ID, &broken)
		return r.Status == "running" && r.TurnID != "" && broken.Status == "failed"
	})
	if broken.Error != "retained worktree is unavailable" || broken.ThreadID != r.ThreadID {
		t.Fatal("failed recovery lost retained identity", broken)
	}
	starts := 0
	for _, call := range recordedCalls(t, record) {
		if call.Method == "thread/start" {
			starts++
		}
	}
	if starts != 1 {
		t.Fatal("missing workspace triggered a fresh task")
	}
}

func TestStartupStartsOnlyPreviouslyAcceptedUnstartedWork(t *testing.T) {
	for _, workspaceExists := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-workspace", true: "after-workspace"}[workspaceExists], func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "hold")
			record := filepath.Join(t.TempDir(), "calls.jsonl")
			t.Setenv("CF_FAKE_RECORD", record)
			a, ctx := fixture(t)
			r := prepareRun(t, a, ctx)
			if workspaceExists {
				if err := a.Runs.workspace(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			r.Status = "queued"
			if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
				t.Fatal(err)
			}
			if err := a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
			calls := recordedCalls(t, record)
			if len(calls) != 2 || calls[0].Method != "thread/start" || calls[1].Method != "turn/start" || !strings.Contains(string(calls[1].Params["input"]), "Investigate Regression") {
				t.Fatal("accepted unstarted work was skipped or launched more than once", calls)
			}
		})
	}
}

func TestStartupNeverReplacesAnUnavailableRetainedThread(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "resume-missing")
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if err := a.Runs.workspace(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Status, r.ThreadID, r.TurnID = "running", "thread-1", "turn-1"
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "failed" })
	calls := recordedCalls(t, record)
	if r.ThreadID != "thread-1" || len(calls) != 1 || calls[0].Method != "thread/resume" {
		t.Fatal("unavailable retained thread was replaced by fresh work", r, calls)
	}
}

func TestStartupHonorsCleanupEvenWhenDirtyRemovalFails(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if err := a.Runs.workspace(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Worktree, "unsaved.txt"), []byte("retained work"), 0600); err != nil {
		t.Fatal(err)
	}
	r.Status, r.ThreadID, r.ResumePending = "interrupted", "thread-1", true
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Cleanup(ctx, r.ID); err == nil {
		t.Fatal("dirty cleanup succeeded")
	}
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Get(ctx, "run", r.ID, &r)
	if r.Status != "interrupted" || r.ResumePending || !r.InterruptRequested {
		t.Fatal("startup reversed explicit cleanup", r)
	}
	if _, err := a.Runs.get(r.ID); err == nil {
		t.Fatal("startup continued work after explicit cleanup")
	}
	if b, err := os.ReadFile(filepath.Join(r.Worktree, "unsaved.txt")); err != nil || string(b) != "retained work" {
		t.Fatal("dirty work was lost", err)
	}
}
