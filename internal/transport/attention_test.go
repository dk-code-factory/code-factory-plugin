package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPAttentionQueueWaitAnswerContinueAndStaleRecovery(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "question")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	vault, err := store.NewVault(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &app.App{Store: db, Vault: vault, DataDir: dir, GitHub: providers.NewGitHub()}
	a.Runs = app.NewManager(ctx, a, testutil.FakeCodex(t))
	defer a.Runs.Close()
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	for _, args := range [][]string{{"init", "--initial-branch=main"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = checkout
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	repo := domain.Repository{ID: "pilot/queue", Checkout: checkout}
	if err := a.SaveRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	item := domain.Item{ID: "pilot/queue#1", Repository: repo.ID, Number: 1, Kind: "issue", Title: "Question queue", State: "open"}
	item.Revision = item.ContentRevision()
	if err := a.Import(ctx, repo, []domain.Item{item}, "owner", time.Now().UTC(), true); err != nil {
		t.Fatal(err)
	}
	svc := New(a, events.New(db, vault))
	st, ct := mcp.NewInMemoryTransports()
	ss, err := svc.MCP.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "queue-sdk-fixture", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	call := func(name string, args any, out any, wantError bool) {
		t.Helper()
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError != wantError {
			t.Fatalf("%s: result=%+v error=%v", name, result, err)
		}
		if out != nil {
			b, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct{ Data json.RawMessage }
			if err := json.Unmarshal(b, &envelope); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(envelope.Data, out); err != nil {
				t.Fatal(err)
			}
		}
	}
	wait := func(f func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if f() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("queue transition timed out")
	}
	var queue app.AttentionPage
	var runs [2]domain.Run
	for i := range runs {
		call("prepare_launch", app.LaunchInput{ItemID: item.ID, ProfileID: "investigate"}, &runs[i], false)
		call("start_run", StartInput{ID: runs[i].ID}, nil, false)
	}
	wait(func() bool {
		call("list_attention", app.AttentionInput{Freshness: app.ID()}, &queue, false)
		return len(queue.Requests) == 2
	})
	for _, entry := range queue.Requests {
		if entry.Run.Status != "waiting" || !entry.Request.Blocking || entry.Run.ItemID != item.ID || entry.Request.Method != "item/tool/requestUserInput" {
			t.Fatal("agent did not pause with its question", entry)
		}
	}
	first, other := queue.Requests[0], queue.Requests[1]
	answer := map[string]any{"answers": map[string]any{"scope": map[string]any{"answers": []string{"Focused"}}}}
	call("answer_request", AnswerInput{ID: first.Run.ID, Answer: answer}, nil, true)
	call("answer_request", AnswerInput{ID: first.Request.ID, Answer: map[string]any{}}, nil, true)
	call("answer_request", AnswerInput{ID: first.Request.ID, Answer: answer}, nil, false)
	call("answer_request", AnswerInput{ID: first.Request.ID, Answer: answer}, nil, true)
	var state struct{ Run domain.Run }
	wait(func() bool {
		call("read_run", IDInput{ID: first.Run.ID, Freshness: app.ID()}, &state, false)
		return state.Run.Status == "idle"
	})
	if state.Run.ID != first.Run.ID || state.Run.ThreadID == "" {
		t.Fatal("answer did not continue the originating session", state)
	}
	call("list_attention", app.AttentionInput{Freshness: "after-answer"}, &queue, false)
	if len(queue.Requests) != 1 || queue.Requests[0].Request.ID != other.Request.ID {
		t.Fatal("answer removed another agent's request", queue)
	}
	call("interrupt_run", IDInput{ID: other.Run.ID}, nil, false)
	call("answer_request", AnswerInput{ID: other.Request.ID, Answer: answer}, nil, true)
	// Interruption may retain a question as a fresh textual ask. The original
	// RPC must be gone and any retained ask must keep the owning run identity.
	wait(func() bool {
		call("list_attention", app.AttentionInput{Freshness: app.ID()}, &queue, false)
		for _, entry := range queue.Requests {
			if entry.Request.ID == other.Request.ID || entry.Run.ID != other.Run.ID {
				return false
			}
		}
		return true
	})
}
