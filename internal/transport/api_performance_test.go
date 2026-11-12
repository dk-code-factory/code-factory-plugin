package transport

import (
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/events"
	"codefactory/internal/factory"
	"codefactory/internal/httpbudget"
	"codefactory/internal/store"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
	t.Setenv("CODE_FACTORY_API_PERFORMANCE", "1")
	TestPluginAPIBudget(t)
	t.Run("protocol", testProtocolAPILatency)
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
			if f.server != nil {
				awaitTransportGroup(t, &f.server.apiLogWorkers, "event latency log workers")
				drainTransportBudget(t)
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

// Keep protocol inventory coverage independent of the API v2 tool catalog gate.
func testProtocolAPILatency(t *testing.T) {
	f := newTrackingFixture(t)
	type sample struct {
		Name    string  `json:"name"`
		MS      float64 `json:"ms"`
		Bytes   int     `json:"bytes"`
		Outcome string  `json:"outcome"`
	}
	samples := []sample{}
	defer func() {
		maxima := map[string]float64{}
		for _, s := range samples {
			maxima[s.Name] = max(maxima[s.Name], s.MS)
		}
		data, err := json.MarshalIndent(map[string]any{"limit_ms": 100, "samples": samples, "maxima": maxima}, "", "  ")
		path := filepath.Join("..", "..", "verification-results", "protocol-api-latency.json")
		if err == nil {
			err = os.MkdirAll(filepath.Dir(path), 0700)
		}
		if err == nil {
			err = os.WriteFile(path, data, 0600)
		}
		if err != nil {
			t.Error(err)
		}
	}()
	record := func(name string, n int, elapsed time.Duration, outcomes ...string) {
		t.Helper()
		outcome := "success"
		if len(outcomes) > 0 {
			outcome = outcomes[0]
		}
		samples = append(samples, sample{name, float64(elapsed) / float64(time.Millisecond), n, outcome})
		if elapsed > 100*time.Millisecond {
			t.Errorf("%s exceeded 100 ms: %s", name, elapsed)
		}
	}
	seenMethods := map[string]bool{}
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
	if len(catalog["tools"].([]any)) == 0 {
		t.Fatal("empty tool catalog")
	}
	rpc("tools/call", map[string]any{"name": "open_factory", "arguments": map[string]any{}})
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
}
