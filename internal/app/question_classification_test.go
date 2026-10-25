package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitRecovery(t *testing.T, a *App, stage string) domain.QuestionRecovery {
	t.Helper()
	var found domain.QuestionRecovery
	waitFor(t, func() bool {
		rs, _ := store.List[domain.QuestionRecovery](a.Runs.ctx, a.Store, "question-recovery")
		for _, r := range rs {
			if r.Stage == stage {
				found = r
				return true
			}
		}
		return false
	})
	return found
}
func assertNoAttention(t *testing.T, a *App) {
	t.Helper()
	as, err := store.List[domain.Attention](a.Runs.ctx, a.Store, "attention")
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range as {
		if at.Status == "pending" {
			t.Fatalf("unconfirmed text created attention: %+v", at)
		}
	}
}

func waitRecoveryWork(t *testing.T, m *Manager) {
	t.Helper()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery processing did not finish")
	}
}

func TestClassifierFailuresClarifyCompletionWithoutAttention(t *testing.T) {
	for _, failure := range []string{"startup", "disconnect", "timeout", "invalid", "config"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "complete")
			t.Setenv("CF_FAKE_HELPER_FAILURE", failure)
			a, ctx := fixture(t)
			a.Runs.questionCheckTimeout = 150 * time.Millisecond
			run := prepareRun(t, a, ctx)
			if failure == "config" {
				if err := a.Store.Put(ctx, "config", "settings", map[string]any{"priorityLabels": "invalid config"}, ""); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			r := waitRecovery(t, a, "resolved")
			if r.Attempts != 1 || r.Question != "" || r.SourceText != "Task complete" || r.DiagnosticError == "" {
				t.Fatalf("missing clarification provenance: %+v", r)
			}
			assertNoAttention(t, a)
		})
	}
}

func TestClarificationSharesBudgetAndRequiresEvidence(t *testing.T) {
	for _, reply := range []string{"native", "question", "malformed", "disconnect", "failed"} {
		t.Run(reply, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "recover-first")
			t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
			t.Setenv("CF_FAKE_CLARIFICATION", reply)
			record := filepath.Join(t.TempDir(), "protocol.jsonl")
			t.Setenv("CF_FAKE_RECORD", record)
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			if reply == "malformed" || reply == "disconnect" || reply == "failed" {
				r := waitRecovery(t, a, "unresolved")
				if r.Attempts != 2 || r.Confirmation != "" || r.Question != "" {
					t.Fatalf("unconfirmed attempts became question: %+v", r)
				}
				assertNoAttention(t, a)
				a.Runs.Close()
				before, err := os.ReadFile(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.Runs.Recover(ctx); err != nil {
					t.Fatal(err)
				}
				waitRecoveryWork(t, a.Runs)
				after, err := os.ReadFile(record)
				if err != nil {
					t.Fatal(err)
				}
				if len(after) <= len(before) || !strings.Contains(string(after[len(before):]), "outputSchema") {
					t.Fatal("restart did not actually reprocess the unresolved recovery")
				}
				if strings.Count(string(after), "question-only recovery clarification") != 2 {
					t.Fatal("restart delivered an additional automatic recovery turn")
				}
				assertNoAttention(t, a)
				_ = a.Store.Get(ctx, "question-recovery", r.ID, &r)
				if r.Attempts != 2 {
					t.Fatal("restart reset budget")
				}
				return
			}
			at := pendingQuestion(t, a, "rpc")
			var r domain.QuestionRecovery
			_ = a.Store.Get(ctx, "question-recovery", at.RecoveryID, &r)
			want := 1
			if reply == "question" {
				want = 2
			}
			if r.Attempts != want || !confirmedQuestion(r) {
				t.Fatalf("bad shared budget: %+v", r)
			}
		})
	}
}

func TestRestartCheckingAutomaticallyClarifiesRetainedThread(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
	record := filepath.Join(t.TempDir(), "protocol.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status = "interrupted"
	run.ThreadID = "thread-1"
	if err := os.MkdirAll(run.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Put(ctx, "run", run.ID, run, "")
	r := domain.QuestionRecovery{ID: "unfinished-check", RunID: run.ID, SourceTurnID: "old", Stage: "checking", Question: "Task complete"}
	_ = a.Store.Put(ctx, "question-recovery", r.ID, r, "")
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	r = waitRecovery(t, a, "resolved")
	assertNoAttention(t, a)
	if r.Attempts != 1 || r.SourceText != "Task complete" {
		t.Fatalf("restart promoted unclassified text: %+v", r)
	}
	var after domain.Run
	_ = a.Store.Get(ctx, "run", run.ID, &after)
	if after.ThreadID != run.ThreadID || after.Worktree != run.Worktree || string(raw(after.Profile)) != string(raw(run.Profile)) {
		t.Fatal("clarification changed original task")
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"method":"thread/resume"`) || strings.Contains(string(data), "Resume the existing task") {
		t.Fatal("restart continued task instead of clarification")
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var call struct {
			Method string
			Params struct {
				Input             []struct{ Text string }
				CollaborationMode struct{ Mode string }
				SandboxPolicy     struct{ Type string }
				ApprovalPolicy    string
			}
		}
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		if call.Method == "turn/start" && len(call.Params.Input) > 0 && strings.Contains(call.Params.Input[0].Text, "question-only recovery clarification") {
			found = true
			if call.Params.CollaborationMode.Mode != "plan" || call.Params.SandboxPolicy.Type != "readOnly" || call.Params.ApprovalPolicy != "never" {
				t.Fatal("clarification turn did not retain Plan mode and constrained policy")
			}
		}
	}
	if !found {
		t.Fatal("no clarification turn recorded")
	}
}

func TestRestartClarificationPreservesClaimedAttempt(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
	t.Setenv("CF_FAKE_CLARIFICATION", "malformed")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status = "interrupted"
	run.ThreadID = "thread-1"
	_ = os.MkdirAll(run.Worktree, 0700)
	_ = a.Store.Put(ctx, "run", run.ID, run, "")
	r := domain.QuestionRecovery{ID: "in-flight-clarification", RunID: run.ID, Stage: "clarifying", SourceText: "Task complete", Attempts: 1}
	_ = a.Store.Put(ctx, "question-recovery", r.ID, r, "")
	for n := 0; n < 2; n++ {
		if err := a.Runs.Recover(ctx); err != nil {
			t.Fatal(err)
		}
	}
	r = waitRecovery(t, a, "unresolved")
	if r.Attempts != 2 || r.Confirmation != "" {
		t.Fatalf("restart lost budget: %+v", r)
	}
	assertNoAttention(t, a)
}

func TestLegacyCompletionRequestReconcilesWithoutDiscardingRealQuestion(t *testing.T) {
	for _, later := range []bool{false, true} {
		t.Run(map[bool]string{false: "startup", true: "later-completion"}[later], func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "complete")
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			run.Status = "interrupted"
			run.ThreadID = "thread-1"
			_ = os.MkdirAll(run.Worktree, 0700)
			_ = a.Store.Put(ctx, "run", run.ID, run, "")
			source := "Task complete"
			if later {
				source = "Which target?"
			}
			r := domain.QuestionRecovery{ID: "legacy", RunID: run.ID, Stage: "fallback", Question: source}
			_ = a.Store.Put(ctx, "question-recovery", r.ID, r, "")
			old := domain.Attention{ID: r.ID + ":text", RunID: run.ID, RecoveryID: r.ID, Origin: "text", Method: questionMethod, Status: "pending", Blocking: true, Params: raw(map[string]any{"questions": []question{{ID: "reply", Question: source}}})}
			_ = a.Store.Put(ctx, "attention", old.ID, old, "")
			genuine := domain.QuestionRecovery{ID: "real", RunID: run.ID, Stage: "fallback", Question: "Still need your decision?", Confirmation: "native"}
			_ = a.Store.Put(ctx, "question-recovery", genuine.ID, genuine, "")
			_ = a.Runs.recoverTextQuestion(ctx, &genuine)
			if later {
				run.Status = "idle"
				_ = a.Store.Put(ctx, "run", run.ID, run, "")
				live := &session{run: run, pending: map[string]domain.Attention{old.ID: old}, completed: map[string]bool{}}
				a.Runs.mu.Lock()
				a.Runs.live[run.ID] = live
				a.Runs.mu.Unlock()
				a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"item": map[string]string{"type": "agentMessage", "text": "superseded completion"}})})
				a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]string{"id": "later-turn", "status": "completed"}})})
			} else if err := a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { _ = a.Store.Get(ctx, "attention", old.ID, &old); return old.Status == "resolved" })
			var at domain.Attention
			_ = a.Store.Get(ctx, "attention", genuine.ID+":text", &at)
			if at.Status != "pending" {
				t.Fatal("completion discarded confirmed unanswered question")
			}
			events, _ := a.Store.Events(ctx, 0, 200)
			found := false
			for _, event := range events {
				if event.Kind == "attention.resolved" && strings.Contains(string(event.Data), old.ID) {
					found = true
				}
			}
			if !found {
				t.Fatal("retirement omitted resolution event")
			}
		})
	}
}

func TestClarificationTakeoverAndAnswerRace(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
	t.Setenv("CF_FAKE_CLARIFICATION", "native")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	_, err := a.Runs.Start(ctx, run.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	at := pendingQuestion(t, a, "rpc")
	var wins atomic.Int32
	raceCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if a.Runs.Answer(raceCtx, at.ID, raw(map[string]any{"answers": map[string]any{"target": map[string]any{"answers": []string{"One"}}}})) == nil {
			wins.Add(1)
		}
	}()
	go func() { defer wg.Done(); _ = a.Runs.Interrupt(raceCtx, run.ID) }()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-raceCtx.Done():
		t.Fatal("answer/takeover race did not finish before its deadline")
	}
	if wins.Load() > 1 {
		t.Fatal("duplicate answer")
	}
	live, _ := a.Runs.get(run.ID)
	// A late native callback from the cancelled recovery cannot recreate attention.
	_ = a.Runs.Interrupt(raceCtx, run.ID)
	live.mu.Lock()
	turn := live.recovery.RepairTurnID
	live.mu.Unlock()
	a.Runs.onMessage(live, codex.Message{ID: raw(77), Method: questionMethod, Params: raw(map[string]any{"turnId": turn, "questions": []question{{ID: "stale", Question: "Late callback?"}}})})
	var r domain.QuestionRecovery
	_ = a.Store.Get(ctx, "question-recovery", at.RecoveryID, &r)
	if r.Stage != "cancelled" && r.Stage != "resolved" {
		t.Fatalf("takeover lost: %+v", r)
	}
	as, _ := store.List[domain.Attention](ctx, a.Store, "attention")
	for _, candidate := range as {
		if string(candidate.RequestID) == "77" {
			t.Fatal("late callback recreated question")
		}
	}
}

func TestUnconfirmedRecoveryNeverMaterializesText(t *testing.T) {
	a, ctx := fixture(t)
	for _, stage := range []string{"checking", "clarifying", "fallback", "unresolved"} {
		r := domain.QuestionRecovery{ID: stage, RunID: "retained", Stage: stage, Question: "Completed both review axes.", SourceText: "Completed both review axes."}
		if err := a.Runs.recoverTextQuestion(ctx, &r); err != nil {
			t.Fatal(err)
		}
	}
	assertNoAttention(t, a)
	checking := domain.QuestionRecovery{Stage: "checking", Question: "Task complete", Attempts: 2}
	migrateRecovery(&checking)
	if confirmedQuestion(checking) || checking.Question != "" || checking.SourceText != "Task complete" {
		t.Fatal("unfinished legacy checking became confirmed")
	}
	for _, stage := range []string{"requesting-form", "awaiting-answer", "fallback"} {
		repair := domain.QuestionRecovery{Stage: stage, Question: "Which target?", Attempts: 1}
		migrateRecovery(&repair)
		if !confirmedQuestion(repair) {
			t.Fatal("legacy form repair lost positive evidence")
		}
	}
}

func TestClarificationResultRejectsIncompleteOrTrailingDecisions(t *testing.T) {
	for _, text := range []string{
		`{}`, `{"needsInput":true}`, `{"question":"Which target?"}`,
		`{"needsInput":null,"question":""}`, `{"needsInput":false,"question":null}`,
		`{"needsInput":true,"question":"  "}`, `{"needsInput":"false","question":""}`,
		`{"needsInput":false,"question":"","extra":true}`,
		`{"needsInput":false,"question":""} {}`, `{"needsInput":false,"question":""} trailing`,
	} {
		t.Run(text, func(t *testing.T) {
			if _, _, err := clarificationResult(text); err == nil {
				t.Fatal("invalid clarification accepted")
			}
		})
	}
	for _, needs := range []bool{false, true} {
		question := ""
		if needs {
			question = "Which target?"
		}
		text := string(raw(map[string]any{"needsInput": needs, "question": question}))
		if got, q, err := clarificationResult(text); err != nil || got != needs || q != question {
			t.Fatalf("valid clarification rejected: %v %q %v", got, q, err)
		}
	}
}

func TestTextFallbackDoesNotReviveFinalOrUnrelatedRequests(t *testing.T) {
	a, ctx := fixture(t)
	for _, change := range []string{"answered", "dismissed", "resolved", "pending", "other-run", "other-recovery", "other-origin", "cancelled-recovery", "answered-recovery", "resolved-recovery", "unconfirmed-recovery", "live-native"} {
		t.Run(change, func(t *testing.T) {
			r := domain.QuestionRecovery{ID: change, RunID: "retained", Stage: "fallback", Question: "Which target?", Confirmation: "native", Attempts: 1}
			old := domain.Attention{ID: r.ID + ":text", RunID: r.RunID, RecoveryID: r.ID, Origin: "text", Method: questionMethod, Status: "superseded"}
			stale := r
			switch change {
			case "other-run":
				old.RunID = "other"
			case "other-recovery":
				old.RecoveryID = "other"
			case "other-origin":
				old.Origin = "rpc"
			case "cancelled-recovery", "answered-recovery", "resolved-recovery":
				r.Stage = strings.TrimSuffix(change, "-recovery")
			case "unconfirmed-recovery":
				r.Confirmation = ""
			case "live-native":
				r.Stage = "awaiting-answer"
			case "pending":
				old.Status, old.Blocking = "pending", true
				old.Params = raw(map[string]any{"questions": []question{{ID: "reply", Question: r.Question}}})
			default:
				old.Status = change
			}
			if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
				t.Fatal(err)
			}
			if err := a.Store.Put(ctx, "attention", old.ID, old, ""); err != nil {
				t.Fatal(err)
			}
			cursor := a.Store.Latest(ctx)
			if err := a.Runs.recoverTextQuestion(ctx, &stale); err != nil {
				t.Fatal(err)
			}
			var after domain.Attention
			if err := a.Store.Get(ctx, "attention", old.ID, &after); err != nil || string(raw(after)) != string(raw(old)) || a.Store.Latest(ctx) != cursor {
				t.Fatalf("fallback changed final or unrelated request: %+v %v", after, err)
			}
		})
	}
}

func TestLegacyUnansweredTextQuestionSurvivesReclassification(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status = "interrupted"
	run.ThreadID = "thread-1"
	_ = a.Store.Put(ctx, "run", run.ID, run, "")
	r := domain.QuestionRecovery{ID: "legacy-question", RunID: run.ID, Stage: "fallback", Question: "Task complete. Which target?"}
	_ = a.Store.Put(ctx, "question-recovery", r.ID, r, "")
	old := domain.Attention{ID: r.ID + ":text", RunID: run.ID, RecoveryID: r.ID, Origin: "text", Method: questionMethod, Status: "pending", Blocking: true, Params: raw(map[string]any{"questions": []question{{ID: "reply", Question: r.Question}}})}
	_ = a.Store.Put(ctx, "attention", old.ID, old, "")
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "question-recovery", r.ID, &r)
		return r.TextAttentionID != ""
	})
	var at domain.Attention
	if err := a.Store.Get(ctx, "attention", recoveryTextID(r), &at); err != nil || at.Status != "pending" || at.RecoveryID != r.ID || !at.Blocking || questionText(at.Params) != "Which target should I use?" || at.ID == old.ID {
		t.Fatalf("confirmed question did not replace legacy text: %+v %v", at, err)
	}
	if r.Confirmation != "classifier" || r.Attempts != 0 {
		t.Fatalf("classification fabricated a repair attempt: %+v", r)
	}
	if err := a.Store.Get(ctx, "attention", old.ID, &old); err != nil || old.Status != "superseded" || questionText(old.Params) != r.SourceText {
		t.Fatalf("reclassification rewrote legacy request history: %+v %v", old, err)
	}
	answer := raw(map[string]any{"answers": map[string]any{"reply": map[string]any{"answers": []string{"One"}}}})
	if err := a.Runs.Answer(ctx, old.ID, answer); err == nil {
		t.Fatal("stale legacy answer targeted the confirmed question")
	}
	if err := a.Runs.DismissQuestion(ctx, old.ID); err == nil {
		t.Fatal("stale legacy dismissal targeted the confirmed question")
	}
	if err := a.Runs.DismissQuestion(ctx, at.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoAttention(t, a)
}

func TestLegacyClarificationSupersedesOnlyMatchingSynthetic(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
	t.Setenv("CF_FAKE_CLARIFICATION", "native")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status = "interrupted"
	run.ThreadID = "thread-1"
	_ = os.MkdirAll(run.Worktree, 0700)
	_ = a.Store.Put(ctx, "run", run.ID, run, "")
	legacy := domain.QuestionRecovery{ID: "legacy", RunID: run.ID, Stage: "fallback", Question: "Task complete"}
	_ = a.Store.Put(ctx, "question-recovery", legacy.ID, legacy, "")
	old := domain.Attention{ID: legacy.ID + ":text", RunID: run.ID, RecoveryID: legacy.ID, Origin: "text", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "reply", Question: legacy.Question}}})}
	_ = a.Store.Put(ctx, "attention", old.ID, old, "")
	other := domain.QuestionRecovery{ID: "real", RunID: run.ID, Stage: "fallback", Question: "Unrelated pending decision?", Confirmation: "native"}
	_ = a.Store.Put(ctx, "question-recovery", other.ID, other, "")
	_ = a.Runs.recoverTextQuestion(ctx, &other)
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	native := pendingQuestion(t, a, "rpc")
	if native.RecoveryID != legacy.ID {
		t.Fatal("wrong recovery supplied form")
	}
	_ = a.Store.Get(ctx, "attention", old.ID, &old)
	if old.Status != "superseded" {
		t.Fatal("native form did not retire matching legacy text")
	}
	var retained domain.Attention
	_ = a.Store.Get(ctx, "attention", other.ID+":text", &retained)
	if retained.Status != "pending" {
		t.Fatal("native form retired unrelated question")
	}
	events, _ := a.Store.Events(ctx, 0, 200)
	found := false
	for i, event := range events {
		if event.Kind == "attention.required" && strings.Contains(string(event.Data), native.ID) {
			if i == 0 || events[i-1].Kind != "attention.resolved" || !strings.Contains(string(events[i-1].Data), old.ID) {
				t.Fatal("replacement events were not emitted atomically")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing replacement attention event")
	}
	a.Runs.Close()
	for n := 0; n < 2; n++ {
		if err := a.Runs.Recover(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Store.Get(ctx, "question-recovery", legacy.ID, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Attempts != 1 || legacy.Confirmation != "native" || legacy.Question != "Which target?" {
		t.Fatalf("restart lost native evidence or shared attempt budget: %+v", legacy)
	}
	if err := a.Store.Get(ctx, "attention", native.ID, &native); err != nil || native.Status != "stale" {
		t.Fatalf("lost native request remained live: %+v %v", native, err)
	}
	if err := a.Store.Get(ctx, "attention", old.ID, &old); err != nil || old.Status != "superseded" || questionText(old.Params) != "Task complete" {
		t.Fatalf("restart rewrote superseded legacy history: %+v %v", old, err)
	}
	var fallback domain.Attention
	if err := a.Store.Get(ctx, "attention", recoveryTextID(legacy), &fallback); err != nil || fallback.Status != "pending" || fallback.ID == old.ID || questionText(fallback.Params) != legacy.Question || fallback.Blocking != native.Blocking {
		t.Fatalf("restart lost answerable native question: %+v %v", fallback, err)
	}
	if err := a.Store.Get(ctx, "attention", retained.ID, &retained); err != nil || retained.Status != "pending" || questionText(retained.Params) != other.Question {
		t.Fatalf("restart changed unrelated unanswered question: %+v %v", retained, err)
	}
	atts, err := store.List[domain.Attention](ctx, a.Store, "attention")
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	for _, at := range atts {
		if at.RecoveryID == legacy.ID && at.Status == "pending" {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("restart produced %d pending questions for one recovery", pending)
	}
	after, err := a.Store.Events(ctx, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for i, event := range events {
		if i >= len(after) || string(raw(event)) != string(raw(after[i])) {
			t.Fatal("restart rewrote attention supersession history")
		}
	}
	answer := raw(map[string]any{"answers": map[string]any{"reply": map[string]any{"answers": []string{"One"}}}})
	if err := a.Runs.Answer(ctx, old.ID, answer); err == nil {
		t.Fatal("stale superseded answer targeted recovered native question")
	}
	if err := a.Runs.DismissQuestion(ctx, old.ID); err == nil {
		t.Fatal("stale superseded dismissal targeted recovered native question")
	}
	if err := a.Runs.Answer(ctx, fallback.ID, answer); err != nil {
		t.Fatalf("fresh recovered question was not answerable: %v", err)
	}
	if err := a.Store.Get(ctx, "attention", fallback.ID, &fallback); err != nil || fallback.Status != "answered" {
		t.Fatalf("fresh recovered answer was not claimed: %+v %v", fallback, err)
	}
	if err := a.Store.Get(ctx, "question-recovery", legacy.ID, &legacy); err != nil || legacy.Stage != "resolved" || legacy.Attempts != 1 {
		t.Fatalf("fresh answer lost recovery or attempt budget: %+v %v", legacy, err)
	}
}

func TestRestartClaimedRecoveryAnswerDoesNotAskAgain(t *testing.T) {
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status = "interrupted"
	run.ThreadID = "thread-1"
	_ = a.Store.Put(ctx, "run", run.ID, run, "")
	r := domain.QuestionRecovery{ID: "answered", RunID: run.ID, Stage: "answered", Question: "Which target?", Confirmation: "native", Attempts: 1}
	_ = a.Store.Put(ctx, "question-recovery", r.ID, r, "")
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Get(ctx, "question-recovery", r.ID, &r)
	if r.Stage != "resolved" || r.Attempts != 1 {
		t.Fatal("claimed recovery answer was replayed or retained as progress")
	}
	assertNoAttention(t, a)
	_ = a.Store.Get(ctx, "run", run.ID, &run)
	if run.Status != "interrupted" {
		t.Fatal("restart continued the original task automatically")
	}
}

func TestNativeAnswerClaimSurvivesProcessLoss(t *testing.T) {
	for _, boundary := range []string{"disconnect-after-answer", "restart-after-attention-claim"} {
		t.Run(boundary, func(t *testing.T) {
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			run.Status, run.ThreadID = "interrupted", "thread-1"
			if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
				t.Fatal(err)
			}
			r := domain.QuestionRecovery{ID: "claimed-native", RunID: run.ID, Stage: "awaiting-answer", Confirmation: "native", Question: "Which target?", Attempts: 1}
			if boundary == "disconnect-after-answer" {
				r.Stage = "answered"
			}
			if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
				t.Fatal(err)
			}
			at := domain.Attention{ID: "claimed", RunID: run.ID, RecoveryID: r.ID, Origin: "rpc", Method: questionMethod, Status: "answered", Params: raw(map[string]any{"questions": []question{{ID: "target", Question: r.Question}}})}
			if err := a.Store.Put(ctx, "attention", at.ID, at, ""); err != nil {
				t.Fatal(err)
			}
			if boundary == "disconnect-after-answer" {
				live := &session{run: run, recovery: &r, pending: map[string]domain.Attention{}}
				a.Runs.onMessage(live, codex.Message{Method: "process/closed"})
			}
			if err := a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			waitRecoveryWork(t, a.Runs)
			if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "resolved" || r.Attempts != 1 {
				t.Fatalf("process loss revived claimed native answer: %+v %v", r, err)
			}
			assertNoAttention(t, a)
			if err := a.Store.Get(ctx, "attention", at.ID, &at); err != nil || at.Status != "answered" {
				t.Fatalf("process loss changed durable answer claim: %+v %v", at, err)
			}
			if _, err := a.Runs.get(run.ID); err == nil {
				t.Fatal("process loss reconnected to replay an answer")
			}
		})
	}
}

func TestTakeoverInvalidatesLegacyClassifierCallback(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "classifier-release")
	t.Setenv("CF_FAKE_GATE_METHOD", "turn/start")
	t.Setenv("CF_FAKE_GATE_FILE", gate)
	defer func() { _ = os.WriteFile(gate, nil, 0600) }()
	record := filepath.Join(t.TempDir(), "protocol.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status = "idle"
	_ = a.Store.Put(ctx, "run", run.ID, run, "")
	live := &session{run: run, pending: map[string]domain.Attention{}, completed: map[string]bool{}}
	a.Runs.mu.Lock()
	a.Runs.live[run.ID] = live
	a.Runs.mu.Unlock()
	r := domain.QuestionRecovery{ID: "legacy", RunID: run.ID, Stage: "fallback", Question: "Which target?"}
	_ = a.Store.Put(ctx, "question-recovery", r.ID, r, "")
	done := make(chan struct{})
	go func() { defer close(done); a.Runs.recheckAmbiguous(run.ID, "", false) }()
	waitFor(t, func() bool { _, err := os.Stat(gate + ".waiting"); return err == nil })
	takeoverCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := a.Runs.Interrupt(takeoverCtx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-takeoverCtx.Done():
		t.Fatal("legacy classifier callback did not finish after takeover")
	}
	assertNoAttention(t, a)
	_ = a.Store.Get(ctx, "question-recovery", r.ID, &r)
	if r.Confirmation != "" {
		t.Fatal("stale classifier survived owner takeover")
	}
}

func TestStartupTakeoverCancelsRecoveryBeforeSessionAttachment(t *testing.T) {
	for _, failure := range []string{"", "invalid"} {
		t.Run("classifier-"+failure, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "complete")
			gate := filepath.Join(t.TempDir(), "classifier-release")
			t.Setenv("CF_FAKE_GATE_METHOD", "turn/start")
			t.Setenv("CF_FAKE_GATE_FILE", gate)
			defer func() { _ = os.WriteFile(gate, nil, 0600) }()
			t.Setenv("CF_FAKE_HELPER_FAILURE", failure)
			t.Setenv("CF_FAKE_CLARIFICATION", "native")
			record := filepath.Join(t.TempDir(), "protocol.jsonl")
			t.Setenv("CF_FAKE_RECORD", record)
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			run.Status, run.ThreadID = "interrupted", "thread-1"
			if err := os.MkdirAll(run.Worktree, 0700); err != nil {
				t.Fatal(err)
			}
			if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
				t.Fatal(err)
			}
			r := domain.QuestionRecovery{ID: "startup-check", RunID: run.ID, Stage: "checking", SourceText: "Which target?"}
			if failure != "" {
				r.Stage = "clarifying"
			}
			if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
				t.Fatal(err)
			}
			if err := a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { _, err := os.Stat(gate + ".waiting"); return err == nil })
			if _, err := a.Runs.get(run.ID); err == nil {
				t.Fatal("startup test already had an attached session")
			}
			if err := a.Runs.Interrupt(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(gate, nil, 0600); err != nil {
				t.Fatal(err)
			}
			waitRecoveryWork(t, a.Runs)
			if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "cancelled" || r.Confirmation != "" || r.Attempts != 0 {
				t.Fatalf("startup callback survived takeover: %+v %v", r, err)
			}
			assertNoAttention(t, a)
			data, err := os.ReadFile(record)
			if err != nil || strings.Contains(string(data), `"method":"thread/resume"`) || strings.Contains(string(data), "question-only recovery clarification") {
				t.Fatalf("takeover reconnected or clarified automatically: %s %v", data, err)
			}
			if _, err := a.Runs.get(run.ID); err == nil {
				t.Fatal("cancelled startup callback attached a session")
			}
			if _, _, err := a.Runs.start(ctx, run.ID, true, "", &r); err == nil {
				t.Fatal("a retained reconnect accepted cancelled recovery")
			}
		})
	}
}

func TestStartupTakeoverCancelsAfterSessionAttachment(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_CLARIFICATION", "native")
	gate := filepath.Join(t.TempDir(), "initialize-release")
	t.Setenv("CF_FAKE_GATE_METHOD", "initialize")
	t.Setenv("CF_FAKE_GATE_FILE", gate)
	record := gate + ".protocol"
	t.Setenv("CF_FAKE_RECORD", record)
	defer func() { _ = os.WriteFile(gate, nil, 0600) }()
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ThreadID = "interrupted", "thread-1"
	if err := os.MkdirAll(run.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	r := domain.QuestionRecovery{ID: "attaching-clarification", RunID: run.ID, Stage: "clarifying", SourceText: "Task complete"}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Runs.start(ctx, run.ID, true, "", &r); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(gate + ".waiting"); return err == nil })
	var claimed domain.QuestionRecovery
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &claimed); err != nil || claimed.Attempts != 1 {
		t.Fatalf("setup did not durably claim before delivery: %+v %v", claimed, err)
	}
	before, err := os.ReadFile(record)
	if err != nil || strings.Contains(string(before), `"method":"turn/start"`) {
		t.Fatalf("gated setup already delivered a turn: %s %v", before, err)
	}
	takeoverCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := a.Runs.Interrupt(takeoverCtx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitRecoveryWork(t, a.Runs)
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "cancelled" || r.Confirmation != "" || r.Attempts != 1 {
		t.Fatalf("session attachment raced past takeover: %+v %v", r, err)
	}

	live, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Runs.onMessage(live, codex.Message{ID: raw(88), Method: questionMethod, Params: raw(map[string]any{"questions": []question{{ID: "late", Question: "Late setup callback?", IsOther: true}}})})
	a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"item": map[string]any{"id": "late-async", "type": "agentMessage", "text": "Late async?", "questions": []map[string]any{{"title": "Late async?", "options": []string{"One", "Two"}}}}})})
	assertNoAttention(t, a)
	after, err := os.ReadFile(record)
	if err != nil || strings.Contains(string(after), `"method":"turn/start"`) {
		t.Fatalf("cancelled setup delivered a turn: %s %v", after, err)
	}
	t.Setenv("CF_FAKE_GATE_METHOD", "")
	t.Setenv("CF_FAKE_MODE", "question")
	if err := a.Runs.Message(ctx, run.ID, "Ask a new genuine question", ""); err != nil {
		t.Fatal(err)
	}
	genuine := pendingQuestion(t, a, "rpc")
	if genuine.RunID != run.ID || genuine.RecoveryID != "" {
		t.Fatalf("owner continuation did not create its own genuine request: %+v", genuine)
	}
}

func TestHistoricalClassifierDoesNotBlockAnotherRunsQuestion(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "recover-first")
	t.Setenv("CF_FAKE_HELPER_FAILURE", "timeout")
	record := filepath.Join(t.TempDir(), "protocol.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	historical := prepareRun(t, a, ctx)
	historical.Status = "interrupted"
	if err := a.Store.Put(ctx, "run", historical.ID, historical, ""); err != nil {
		t.Fatal(err)
	}
	r := domain.QuestionRecovery{ID: "stalled-historical", RunID: historical.ID, Stage: "checking", SourceText: "Task complete"}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	historicalDone := make(chan struct{})
	a.Runs.background(func() { defer close(historicalDone); a.Runs.recheckAmbiguous(historical.ID, "", false) })
	waitFor(t, func() bool { data, _ := os.ReadFile(record); return strings.Contains(string(data), "outputSchema") })
	// The existing historical process retains its captured timeout fixture;
	// subsequently spawned task/classifier processes can complete normally.
	t.Setenv("CF_FAKE_HELPER_FAILURE", "")
	fresh := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, fresh.ID, false); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		atts, err := store.List[domain.Attention](ctx, a.Store, "attention")
		if err != nil {
			t.Fatal(err)
		}
		for _, at := range atts {
			if at.RunID == fresh.ID && at.Status == "pending" && at.Method == questionMethod {
				select {
				case <-historicalDone:
					t.Fatal("historical classifier completed before the independent question")
				default:
					return
				}
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("another run's genuine question waited on the historical classifier")
		case <-tick.C:
		}
	}
}

func TestRetainedSetupFailureExhaustsSharedBudget(t *testing.T) {
	for _, method := range []string{"initialize", "thread/resume", "missing-thread", "missing-worktree"} {
		t.Run(method, func(t *testing.T) {
			t.Setenv("CF_FAKE_SETUP_FAILURE", method)
			record := filepath.Join(t.TempDir(), "protocol.jsonl")
			t.Setenv("CF_FAKE_RECORD", record)
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			run.Status, run.ThreadID = "interrupted", "thread-1"
			if method == "missing-thread" {
				run.ThreadID = ""
			}
			if method != "missing-worktree" {
				if err := os.MkdirAll(run.Worktree, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
				t.Fatal(err)
			}
			attempts := 0
			if method == "thread/resume" {
				attempts = 1
			}
			r := domain.QuestionRecovery{ID: "setup-failure", RunID: run.ID, Stage: "clarifying", SourceText: "Task complete", Attempts: attempts}
			if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
				t.Fatal(err)
			}
			if _, _, err := a.Runs.start(ctx, run.ID, true, "", &r); err != nil {
				t.Fatal(err)
			}
			waitRecoveryWork(t, a.Runs)
			// Observe failed setup/closure state before checking subsequent
			// startup; no delivered turn is fabricated for failed setup.
			waitFor(t, func() bool {
				events, _ := a.Store.Events(ctx, 0, 200)
				failed := 0
				for _, event := range events {
					if event.Kind == "run.state" && event.Entity == run.ID && strings.Contains(string(event.Data), `"status":"failed"`) {
						failed++
					}
				}
				return failed >= 2
			})
			for n := 0; n < 2; n++ {
				a.Runs.Close()
				if err := a.Runs.Recover(ctx); err != nil {
					t.Fatal(err)
				}
				waitRecoveryWork(t, a.Runs)
			}
			if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "unresolved" || r.Attempts != 2 || r.DiagnosticError != clarificationUnavailable {
				t.Fatalf("setup failure did not retain terminal diagnostics/budget: %+v %v", r, err)
			}
			assertNoAttention(t, a)
			data, err := os.ReadFile(record)
			expectedProcesses := 2 - attempts
			if strings.HasPrefix(method, "missing-") {
				expectedProcesses = 0
			}
			if err != nil && !(os.IsNotExist(err) && expectedProcesses == 0) || strings.Count(string(data), `"method":"initialize"`) != expectedProcesses || strings.Contains(string(data), `"method":"turn/start"`) {
				t.Fatalf("setup failure relaunched or delivered a turn: %s %v", data, err)
			}
		})
	}
}

func TestStalledRecoveryTakeoverDoesNotBlockAnotherRunsQuestion(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "recover-first")
	gate := filepath.Join(t.TempDir(), "turn-release")
	t.Setenv("CF_FAKE_GATE_METHOD", "turn/start")
	t.Setenv("CF_FAKE_GATE_FILE", gate)
	defer func() { _ = os.WriteFile(gate, nil, 0600) }()
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ThreadID = "interrupted", "thread-1"
	if err := os.MkdirAll(run.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	r := domain.QuestionRecovery{ID: "stalled-turn", RunID: run.ID, Stage: "clarifying", SourceText: "Task complete"}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Runs.start(ctx, run.ID, true, "", &r); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(gate + ".waiting"); return err == nil })
	t.Setenv("CF_FAKE_GATE_METHOD", "")
	takeoverDone := make(chan error, 1)
	go func() { takeoverDone <- a.Runs.Interrupt(ctx, run.ID) }()
	// The provider gate holds session.mu. Observe Interrupt actually blocked
	// on that mutex before testing another run; merely starting its goroutine
	// would allow it to be scheduled only after the gate was released.
	waitFor(t, func() bool {
		stacks := make([]byte, 1<<20)
		stacks = stacks[:runtime.Stack(stacks, true)]
		for _, stack := range strings.Split(string(stacks), "\n\n") {
			if strings.Contains(stack, "[sync.Mutex.Lock") && strings.Contains(stack, "(*Manager).Interrupt(") {
				return true
			}
		}
		return false
	})
	fresh := prepareRun(t, a, ctx)
	started := make(chan error, 1)
	go func() { _, err := a.Runs.Start(ctx, fresh.ID, false); started <- err }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("takeover of a stalled turn blocked another run's start")
	}
	newQuestion := pendingQuestion(t, a, "rpc")
	if newQuestion.RunID != fresh.ID {
		t.Fatal("wrong run supplied the independent question")
	}
	select {
	case err := <-takeoverDone:
		t.Fatalf("takeover finished before the gated turn was released: %v", err)
	default:
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-takeoverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("takeover did not finish after the gated turn was released")
	}
}

func TestSetupFailureStillRechecksLaterSuccessfulWork(t *testing.T) {
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ThreadID = "idle", "thread-1"
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	r := domain.QuestionRecovery{ID: "unavailable-legacy", RunID: run.ID, Stage: "unresolved", SourceText: "Which target?", Attempts: 1, DiagnosticError: clarificationUnavailable}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	old := domain.Attention{ID: r.ID + ":text", RunID: run.ID, RecoveryID: r.ID, Origin: "text", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "reply", Question: r.SourceText}}})}
	if err := a.Store.Put(ctx, "attention", old.ID, old, ""); err != nil {
		t.Fatal(err)
	}
	live := &session{run: run, pending: map[string]domain.Attention{old.ID: old}, completed: map[string]bool{}}
	a.Runs.mu.Lock()
	a.Runs.live[run.ID] = live
	a.Runs.mu.Unlock()
	a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"item": map[string]string{"type": "agentMessage", "text": "superseded completion"}})})
	a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]string{"id": "owner-directed-later-work", "status": "completed"}})})
	waitRecoveryWork(t, a.Runs)
	if err := a.Store.Get(ctx, "attention", old.ID, &old); err != nil || old.Status != "resolved" {
		t.Fatalf("unavailable setup suppressed later obsolescence evidence: %+v %v", old, err)
	}
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "resolved" || r.Attempts != 1 {
		t.Fatalf("later evidence changed the durable attempt budget: %+v %v", r, err)
	}
	assertNoAttention(t, a)
}

func TestHistoricalRetirementStillChecksLatestQuestion(t *testing.T) {
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ThreadID = "idle", "thread-1"
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	legacy := domain.QuestionRecovery{ID: "obsolete-before-later-question", RunID: run.ID, Stage: "fallback", Question: "superseded completion"}
	if err := a.Store.Put(ctx, "question-recovery", legacy.ID, legacy, ""); err != nil {
		t.Fatal(err)
	}
	old := domain.Attention{ID: legacy.ID + ":text", RunID: run.ID, RecoveryID: legacy.ID, Origin: "text", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "reply", Question: legacy.Question}}})}
	if err := a.Store.Put(ctx, "attention", old.ID, old, ""); err != nil {
		t.Fatal(err)
	}
	live := &session{run: run, pending: map[string]domain.Attention{old.ID: old}, completed: map[string]bool{}}
	a.Runs.mu.Lock()
	a.Runs.live[run.ID] = live
	a.Runs.mu.Unlock()
	a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"item": map[string]string{"type": "agentMessage", "text": "Which target?"}})})
	a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]string{"id": "latest-question", "status": "completed"}})})
	waitRecoveryWork(t, a.Runs)
	if err := a.Store.Get(ctx, "attention", old.ID, &old); err != nil || old.Status != "resolved" {
		t.Fatalf("older request did not retire before new detection: %+v %v", old, err)
	}
	at := pendingQuestion(t, a, "text")
	if at.RecoveryID == legacy.ID || questionText(at.Params) != "Which target should I use?" {
		t.Fatalf("historical retirement lost the latest genuine question: %+v", at)
	}
}

func TestLatestDetectionPreservesActiveHistoricalClarification(t *testing.T) {
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status = "idle"
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	legacy := domain.QuestionRecovery{ID: "historical-before-repair-start", RunID: run.ID, Stage: "clarifying", SourceText: "Task complete"}
	if err := a.Store.Put(ctx, "question-recovery", legacy.ID, legacy, ""); err != nil {
		t.Fatal(err)
	}
	// Stage the lock-owned boundary after the failed historical check installs
	// clarification and before its queued repair obtains the session lock.
	live := &session{run: run, recovery: &legacy, lastCompletedTurn: "latest-completion", pending: map[string]domain.Attention{}, completed: map[string]bool{}}
	a.Runs.checkStoppedTurn(live, "latest-completion", "Task complete", 0)
	live.mu.Lock()
	retained := live.recovery == &legacy && legacy.Stage == "clarifying"
	live.mu.Unlock()
	if !retained {
		t.Fatal("latest detection replaced the active historical clarification")
	}
	var unexpected domain.QuestionRecovery
	if err := a.Store.Get(ctx, "question-recovery", run.ID+":latest-completion", &unexpected); err != store.ErrNotFound {
		t.Fatalf("latest detection created a competing recovery: %+v %v", unexpected, err)
	}
}

func TestHistoricalClarificationStillChecksLatestQuestion(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_CLARIFICATION", "hold")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		var initial domain.QuestionRecovery
		return a.Store.Get(ctx, "question-recovery", run.ID+":turn-1", &initial) == nil && initial.Stage == "resolved"
	})
	waitRecoveryWork(t, a.Runs)
	legacy := domain.QuestionRecovery{ID: "historical-failed-recheck", RunID: run.ID, Stage: "checking", SourceText: "Task complete"}
	if err := a.Store.Put(ctx, "question-recovery", legacy.ID, legacy, ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
	live, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"item": map[string]string{"type": "agentMessage", "text": "Which target?"}})})
	a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]string{"id": "latest-question-after-history", "status": "completed"}})})
	waitFor(t, func() bool {
		return a.Store.Get(ctx, "question-recovery", legacy.ID, &legacy) == nil && legacy.Stage == "clarifying" && legacy.RepairTurnID != ""
	})
	var latest domain.QuestionRecovery
	if err := a.Store.Get(ctx, "question-recovery", run.ID+":latest-question-after-history", &latest); err != store.ErrNotFound {
		t.Fatalf("latest detection overtook historical clarification: %+v %v", latest, err)
	}
	// The real fixture clarification is held; inject its negative result and
	// completion, then permit successful classification of the latest response.
	t.Setenv("CF_FAKE_HELPER_FAILURE", "")
	a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"item": map[string]string{"type": "agentMessage", "text": `{"needsInput":false,"question":""}`}})})
	a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]string{"id": legacy.RepairTurnID, "status": "completed"}})})
	waitRecoveryWork(t, a.Runs)
	if err := a.Store.Get(ctx, "question-recovery", legacy.ID, &legacy); err != nil || legacy.Stage != "resolved" || legacy.Attempts != 1 {
		t.Fatalf("historical clarification lost its final state or attempt: %+v %v", legacy, err)
	}
	at := pendingQuestion(t, a, "text")
	if at.RecoveryID != run.ID+":latest-question-after-history" || at.TurnID != "latest-question-after-history" || questionText(at.Params) != "Which target should I use?" {
		t.Fatalf("completed historical clarification lost the latest genuine question: %+v", at)
	}
}

func TestRestartNativeEvidencePreservesNonblockingQuestion(t *testing.T) {
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status = "interrupted"
	_ = a.Store.Put(ctx, "run", run.ID, run, "")
	r := domain.QuestionRecovery{ID: "unfinished", RunID: run.ID, Stage: "checking", Question: "Task complete", Attempts: 1}
	_ = a.Store.Put(ctx, "question-recovery", r.ID, r, "")
	old := domain.Attention{ID: r.ID + ":text", RunID: run.ID, RecoveryID: r.ID, Origin: "text", Method: questionMethod, Blocking: true, Status: "superseded", Params: raw(map[string]any{"questions": []question{{ID: "reply", Question: "Task complete"}}})}
	_ = a.Store.Put(ctx, "attention", old.ID, old, "")
	native := domain.Attention{ID: "native", RunID: run.ID, RecoveryID: r.ID, Origin: "rpc", Method: questionMethod, Blocking: false, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "target", Question: "Which target?"}}})}
	_ = a.Store.Put(ctx, "attention", native.ID, native, "")
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	at := pendingQuestion(t, a, "text")
	_ = a.Store.Get(ctx, "question-recovery", r.ID, &r)
	if at.ID == old.ID || at.ID != r.TextAttentionID || at.Blocking || questionText(at.Params) != "Which target?" || r.Confirmation != "native" || r.Question != "Which target?" || r.Attempts != 1 {
		t.Fatalf("known native evidence or delivery lost: %+v %+v", r, at)
	}
}

func TestAsyncRecoveryRetainsSingleRouteAcrossRestart(t *testing.T) {
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ThreadID = "interrupted", "thread-1"
	if err := os.MkdirAll(run.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	blocking := false
	r := domain.QuestionRecovery{ID: "async-recovery", RunID: run.ID, Stage: "awaiting-answer", Question: "Which target?", Confirmation: "native", Attempts: 1, OriginalBlocking: &blocking}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	at := domain.Attention{ID: "original-async", RunID: run.ID, RecoveryID: r.ID, Origin: "async", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "q1", Question: r.Question, IsOther: true}}})}
	if err := a.Store.Put(ctx, "attention", at.ID, at, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitRecoveryWork(t, a.Runs)
	atts, err := store.List[domain.Attention](ctx, a.Store, "attention")
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	for _, candidate := range atts {
		if candidate.RecoveryID == r.ID && candidate.Status == "pending" {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("restart retained %d answer routes: %+v", pending, atts)
	}
}
func TestAsyncRecoveryUncertainClaimSurvivesStaleSnapshotAndRestart(t *testing.T) {
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ThreadID = "interrupted", "thread-1"
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	blocking := false
	r := domain.QuestionRecovery{ID: "async-claimed", RunID: run.ID, Stage: "awaiting-answer", Question: "Which target?", Confirmation: "native", Attempts: 1, OriginalBlocking: &blocking}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	at := domain.Attention{ID: "original-async", RunID: run.ID, RecoveryID: r.ID, Origin: "async", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "q1", Question: r.Question, IsOther: true}}})}
	if err := a.Store.Put(ctx, "attention", at.ID, at, ""); err != nil {
		t.Fatal(err)
	}
	answer := raw(map[string]any{"answers": map[string]any{"q1": map[string]any{"answers": []string{"One"}}}})
	if err := a.Runs.Answer(ctx, at.ID, answer); err == nil {
		t.Fatal("missing worktree should make delivery uncertain")
	}
	for _, stage := range []string{"awaiting-answer", "fallback"} {
		stale := r
		stale.Stage = stage
		if err := a.Runs.saveRecovery(&stale); err != nil || stale.Stage != "resolved" {
			t.Fatalf("stale recovery snapshot reopened an uncertain originating claim: %+v %v", stale, err)
		}
	}
	a.Runs.Close()
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitRecoveryWork(t, a.Runs)
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "attention", at.ID, &at); err != nil {
		t.Fatal(err)
	}
	if r.Stage != "resolved" {
		t.Fatalf("uncertain async claim did not resolve originating recovery: %+v %+v", r, at)
	}
	assertNoAttention(t, a)
}

func TestAsyncRecoveryRetiresOnlyMatchingDuplicate(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ThreadID = "interrupted", "thread-1"
	if err := os.MkdirAll(run.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	blocking := false
	r := domain.QuestionRecovery{ID: "already-duplicated", RunID: run.ID, Stage: "fallback", Question: "Which target?", Confirmation: "native", OriginalBlocking: &blocking, Attempts: 2}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	async := domain.Attention{ID: "durable-async", RunID: run.ID, RecoveryID: r.ID, Origin: "async", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "q1", Question: r.Question, IsOther: true}}})}
	text := domain.Attention{ID: recoveryTextID(r), RunID: run.ID, RecoveryID: r.ID, Origin: "text", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "reply", Question: r.Question, IsOther: true}}})}
	unrelated := async
	unrelated.ID = "other-question"
	unrelated.RecoveryID = "other-recovery"
	otherRun := text
	otherRun.ID = "other-run-question"
	otherRun.RunID = "other-run"
	for _, at := range []domain.Attention{async, text, unrelated, otherRun} {
		if err := a.Store.Put(ctx, "attention", at.ID, at, ""); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 2; n++ {
		if err := a.Runs.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		waitRecoveryWork(t, a.Runs)
	}
	var old domain.Attention
	if err := a.Store.Get(ctx, "attention", text.ID, &old); err != nil || old.Status != "superseded" || string(old.Params) != string(text.Params) {
		t.Fatalf("duplicate retirement lost history: %+v %v", old, err)
	}
	if err := a.Store.Get(ctx, "attention", async.ID, &async); err != nil || async.Status != "pending" || async.Blocking {
		t.Fatalf("async identity or delivery changed: %+v %v", async, err)
	}
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "awaiting-answer" || r.Attempts != 2 || r.Confirmation != "native" || recoveryBlocking(r) {
		t.Fatalf("originating recovery changed: %+v %v", r, err)
	}
	for _, want := range []domain.Attention{unrelated, otherRun} {
		var got domain.Attention
		if err := a.Store.Get(ctx, "attention", want.ID, &got); err != nil || string(raw(got)) != string(raw(want)) {
			t.Fatalf("retired unrelated request: %+v %v", got, err)
		}
	}
	if err := a.Runs.saveAttention(ctx, text, "attention.required"); err == nil {
		t.Fatal("stale text snapshot reopened terminal request")
	}
	if err := a.Store.Get(ctx, "attention", text.ID, &old); err != nil || old.Status != "superseded" {
		t.Fatalf("stale snapshot changed retired text: %+v %v", old, err)
	}
	events, err := a.Store.Events(ctx, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Kind == "attention.resolved" && strings.Contains(string(event.Data), text.ID) {
			found = true
		}
	}
	if !found {
		t.Fatal("duplicate retirement omitted resolution history")
	}
	answer := raw(map[string]any{"answers": map[string]any{"q1": map[string]any{"answers": []string{"One"}}}})
	if err := a.Runs.Answer(ctx, async.ID, answer); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.saveAttention(ctx, domain.Attention{ID: "late-async", RunID: run.ID, RecoveryID: r.ID, Origin: "async", Method: questionMethod, Status: "pending", Params: async.Params}, "attention.required"); err == nil {
		t.Fatal("resolved origin accepted a fresh sibling route")
	}
	var completed domain.QuestionRecovery
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &completed); err != nil || completed.Stage != "resolved" || completed.Attempts != 2 {
		t.Fatalf("async answer lost durable originating claim: %+v %v", completed, err)
	}
}
func TestOriginatingRecoveryClaimsExcludeSiblingAnswers(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ThreadID = "interrupted", "thread-1"
	if err := os.MkdirAll(run.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	r := domain.QuestionRecovery{ID: "sibling-claim", RunID: run.ID, Stage: "fallback", Question: "Which target?", Confirmation: "native", Attempts: 1}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	async := domain.Attention{ID: "sibling-async", RunID: run.ID, RecoveryID: r.ID, Origin: "async", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "q1", Question: r.Question, IsOther: true}}})}
	text := domain.Attention{ID: recoveryTextID(r), RunID: run.ID, RecoveryID: r.ID, Origin: "text", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "reply", Question: r.Question, IsOther: true}}})}
	for _, at := range []domain.Attention{async, text} {
		if err := a.Store.Put(ctx, "attention", at.ID, at, ""); err != nil {
			t.Fatal(err)
		}
	}
	claimCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for _, at := range []domain.Attention{async, text} {
		go func(at domain.Attention) {
			id := "reply"
			if at.Origin == "async" {
				id = "q1"
			}
			results <- a.Runs.Answer(claimCtx, at.ID, raw(map[string]any{"answers": map[string]any{id: map[string]any{"answers": []string{"One"}}}}))
		}(at)
	}
	accepted := 0
	for n := 0; n < 2; n++ {
		select {
		case err := <-results:
			if err == nil {
				accepted++
			}
		case <-claimCtx.Done():
			t.Fatal("sibling answer claims stalled")
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d answers for one originating recovery", accepted)
	}
	atts, err := store.List[domain.Attention](ctx, a.Store, "attention")
	if err != nil {
		t.Fatal(err)
	}
	claimed := 0
	for _, at := range atts {
		if at.RecoveryID == r.ID {
			if at.Status == "pending" {
				t.Fatal("originating claim left a pending sibling")
			}
			if at.Status == "answered" {
				claimed++
			}
		}
	}
	if claimed != 1 {
		t.Fatalf("retained %d durable answer claims", claimed)
	}
	a.Runs.Close()
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitRecoveryWork(t, a.Runs)
	assertNoAttention(t, a)
}

func TestRetainedSetupReusesClaimForLastPermittedTurn(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_CLARIFICATION", "native")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ThreadID = "interrupted", "thread-1"
	if err := os.MkdirAll(run.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	r := domain.QuestionRecovery{ID: "last-intended-turn", RunID: run.ID, Stage: "clarifying", SourceText: "Task complete", Attempts: 1}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Runs.start(ctx, run.ID, true, "", &r); err != nil {
		t.Fatal(err)
	}
	at := pendingQuestion(t, a, "rpc")
	var persisted domain.QuestionRecovery
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &persisted); err != nil || persisted.Attempts != 2 || persisted.Confirmation != "native" || at.RecoveryID != r.ID {
		t.Fatalf("setup double-counted or skipped last permitted turn: %+v %+v %v", persisted, at, err)
	}
}
func TestRestartUnavailableSetupUsesRemainingBudget(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
	t.Setenv("CF_FAKE_SETUP_FAILURE", "thread/resume")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ThreadID = "interrupted", "thread-1"
	if err := os.MkdirAll(run.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	r := domain.QuestionRecovery{ID: "older-early-stop", RunID: run.ID, Stage: "unresolved", SourceText: "Task complete", Attempts: 1, DiagnosticError: clarificationUnavailable}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitRecoveryWork(t, a.Runs)
	a.Runs.Close()
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitRecoveryWork(t, a.Runs)
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "unresolved" || r.Attempts != 2 || r.DiagnosticError != clarificationUnavailable {
		t.Fatalf("restart did not consume only remaining durable attempt: %+v %v", r, err)
	}
	assertNoAttention(t, a)
}

func TestAsyncRecoveryTakeoverPreservesGenuineQuestion(t *testing.T) {
	for _, timing := range []string{"takeover-before-restart", "restart-before-takeover"} {
		t.Run(timing, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "complete")
			t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
			t.Setenv("CF_FAKE_CLARIFICATION", "hold")
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			var r domain.QuestionRecovery
			waitFor(t, func() bool {
				records, _ := store.List[domain.QuestionRecovery](ctx, a.Store, "question-recovery")
				for _, candidate := range records {
					if candidate.RunID == run.ID && candidate.Stage == "clarifying" && candidate.RepairTurnID != "" {
						r = candidate
						return true
					}
				}
				return false
			})
			live, err := a.Runs.get(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"turnId": r.RepairTurnID, "item": map[string]any{"id": "clarification-async", "type": "agentMessage", "text": "Which target?", "questions": []map[string]any{{"title": "Which target?", "options": []string{"One", "Two"}}}}})})
			at := pendingQuestion(t, a, "async")
			if at.RecoveryID != r.ID || at.Blocking {
				t.Fatalf("clarification did not retain genuine nonblocking async question: %+v", at)
			}
			if timing == "restart-before-takeover" {
				a.Runs.Close()
				if err := a.Runs.Recover(ctx); err != nil {
					t.Fatal(err)
				}
				waitRecoveryWork(t, a.Runs)
				if _, err := a.Runs.get(run.ID); err == nil {
					t.Fatal("restart unexpectedly attached a live session")
				}
			}
			takeoverCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			if err := a.Runs.Interrupt(takeoverCtx, run.ID); err != nil {
				t.Fatal(err)
			}
			a.Runs.Close()
			if err := a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			waitRecoveryWork(t, a.Runs)
			if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "cancelled" || r.Attempts != 1 {
				t.Fatalf("takeover lost automatic-work cancellation or its claim: %+v %v", r, err)
			}
			if err := a.Store.Get(ctx, "attention", at.ID, &at); err != nil || at.Status != "pending" || at.Blocking {
				t.Fatalf("takeover inferred that genuine async question was obsolete: %+v %v", at, err)
			}
			t.Setenv("CF_FAKE_HELPER_FAILURE", "")
			t.Setenv("CF_FAKE_CLARIFICATION", "")
			answer := raw(map[string]any{"answers": map[string]any{"q1": map[string]any{"answers": []string{"One"}}}})
			if err := a.Runs.Answer(ctx, at.ID, answer); err != nil {
				t.Fatalf("cancelled automatic recovery stranded genuine async answer: %v", err)
			}
			if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "resolved" || r.Attempts != 1 {
				t.Fatalf("explicit answer lost originating final claim: %+v %v", r, err)
			}
			if err := a.Store.Get(ctx, "attention", at.ID, &at); err != nil || at.Status != "answered" {
				t.Fatalf("async answer was not claimed: %+v %v", at, err)
			}
		})
	}
}

func TestAsyncRecoveryDisconnectRetainsOnlyAsyncRoute(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
	t.Setenv("CF_FAKE_CLARIFICATION", "hold")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	var r domain.QuestionRecovery
	waitFor(t, func() bool {
		records, _ := store.List[domain.QuestionRecovery](ctx, a.Store, "question-recovery")
		for _, candidate := range records {
			if candidate.RunID == run.ID && candidate.Stage == "clarifying" && candidate.RepairTurnID != "" {
				r = candidate
				return true
			}
		}
		return false
	})
	live, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"turnId": r.RepairTurnID, "item": map[string]any{"id": "disconnect-async", "type": "agentMessage", "text": "Which target?", "questions": []map[string]any{{"title": "Which target?", "options": []string{"One", "Two"}}}}})})
	at := pendingQuestion(t, a, "async")
	live.mu.Lock()
	client := live.client
	live.mu.Unlock()
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		var current domain.Run
		return a.Store.Get(ctx, "run", run.ID, &current) == nil && current.Status == "interrupted" && current.TurnID == ""
	})
	// process/closed persists interrupted state before fallback. Joining its
	// session lock waits for the complete callback before counting answer routes.
	live.mu.Lock()
	live.mu.Unlock()
	atts, err := store.List[domain.Attention](ctx, a.Store, "attention")
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	for _, candidate := range atts {
		if candidate.RecoveryID == r.ID && candidate.Status == "pending" {
			pending++
			if candidate.ID != at.ID || candidate.Origin != "async" || candidate.Blocking {
				t.Fatalf("disconnect materialized a second answer route: %+v", candidate)
			}
		}
	}
	if pending != 1 {
		t.Fatalf("disconnect retained %d answer routes", pending)
	}
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Attempts != 1 || r.Stage != "awaiting-answer" || recoveryBlocking(r) {
		t.Fatalf("disconnect changed originating attempts/delivery: %+v %v", r, err)
	}
}
func TestRejectedAsyncRecoveryAnswerPreventsQueuedContinuation(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
	t.Setenv("CF_FAKE_CLARIFICATION", "hold")
	t.Setenv("CF_FAKE_SETUP_FAILURE", "turn/steer")
	gate := filepath.Join(t.TempDir(), "steer-release")
	t.Setenv("CF_FAKE_GATE_METHOD", "turn/steer")
	t.Setenv("CF_FAKE_GATE_FILE", gate)
	record := filepath.Join(t.TempDir(), "protocol.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	defer func() { _ = os.WriteFile(gate, nil, 0600) }()
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	var r domain.QuestionRecovery
	waitFor(t, func() bool {
		records, _ := store.List[domain.QuestionRecovery](ctx, a.Store, "question-recovery")
		for _, candidate := range records {
			if candidate.RunID == run.ID && candidate.Stage == "clarifying" && candidate.RepairTurnID != "" {
				r = candidate
				return true
			}
		}
		return false
	})
	live, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"turnId": r.RepairTurnID, "item": map[string]any{"id": "answer-async", "type": "agentMessage", "text": "Which target?", "questions": []map[string]any{{"title": "Which target?", "options": []string{"One", "Two"}}}}})})
	at := pendingQuestion(t, a, "async")
	answer := raw(map[string]any{"answers": map[string]any{"q1": map[string]any{"answers": []string{"One"}}}})
	answerDone := make(chan error, 1)
	go func() { answerDone <- a.Runs.Answer(ctx, at.ID, answer) }()
	waitFor(t, func() bool { _, err := os.Stat(gate + ".waiting"); return err == nil })
	completed := make(chan struct{})
	go func() {
		a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]any{"id": r.RepairTurnID, "status": "completed"}})})
		close(completed)
	}()
	// Queue a real completion callback behind the answer's locked delivery.
	waitFor(t, func() bool {
		stacks := make([]byte, 1<<20)
		stacks = stacks[:runtime.Stack(stacks, true)]
		for _, stack := range strings.Split(string(stacks), "\n\n") {
			if strings.Contains(stack, "[sync.Mutex.Lock") && strings.Contains(stack, "(*Manager).onMessage(") {
				return true
			}
		}
		return false
	})
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-answerDone:
		if err == nil || !strings.Contains(err.Error(), "could not be confirmed") {
			t.Fatalf("rejected steer did not report uncertain delivery: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("answer did not finish after delivery rejection")
	}
	select {
	case <-completed:
	case <-time.After(3 * time.Second):
		t.Fatal("queued completion did not finish")
	}
	waitRecoveryWork(t, a.Runs)
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Continue the original task using the user's answer") {
		t.Fatal("queued completion resumed the task after rejected answer delivery")
	}
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "resolved" || r.Attempts != 1 {
		t.Fatalf("failed delivery lost final originating claim or budget: %+v %v", r, err)
	}
	if err := a.Store.Get(ctx, "attention", at.ID, &at); err != nil || at.Status != "uncertain" {
		t.Fatalf("failed answer did not retain uncertain delivery: %+v %v", at, err)
	}
	if err := a.Runs.Answer(ctx, at.ID, answer); err == nil {
		t.Fatal("failed answer became replayable")
	}
}

func TestLiveAsyncRecoveryAnswerRestoresOriginalPolicy(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
	t.Setenv("CF_FAKE_CLARIFICATION", "hold")
	t.Setenv("CF_FAKE_COMPLETE_STEER", "1")
	record := filepath.Join(t.TempDir(), "protocol.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Profile.Mode, run.Profile.Model, run.Profile.Effort = "default", "task-model", "low"
	run.Profile.Sandbox, run.Profile.Approval = "workspace-write", "on-request"
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	originalProfile := string(raw(run.Profile))
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	var r domain.QuestionRecovery
	waitFor(t, func() bool {
		records, _ := store.List[domain.QuestionRecovery](ctx, a.Store, "question-recovery")
		for _, candidate := range records {
			if candidate.RunID == run.ID && candidate.Stage == "clarifying" && candidate.RepairTurnID != "" {
				r = candidate
				return true
			}
		}
		return false
	})
	live, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"turnId": r.RepairTurnID, "item": map[string]any{"id": "answer-async", "type": "agentMessage", "text": "Which target?", "questions": []map[string]any{{"title": "Which target?", "options": []string{"One", "Two"}}}}})})
	at := pendingQuestion(t, a, "async")
	duplicate := domain.Attention{ID: recoveryTextID(r), RunID: run.ID, RecoveryID: r.ID, Origin: "text", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "reply", Question: "Which target?", IsOther: true}}})}
	live.mu.Lock()
	if err := a.Store.Put(ctx, "attention", duplicate.ID, duplicate, ""); err != nil {
		live.mu.Unlock()
		t.Fatal(err)
	}
	live.pending[duplicate.ID] = duplicate
	live.mu.Unlock()
	t.Setenv("CF_FAKE_HELPER_FAILURE", "")
	answer := raw(map[string]any{"answers": map[string]any{"q1": map[string]any{"answers": []string{"One"}}}})
	if err := a.Runs.Answer(ctx, at.ID, answer); err != nil {
		t.Fatal(err)
	}
	var continuation json.RawMessage
	waitFor(t, func() bool {
		data, _ := os.ReadFile(record)
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var call struct {
				Method string
				Params json.RawMessage
			}
			if json.Unmarshal([]byte(line), &call) == nil && call.Method == "turn/start" && strings.Contains(string(call.Params), "Continue the original task using the user's answer") {
				continuation = call.Params
				return true
			}
		}
		return false
	})
	live.mu.Lock()
	_, stillPending := live.pending[duplicate.ID]
	live.mu.Unlock()
	if stillPending {
		t.Fatal("originating answer left a retired sibling blocking its live session")
	}
	var retired domain.Attention
	if err := a.Store.Get(ctx, "attention", duplicate.ID, &retired); err != nil || retired.Status != "resolved" || string(retired.Params) != string(duplicate.Params) {
		t.Fatalf("live answer did not retire matching duplicate with history intact: %+v %v", retired, err)
	}
	var policy struct {
		ApprovalPolicy    string
		SandboxPolicy     struct{ Type string }
		CollaborationMode struct {
			Mode     string
			Settings struct {
				Model           string
				ReasoningEffort string `json:"reasoning_effort"`
			}
		}
	}
	if err := json.Unmarshal(continuation, &policy); err != nil || policy.ApprovalPolicy != run.Profile.Approval || policy.SandboxPolicy.Type != "workspaceWrite" || policy.CollaborationMode.Mode != "default" || policy.CollaborationMode.Settings.Model != run.Profile.Model || policy.CollaborationMode.Settings.ReasoningEffort != run.Profile.Effort {
		t.Fatalf("answer continuation did not restore original policy: %s %v", continuation, err)
	}
	if err := a.Store.Get(ctx, "run", run.ID, &run); err != nil || string(raw(run.Profile)) != originalProfile {
		t.Fatalf("answer mutated original task profile: %+v %v", run.Profile, err)
	}
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "resolved" || r.Attempts != 1 {
		t.Fatalf("live async answer lost final claim or budget: %+v %v", r, err)
	}
	if err := a.Store.Get(ctx, "attention", at.ID, &at); err != nil || at.Status != "answered" {
		t.Fatalf("live async answer was not durably claimed: %+v %v", at, err)
	}
}

func TestTakeoverAfterClaimPreventsQueuedRepairContinuation(t *testing.T) {
	for _, timing := range []string{"before-acknowledgement", "after-acknowledgement"} {
		t.Run(timing, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "complete")
			t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
			t.Setenv("CF_FAKE_CLARIFICATION", "hold")
			record := filepath.Join(t.TempDir(), "protocol.jsonl")
			t.Setenv("CF_FAKE_RECORD", record)
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			var r domain.QuestionRecovery
			waitFor(t, func() bool {
				records, _ := store.List[domain.QuestionRecovery](ctx, a.Store, "question-recovery")
				for _, candidate := range records {
					if candidate.RunID == run.ID && candidate.Stage == "clarifying" && candidate.RepairTurnID != "" {
						r = candidate
						return true
					}
				}
				return false
			})
			live, err := a.Runs.get(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"turnId": r.RepairTurnID, "item": map[string]any{"id": "takeover-async", "type": "agentMessage", "text": "Which target?", "questions": []map[string]any{{"title": "Which target?", "options": []string{"One", "Two"}}}}})})
			at := pendingQuestion(t, a, "async")
			answer := raw(map[string]any{"answers": map[string]any{"q1": map[string]any{"answers": []string{"One"}}}})
			if err := a.Runs.Answer(ctx, at.ID, answer); err != nil {
				t.Fatal(err)
			}
			live.mu.Lock()
			claimed := live.recovery
			if claimed == nil || claimed.Stage != "answered" {
				live.mu.Unlock()
				t.Fatal("live repair answer was not claimed before takeover")
			}
			if timing == "after-acknowledgement" {
				// Fix the completed-turn state and hold its already-queued continuation
				// until takeover finishes. Actual completion/policy wiring is covered by
				// TestLiveAsyncRecoveryAnswerRestoresOriginalPolicy.
				live.run.Status, live.run.TurnID = "idle", ""
				live.lastCompletedTurn, live.lastCompletedStatus = claimed.RepairTurnID, "completed"
				live.completed[claimed.RepairTurnID] = true
				if err := a.Runs.persist(live, "run.state"); err != nil {
					live.mu.Unlock()
					t.Fatal(err)
				}
			}
			live.mu.Unlock()
			release, continued := make(chan struct{}), make(chan struct{})
			go func() { <-release; a.Runs.continueAfterRepair(live, claimed); close(continued) }()
			takeoverCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			err = a.Runs.Interrupt(takeoverCtx, run.ID)
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-continued:
			case <-takeoverCtx.Done():
				t.Fatal("queued repair continuation did not finish")
			}
			waitRecoveryWork(t, a.Runs)
			if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "resolved" || r.Attempts != 1 {
				t.Fatalf("takeover failed to finalise the claim and invalidate continuation: %+v %v", r, err)
			}
			if err := a.Store.Get(ctx, "attention", at.ID, &at); err != nil || at.Status != "answered" {
				t.Fatalf("takeover lost durable answer claim: %+v %v", at, err)
			}
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "Continue the original task using the user's answer") {
				t.Fatal("queued repair callback resumed the task after takeover")
			}
			if err := a.Runs.Answer(ctx, at.ID, answer); err == nil {
				t.Fatal("takeover made the claimed answer replayable")
			}
		})
	}
}
func TestCompletedNativeRecoveryPreservesQuestionBesideUnrelatedAttention(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	t.Setenv("CF_FAKE_HELPER_FAILURE", "invalid")
	t.Setenv("CF_FAKE_CLARIFICATION", "hold")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	var r domain.QuestionRecovery
	waitFor(t, func() bool {
		records, _ := store.List[domain.QuestionRecovery](ctx, a.Store, "question-recovery")
		for _, candidate := range records {
			if candidate.RunID == run.ID && candidate.Stage == "clarifying" && candidate.RepairTurnID != "" {
				r = candidate
				return true
			}
		}
		return false
	})
	live, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := domain.Attention{ID: "unrelated-pending", RunID: run.ID, Origin: "async", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "other", Question: "Unrelated decision?", IsOther: true}}})}
	live.mu.Lock()
	if err := a.Store.Put(ctx, "attention", unrelated.ID, unrelated, ""); err != nil {
		live.mu.Unlock()
		t.Fatal(err)
	}
	live.pending[unrelated.ID] = unrelated
	live.mu.Unlock()
	a.Runs.onMessage(live, codex.Message{ID: raw(42), Method: questionMethod, Params: raw(map[string]any{"turnId": r.RepairTurnID, "isBlocking": false, "questions": []question{{ID: "target", Question: "Which target?", IsOther: true}}})})
	native := pendingQuestion(t, a, "rpc")
	a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]string{"id": r.RepairTurnID, "status": "completed"}})})
	var fallback domain.Attention
	if err := a.Store.Get(ctx, "attention", recoveryTextID(r), &fallback); err != nil || fallback.Status != "pending" || fallback.RecoveryID != r.ID || fallback.Blocking || questionText(fallback.Params) != "Which target?" {
		t.Fatalf("unrelated attention suppressed genuine nonblocking recovery: %+v %v", fallback, err)
	}
	if err := a.Store.Get(ctx, "attention", native.ID, &native); err != nil || native.Status != "stale" {
		t.Fatalf("completed native handle remained live: %+v %v", native, err)
	}
	var retained domain.Attention
	if err := a.Store.Get(ctx, "attention", unrelated.ID, &retained); err != nil || string(raw(retained)) != string(raw(unrelated)) {
		t.Fatalf("recovery changed unrelated pending question: %+v %v", retained, err)
	}
	if err := a.Store.Get(ctx, "question-recovery", r.ID, &r); err != nil || r.Stage != "fallback" || r.Attempts != 1 || recoveryBlocking(r) {
		t.Fatalf("recovery lost attempt budget or nonblocking delivery: %+v %v", r, err)
	}
}

func TestRetiredResumeCannotOverwriteOwnerReplacement(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	gate := filepath.Join(t.TempDir(), "resume-release")
	t.Setenv("CF_FAKE_GATE_METHOD", "thread/resume")
	t.Setenv("CF_FAKE_GATE_FILE", gate)
	defer func() { _ = os.WriteFile(gate, nil, 0600) }()
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ThreadID = "interrupted", "thread-1"
	if err := os.MkdirAll(run.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	r := domain.QuestionRecovery{ID: "retired-resume", RunID: run.ID, Stage: "clarifying", SourceText: "Task complete"}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Runs.start(ctx, run.ID, true, "", &r); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(gate + ".waiting"); return err == nil })
	old, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	old.mu.Lock()
	locked := true
	defer func() {
		if locked {
			old.mu.Unlock()
		}
	}()
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// The retained process responds successfully, then launch is held at its
	// post-resume session lock. Stage the same locked retirement/replacement
	// transition as takeover and start before allowing that old launch to run.
	waitFor(t, func() bool {
		stacks := make([]byte, 1<<20)
		stacks = stacks[:runtime.Stack(stacks, true)]
		for _, stack := range strings.Split(string(stacks), "\n\n") {
			if strings.Contains(stack, "[sync.Mutex.Lock") && strings.Contains(stack, "(*Manager).launch(") {
				return true
			}
		}
		return false
	})
	a.Runs.cancelRecoveryLocked(old)
	old.retired = true
	if old.client != nil {
		_ = old.client.Close()
	}
	replacementRun := run
	replacementRun.Status, replacementRun.TurnID = "running", "owner-replacement-turn"
	replacementRun.Error = "owner replacement remains active"
	replacement := &session{run: replacementRun, pending: map[string]domain.Attention{}, completed: map[string]bool{}}
	a.Runs.mu.Lock()
	a.Runs.live[run.ID] = replacement
	a.Runs.mu.Unlock()
	if err := a.Store.Put(ctx, "run", run.ID, replacementRun, ""); err != nil {
		t.Fatal(err)
	}
	old.mu.Unlock()
	locked = false
	waitRecoveryWork(t, a.Runs)
	var after domain.Run
	if err := a.Store.Get(ctx, "run", run.ID, &after); err != nil || string(raw(after)) != string(raw(replacementRun)) {
		t.Fatalf("retired successful resume overwrote replacement state: %+v %v", after, err)
	}
	current, err := a.Runs.get(run.ID)
	if err != nil || current != replacement {
		t.Fatalf("retired startup replaced current owner session: %p %v", current, err)
	}
}
func TestTakeoverInvalidatesEntireQueuedCompletion(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "historical-release")
	t.Setenv("CF_FAKE_GATE_METHOD", "turn/start")
	t.Setenv("CF_FAKE_GATE_FILE", gate)
	defer func() { _ = os.WriteFile(gate, nil, 0600) }()
	record := gate + ".protocol"
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status = "idle"
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	legacy := domain.QuestionRecovery{ID: "older-unresolved", RunID: run.ID, Stage: "unresolved", SourceText: "Task complete", Attempts: 2}
	if err := a.Store.Put(ctx, "question-recovery", legacy.ID, legacy, ""); err != nil {
		t.Fatal(err)
	}
	live := &session{run: run, recovery: &legacy, pending: map[string]domain.Attention{}, completed: map[string]bool{}}
	a.Runs.mu.Lock()
	a.Runs.live[run.ID] = live
	a.Runs.mu.Unlock()
	a.Runs.onMessage(live, codex.Message{Method: "item/completed", Params: raw(map[string]any{"item": map[string]string{"type": "agentMessage", "text": "Which target?"}})})
	a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]string{"id": "queued-completion", "status": "completed"}})})
	waitFor(t, func() bool { _, err := os.Stat(gate + ".waiting"); return err == nil })
	takeoverCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := a.Runs.Interrupt(takeoverCtx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitRecoveryWork(t, a.Runs)
	assertNoAttention(t, a)
	var unexpected domain.QuestionRecovery
	if err := a.Store.Get(ctx, "question-recovery", run.ID+":queued-completion", &unexpected); err != store.ErrNotFound {
		t.Fatalf("takeover allowed later stage of queued callback to classify again: %+v %v", unexpected, err)
	}
	data, err := os.ReadFile(record)
	if err != nil || strings.Count(string(data), "outputSchema") != 1 {
		t.Fatalf("stale completion launched another automatic classifier: %s %v", data, err)
	}
}
