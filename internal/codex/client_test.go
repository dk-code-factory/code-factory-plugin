package codex

import (
	"codefactory/internal/testutil"
	"context"
	"os"
	"testing"
	"time"
)

func TestProcessProtocolAndDisconnect(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "complete")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	messages := make(chan Message, 16)
	c, e := Start(ctx, testutil.FakeCodex(t), nil, func(m Message) { messages <- m })
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = c.Initialize(ctx); e != nil {
		t.Fatal(e)
	}
	var thread struct{ Thread struct{ ID string } }
	if e = c.Call(ctx, "thread/start", map[string]any{}, &thread); e != nil {
		t.Fatal(e)
	}
	if thread.Thread.ID != "thread-1" {
		t.Fatal(thread)
	}
	if e = c.Call(ctx, "turn/start", map[string]any{"threadId": thread.Thread.ID}, nil); e != nil {
		t.Fatal(e)
	}
	seen := false
	for !seen {
		select {
		case m := <-messages:
			seen = m.Method == "turn/completed"
		case <-ctx.Done():
			t.Fatal("no completion")
		}
	}
	_ = c.Close()
	if e = c.Call(ctx, "thread/read", map[string]any{}, nil); e == nil {
		t.Fatal("disconnected process accepted request")
	}
}
func TestInstalledCodexHandshake(t *testing.T) {
	if os.Getenv("CODE_FACTORY_REAL_CODEX") != "1" {
		t.Skip("set CODE_FACTORY_REAL_CODEX=1 for installed 0.161.0 handshake; no model call")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, e := Version(ctx, "codex"); e != nil {
		t.Fatal(e)
	}
	c, e := Start(ctx, "codex", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = c.Initialize(ctx); e != nil {
		t.Fatal(e)
	}
	var account struct {
		RequiresOpenAIAuth bool `json:"requiresOpenaiAuth"`
	}
	if e = c.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &account); e != nil {
		t.Fatal(e)
	}
	var skills struct{ Data []any }
	if e = c.Call(ctx, "skills/list", map[string]any{"cwds": []string{t.TempDir()}}, &skills); e != nil {
		t.Fatal(e)
	}
	t.Log("Installed Codex initialized; account and skill discovery endpoints responded")
}

// The handler issues an RPC while more than the old channel capacity of output
// precedes its reply. The reader must continue routing replies under this load.
func TestOutputBurstDoesNotBlockResponses(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "burst")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan *Client, 1)
	result := make(chan error, 1)
	c, e := Start(ctx, testutil.FakeCodex(t), nil, func(m Message) {
		if m.Method == "turn/started" {
			client := <-ready
			result <- client.Call(ctx, "skills/list", map[string]any{}, nil)
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ready <- c
	if e = c.Initialize(ctx); e != nil {
		t.Fatal(e)
	}
	if e = c.Call(ctx, "turn/start", map[string]any{}, nil); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-result:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("output burst deadlocked RPC")
	}
}

func TestLateRPCResponseDoesNotSatisfyNextRequest(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, e := Start(ctx, testutil.FakeCodex(t), nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = c.Initialize(ctx); e != nil {
		t.Fatal(e)
	}
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if c.Call(short, "skills/list", map[string]any{}, nil) == nil {
		t.Fatal("slow RPC ignored deadline")
	}
	var result struct{ Data []any }
	if e = c.Call(ctx, "skills/list", map[string]any{}, &result); e != nil {
		t.Fatal(e)
	}
	if result.Data == nil {
		t.Fatal("late response replaced current result")
	}
}
