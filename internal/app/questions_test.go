package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func pendingQuestion(t *testing.T, a *App, origin string) domain.Attention {
	t.Helper()
	var found domain.Attention
	waitFor(t, func() bool {
		atts, _ := store.List[domain.Attention](a.Runs.ctx, a.Store, "attention")
		for _, at := range atts {
			if at.Status == "pending" && at.Origin == origin {
				found = at
				return true
			}
		}
		return false
	})
	return found
}

func TestStoppedQuestionRecoveryNativeAndFallback(t *testing.T) {
	for _, tc := range []struct {
		mode, origin string
		attempts     int
	}{{"recover-first", "rpc", 1}, {"recover-second", "rpc", 2}, {"recover-fail", "text", 2}} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", tc.mode)
			record := filepath.Join(t.TempDir(), "protocol.jsonl")
			t.Setenv("CF_FAKE_RECORD", record)
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			run.Profile.Model = "fixture-model"
			run.Profile.Sandbox = "workspace-write"
			run.Profile.Approval = "untrusted"
			if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			at := pendingQuestion(t, a, tc.origin)
			var recovery domain.QuestionRecovery
			if err := a.Store.Get(ctx, "question-recovery", at.RecoveryID, &recovery); err != nil || recovery.Attempts != tc.attempts {
				t.Fatalf("wrong recovery attempts: %d, %v", recovery.Attempts, err)
			}
			before := run
			_ = a.Store.Get(ctx, "run", run.ID, &before)
			key := "target"
			if tc.origin == "text" {
				key = "reply"
			}
			answer := raw(map[string]any{"answers": map[string]any{key: map[string]any{"answers": []string{"One"}}}})
			var answerErr error
			if tc.origin == "text" {
				answerErr = a.Runs.Message(ctx, run.ID, "One", "")
			} else {
				answerErr = a.Runs.Answer(ctx, at.ID, answer)
			}
			if err := answerErr; err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool {
				_ = a.Store.Get(ctx, "run", run.ID, &run)
				_ = a.Store.Get(ctx, "question-recovery", recovery.ID, &recovery)
				return run.Status == "idle" && recovery.Stage == "resolved"
			})
			waitFor(t, func() bool {
				events, _ := a.Store.Events(ctx, 0, 200)
				for _, event := range events {
					if event.Entity != run.ID {
						continue
					}
					var out struct {
						Params struct{ Item struct{ Text string } }
					}
					_ = json.Unmarshal(event.Data, &out)
					if out.Params.Item.Text == "Task complete" {
						return true
					}
				}
				return false
			})
			if run.ThreadID != before.ThreadID || run.Worktree != before.Worktree || string(raw(run.Profile)) != string(raw(before.Profile)) {
				t.Fatal("answer replaced the original session")
			}
			if err := a.Runs.Answer(ctx, at.ID, answer); err == nil {
				t.Fatal("duplicate answer accepted")
			}
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			plans, defaults := 0, 0
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var call struct {
					Method string
					Params struct {
						CollaborationMode struct{ Mode string }
						SandboxPolicy     struct{ Type string }
						ApprovalPolicy    string
					}
				}
				_ = json.Unmarshal([]byte(line), &call)
				if call.Method == "turn/start" {
					switch call.Params.CollaborationMode.Mode {
					case "plan":
						plans++
					case "default":
						defaults++
						if defaults > 1 && (call.Params.SandboxPolicy.Type != "workspaceWrite" || call.Params.ApprovalPolicy != "untrusted") {
							t.Fatal("original task policy was not restored after constrained recovery")
						}
					}
				}
			}
			if plans != tc.attempts || defaults != 2 {
				t.Fatalf("mode restoration or repair bound failed: plan=%d default=%d", plans, defaults)
			}
			events, err := a.Store.Events(ctx, 0, 200)
			if err != nil {
				t.Fatal(err)
			}
			internal, human := 0, 0
			for _, event := range events {
				if event.Entity != run.ID || event.Kind != "run.output" {
					continue
				}
				var output struct {
					Params struct {
						CodeFactorySource string
						Item              struct{ Type string }
					}
				}
				_ = json.Unmarshal(event.Data, &output)
				if output.Params.Item.Type == "userMessage" {
					if output.Params.CodeFactorySource == "question-recovery" {
						internal++
					} else {
						human++
					}
				}
			}
			expectedInternal := tc.attempts
			if tc.origin == "rpc" {
				expectedInternal++
			}
			if internal != expectedInternal || human < 1 {
				t.Fatalf("recovery input author classification failed: internal=%d human=%d", internal, human)
			}
		})
	}
}

func TestHumanReplyInRepairTurnKeepsItsConversationRole(t *testing.T) {
	s := &session{internalInputs: map[string]string{"turn": "Internal repair prompt"}}
	message := func(text string) codex.Message {
		return codex.Message{Method: "item/completed", Params: raw(map[string]any{"turnId": "turn", "item": map[string]any{"type": "userMessage", "content": []any{map[string]string{"type": "text", "text": text}}}})}
	}
	if !isRecoveryInput(s, message("Internal repair prompt")) || isRecoveryInput(s, message("My actual answer")) {
		t.Fatal("a real answer was confused with automatic repair input")
	}
}

func TestStructuredMessageQuestionSurvivesRestartAndClaimsAnswerOnce(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "async-question")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	at := pendingQuestion(t, a, "async")
	if at.Blocking || len(at.RequestID) != 0 {
		t.Fatal("async question fabricated a blocking RPC")
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", run.ID, &run); return run.Status == "idle" })
	a.Runs.Close()
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	var retained domain.Attention
	_ = a.Store.Get(ctx, "attention", at.ID, &retained)
	if retained.Status != "pending" {
		t.Fatal("restart discarded the message question")
	}
	privateAnswer := "private-fallback-value-8310"
	answer := raw(map[string]any{"answers": map[string]any{"q1": map[string]any{"answers": []string{privateAnswer}}}})
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.Runs.Answer(ctx, at.ID, answer) == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted %d answers", accepted.Load())
	}
	_ = a.Store.Get(ctx, "attention", at.ID, &retained)
	if retained.Status != "answered" {
		t.Fatal("answer claim lost")
	}
	live, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Runs.onMessage(live, codex.Message{Method: "item/agentMessage/delta", Params: raw(map[string]string{"itemId": "echo", "delta": privateAnswer})})
	events, err := a.Store.Events(ctx, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(string(event.Data), privateAnswer) {
			t.Fatal("synthetic answer escaped redaction into the journal")
		}
	}
}

func TestOrdinaryCompletionDoesNotCreateQuestion(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		records, _ := store.List[domain.QuestionRecovery](ctx, a.Store, "question-recovery")
		return len(records) > 0 && records[0].Stage == "resolved"
	})
	atts, err := store.List[domain.Attention](ctx, a.Store, "attention")
	if err != nil || len(atts) != 0 {
		t.Fatal("ordinary completion became a user question")
	}
}

func TestRecoveryRejectsMalformedForms(t *testing.T) {
	for _, params := range []string{`{"questions":[]}`, `{"questions":[{"id":"a","question":"?"},{"id":"a","question":"?"}]}`, `{"questions":[{"id":"a","question":"?","options":[{"label":""}]}]}`} {
		if validQuestions([]byte(params)) {
			t.Fatal("malformed native form accepted")
		}
	}
}

func TestQuestionRepairUserTakeoverCancelsFurtherAttempts(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "recover-first")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Profile.Model = "fixture-model"
	_ = a.Store.Put(ctx, "run", run.ID, run, "")
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	at := pendingQuestion(t, a, "rpc")
	if err := a.Runs.Interrupt(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", run.ID, &run); return run.Status == "interrupted" })
	if err := a.Runs.Message(ctx, run.ID, "Continue under my direction.", ""); err != nil {
		t.Fatal(err)
	}
	var recovery domain.QuestionRecovery
	_ = a.Store.Get(ctx, "question-recovery", at.RecoveryID, &recovery)
	if recovery.Stage != "cancelled" || recovery.Attempts != 1 {
		t.Fatal("takeover did not stop bounded repair")
	}
	if err := a.Runs.Answer(ctx, at.ID, raw(map[string]any{"answers": map[string]any{"target": map[string]any{"answers": []string{"One"}}}})); err == nil {
		t.Fatal("interrupted repair form remained answerable")
	}
}

func TestLostNativeQuestionsRemainAnswerableWithoutConvertingApprovals(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "question")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	at := pendingQuestion(t, a, "rpc")
	at.Origin = "" // Published historical records have no origin metadata.
	if err := a.Store.Put(ctx, "attention", at.ID, at, ""); err != nil {
		t.Fatal(err)
	}
	approval := domain.Attention{ID: "lost-approval", RunID: run.ID, Method: "item/commandExecution/requestApproval", Status: "pending", Blocking: true, Params: raw(map[string]any{"command": "git status"})}
	if err := a.Store.Put(ctx, "attention", approval.ID, approval, ""); err != nil {
		t.Fatal(err)
	}
	a.Runs.Close()
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	var old domain.Attention
	_ = a.Store.Get(ctx, "attention", at.ID, &old)
	if old.Status != "stale" {
		t.Fatal("lost RPC identity remained valid")
	}
	fallback := pendingQuestion(t, a, "text")
	if fallback.RunID != run.ID || len(fallback.RequestID) != 0 {
		t.Fatal("lost question changed session or fabricated an RPC")
	}
	_ = a.Store.Get(ctx, "attention", approval.ID, &old)
	if old.Status != "stale" {
		t.Fatal("lost approval remained valid")
	}
	atts, _ := store.List[domain.Attention](ctx, a.Store, "attention")
	for _, candidate := range atts {
		if candidate.Status == "pending" && candidate.RecoveryID == "lost-question:"+approval.ID {
			t.Fatal("approval was converted into textual authorization")
		}
	}
}

func TestTextQuestionDismissalCannotAnswerOrApproveNativeRequests(t *testing.T) {
	a, ctx := fixture(t)
	for _, at := range []domain.Attention{{ID: "text", RunID: "retained", Origin: "text", Status: "pending"}, {ID: "native", RunID: "retained", Origin: "rpc", Status: "pending", Method: "item/commandExecution/requestApproval"}} {
		if err := a.Store.Put(ctx, "attention", at.ID, at, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Runs.DismissQuestion(ctx, "text"); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.DismissQuestion(ctx, "text"); err == nil {
		t.Fatal("dismissed question accepted twice")
	}
	if err := a.Runs.DismissQuestion(ctx, "native"); err == nil {
		t.Fatal("native approval bypassed by textual dismissal")
	}
	var native domain.Attention
	_ = a.Store.Get(ctx, "attention", "native", &native)
	if native.Status != "pending" {
		t.Fatal("dismissal altered native approval")
	}
	runs, _ := store.List[domain.Run](ctx, a.Store, "run")
	if len(runs) != 0 {
		t.Fatal("dismissal started agent work")
	}
}

func TestCompletedTurnRetainsUnansweredNativeQuestionAsText(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "nonblocking")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	at := pendingQuestion(t, a, "rpc")
	live, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]string{"id": at.TurnID, "status": "completed"}})})
	fallback := pendingQuestion(t, a, "text")
	if fallback.Blocking {
		t.Fatal("lost explicitly nonblocking question became blocking")
	}
	if fallback.RunID != run.ID || fallback.Method != questionMethod {
		t.Fatal("completed turn lost the unanswered question")
	}
	var original domain.Attention
	_ = a.Store.Get(ctx, "attention", at.ID, &original)
	if original.Status != "stale" {
		t.Fatal("completed native request ID remained live")
	}
}

func TestQuestionHelperIsolationAndModelSelection(t *testing.T) {
	for _, helper := range []string{"", "helper-model"} {
		t.Run("helper-"+helper, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "recover-first")
			record := filepath.Join(t.TempDir(), "protocol.jsonl")
			t.Setenv("CF_FAKE_RECORD", record)
			a, ctx := fixture(t)
			config, _ := a.Config(ctx)
			config.QuestionHelperModel = helper
			if err := a.SaveConfig(ctx, config); err != nil {
				t.Fatal(err)
			}
			run := prepareRun(t, a, ctx)
			run.Profile.Model = "task-model"
			if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			pendingQuestion(t, a, "rpc")
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var call struct {
					Method string
					Params struct {
						Ephemeral                           bool
						Sandbox, ApprovalPolicy, Model, Cwd string
						Config                              map[string]json.RawMessage
					}
				}
				_ = json.Unmarshal([]byte(line), &call)
				if call.Method != "thread/start" || !call.Params.Ephemeral {
					continue
				}
				found = true
				expected := helper
				if expected == "" {
					expected = "task-model"
				}
				if call.Params.Model != expected || call.Params.Sandbox != "read-only" || call.Params.ApprovalPolicy != "never" || call.Params.Cwd != a.DataDir || string(call.Params.Config["features.apps"]) != "false" {
					t.Fatal("helper isolation or model selection changed")
				}
			}
			if !found {
				t.Fatal("no isolated helper thread")
			}
		})
	}
}

func TestRestartDuringRepairRetainsOneFallbackWithoutReplayingAttempts(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "recover-first")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	at := pendingQuestion(t, a, "rpc")
	a.Runs.Close()
	for n := 0; n < 2; n++ {
		if err := a.Runs.Recover(ctx); err != nil {
			t.Fatal(err)
		}
	}
	fallback := pendingQuestion(t, a, "text")
	var r domain.QuestionRecovery
	if err := a.Store.Get(ctx, "question-recovery", at.RecoveryID, &r); err != nil || r.Attempts != 1 || r.Stage != "fallback" || fallback.RecoveryID != r.ID {
		t.Fatal("restart replayed or replaced repair")
	}
	atts, _ := store.List[domain.Attention](ctx, a.Store, "attention")
	pending := 0
	for _, candidate := range atts {
		if candidate.Status == "pending" {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("restart created %d pending requests", pending)
	}
}

func TestRestartMaterializesAlreadyPersistedFallback(t *testing.T) {
	a, ctx := fixture(t)
	r := domain.QuestionRecovery{ID: "persisted-fallback", RunID: "run", Stage: "fallback", Question: "Which target?", Attempts: 2}
	if err := a.Store.Put(ctx, "question-recovery", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		if err := a.Runs.Recover(ctx); err != nil {
			t.Fatal(err)
		}
	}
	at := pendingQuestion(t, a, "text")
	if at.ID != r.ID+":text" {
		t.Fatal("fallback identity changed")
	}
	atts, _ := store.List[domain.Attention](ctx, a.Store, "attention")
	if len(atts) != 1 {
		t.Fatal("restart duplicated fallback")
	}
}
