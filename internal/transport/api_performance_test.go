package transport

import (
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/factory"
	"codefactory/internal/httpbudget"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var apiPerformance = flag.Bool("api-performance", false, "run isolated API latency measurements")

// Run after correctness suites, without race instrumentation or parallel workers.
func TestUserFacingAPILatency(t *testing.T) {
	if !*apiPerformance {
		t.Skip("isolated API performance gate")
	}
	type sample struct {
		Name    string  `json:"name"`
		MS      float64 `json:"ms"`
		Bytes   int     `json:"bytes"`
		Outcome string  `json:"outcome"`
	}
	samples := []sample{}
	defer func() {
		if path := filepath.Join("..", "..", "verification-results", "api-latency.json"); path != "" {
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Error(err)
				return
			}
			data, err := json.MarshalIndent(map[string]any{"limit_ms": 100, "samples": samples}, "", "  ")
			if err != nil {
				t.Error(err)
				return
			}
			if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
				t.Error(err)
			}
		}
	}()
	record := func(name string, n int, elapsed time.Duration, outcomes ...string) {
		t.Helper()
		outcome := "success"
		if len(outcomes) != 0 {
			outcome = outcomes[0]
		}
		samples = append(samples, sample{Name: name, MS: float64(elapsed) / float64(time.Millisecond), Bytes: n, Outcome: outcome})
		if elapsed > 100*time.Millisecond {
			t.Errorf("%s exceeded 100 ms: %s", name, elapsed)
		}
	}

	for _, delay := range []string{"0", "125"} {
		t.Run("provider-delay-"+delay, func(t *testing.T) {
			t.Setenv("CF_FAKE_OPERATION_DELAY_MS", delay)
			f := newTrackingFixture(t)
			f.http.Client().Timeout = 2 * time.Second
			// Never read operator authentication or invoke installed service control.
			tools := t.TempDir()
			for _, name := range []string{"gh", "systemctl", "tunnel-client"} {
				if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", tools+":"+os.Getenv("PATH"))
			checkout := t.TempDir()
			for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture"}} {
				c := exec.Command("git", args...)
				c.Dir = checkout
				if b, e := c.CombinedOutput(); e != nil {
					t.Fatal(e, string(b))
				}
			}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if delay == "125" {
					time.Sleep(125 * time.Millisecond)
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/user" {
					fmt.Fprint(w, `{"login":"fixture"}`)
				} else {
					fmt.Fprint(w, `[]`)
				}
			}))
			defer provider.Close()
			gh := providers.NewGitHub()
			gh.BaseURL = provider.URL
			gh.Token = func(context.Context) (string, error) { return "fixture", nil }
			f.a.GitHub = gh
			settings := factory.Defaults()
			settings.Repositories = []factory.Repository{{ID: "o/r", Checkout: checkout, Base: "HEAD"}}
			if e := f.a.Store.Put(f.ctx, "factory-settings", "effective", settings, ""); e != nil {
				t.Fatal(e)
			}
			if e := f.a.SaveRepository(f.ctx, domain.Repository{ID: "o/r", Checkout: checkout, Base: "HEAD"}); e != nil {
				t.Fatal(e)
			}
			item := domain.Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "issue", State: "open", Title: "Fixture", Body: strings.Repeat("retained body ", 100), Revision: "r1", Updated: time.Now().UTC()}
			if e := f.a.Store.Put(f.ctx, "item", item.ID, item, ""); e != nil {
				t.Fatal(e)
			}
			if e := f.a.Store.Put(f.ctx, "user", item.ID, domain.UserState{ID: item.ID}, ""); e != nil {
				t.Fatal(e)
			}
			if e := f.a.Store.Put(f.ctx, "identity", "github", domain.GitHubViewer{Login: "fixture"}, ""); e != nil {
				t.Fatal(e)
			}
			if err := f.a.Store.Write(f.ctx, func(tx *store.Tx) error {
				for n := 2; n <= 1000; n++ {
					id := fmt.Sprintf("o/r#%d", n)
					row := item
					row.ID = id
					row.Number = n
					row.Body = strings.Repeat("saved source ", 1000)
					if err := tx.Put("item", id, row); err != nil {
						return err
					}
					if err := tx.Put("user", id, domain.UserState{ID: id}); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			seenMethods := map[string]bool{}
			call := func(name string, args map[string]any) map[string]any {
				t.Helper()
				var operationID string
				polled := false
				recordTool := func(label string, result map[string]any, n int, elapsed time.Duration, requestError error) {
					t.Helper()
					outcome := "success"
					var shapeError string
					if requestError != nil || result["isError"] == true {
						outcome = "error"
					} else {
						sc, _ := result["structuredContent"].(map[string]any)
						if raw, ok := sc["operation"]; ok {
							pending, _ := raw.(map[string]any)
							id, _ := pending["id"].(string)
							if id == "" || pending["tool"] != name || pending["state"] != "running" || sc["data"] != nil || (operationID != "" && id != operationID) {
								outcome, shapeError = "error", "invalid or changed accepted operation identity"
							} else {
								operationID = id
								outcome = "accepted_pending"
							}
						} else if _, ok := sc["data"]; !ok {
							outcome, shapeError = "error", "completed tool response omitted structuredContent.data"
						}
					}
					record(label, n, elapsed, outcome)
					seenMethods["tools/call"] = true
					if shapeError != "" {
						t.Fatalf("%s: %s: %v", name, shapeError, result)
					}
				}
				result, n, d, e := trackingRequest(f.http.Client(), f.http.URL, f.token, name, args)
				recordTool(name, result, n, d, e)
				deadline := time.Now().Add(10 * time.Second)
				for e == nil && result["isError"] != true {
					sc, _ := result["structuredContent"].(map[string]any)
					pending, _ := sc["operation"].(map[string]any)
					if pending == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("%s operation did not finish", name)
					}
					time.Sleep(10 * time.Millisecond)
					polled = true
					result, n, d, e = trackingRequest(f.http.Client(), f.http.URL, f.token, "operation_result", map[string]any{"id": pending["id"]})
					recordTool("operation_result", result, n, d, e)
				}
				if e != nil || result["isError"] == true {
					t.Fatalf("%s failed: %v %v", name, e, result)
				}
				seen[name] = true
				if polled {
					seen["operation_result"] = true
				}
				if sc, ok := result["structuredContent"].(map[string]any); ok {
					if data, ok := sc["data"].(map[string]any); ok {
						return data
					}
				}
				return nil
			}
			rpc := func(method string, params map[string]any, expectedError ...int) map[string]any {
				t.Helper()
				notification := strings.HasPrefix(method, "notifications/")
				version := "2025-11-25"
				if method == "server/discover" {
					version = "2026-07-28"
					params["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": version, "io.modelcontextprotocol/clientInfo": map[string]string{"name": "latency-fixture", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
				}
				message := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
				if !notification {
					message["id"] = 1
				}
				b, _ := json.Marshal(message)
				req, _ := http.NewRequest("POST", f.http.URL+"/mcp", bytes.NewReader(b))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				req.Header.Set("MCP-Protocol-Version", version)
				req.Header.Set("Mcp-Method", method)
				req.Header.Set("Authorization", "Bearer "+f.token)
				start := time.Now()
				res, e := f.http.Client().Do(req)
				if e != nil {
					t.Fatal(e)
				}
				body, e := io.ReadAll(res.Body)
				res.Body.Close()
				elapsed := time.Since(start)
				if notification {
					record(method, len(body), elapsed, "notification_accepted")
					if e != nil || res.StatusCode != http.StatusAccepted || len(body) != 0 {
						t.Fatal("notification was not accepted without RPC response", method, res.StatusCode, e, string(body))
					}
					seenMethods[method] = true
					return nil
				}
				var envelope map[string]any
				if e != nil || res.StatusCode != http.StatusOK || json.Unmarshal(body, &envelope) != nil {
					t.Fatal(method, res.StatusCode, e, string(body))
				}
				if len(expectedError) > 0 {
					record(method, len(body), elapsed, "protocol_rejection")
					failure, _ := envelope["error"].(map[string]any)
					if failure["code"] != float64(expectedError[0]) || envelope["result"] != nil {
						t.Fatal("unsupported protocol handler returned wrong rejection", method, string(body))
					}
					seenMethods[method] = true
					return nil
				}
				record(method, len(body), elapsed)
				result, ok := envelope["result"].(map[string]any)
				if envelope["error"] != nil || !ok {
					t.Fatal(method, string(body))
				}
				seenMethods[method] = true
				return result
			}
			initialized := rpc("initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "latency-fixture", "version": "1"}})
			if initialized["protocolVersion"] != "2025-11-25" || initialized["serverInfo"] == nil {
				t.Fatal("initialize lost negotiated version or identity", initialized)
			}
			discovery := rpc("server/discover", map[string]any{})
			capabilities, _ := discovery["capabilities"].(map[string]any)
			if capabilities["events"] == nil || discovery["supportedVersions"] == nil {
				t.Fatal("discovery lost production events capability or versions", discovery)
			}
			prompts := rpc("prompts/list", map[string]any{})
			if list, ok := prompts["prompts"].([]any); !ok || len(list) != 0 {
				t.Fatal("unexpected prompt inventory", prompts)
			}
			rpc("prompts/get", map[string]any{"name": "unregistered-fixture"}, -32602)
			rpc("completion/complete", map[string]any{"ref": map[string]any{"type": "ref/resource", "uri": ResourceURI}, "argument": map[string]any{"name": "value", "value": ""}}, -32601)
			rpc("resources/subscribe", map[string]any{"uri": ResourceURI}, -32601)
			rpc("resources/unsubscribe", map[string]any{"uri": ResourceURI}, -32601)
			rpc("logging/setLevel", map[string]any{"level": "warning"})
			rpc("notifications/initialized", map[string]any{})
			rpc("notifications/cancelled", map[string]any{"requestId": "no-running-fixture-request"})
			rpc("notifications/roots/list_changed", map[string]any{})
			rpc("notifications/progress", map[string]any{"progressToken": "fixture-progress", "progress": 1, "total": 1})

			catalog := rpc("tools/list", map[string]any{})
			t.Logf("TOOLS %d", len(catalog["tools"].([]any)))
			rpc("resources/list", map[string]any{})
			for _, uri := range []string{ResourceURI, SettingsResourceURI} {
				rpc("resources/read", map[string]any{"uri": uri})
			}
			rpc("events/list", map[string]any{})
			f.server.Events.Client = &http.Client{Transport: latencyCallbackTransport(func(r *http.Request) (*http.Response, error) {
				var challenge map[string]any
				if err := json.NewDecoder(r.Body).Decode(&challenge); err != nil {
					return nil, err
				}
				body, _ := json.Marshal(map[string]any{"challenge": challenge["challenge"]})
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
			})}
			subscription := map[string]any{"name": "inbox.changed", "delivery": map[string]any{"mode": "webhook", "url": "https://callback.example.invalid/events", "secret": "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))}}
			rpc("events/subscribe", subscription)
			rpc("events/unsubscribe", subscription)
			rpc("ping", map[string]any{})
			rpc("resources/templates/list", map[string]any{})
			webhookService := factory.New(f.ctx, f.a, "")
			defer webhookService.Close()
			webhook := httptest.NewServer(webhookService.Webhook(func(context.Context) (string, error) { return "fixture-webhook-key", nil }))
			defer webhook.Close()
			webhook.Client().Timeout = 2 * time.Second
			body := []byte(`{"repository":{"full_name":"o/r"},"action":"synchronize","pull_request":{"number":1,"state":"open","title":"Fixture"}}`)
			for n := 0; n < 20; n++ {
				req, _ := http.NewRequest("POST", webhook.URL+"/github/webhook", bytes.NewReader(body))
				id := fmt.Sprintf("latency-delivery-%d", n)
				req.Header.Set("X-GitHub-Delivery", id)
				req.Header.Set("X-GitHub-Event", "pull_request")
				mac := hmac.New(sha256.New, []byte("fixture-webhook-key"))
				mac.Write(body)
				req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
				start := time.Now()
				res, err := webhook.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				received, err := io.ReadAll(res.Body)
				res.Body.Close()
				outcome := "success"
				if err != nil || res.StatusCode != 202 {
					outcome = "error"
				}
				record("github/webhook", len(received), time.Since(start), outcome)
				if err != nil || res.StatusCode != 202 {
					t.Fatal("signed webhook was not accepted", res.StatusCode, err)
				}
				var saved map[string]any
				if err = f.a.Store.Get(f.ctx, "factory-delivery", id, &saved); err != nil || saved["state"] != "pending" {
					t.Fatal("webhook acknowledged without durable delivery", err, saved)
				}
			}

			for n := 0; n < 3; n++ {
				call("open_factory", map[string]any{})
				call("open_settings", map[string]any{})
				for _, view := range []string{"working", "issues", "mine", "review", "messages", "configuration", "conversation", "events"} {
					call("factory_state", map[string]any{"view": view, "itemId": item.ID})
				}
				call("factory_item", map[string]any{"id": item.ID})
				call("session_questions", map[string]any{})
				call("session_transcript", map[string]any{"id": item.ID})
				for _, kind := range []string{"events", "deliveries", "batches", "rules"} {
					call("factory_events", map[string]any{"kind": kind})
				}
				call("user_action", map[string]any{"id": item.ID, "action": "read", "revision": "r1"})
				call("link_items", map[string]any{"source": item.ID, "target": "o/r#2", "remove": n%2 == 1})
				proposal := call("propose_configuration", map[string]any{"settings": settings})
				call("confirm_configuration", map[string]any{"id": proposal["id"], "confirm": true})
				call("inspect_configuration", map[string]any{"cwd": checkout})
				call("diagnostics", map[string]any{})
				call("sync_repositories", map[string]any{})
			}
			call("session_chat", map[string]any{"itemId": item.ID, "text": "Inspect fixture"})
			var qid string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				questions, e := store.List[factory.Question](f.ctx, f.a.Store, "factory-question")
				if e != nil {
					break
				}
				for _, q := range questions {
					if q.SessionID == item.ID && q.Status == "pending" {
						qid = q.ID
						break
					}
				}
				if qid != "" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if qid != "" {
				call("answer_session_question", map[string]any{"id": qid, "answer": map[string]any{"answers": map[string]any{"scope": map[string]any{"answers": []string{"Focused"}}}}})
			} else {
				t.Error("no native question")
			}
			call("session_control", map[string]any{"id": item.ID, "action": "interrupt"})
			call("session_control", map[string]any{"id": item.ID, "action": "resume"})
			call("session_control", map[string]any{"id": item.ID, "action": "interrupt"})
			call("session_control", map[string]any{"id": item.ID, "action": "cleanup"})
			call("configuration_chat", map[string]any{"text": "Inspect fixture configuration"})
			// Resolve an explicitly retained uncertain creation without replaying work.
			if e := f.a.Store.Put(f.ctx, "factory-session", "uncertain", factory.Session{ID: "uncertain", ThreadPending: true, Execution: "interrupted"}, ""); e != nil {
				t.Fatal(e)
			}
			call("resolve_session_delivery", map[string]any{"id": "uncertain", "decision": "retry"})
			ackElapsed, ackBytes := subscriptionListenHandshake(t)
			record("subscriptions/listen", ackBytes, ackElapsed, "stream_ack")
			seenMethods["subscriptions/listen"] = true
			// The SDK has no public method catalog. Read only its registered map
			// keys, including production custom methods; a changed SDK layout or
			// newly registered method must fail rather than silently lose coverage.
			methods := reflect.ValueOf(f.server.MCP).Elem().FieldByName("receiveMethods")
			if !methods.IsValid() || methods.Kind() != reflect.Map {
				t.Fatal("SDK receiving-method inventory unavailable")
			}
			for _, key := range methods.MapKeys() {
				if !seenMethods[key.String()] {
					t.Errorf("registered protocol method %s has no latency scenario", key.String())
				}
			}
			for method := range seenMethods {
				if !methods.MapIndex(reflect.ValueOf(method)).IsValid() {
					t.Errorf("timed method %s is not registered", method)
				}
			}
			t.Logf("timed %d registered protocol methods including incoming notifications and custom events", methods.Len())

			// The discovered catalog is the coverage oracle; adding a tool without a
			// successful performance scenario must fail this gate.
			if delay == "125" {
				for _, raw := range catalog["tools"].([]any) {
					name := raw.(map[string]any)["name"].(string)
					if !seen[name] {
						t.Errorf("tool %s has no successful latency sample", name)
					}
				}
			}

		})
	}
	maxima := map[string]float64{}
	for _, s := range samples {
		if s.MS > maxima[s.Name] {
			maxima[s.Name] = s.MS
		}
	}
	names := []string{}
	for name := range maxima {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Logf("%s maximum %.3f ms", name, maxima[name])
	}
}

type latencyCallbackTransport func(*http.Request) (*http.Response, error)

func (f latencyCallbackTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Rejection timing is recorded independently from successful subscription calls.
// Only the callback transport is delayed; no external callback network is used.
func TestEventCallbackAPILatency(t *testing.T) {
	if !*apiPerformance {
		t.Skip("isolated API performance gate")
	}
	type sample struct {
		Name    string  `json:"name"`
		Outcome string  `json:"outcome"`
		MS      float64 `json:"ms"`
		Bytes   int     `json:"bytes"`
	}
	type response struct {
		method   string
		body     []byte
		envelope map[string]any
		status   int
		elapsed  time.Duration
		err      error
	}
	samples := []sample{}
	defer func() {
		path := filepath.Join("..", "..", "verification-results", "event-api-latency.json")
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Error(e)
			return
		}
		b, e := json.MarshalIndent(map[string]any{"limit_ms": 100, "samples": samples}, "", "  ")
		if e != nil {
			t.Error(e)
			return
		}
		if e = os.WriteFile(path, append(b, '\n'), 0600); e != nil {
			t.Error(e)
		}
	}()
	record := func(t *testing.T, label, outcome string, got response) {
		t.Helper()
		samples = append(samples, sample{Name: label, Outcome: outcome, MS: float64(got.elapsed) / float64(time.Millisecond), Bytes: len(got.body)})
		if got.elapsed > 100*time.Millisecond {
			t.Errorf("%s exceeded 100 ms: %s", label, got.elapsed)
		}
		if outcome == "deadline_rejected" {
			if got.err != nil || got.status != http.StatusServiceUnavailable || got.envelope["error"] != "outcome_unknown" {
				t.Fatalf("%s did not return truthful timeout: %d %v %s", label, got.status, got.err, got.body)
			}
			return
		}
		if got.err != nil || got.status != http.StatusOK {
			t.Fatalf("%s HTTP failure: %d %v %s", label, got.status, got.err, got.body)
		}
		if outcome == "callback_rejected" {
			failure, _ := got.envelope["error"].(map[string]any)
			data, _ := failure["data"].(map[string]any)
			if failure["code"] != float64(-32015) || data["reason"] != "connection_failed" {
				t.Fatalf("%s did not return callback rejection: %s", label, got.body)
			}
		} else if got.envelope["error"] != nil || got.envelope["result"] == nil {
			t.Fatalf("%s did not succeed: %s", label, got.body)
		}
	}
	for _, phase := range []string{"verification-headers", "verification-body", "delivery-headers", "delivery-body", "store-admission"} {
		t.Run(phase, func(t *testing.T) {
			f := newEventLatencyFixture(t)
			f.http.Client().Timeout = 2 * time.Second
			workCtx, cancelWork := context.WithCancel(f.ctx)
			var workers sync.WaitGroup
			t.Cleanup(func() {
				cancelWork()
				f.http.CloseClientConnections()
				exited := make(chan struct{})
				go func() { workers.Wait(); close(exited) }()
				select {
				case <-exited:
				case <-time.After(time.Second):
					t.Error("event latency fixture workers did not exit")
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if e := httpbudget.Drain(ctx); e != nil {
					t.Error("event latency cleanup did not drain actual handlers", e)
				}
			})
			launch := func(work func()) {
				workers.Add(1)
				go func() { defer workers.Done(); work() }()
			}
			rpc := func(method string, params map[string]any) response {
				got := response{method: method}
				b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
				req, _ := http.NewRequestWithContext(workCtx, "POST", f.http.URL+"/mcp", bytes.NewReader(b))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				req.Header.Set("MCP-Protocol-Version", "2025-11-25")
				req.Header.Set("Authorization", "Bearer "+f.token)
				start := time.Now()
				res, e := f.http.Client().Do(req)
				if e != nil {
					got.err = e
					got.elapsed = time.Since(start)
					return got
				}
				got.status = res.StatusCode
				got.body, got.err = io.ReadAll(res.Body)
				res.Body.Close()
				got.elapsed = time.Since(start)
				if got.err == nil {
					got.err = json.Unmarshal(got.body, &got.envelope)
				}
				return got
			}
			started, closed := make(chan struct{}), make(chan struct{})
			f.server.Events.Client = &http.Client{Transport: latencyCallbackTransport(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					return nil, e
				}
				verification := body["type"] == "verification"
				payload := []byte("{}")
				if verification {
					payload, _ = json.Marshal(map[string]any{"challenge": body["challenge"]})
				}
				res := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload))}
				if r.URL.Path != "/delayed" {
					return res, nil
				}
				if strings.HasPrefix(phase, "delivery-") && verification {
					return res, nil
				}
				res.Body = &latencyCallbackBody{Reader: bytes.NewReader(payload), closed: closed, before: func() {
					if !strings.HasSuffix(phase, "headers") {
						close(started)
						time.Sleep(125 * time.Millisecond)
					}
				}}
				if strings.HasSuffix(phase, "headers") {
					close(started)
					time.Sleep(125 * time.Millisecond)
				}
				return res, nil
			})}
			subscription := func(path string) map[string]any {
				return map[string]any{"name": "inbox.changed", "delivery": map[string]any{"mode": "webhook", "url": "https://callback.example.invalid/" + path, "secret": "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))}}
			}
			toRemove := subscription("remove")
			record(t, phase+"/events/subscribe/immediate", "success", rpc("events/subscribe", toRemove))
			delayed := subscription("delayed")
			slow := make(chan response, 1)
			tick := make(chan error, 1)
			if phase == "store-admission" {
				launch(func() {
					tick <- f.a.Store.Write(workCtx, func(tx *store.Tx) error { close(started); time.Sleep(125 * time.Millisecond); return nil })
					close(closed)
				})
			} else if strings.HasPrefix(phase, "delivery-") {
				record(t, phase+"/events/subscribe/verified", "success", rpc("events/subscribe", delayed))
				if e := f.a.Store.Write(f.ctx, func(tx *store.Tx) error { return tx.Event("inbox.changed", "item-1", nil) }); e != nil {
					t.Fatal(e)
				}
				launch(func() { tick <- f.server.Events.Tick(workCtx) })
				toRemove = delayed
			} else {
				launch(func() { slow <- rpc("events/subscribe", delayed) })
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("callback delay did not start")
			}
			calls := []struct {
				method string
				args   map[string]any
			}{{"events/list", map[string]any{}}, {"events/subscribe", subscription("parallel")}, {"events/unsubscribe", toRemove}}
			parallel := make(chan response, len(calls))
			for _, call := range calls {
				launch(func() { parallel <- rpc(call.method, call.args) })
			}
			for range calls {
				got := latencyAwait(t, "concurrent event response", parallel)
				outcome := "success"
				if phase == "store-admission" && got.method != "events/list" {
					outcome = "deadline_rejected"
				}
				record(t, phase+"/"+got.method+"/concurrent", outcome, got)
			}
			if strings.HasPrefix(phase, "verification-") {
				record(t, phase+"/events/subscribe/bounded-rejection", "callback_rejected", latencyAwait(t, "delayed verification response", slow))
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("delayed callback did not close")
			}
			if strings.HasPrefix(phase, "delivery-") || phase == "store-admission" {
				if e := latencyAwait(t, "background event/store worker", tick); e != nil {
					t.Fatal(e)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if e := httpbudget.Drain(ctx); e != nil {
				t.Fatal("timed-out handler did not exit", e)
			}
			rows, e := store.List[map[string]any](f.ctx, f.a.Store, "subscription")
			if e != nil {
				t.Fatal(e)
			}
			if phase == "store-admission" && (len(rows) != 1 || rows[0]["url"] != "https://callback.example.invalid/remove") {
				t.Fatal("canceled store waiter changed subscription state", rows)
			}
			for _, row := range rows {
				if row["url"] == "https://callback.example.invalid/delayed" {
					t.Fatal("late callback activated or resurrected subscription")
				}
			}
			record(t, phase+"/events/unsubscribe/idempotent", "success", rpc("events/unsubscribe", delayed))
		})
	}
	t.Run("subscriptions/listen-first-ack", func(t *testing.T) {
		elapsed, n := subscriptionListenHandshake(t)
		samples = append(samples, sample{Name: "subscriptions/listen/first-ack", Outcome: "stream_ack", MS: float64(elapsed) / float64(time.Millisecond), Bytes: n})
		if elapsed > 100*time.Millisecond {
			t.Errorf("stream acknowledgement exceeded 100 ms: %s", elapsed)
		}
	})
	for _, sample := range samples {
		t.Logf("%s %s %.3f ms", sample.Name, sample.Outcome, sample.MS)
	}
}

type latencyCallbackBody struct {
	io.Reader
	before func()
	closed chan struct{}
	once   sync.Once
}

func (b *latencyCallbackBody) Read(p []byte) (int, error) {
	b.once.Do(b.before)
	return b.Reader.Read(p)
}
func (b *latencyCallbackBody) Close() error { close(b.closed); return nil }

func latencyAwait[T any](t *testing.T, label string, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
	var zero T
	return zero
}

// This event-only fixture owns its cleanup so a missing handler exit fails
// promptly instead of entering an unconditional database close behind that work.
func newEventLatencyFixture(t *testing.T) *trackingFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	db, e := store.Open(filepath.Join(dir, "state.db"))
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	vault, e := store.NewVault(db, dir)
	if e != nil {
		cancel()
		db.Close()
		t.Fatal(e)
	}
	a := &app.App{Store: db, Vault: vault, DataDir: dir}
	native := factory.New(ctx, a, "unused")
	f := &trackingFixture{a: a, ctx: ctx, token: strings.Repeat("f", 64)}
	t.Cleanup(func() {
		cancel()
		if f.http != nil {
			f.http.CloseClientConnections()
		}
		drainCtx, drainCancel := context.WithTimeout(context.Background(), time.Second)
		defer drainCancel()
		if e := httpbudget.Drain(drainCtx); e != nil {
			t.Error("skipping store close while actual HTTP workers remain", e)
			return
		}
		closed := make(chan error, 1)
		go func() {
			if f.http != nil {
				f.http.Close()
			}
			native.Close()
			closed <- db.Close()
		}()
		select {
		case e := <-closed:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(time.Second):
			t.Error("event latency fixture close did not finish")
		}
	})
	if e = a.InitCore(ctx); e != nil {
		t.Fatal(e)
	}
	if e = native.Init(ctx); e != nil {
		t.Fatal(e)
	}
	f.server = NewFactory(a, native, events.New(db, vault))
	f.http = httptest.NewServer(f.server.Handler(f.token))
	return f
}

// Exercise the real SDK custom-method boundary in the ordinary correctness
// suite: old protocols detach HTTP cancellation unless its origin is preserved.
func TestEventStoreAdmissionPreservesRequestCancellation(t *testing.T) {
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		for _, method := range []string{"events/subscribe", "events/unsubscribe"} {
			for _, trigger := range []string{"response-deadline", "caller-cancel"} {
				t.Run(version+"/"+method+"/"+trigger, func(t *testing.T) {
					f := newEventLatencyFixture(t)
					f.http.Client().Timeout = time.Second
					f.server.Events.Client = &http.Client{Transport: latencyCallbackTransport(func(r *http.Request) (*http.Response, error) {
						var body map[string]any
						if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
							return nil, e
						}
						b, _ := json.Marshal(map[string]any{"challenge": body["challenge"]})
						return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
					})}
					subscription := func(path string) map[string]any {
						return map[string]any{"name": "inbox.changed", "delivery": map[string]any{"mode": "webhook", "url": "https://callback.example.invalid/" + path, "secret": "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))}}
					}
					type result struct {
						status int
						body   []byte
						err    error
					}
					rpc := func(ctx context.Context, method string, args map[string]any) result {
						params := map[string]any{}
						for key, value := range args {
							params[key] = value
						}
						params["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": version, "io.modelcontextprotocol/clientInfo": map[string]string{"name": "fixture", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
						b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
						req, _ := http.NewRequestWithContext(ctx, "POST", f.http.URL+"/mcp", bytes.NewReader(b))
						req.Header.Set("Content-Type", "application/json")
						req.Header.Set("Accept", "application/json, text/event-stream")
						req.Header.Set("Authorization", "Bearer "+f.token)
						req.Header.Set("MCP-Protocol-Version", version)
						req.Header.Set("Mcp-Method", method)
						req.Header.Set("Mcp-Name", "inbox.changed")
						res, e := f.http.Client().Do(req)
						if e != nil {
							return result{err: e}
						}
						defer res.Body.Close()
						body, e := io.ReadAll(res.Body)
						return result{res.StatusCode, body, e}
					}
					initial := rpc(f.ctx, "events/subscribe", subscription("remove"))
					var envelope map[string]any
					if initial.err != nil || initial.status != 200 || json.Unmarshal(initial.body, &envelope) != nil || envelope["result"] == nil {
						t.Fatal("initial subscription failed", initial.status, initial.err, string(initial.body))
					}
					entered := make(chan struct{})
					var enterOnce sync.Once
					f.server.MCP.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
						return func(ctx context.Context, name string, req mcp.Request) (mcp.Result, error) {
							if name == method {
								enterOnce.Do(func() { close(entered) })
							}
							return next(ctx, name, req)
						}
					})
					requestCtx, cancelRequest := context.WithCancel(f.ctx)
					started, writerExited, requestExited := make(chan struct{}), make(chan struct{}), make(chan struct{})
					writerResult, response := make(chan error, 1), make(chan result, 1)
					t.Cleanup(func() {
						cancelRequest()
						for _, exited := range []<-chan struct{}{requestExited, writerExited} {
							select {
							case <-exited:
							case <-time.After(time.Second):
								t.Error("cancellation regression worker did not exit")
							}
						}
						ctx, cancel := context.WithTimeout(context.Background(), time.Second)
						defer cancel()
						if e := httpbudget.Drain(ctx); e != nil {
							t.Error("cancellation regression did not drain", e)
						}
					})
					go func() {
						defer close(writerExited)
						writerResult <- f.a.Store.Write(f.ctx, func(tx *store.Tx) error { close(started); time.Sleep(125 * time.Millisecond); return nil })
					}()
					latencyAwait(t, "held store writer", started)
					args := subscription("remove")
					if method == "events/subscribe" {
						args = subscription("parallel")
					}
					go func() { defer close(requestExited); response <- rpc(requestCtx, method, args) }()
					latencyAwait(t, "actual custom method dispatch", entered)
					if trigger == "caller-cancel" {
						cancelRequest()
					}
					got := latencyAwait(t, "canceled custom method response", response)
					if trigger == "caller-cancel" {
						if got.err == nil {
							t.Fatal("caller cancellation returned a response", got.status, string(got.body))
						}
					} else {
						var failure map[string]any
						if got.err != nil || got.status != 503 || json.Unmarshal(got.body, &failure) != nil || failure["error"] != "outcome_unknown" {
							t.Fatal("deadline did not return truthful503", got.status, got.err, string(got.body))
						}
					}
					if e := latencyAwait(t, "released store writer", writerResult); e != nil {
						t.Fatal(e)
					}
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					if e := httpbudget.Drain(ctx); e != nil {
						t.Fatal("late custom handler did not exit", e)
					}
					rows, e := store.List[map[string]any](f.ctx, f.a.Store, "subscription")
					if e != nil || len(rows) != 1 || rows[0]["url"] != "https://callback.example.invalid/remove" {
						t.Fatal("canceled late admission changed subscription state", rows, e)
					}
				})
			}
		}
	}
}
