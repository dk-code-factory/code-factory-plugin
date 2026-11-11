package transport

import (
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/events"
	"codefactory/internal/factory"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	native := factory.New(ctx, a, testutil.FakeCodex(t))
	if err = native.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.InitCore(ctx); err != nil {
		t.Fatal(err)
	}
	f := &trackingFixture{a: a, ctx: ctx, server: NewFactory(a, native, events.New(db, vault)), token: strings.Repeat("f", 64)}
	f.http = httptest.NewServer(f.server.Handler(f.token))
	t.Cleanup(func() { f.http.Close(); cancel(); native.Close(); db.Close() })
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
	started := time.Now()
	result, n, elapsed, err := trackingRequest(client, url, token, name, args)
	for err == nil {
		fields, _ := result["structuredContent"].(map[string]any)
		pending, _ := fields["operation"].(map[string]any)
		if pending == nil {
			return result, n, time.Since(started), nil
		}
		if time.Since(started) > 15*time.Second {
			return nil, n, elapsed, fmt.Errorf("operation did not finish")
		}
		time.Sleep(10 * time.Millisecond)
		result, n, elapsed, err = trackingRequest(client, url, token, "operation_result", map[string]any{"id": pending["id"]})
	}
	return result, n, elapsed, err
}

func trackingRequest(client *http.Client, url, token, name string, args map[string]any) (map[string]any, int, time.Duration, error) {
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
