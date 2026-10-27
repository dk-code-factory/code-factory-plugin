package aiworkflows

import (
	"codefactory/internal/codex"
	"codefactory/internal/testutil"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dk-code-factory/agent-workflow/pkg/workflows"
)

func fixtureRPCContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func startFixture(t *testing.T, dir string, kind Kind, callback func(codex.Message)) *Client {
	t.Helper()
	// Client lifetime outlives each RPC's admission deadline.
	c, err := Start(context.Background(), testutil.FakeCodex(t), nil, dir, kind, callback)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err = c.Initialize(fixtureRPCContext(t)); err != nil {
		t.Fatal(err)
	}
	if err = c.Call(fixtureRPCContext(t), "thread/start", map[string]any{"cwd": dir, "sandbox": "read-only", "approvalPolicy": "never"}, nil); err != nil {
		t.Fatal(err)
	}
	return c
}

func waitRecord(t *testing.T, dir string, kind Kind, status string) workflows.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(dir, "ai-workflows/runs/*.json"))
		for _, name := range files {
			data, _ := os.ReadFile(name)
			var run workflows.Run
			if json.Unmarshal(data, &run) == nil && run.WorkflowRef == "workflows/"+string(kind)+".yml" && run.Status == status {
				return run
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("missing %s workflow status %s", kind, status)
	return workflows.Run{}
}

func TestEveryAIWorkflowUsesYAMLAndWaitsForProviderCompletion(t *testing.T) {
	for _, kind := range []Kind{Task, Review, Assessment, QuestionCheck, AttentionCheck, QuestionRepair} {
		t.Run(string(kind), func(t *testing.T) {
			mode := "complete"
			if kind == Assessment {
				mode = "inference"
			}
			t.Setenv("CF_FAKE_MODE", mode)
			dir := t.TempDir()
			c := startFixture(t, dir, kind, nil)
			var response struct{ Turn struct{ ID string } }
			if err := c.Call(fixtureRPCContext(t), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "Inspect this task"}}}, &response); err != nil {
				t.Fatal(err)
			}
			if response.Turn.ID == "" {
				t.Fatal("missing start acknowledgement")
			}
			run := waitRecord(t, dir, kind, workflows.RunStatusSucceeded)
			if len(run.Jobs) != 1 {
				t.Fatal("workflow did not execute its declared job", run)
			}
		})
	}
}

func TestWorkflowPreservesPolicySkillsAndInteractiveReplies(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "question")
	dir := t.TempDir()
	log := filepath.Join(dir, "rpc.jsonl")
	t.Setenv("CF_FAKE_RECORD", log)
	question := make(chan codex.Message, 1)
	c := startFixture(t, dir, Review, func(msg codex.Message) {
		if msg.Method == "item/tool/requestUserInput" {
			question <- msg
		}
	})
	params := map[string]any{"threadId": "thread-1", "startIfIdle": true, "effort": "high", "approvalPolicy": "on-request", "sandboxPolicy": map[string]any{"type": "readOnly"}, "collaborationMode": map[string]any{"mode": "plan"}, "input": []map[string]string{{"type": "text", "text": "Review retained session"}, {"type": "skill", "name": "review-skill", "path": "/private/snapshot/SKILL.md"}}}
	if err := c.Call(fixtureRPCContext(t), "turn/start", params, nil); err != nil {
		t.Fatal(err)
	}
	waitRecord(t, dir, Review, workflows.RunStatusRunning)
	select {
	case msg := <-question:
		if err := c.Reply(msg.ID, map[string]any{"answers": map[string]any{}}); err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native question was not forwarded")
	}
	waitRecord(t, dir, Review, workflows.RunStatusSucceeded)
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		var msg struct {
			Method string
			Params map[string]any
		}
		_ = json.Unmarshal([]byte(line), &msg)
		if msg.Method != "turn/start" {
			continue
		}
		want, _ := json.Marshal(params)
		got, _ := json.Marshal(msg.Params)
		if string(got) != string(want) {
			t.Fatalf("workflow changed native authority or inputs: %s != %s", got, want)
		}
		return
	}
	t.Fatal("workflow did not start provider turn")
}

func TestInvalidWorkflowFailsBeforeProviderEffects(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	dir := t.TempDir()
	log := filepath.Join(dir, "rpc.jsonl")
	t.Setenv("CF_FAKE_RECORD", log)
	c := startFixture(t, dir, Task, nil)
	if err := os.WriteFile(filepath.Join(c.dir, "workflows/task.yml"), []byte("jobs: [invalid]"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(fixtureRPCContext(t), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "Must not execute"}}}, nil); err == nil {
		t.Fatal("invalid YAML accepted")
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), `"method":"turn/start"`) {
		t.Fatal("provider started before workflow validation")
	}
}

func TestInterruptAndFollowUpUseRetainedSessionWithoutReplay(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	dir := t.TempDir()
	c := startFixture(t, dir, Task, nil)
	params := map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "First task"}}}
	if err := c.Call(fixtureRPCContext(t), "turn/start", params, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(fixtureRPCContext(t), "turn/start", params, nil); err == nil {
		t.Fatal("concurrent turn accepted")
	}
	if err := c.Call(fixtureRPCContext(t), "turn/interrupt", map[string]string{"threadId": "thread-1", "turnId": "turn-1"}, nil); err != nil {
		t.Fatal(err)
	}
	waitRecord(t, dir, Task, workflows.RunStatusFailed)
	params["input"] = []map[string]string{{"type": "text", "text": "Follow-up"}}
	if err := c.CallWorkflow(fixtureRPCContext(t), QuestionRepair, params, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(fixtureRPCContext(t), "turn/interrupt", map[string]string{"threadId": "thread-1", "turnId": "turn-1"}, nil); err != nil {
		t.Fatal(err)
	}
	waitRecord(t, dir, QuestionRepair, workflows.RunStatusFailed)
}

func TestStructuredWorkflowRejectsInvalidOutputWithoutRepair(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete") // Non-JSON task text is invalid assessment output.
	dir := t.TempDir()
	log := filepath.Join(dir, "rpc.jsonl")
	t.Setenv("CF_FAKE_RECORD", log)
	terminal := make(chan codex.Message, 1)
	c := startFixture(t, dir, Assessment, func(msg codex.Message) {
		if msg.Method == "turn/completed" {
			terminal <- msg
		}
	})
	if err := c.Call(fixtureRPCContext(t), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "Assess item"}}}, nil); err != nil {
		t.Fatal(err)
	}
	waitRecord(t, dir, Assessment, workflows.RunStatusFailed)
	select {
	case msg := <-terminal:
		var data struct{ Turn struct{ Status string } }
		if json.Unmarshal(msg.Params, &data) != nil || data.Turn.Status != "failed" {
			t.Fatal("workflow schema failure appeared successful to caller", string(msg.Params))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing failed completion notification")
	}
	data, _ := os.ReadFile(log)
	if strings.Count(string(data), `"method":"turn/start"`) != 1 {
		t.Fatal("invalid output caused task replay or repair")
	}
	if !strings.Contains(string(data), `"outputSchema"`) || !strings.Contains(string(data), `"confidence"`) {
		t.Fatal("YAML schema was not supplied to provider")
	}
}

func TestWorkflowCheckpointsOmitPrivateContentAndCloseJoinsExecution(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	dir := t.TempDir()
	c := startFixture(t, dir, Task, nil)
	if err := c.Call(fixtureRPCContext(t), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "private-secret-canary"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil { /* A completed process may already have exited. */
	}
	waitRecord(t, dir, Task, workflows.RunStatusCanceled)
	files, _ := filepath.Glob(filepath.Join(dir, "ai-workflows/runs/*.json"))
	for _, file := range files {
		data, _ := os.ReadFile(file)
		if strings.Contains(string(data), "private-secret-canary") {
			t.Fatal("private prompt leaked into workflow checkpoint")
		}
	}
	if _, err := os.Stat(c.dir); !os.IsNotExist(err) {
		t.Fatal("closed client retained temporary definitions")
	}
	if err := c.Call(fixtureRPCContext(t), "turn/start", map[string]any{"input": []map[string]string{{"type": "text", "text": "Must not restart"}}}, nil); err == nil {
		t.Fatal("closed client started work")
	}
}

type delayedTerminalStore struct {
	store            workflows.RunStore
	entered, release chan struct{}
}

func (s delayedTerminalStore) Save(ctx context.Context, run *workflows.Run) error {
	if run.Status != workflows.RunStatusRunning {
		close(s.entered)
		<-s.release
	}
	return s.store.Save(ctx, run)
}

func TestInteractiveCompletionDoesNotWaitForCheckpointFlush(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	dir := t.TempDir()
	completed := make(chan struct{}, 1)
	c := startFixture(t, dir, Task, func(msg codex.Message) {
		if msg.Method == "turn/completed" {
			completed <- struct{}{}
		}
	})
	entered, release := make(chan struct{}), make(chan struct{})
	c.store = delayedTerminalStore{store: c.store, entered: entered, release: release}
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	if err := c.Call(fixtureRPCContext(t), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "Complete retained task"}}}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("workflow did not reach final checkpoint")
	}
	select {
	case <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("checkpoint flush delayed native completion and answer routing")
	}
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("close discarded pending successful checkpoint")
	case <-time.After(25 * time.Millisecond):
	}
	finish()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not drain workflow")
	}
	waitRecord(t, dir, Task, workflows.RunStatusSucceeded)
}

// Fail only the first run, after its native completion. Other turns must keep
// using the same session while this observational checkpoint is blocked.
type failingTerminalStore struct {
	store            workflows.RunStore
	entered, release chan struct{}
	once             sync.Once
	failedID         string
}

func (s *failingTerminalStore) Save(ctx context.Context, run *workflows.Run) error {
	if run.Status != workflows.RunStatusRunning {
		s.once.Do(func() {
			s.failedID = run.ID
			close(s.entered)
			<-s.release
		})
		if run.ID == s.failedID {
			return errors.New("private-provider-error-canary")
		}
	}
	return s.store.Save(ctx, run)
}

func receiveMessage(t *testing.T, messages <-chan codex.Message) codex.Message {
	t.Helper()
	select {
	case msg := <-messages:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("missing workflow notification")
		return codex.Message{}
	}
}

func TestInteractiveCheckpointFailureReportsDiagnosticWithoutChangingCompletion(t *testing.T) {
	for _, kind := range []Kind{Task, Review, QuestionRepair} {
		t.Run(string(kind), func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "recover-first") // Distinct turn IDs on the same thread.
			dir := t.TempDir()
			log := filepath.Join(dir, "rpc.jsonl")
			t.Setenv("CF_FAKE_RECORD", log)
			messages := make(chan codex.Message, 16)
			c := startFixture(t, dir, kind, func(msg codex.Message) {
				if msg.Method == "turn/completed" || msg.Method == "workflow/error" {
					messages <- msg
				}
			})
			store := &failingTerminalStore{store: c.store, entered: make(chan struct{}), release: make(chan struct{})}
			c.store = store
			var once sync.Once
			release := func() { once.Do(func() { close(store.release) }) }
			t.Cleanup(release)
			params := map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "private-prompt-and-answer-canary"}}}
			if err := c.Call(fixtureRPCContext(t), "turn/start", params, nil); err != nil {
				t.Fatal(err)
			}
			select {
			case <-store.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("terminal checkpoint not reached")
			}
			assertCompletion := func(wantID, wantStatus string) {
				t.Helper()
				msg := receiveMessage(t, messages)
				var data struct{ Turn struct{ ID, Status string } }
				if msg.Method != "turn/completed" || json.Unmarshal(msg.Params, &data) != nil || data.Turn.ID != wantID || data.Turn.Status != wantStatus {
					t.Fatalf("native completion changed: %s %s", msg.Method, msg.Params)
				}
			}
			assertCompletion("turn-1", "completed") // Must arrive before checkpoint release.
			params["input"] = []map[string]string{{"type": "text", "text": "question-only recovery follow-up"}}
			if err := c.CallWorkflow(fixtureRPCContext(t), QuestionRepair, params, nil); err != nil {
				t.Fatal(err)
			}
			release()
			msg := receiveMessage(t, messages)
			var diagnostic struct{ ThreadID, TurnID, Message string }
			if msg.Method != "workflow/error" || json.Unmarshal(msg.Params, &diagnostic) != nil || diagnostic.ThreadID != "thread-1" || diagnostic.TurnID != "turn-1" || diagnostic.Message != "AI workflow failed; inspect its private execution status." {
				t.Fatalf("missing sanitized diagnostic for originating turn: %s %s", msg.Method, msg.Params)
			}
			if strings.Contains(string(msg.Params), "canary") || strings.Contains(string(msg.Params), "Which target") {
				t.Fatal("private workflow content escaped in diagnostic")
			}
			c.mu.Lock()
			followUpActive := c.active != nil && c.active.id == "turn-2" && !c.active.ended
			c.mu.Unlock()
			if !followUpActive {
				t.Fatal("old checkpoint changed follow-up admission or terminal status")
			}
			if err := c.Call(fixtureRPCContext(t), "turn/interrupt", map[string]string{"threadId": "thread-1", "turnId": "turn-2"}, nil); err != nil {
				t.Fatal(err)
			}
			assertCompletion("turn-2", "interrupted")
			_ = c.Close()
			select {
			case extra := <-messages:
				t.Fatalf("duplicate terminal or diagnostic: %s %s", extra.Method, extra.Params)
			default:
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(data), `"method":"turn/start"`) != 2 || strings.Count(string(data), `"threadId":"thread-1"`) != 2 {
				t.Fatal("workflow failure replayed or moved a native turn", string(data))
			}
		})
	}
}

func TestInteractiveCheckpointDiagnosticDoesNotDeadlockClose(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	dir := t.TempDir()
	var owner sync.Mutex
	attempted, delivered := make(chan struct{}), make(chan struct{})
	c := startFixture(t, dir, Task, func(msg codex.Message) {
		if msg.Method == "workflow/error" {
			close(attempted)
			owner.Lock()
			defer owner.Unlock()
			close(delivered)
		}
	})
	store := &failingTerminalStore{store: c.store, entered: make(chan struct{}), release: make(chan struct{})}
	c.store = store
	var once sync.Once
	release := func() { once.Do(func() { close(store.release) }) }
	t.Cleanup(release)
	if err := c.Call(fixtureRPCContext(t), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "Complete task"}}}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal checkpoint not reached")
	}
	owner.Lock() // Manager.Close holds the session lock while joining execution.
	locked := true
	unlock := func() {
		if locked {
			locked = false
			owner.Unlock()
		}
	}
	t.Cleanup(unlock)
	release()
	select {
	case <-attempted:
	case <-time.After(5 * time.Second):
		t.Fatal("diagnostic callback not reached")
	}
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	t.Cleanup(func() {
		unlock()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Error("close goroutine did not exit")
		}
		select {
		case <-delivered:
		case <-time.After(5 * time.Second):
			t.Error("diagnostic callback did not exit")
		}
	})
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown joined a callback waiting for the owner's session lock")
	}
	unlock()
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("diagnostic callback remained blocked after shutdown")
	}
}

func TestInteractiveSuccessAndNativeFailuresDoNotReportWorkflowError(t *testing.T) {
	for _, tc := range []struct{ status, mode, prompt string }{
		{"completed", "complete", "Task"},
		{"interrupted", "hold", "Task"},
		{"failed", "complete", "question-only recovery clarification"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", tc.mode)
			t.Setenv("CF_FAKE_CLARIFICATION", "failed")
			messages := make(chan codex.Message, 16)
			c := startFixture(t, t.TempDir(), Task, func(msg codex.Message) {
				if msg.Method == "turn/completed" || msg.Method == "workflow/error" {
					messages <- msg
				}
			})
			if err := c.Call(fixtureRPCContext(t), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": tc.prompt}}}, nil); err != nil {
				t.Fatal(err)
			}
			if tc.status == "interrupted" {
				if err := c.Call(fixtureRPCContext(t), "turn/interrupt", map[string]string{"threadId": "thread-1", "turnId": "turn-1"}, nil); err != nil {
					t.Fatal(err)
				}
			}
			msg := receiveMessage(t, messages)
			var terminal struct{ Turn struct{ Status string } }
			if msg.Method != "turn/completed" || json.Unmarshal(msg.Params, &terminal) != nil || terminal.Turn.Status != tc.status {
				t.Fatal("native terminal status changed", msg.Method, string(msg.Params))
			}
			_ = c.Close()
			select {
			case extra := <-messages:
				t.Fatalf("unexpected workflow diagnostic or duplicate completion: %s %s", extra.Method, extra.Params)
			case <-time.After(25 * time.Millisecond):
			}
		})
	}
}

func TestStatusStoreSerializationOmitsPrivateOutputsAnswersAndErrors(t *testing.T) {
	dir := t.TempDir()
	var canaries []string
	outputs := func(location string) map[string]any {
		output, answer := location+"-output-canary", location+"-answer-canary"
		canaries = append(canaries, output, answer)
		return map[string]any{"text": output, "answers": map[string]any{"scope": answer}}
	}
	privateError := func(location string) string {
		value := location + "-provider-error-canary"
		canaries = append(canaries, value)
		return value
	}
	run := workflows.Run{
		ID: "0123456789abcdef0123456789abcdef", WorkflowRef: "workflows/task.yml", Status: workflows.RunStatusFailed,
		Outputs: outputs("run"), Error: privateError("run"),
		Jobs: map[string]workflows.JobExecution{"task": {
			ID: "task", Status: "failed", Outputs: outputs("job"), Error: privateError("job"),
			Steps: map[string]workflows.StepExecution{"model": {ID: "model", Status: "failed", Outputs: outputs("step"), Error: privateError("step")}},
			Iterations: []workflows.JobIteration{{Number: 1, Status: "failed", Outputs: outputs("iteration"), Error: privateError("iteration"),
				Steps: map[string]workflows.StepExecution{"nested": {ID: "nested", Status: "failed", Outputs: outputs("iteration-step"), Error: privateError("iteration-step")}},
			}},
		}},
	}
	if err := (statusStore{dir: dir}).Save(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal("missing persisted status", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range canaries {
		if strings.Contains(string(data), canary) {
			t.Fatalf("private content persisted: %s", canary)
		}
	}
	var saved workflows.Run
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	job := saved.Jobs["task"]
	step := job.Steps["model"]
	if saved.ID != run.ID || saved.WorkflowRef != run.WorkflowRef || saved.Status != run.Status || job.ID != "task" || job.Status != "failed" || step.ID != "model" || step.Status != "failed" {
		t.Fatal("status identity was lost", saved)
	}
	if saved.Error != "AI workflow did not complete" || job.Error != "AI workflow job did not complete" || step.Error != "AI workflow step did not complete" {
		t.Fatal("missing sanitized status errors", saved)
	}
	if len(run.Outputs) == 0 || len(run.Jobs["task"].Iterations) != 1 || run.Jobs["task"].Steps["model"].Error != "step-provider-error-canary" {
		t.Fatal("status persistence mutated private in-memory execution")
	}
}

func TestAdmissionCancellationWinsBeforeAcknowledgementTransfer(t *testing.T) {
	admission, cancelAdmission := context.WithCancel(context.Background())
	defer cancelAdmission()
	workflow, cancelWorkflow := context.WithCancel(context.Background())
	defer cancelWorkflow()
	tn := &turn{ack: make(chan startResult, 1), cancel: cancelWorkflow}
	callbackStarted, releaseCallback, callbackDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stop := context.AfterFunc(admission, func() {
		defer close(callbackDone)
		close(callbackStarted)
		<-releaseCallback
		cancelWorkflow()
	})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseCallback) }) }
	t.Cleanup(func() {
		release()
		select {
		case <-callbackDone:
		case <-time.After(5 * time.Second):
			t.Error("admission cancellation callback did not exit")
		}
	})
	// Queue a valid native acknowledgement, then let cancellation start before
	// the caller takes ownership. stop must reject even an in-progress callback.
	tn.ack <- startResult{response: json.RawMessage(`{"turn":{"id":"turn-1"}}`)}
	cancelAdmission()
	select {
	case <-callbackStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("admission cancellation did not start")
	}
	var response struct{ Turn struct{ ID string } }
	c := &Client{ctx: context.Background()}
	err := c.acceptStart(admission, tn, stop, <-tn.ack, &response)
	if !errors.Is(err, context.Canceled) || response.Turn.ID != "" {
		t.Fatalf("canceled admission accepted buffered acknowledgement: response=%+v err=%v", response, err)
	}
	if workflow.Err() == nil {
		t.Fatal("canceled handoff did not synchronously stop workflow observation")
	}
}

func TestSuccessfulAdmissionOutlivesCallerUntilClientShutdown(t *testing.T) {
	for _, shutdown := range []string{"interrupt", "client-cancel", "close"} {
		t.Run(shutdown, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "hold")
			dir := t.TempDir()
			log := filepath.Join(dir, "rpc.jsonl")
			t.Setenv("CF_FAKE_RECORD", log)
			c := startFixture(t, dir, Task, nil)
			admission, cancelAdmission := context.WithCancel(fixtureRPCContext(t))
			defer cancelAdmission()
			var response struct{ Turn struct{ ID string } }
			if err := c.Call(admission, "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "Observe retained native turn"}}}, &response); err != nil {
				t.Fatal(err)
			}
			cancelAdmission()
			c.mu.Lock()
			tn := c.active
			c.mu.Unlock()
			if tn == nil || response.Turn.ID != "turn-1" {
				t.Fatal("successful admission lost its observed native identity", response)
			}
			status := workflows.RunStatusCanceled
			switch shutdown {
			case "interrupt":
				status = workflows.RunStatusFailed
				if err := c.Call(fixtureRPCContext(t), "turn/interrupt", map[string]string{"threadId": "thread-1", "turnId": response.Turn.ID}, nil); err != nil {
					t.Fatal(err)
				}
			case "client-cancel":
				c.cancel()
			case "close":
				_ = c.Close()
			}
			select {
			case <-tn.finalized:
			case <-time.After(5 * time.Second):
				t.Fatal("client shutdown did not finalize workflow")
			}
			waitRecord(t, dir, Task, status)
			data, err := os.ReadFile(log)
			if err != nil || strings.Count(string(data), `"method":"turn/start"`) != 1 {
				t.Fatal("native turn was replayed", err, string(data))
			}
		})
	}
}

func TestAcknowledgementTransferRejectsCanceledClient(t *testing.T) {
	lifetime, cancelClient := context.WithCancel(context.Background())
	defer cancelClient()
	workflow, cancelWorkflow := context.WithCancel(lifetime)
	defer cancelWorkflow()
	admission, cancelAdmission := context.WithCancel(context.Background())
	defer cancelAdmission()
	stop := context.AfterFunc(admission, cancelWorkflow)
	defer stop()
	tn := &turn{cancel: cancelWorkflow}
	cancelClient()
	var response struct{ Turn struct{ ID string } }
	err := (&Client{ctx: lifetime}).acceptStart(admission, tn, stop, startResult{response: json.RawMessage(`{"turn":{"id":"turn-1"}}`)}, &response)
	if !errors.Is(err, context.Canceled) || response.Turn.ID != "" || workflow.Err() == nil {
		t.Fatalf("closed client accepted acknowledgement: response=%+v err=%v", response, err)
	}
}
