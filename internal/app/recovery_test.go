package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
	"context"
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
		if call.Method == "thread/start" || call.Method == "thread/resume" || call.Method == "turn/start" {
			out = append(out, call)
		}
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

// Persist the real final-message producer while deliberately withholding only
// turn/completed, then join the process to model that exact daemon crash window.
func journalFinalBeforeCompletion(t *testing.T, text, record string) (*App, context.Context, domain.Run) {
	t.Helper()
	t.Setenv("CF_FAKE_MODE", "hold")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return a.Store.Get(ctx, "run", run.ID, &run) == nil && run.Status == "running" && run.TurnID != ""
	})
	live, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"turnId": run.TurnID, "item": map[string]string{"id": "stopped-final", "type": "agentMessage", "phase": "final_answer", "text": text}})})
	page, err := a.Store.RunEvents(ctx, run.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range page["events"].([]domain.Event) {
		var body struct{ Method string }
		_ = json.Unmarshal(event.Data, &body)
		found = found || body.Method == "item/completed" || body.Method == "item/completed/chunk"
		if body.Method == "turn/completed" {
			t.Fatal("fixture completed turn before crash window")
		}
	}
	if !found {
		t.Fatal("final response was not journaled")
	}
	var recovery domain.QuestionRecovery
	if err := a.Store.Get(ctx, "question-recovery", run.ID+":"+run.TurnID, &recovery); err != store.ErrNotFound {
		t.Fatal("question was classified before its completion", recovery, err)
	}
	assertNoAttention(t, a)
	a.Runs.Close()
	return a, ctx, run
}

func TestStartupClassifiesJournaledFinalOfRestoredCompletedTurn(t *testing.T) {
	for _, question := range []bool{true, false} {
		name, text := "ordinary-completion", "Task complete"
		if question {
			name, text = "chunked-question", strings.Repeat("Retained facts ✓ ", 3000)+"Which target should I use?"
		}
		t.Run(name, func(t *testing.T) {
			record := filepath.Join(t.TempDir(), "calls.jsonl")
			a, ctx, before := journalFinalBeforeCompletion(t, text, record)
			t.Setenv("CF_FAKE_RESUME_STATUS", "completed")
			t.Setenv("CF_FAKE_MODE", "recover-first")
			if err := a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			var recovery domain.QuestionRecovery
			waitFor(t, func() bool {
				if a.Store.Get(ctx, "question-recovery", before.ID+":"+before.TurnID, &recovery) != nil {
					return false
				}
				if !question {
					return recovery.Stage == "resolved"
				}
				page, err := a.ListAttention(ctx, AttentionInput{RunID: before.ID})
				return recovery.Stage == "awaiting-answer" && err == nil && len(page.Requests) == 1 && page.Requests[0].Request.RecoveryID == recovery.ID
			})
			if recovery.SourceTurnID != before.TurnID || recovery.SourceText != questionContext(text) {
				t.Fatal("restored classification lost exact owning final response", recovery)
			}
			var after domain.Run
			if err := a.Store.Get(ctx, "run", before.ID, &after); err != nil {
				t.Fatal(err)
			}
			if after.ThreadID != before.ThreadID || after.Worktree != before.Worktree || after.Prompt != before.Prompt || string(raw(after.Profile)) != string(raw(before.Profile)) {
				t.Fatal("restored completion replaced retained work or authority")
			}
			if question {
				page, err := a.ListAttention(ctx, AttentionInput{RunID: before.ID})
				if err != nil || len(page.Requests) != 1 || !confirmedQuestion(recovery) || page.Requests[0].Request.RecoveryID != recovery.ID {
					t.Fatal("restored question missing from Messages", page, err)
				}
			} else {
				assertNoAttention(t, a)
				if recovery.Attempts != 0 {
					t.Fatal("ordinary completion requested repair")
				}
			}
			for n := 0; n < 2; n++ {
				if err := a.Runs.Recover(ctx); err != nil {
					t.Fatal(err)
				}
			}
			episodes, err := store.List[domain.QuestionRecovery](ctx, a.Store, "question-recovery")
			if err != nil || len(episodes) != 1 {
				t.Fatal("restored completion duplicated recovery", episodes, err)
			}
			ordinary, resumes := 0, 0
			for _, call := range recordedCalls(t, record) {
				if call.Method == "thread/resume" {
					resumes++
				}
				if call.Method != "turn/start" || len(call.Params["outputSchema"]) != 0 {
					continue
				}
				var input []struct{ Text string }
				_ = json.Unmarshal(call.Params["input"], &input)
				if len(input) > 0 && !strings.HasPrefix(input[0].Text, "Code Factory question-only recovery") {
					ordinary++
				}
			}
			if ordinary != 1 || resumes != 1 {
				t.Fatal("restored completion replayed ordinary task", ordinary, resumes)
			}
		})
	}
}

func TestTakeoverCancelsRestoredCompletedTurnClassification(t *testing.T) {
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	a, ctx, run := journalFinalBeforeCompletion(t, "Which target should I use?", record)
	gate := filepath.Join(t.TempDir(), "release")
	t.Setenv("CF_FAKE_RESUME_STATUS", "completed")
	t.Setenv("CF_FAKE_GATE_METHOD", "turn/start")
	t.Setenv("CF_FAKE_GATE_FILE", gate)
	defer func() { _ = os.WriteFile(gate, nil, 0600) }()
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(gate + ".waiting"); return err == nil })
	if err := a.Runs.Interrupt(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// Join the released callback while the daemon is still live. Fixture shutdown
	// cannot supply the suppression that this takeover must establish itself.
	waitRecoveryWork(t, a.Runs)
	if a.Runs.ctx.Err() != nil {
		t.Fatal("daemon shutdown supplied cancellation")
	}
	var recovery domain.QuestionRecovery
	if err := a.Store.Get(ctx, "question-recovery", run.ID+":"+run.TurnID, &recovery); err != nil || recovery.Stage != "cancelled" {
		t.Fatal("takeover failed to cancel restored check", recovery, err)
	}
	assertNoAttention(t, a)
	for _, call := range recordedCalls(t, record) {
		if call.Method == "turn/start" && strings.Contains(string(call.Params["input"]), "Code Factory question-only recovery") {
			t.Fatal("late check started repair after takeover")
		}
	}
}
