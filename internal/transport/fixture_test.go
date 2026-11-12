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
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type trackingFixture struct {
	a      *app.App
	native *factory.Service
	ctx    context.Context
	server *Server
	http   *httptest.Server
	token  string
	calls  []string
	close  func()
}

func newTrackingFixture(t *testing.T) *trackingFixture {
	t.Helper()
	return newTrackingFixtureAt(t, t.TempDir())
}

func newTrackingFixtureAt(t *testing.T, dir string) *trackingFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
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
	f := &trackingFixture{a: a, native: native, ctx: ctx, server: NewFactory(a, native, events.New(db, vault)), token: strings.Repeat("f", 64)}
	f.http = httptest.NewServer(f.server.Handler(f.token))
	var closeOnce sync.Once
	f.close = func() {
		closeOnce.Do(func() {
			f.http.Close()
			cancel()
			native.Close()
			db.Close()
			// Every Handler/LogAPI instance owns a worker; wait for actual exit before
			// TempDir disposal. Rejection auditors have their own boundary lifetime.
			awaitTransportGroup(t, &f.server.apiLogWorkers, "API log workers")
			drainTransportBudget(t)
		})
	}
	t.Cleanup(f.close)
	return f
}
func (f *trackingFixture) call(t *testing.T, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	f.calls = append(f.calls, name)
	argsCopy := map[string]any{}
	for key, value := range args {
		argsCopy[key] = value
	}
	operation := isOperationTool(name)
	if operation {
		argsCopy["requestId"] = app.ID()
		argsCopy["instanceId"] = f.server.operations.instance
	}
	original := name
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		result, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, name, argsCopy)
		if err != nil {
			t.Fatal(err)
		}
		if result["isError"] == true {
			return result, true
		}
		response := result["structuredContent"].(map[string]any)["data"].(map[string]any)["response"].(map[string]any)
		state := response["state"]
		if operation && name == original && state == "busy" {
			time.Sleep(time.Millisecond)
			continue
		}
		if state == "failed" || state == "uncertain" || state == "lost" {
			return response, true
		}
		if state == "ready" || state == "succeeded" {
			key := "value"
			if operation {
				key = "result"
			}
			data := response[key]
			if data == nil {
				return map[string]any{}, false
			}
			value := data.(map[string]any)
			if descriptor, ok := value["document"].(map[string]any); ok {
				chunks := int(descriptor["chunks"].(float64))
				wire := []byte{}
				for index := 0; index < chunks; index++ {
					result, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, "document_chunk", map[string]any{"version": descriptor["version"], "index": index})
					if err != nil {
						t.Fatal(err)
					}
					chunk := result["structuredContent"].(map[string]any)["data"].(map[string]any)["response"].(map[string]any)
					decoded, err := base64.StdEncoding.DecodeString(chunk["chunk"].(string))
					if err != nil {
						t.Fatal(err)
					}
					wire = append(wire, decoded...)
				}
				if err := json.Unmarshal(wire, &value); err != nil {
					t.Fatal(err)
				}
			}
			return value, false
		}
		if operation {
			name = "operation_status"
			argsCopy = map[string]any{"instanceId": argsCopy["instanceId"], "requestId": argsCopy["requestId"]}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s did not complete", original)
	return nil, true
}
func trackingCall(client *http.Client, url, token, name string, args map[string]any) (map[string]any, int, time.Duration, error) {
	return trackingRequest(client, url, token, name, args)
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
