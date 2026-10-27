package aiworkflows

import (
	"codefactory/internal/codex"
	"codefactory/internal/testutil"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dk-code-factory/agent-workflow/pkg/workflows"
)

func startFixture(t *testing.T, dir string, kind Kind, callback func(codex.Message)) *Client {
	t.Helper()
	c, err := Start(context.Background(), testutil.FakeCodex(t), nil, dir, kind, callback)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err = c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = c.Call(context.Background(), "thread/start", map[string]any{"cwd": dir, "sandbox": "read-only", "approvalPolicy": "never"}, nil); err != nil {
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
			if err := c.Call(context.Background(), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "Inspect this task"}}}, &response); err != nil {
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
	if err := c.Call(context.Background(), "turn/start", params, nil); err != nil {
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
	if err := c.Call(context.Background(), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "Must not execute"}}}, nil); err == nil {
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
	if err := c.Call(context.Background(), "turn/start", params, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), "turn/start", params, nil); err == nil {
		t.Fatal("concurrent turn accepted")
	}
	if err := c.Call(context.Background(), "turn/interrupt", map[string]string{"threadId": "thread-1", "turnId": "turn-1"}, nil); err != nil {
		t.Fatal(err)
	}
	waitRecord(t, dir, Task, workflows.RunStatusFailed)
	params["input"] = []map[string]string{{"type": "text", "text": "Follow-up"}}
	if err := c.CallWorkflow(context.Background(), QuestionRepair, params, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), "turn/interrupt", map[string]string{"threadId": "thread-1", "turnId": "turn-1"}, nil); err != nil {
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
	if err := c.Call(context.Background(), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "Assess item"}}}, nil); err != nil {
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
	if err := c.Call(context.Background(), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "private-secret-canary"}}}, nil); err != nil {
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
	if err := c.Call(context.Background(), "turn/start", map[string]any{"input": []map[string]string{{"type": "text", "text": "Must not restart"}}}, nil); err == nil {
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
	if err := c.Call(context.Background(), "turn/start", map[string]any{"threadId": "thread-1", "input": []map[string]string{{"type": "text", "text": "Complete retained task"}}}, nil); err != nil {
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
