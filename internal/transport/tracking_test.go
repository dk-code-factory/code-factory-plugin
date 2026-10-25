package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
)

type trackingFixture struct {
	a      *app.App
	ctx    context.Context
	server *Server
	http   *httptest.Server
	token  string
	calls  []string
}

func newTrackingFixture(t *testing.T) *trackingFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	vault, err := store.NewVault(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &app.App{Store: db, Vault: vault, DataDir: dir, GitHub: providers.NewGitHub()}
	a.Runs = app.NewManager(ctx, a, testutil.FakeCodex(t))
	if err = a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	f := &trackingFixture{a: a, ctx: ctx, server: New(a, events.New(db, vault)), token: strings.Repeat("f", 64)}
	f.http = httptest.NewServer(f.server.Handler(f.token))
	t.Cleanup(func() { f.http.Close(); cancel(); a.Runs.Close(); db.Close() })
	return f
}
func (f *trackingFixture) call(t *testing.T, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	f.calls = append(f.calls, name)
	result, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, name, args)
	if err != nil {
		t.Fatal(err)
	}
	if result["isError"] == true {
		return result, true
	}
	data := result["structuredContent"].(map[string]any)["data"].(map[string]any)
	return data, false
}
func trackingCall(client *http.Client, url, token, name string, args map[string]any) (map[string]any, int, time.Duration, error) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	req, _ := http.NewRequest("POST", url+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	req.Header.Set("Authorization", "Bearer "+token)
	start := time.Now()
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	elapsed := time.Since(start)
	if err != nil {
		return nil, 0, elapsed, err
	}
	var envelope struct {
		Result map[string]any
		Error  any
	}
	err = json.Unmarshal(b, &envelope)
	if envelope.Error != nil {
		err = io.ErrUnexpectedEOF
	}
	return envelope.Result, len(b), elapsed, err
}

func TestMCPDailyWorkflowsUseOneOrTwoCallsAndReturnDeliveryState(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "question")
	f := newTrackingFixture(t)
	a, ctx := f.a, f.ctx
	checkout := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = checkout
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(b), err)
		}
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = checkout
	head, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	origin := t.TempDir()
	for _, args := range [][]string{{"clone", "--bare", checkout, origin}, {"-C", checkout, "remote", "add", "origin", origin}, {"-C", origin, "update-ref", "refs/pull/1/head", strings.TrimSpace(string(head))}} {
		if b, e := exec.Command("git", args...).CombinedOutput(); e != nil {
			t.Fatal(string(b), e)
		}
	}
	repo := domain.Repository{ID: "pilot/repo", Checkout: checkout, Base: "main"}
	if err = a.SaveRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	item := domain.Item{ID: "pilot/repo#1", Repository: repo.ID, Kind: "pr", Number: 1, State: "open", Head: strings.TrimSpace(string(head)), URL: "https://github.com/pilot/repo/pull/1", Title: "Review fixture"}
	if err = a.Import(ctx, repo, []domain.Item{item}, "fixture", time.Now(), true); err != nil {
		t.Fatal(err)
	}
	// Direct preparation replaces discovery; the prepared authority remains visible.
	prepared, bad := f.call(t, "prepare_pr_review", map[string]any{"target": item.URL})
	if bad || prepared["status"] != "prepared" || prepared["prompt"] == "" || prepared["profile"] == nil || prepared["skills"] == nil {
		t.Fatal(prepared)
	}
	id := prepared["id"].(string)
	started, bad := f.call(t, "start_run", map[string]any{"id": id})
	if bad || started["delivery"] != "accepted" || started["runId"] != id || len(f.calls) != 2 {
		t.Fatal(started, f.calls)
	}
	var run domain.Run
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = a.Store.Get(ctx, "run", id, &run)
		if run.Status == "waiting" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if run.Status != "waiting" {
		t.Fatal(run.Status, run.Error)
	}
	f.calls = nil
	attention, bad := f.call(t, "list_attention", map[string]any{"runId": id, "freshness": "questions"})
	if bad {
		t.Fatal(attention)
	}
	requests := attention["requests"].([]any)
	if len(requests) != 1 {
		t.Fatal(attention)
	}
	request := requests[0].(map[string]any)["request"].(map[string]any)
	delivered, bad := f.call(t, "answer_request", map[string]any{"id": request["id"], "answer": map[string]any{"answers": map[string]any{"scope": map[string]any{"answers": []string{"Focused"}}}}})
	if bad || delivered["delivery"] != "delivered" || delivered["runId"] != id || len(delivered["attention"].(map[string]any)["requests"].([]any)) != 0 || len(f.calls) != 2 {
		t.Fatal(delivered, f.calls)
	}
	duplicate, bad := f.call(t, "answer_request", map[string]any{"id": request["id"], "answer": map[string]any{"answers": map[string]any{"scope": map[string]any{"answers": []string{"Focused"}}}}})
	if !bad {
		t.Fatal("duplicate delivery accepted", duplicate)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = a.Store.Get(ctx, "run", id, &run)
		if run.Status == "idle" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	f.calls = nil
	tracked, bad := f.call(t, "track_work", map[string]any{"target": "repo#1", "freshness": "follow-up"})
	if bad || tracked["currentRun"].(map[string]any)["id"] != id || tracked["continuation"] == nil || len(f.calls) != 1 {
		t.Fatal(tracked)
	}
	message, bad := f.call(t, "send_message", map[string]any{"id": id, "text": "Follow-up fixture", "expectedTurn": tracked["continuation"].(map[string]any)["expectedTurn"]})
	if bad || message["delivery"] != "accepted" || message["messageDelivery"].(map[string]any)["state"] != "queued" || message["messageId"] == "" || message["runId"] != id || message["threadId"] != run.ThreadID || len(f.calls) != 2 {
		t.Fatal(message, f.calls)
	}
	f.calls = nil
	dashboard, bad := f.call(t, "open_factory", map[string]any{})
	if bad || dashboard["itemCount"] != float64(1) || dashboard["inbox"] != nil || len(f.calls) != 1 {
		t.Fatal(dashboard)
	}
	runs, _ := store.List[domain.Run](ctx, a.Store, "run")
	if len(runs) != 1 {
		t.Fatal("workflow created substitute run", len(runs))
	}
	// Persisted launch resources are intact despite public projections.
	if _, err := os.Stat(run.Worktree); err != nil {
		t.Fatal(err)
	}
}
