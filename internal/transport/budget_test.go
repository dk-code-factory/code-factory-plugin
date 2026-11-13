package transport

import (
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/httpbudget"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type budgetSample struct {
	Scenario     string  `json:"scenario"`
	Tool         string  `json:"tool"`
	Milliseconds float64 `json:"ms"`
	Bytes        int     `json:"bytes"`
	State        string  `json:"state"`
}

// This gate runs by itself after the heavy verification suites. It measures the
// authenticated production handler through complete JSON serialization and wire
// completion. Logging admission, socket/host transit and background completion
// are separate evidence.
func TestPluginAPIBudget(t *testing.T) {
	if os.Getenv("CODE_FACTORY_API_PERFORMANCE") != "1" {
		t.Skip("isolated API performance gate")
	}
	t.Setenv("CF_FAKE_OPERATION_DELAY_MS", "3000")
	t.Setenv("CF_FAKE_MODE", "inference")
	commands := t.TempDir()
	for _, name := range []string{"gh", "systemctl", "tunnel-client"} {
		if err := os.WriteFile(filepath.Join(commands, name), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", commands+":"+os.Getenv("PATH"))
	var mu sync.Mutex
	samples := []budgetSample{}
	defer func() {
		groups := map[string][]float64{}
		for _, sample := range samples {
			groups[sample.Tool] = append(groups[sample.Tool], sample.Milliseconds)
		}
		stats := map[string]map[string]float64{}
		for tool, values := range groups {
			sort.Float64s(values)
			stats[tool] = map[string]float64{"max": values[len(values)-1], "p50": values[(len(values)-1)/2], "p95": values[(len(values)-1)*95/100], "p99": values[(len(values)-1)*99/100]}
		}
		report := map[string]any{"apiVersion": APIVersion, "boundary": "authenticated HTTP response, including validation, serialization and wire completion; excludes logging admission, network transit and background completion", "samples": samples, "tools": stats}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		for _, path := range []string{filepath.Join("..", "..", "web", "performance-results", "api-timing.json"), filepath.Join("..", "..", "verification-results", "api-latency.json")} {
			if err = os.MkdirAll(filepath.Dir(path), 0700); err == nil {
				err = os.WriteFile(path, data, 0600)
			}
			if err != nil {
				t.Error(err)
			}
		}

		for _, sample := range samples {
			if sample.Milliseconds > float64(httpbudget.Duration)/float64(time.Millisecond) {
				t.Errorf("%s/%s exceeded 75 ms: %.3f", sample.Scenario, sample.Tool, sample.Milliseconds)
			}
			if sample.Bytes > responseLimit {
				t.Errorf("%s/%s exceeded 64 KiB: %d", sample.Scenario, sample.Tool, sample.Bytes)
			}
		}
	}()
	for _, scenario := range []string{"empty", "cold", "retained"} {
		t.Run(scenario, func(t *testing.T) {
			f := newTrackingFixture(t)
			if scenario != "empty" {
				count := 1
				if scenario == "retained" {
					count = 1000
				}
				err := f.a.Store.Write(f.ctx, func(tx *store.Tx) error {
					for n := 0; n < count; n++ {
						id := fmt.Sprintf("o/r#%d", n+1)
						if err := tx.Put("item", id, domain.Item{ID: id, Kind: "issue", Repository: "o/r", State: "open", Title: fmt.Sprint("retained item ", n), Body: strings.Repeat("searchable exact source 界<&", 3000), Revision: "r1"}); err != nil {
							return err
						}
						if err := tx.Put("user", id, domain.UserState{ID: id, New: true}); err != nil {
							return err
						}
					}
					return tx.Event("session.output", "o/r#1", map[string]any{"text": strings.Repeat("large output", 10000)})
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			handler := f.server.Handler(f.token)
			dispatch := func(method string, params map[string]any) *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
				req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+f.token)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				req.Header.Set("MCP-Protocol-Version", "2025-11-25")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				return w
			}
			invoke := func(phase, name string, args map[string]any) map[string]any {
				start := time.Now()
				w := dispatch("tools/call", map[string]any{"name": name, "arguments": args})
				duration := time.Since(start)
				var envelope struct {
					Result struct {
						IsError           bool `json:"isError"`
						StructuredContent struct {
							Data struct {
								Response map[string]any `json:"response"`
							}
						}
					}
					Error any
				}
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error != nil || envelope.Result.IsError {
					t.Errorf("%s invalid API response: %s (%v)", name, w.Body.String(), err)
				}
				result := envelope.Result.StructuredContent.Data.Response
				state, _ := result["state"].(string)
				mu.Lock()
				samples = append(samples, budgetSample{scenario + "/" + phase, name, float64(duration.Nanoseconds()) / 1e6, w.Body.Len(), state})
				mu.Unlock()
				return result
			}
			// Oversized read selectors must be rejected before entering retained
			// projection keys/dependencies, including near the HTTP request limit.
			f.server.projections.mu.Lock()
			entriesBefore := len(f.server.projections.entries)
			f.server.projections.mu.Unlock()
			for _, selector := range []struct{ tool, field string }{{"factory_item", "id"}, {"session_transcript", "id"}, {"factory_state", "itemId"}} {
				for _, identifier := range []string{strings.Repeat("x", 900<<10), strings.Repeat("界", 342)} {
					input := map[string]any{selector.field: identifier}
					if selector.tool == "factory_state" {
						input["view"] = "conversation"
					}
					started := time.Now()
					reply := dispatch("tools/call", map[string]any{"name": selector.tool, "arguments": input})
					elapsed := time.Since(started)
					var wire struct {
						Error  json.RawMessage `json:"error"`
						Result struct {
							IsError bool `json:"isError"`
						} `json:"result"`
					}
					mu.Lock()
					samples = append(samples, budgetSample{scenario + "/oversized-selector", selector.tool, float64(elapsed.Nanoseconds()) / 1e6, reply.Body.Len(), "rejected"})
					mu.Unlock()
					// The SDK includes the invalid value in its schema error. Near-limit
					// ASCII inputs therefore use the existing bounded-error backstop;
					// the explicit byte validator returns the1024-byte error otherwise.
					explicit := bytes.Contains(reply.Body.Bytes(), []byte("1024")) || len(identifier) > responseLimit && bytes.Contains(reply.Body.Bytes(), []byte("Tool response exceeded its bound"))
					if err := json.Unmarshal(reply.Body.Bytes(), &wire); err != nil || reply.Code != http.StatusOK || len(wire.Error) == 0 && !wire.Result.IsError || !explicit {
						t.Fatalf("oversized selector did not produce explicit rejection: tool=%s status=%d bytes=%d err=%v", selector.tool, reply.Code, reply.Body.Len(), err)
					}
				}
			}
			f.server.projections.mu.Lock()
			entriesAfter := len(f.server.projections.entries)
			f.server.projections.mu.Unlock()
			if entriesAfter != entriesBefore {
				t.Fatal("rejected selectors retained projection entries", entriesBefore, entriesAfter)
			}
			catalog := dispatch("tools/list", map[string]any{})
			var listing struct {
				Result struct{ Tools []struct{ Name string } }
			}
			if err := json.Unmarshal(catalog.Body.Bytes(), &listing); err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"source": strings.Repeat("large document 界<&", 6000)}
			large, err := f.server.documents.prepare(payload)
			if err != nil {
				t.Fatal(err)
			}
			version := large.(map[string]any)["document"].(documentDescriptor).Version
			args := map[string]map[string]any{
				"open_factory": {}, "open_settings": {}, "factory_state": {"view": "issues"}, "factory_item": {"id": "o/r#1"},
				"factory_events": {"kind": "events"}, "session_questions": {}, "session_transcript": {"id": "o/r#1"},
				"session_chat": {"itemId": "missing", "text": "fixture"}, "configuration_chat": {"text": "fixture"},
				"session_control": {"id": "missing", "action": "interrupt"}, "answer_session_question": {"id": "missing", "answer": map[string]any{}},
				"resolve_session_delivery": {"id": "missing", "decision": "discard"}, "inspect_configuration": {"cwd": t.TempDir()},
				"propose_configuration": {}, "confirm_configuration": {"id": "missing", "confirm": false}, "user_action": {"id": "missing", "action": "acknowledge"},
				"link_items": {"source": "missing", "target": "other"}, "sync_repositories": {}, "diagnostics": {},
				"operation_status": {"instanceId": f.server.operations.instance, "requestId": "unknown"}, "document_chunk": {"version": version, "index": 0},
			}
			for _, tool := range listing.Result.Tools {
				input, ok := args[tool.Name]
				if !ok {
					t.Fatalf("exposed tool %s has no performance input", tool.Name)
				}
				if isOperationTool(tool.Name) {
					input["instanceId"] = f.server.operations.instance
					input["requestId"] = app.ID()
				}
				invoke("all-tools", tool.Name, input)
			}
			if len(listing.Result.Tools) != len(args) {
				t.Fatal("gate inputs and tool catalog diverged")
			}
			// Healthy reads must eventually publish useful current state, not just pending.
			await := func(name string, input map[string]any) map[string]any {
				deadline := time.Now().Add(20 * time.Second)
				for time.Now().Before(deadline) {
					out := invoke("completion", name, input)
					if out["state"] == "ready" || out["state"] == "succeeded" {
						return out
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("%s never completed", name)
				return nil
			}
			for _, input := range args {
				if input["requestId"] == nil || input["requestId"] == "unknown" {
					continue
				}
				deadline := time.Now().Add(30 * time.Second)
				for time.Now().Before(deadline) {
					status := invoke("settle", "operation_status", map[string]any{"requestId": input["requestId"], "instanceId": input["instanceId"]})
					if state := status["state"]; state == "succeeded" || state == "failed" || state == "uncertain" {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			await("factory_state", args["factory_state"])
			if scenario == "empty" {
				return
			}
			detail := await("factory_item", args["factory_item"])
			if detail["value"] == nil {
				t.Fatal("detail never became useful")
			}
			// Independent clients poll while unrelated transcript writes continue.
			stop := make(chan struct{})
			done := make(chan struct{})
			started := make(chan struct{})
			var writes atomic.Int64
			var stopOnce sync.Once
			writerCtx, cancelWriter := context.WithCancel(f.ctx)
			stopWriter := func() {
				stopOnce.Do(func() { close(stop); cancelWriter() })
				awaitTransportSignal(t, done, "continuous latency writer exiting")
			}
			defer stopWriter()
			go func() {
				defer close(done)
				for {
					select {
					case <-stop:
						return
					default:
					}
					err := f.a.Store.Write(writerCtx, func(tx *store.Tx) error {
						return tx.Event("session.output", "unrelated", map[string]string{"delta": "live"})
					})
					if err != nil {
						if writerCtx.Err() == nil {
							t.Errorf("concurrent transcript write: %v", err)
						}
						return
					}
					if writes.Add(1) == 1 {
						close(started)
					}
					time.Sleep(time.Millisecond)
				}
			}()
			select {
			case <-started:
			case <-done:
				t.Fatal("writer stopped before committing its stimulus")
			case <-time.After(5 * time.Second):
				t.Fatal("writer never committed its stimulus")
			}
			halfway := make(chan struct{}, 8)
			resumeReads := make(chan struct{})
			var resumeOnce sync.Once
			resumeClients := func() { resumeOnce.Do(func() { close(resumeReads) }) }
			var clients sync.WaitGroup
			defer func() { resumeClients(); clients.Wait() }()
			for n := 0; n < 8; n++ {
				clients.Add(1)
				go func() {
					defer clients.Done()
					for sample := 0; sample < 12; sample++ {
						out := invoke("concurrent-writes", "factory_state", args["factory_state"])
						if out["state"] == "failed" {
							t.Error("current read failed during transcript writes")
						}
						if sample == 5 {
							halfway <- struct{}{}
							<-resumeReads
						}
					}
				}()
			}
			for n := 0; n < 8; n++ {
				select {
				case <-halfway:
				case <-time.After(5 * time.Second):
					t.Fatal("concurrent readers never reached midpoint")
				}
			}
			baselineWrites := writes.Load()
			deadline := time.Now().Add(5 * time.Second)
			for writes.Load() <= baselineWrites && time.Now().Before(deadline) {
				select {
				case <-done:
					t.Fatal("writer stopped during concurrent reads")
				default:
				}
				time.Sleep(time.Millisecond)
			}
			if writes.Load() <= baselineWrites {
				t.Fatal("no transcript commit between the two measured read batches")
			}
			resumeClients()
			clients.Wait()
			stopWriter()
			// Delay a real fixture provider and native subprocess for several seconds.
			providerStarted := make(chan struct{}, 1)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case providerStarted <- struct{}{}:
				default:
				}
				select {
				case <-r.Context().Done():
					return
				case <-time.After(3 * time.Second):
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/user" {
					fmt.Fprint(w, `{"login":"pilot"}`)
				} else {
					fmt.Fprint(w, "[]")
				}
			}))
			defer provider.Close()
			if err := f.a.Store.Put(f.ctx, "repository", "o/r", domain.Repository{ID: "o/r"}, ""); err != nil {
				t.Fatal(err)
			}
			gh := providers.NewGitHub()
			gh.BaseURL = provider.URL
			gh.Token = func(context.Context) (string, error) { return "fixture", nil }
			f.a.GitHub = gh
			id := app.ID()
			input := map[string]any{"requestId": id, "instanceId": f.server.operations.instance}
			invoke("slow-provider", "sync_repositories", input)
			// Even without repository data, viewer lookup exercises the delayed provider.
			select {
			case <-providerStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("provider delay was not exercised")
			}
			for n := 0; n < 20; n++ {
				invoke("slow-provider", "open_factory", map[string]any{})
				invoke("slow-provider", "factory_state", args["factory_state"])
			}
			await("operation_status", input)
			await("operation_status", map[string]any{"requestId": args["configuration_chat"]["requestId"], "instanceId": f.server.operations.instance})
			// Keep SQLite and the log sink blocked; admission and ready reads stay fast.
			locked := make(chan struct{})
			unlock := make(chan struct{})
			writeDone := make(chan error, 1)
			releaseDB := sync.OnceFunc(func() { close(unlock) })
			defer releaseDB()
			go func() {
				writeDone <- f.a.Store.Write(f.ctx, func(tx *store.Tx) error { close(locked); <-unlock; return nil })
			}()
			select {
			case <-locked:
			case err := <-writeDone:
				t.Fatalf("writer failed before barrier: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("writer did not reach barrier")
			}
			log := f.server.callLog()
			log.mu.Lock()
			releaseLog := sync.OnceFunc(log.mu.Unlock)
			defer releaseLog()
			for n := 0; n < 16; n++ {
				start := time.Now()
				// Extra RPC metadata is ignored by the tool but still crosses the
				// authenticated envelope/capture path with a near-limit body.
				reply := dispatch("tools/call", map[string]any{"name": "open_factory", "arguments": map[string]any{}, "padding": strings.Repeat("large-log-private", 50000)})
				elapsed := time.Since(start)
				var result struct {
					Result struct {
						StructuredContent struct {
							Data struct{ Response struct{ State string } }
						}
					}
					Error any
				}
				if err := json.Unmarshal(reply.Body.Bytes(), &result); err != nil || result.Error != nil || result.Result.StructuredContent.Data.Response.State != "ready" {
					t.Fatal("large-body opener did not stay useful", err, reply.Body.String())
				}
				mu.Lock()
				samples = append(samples, budgetSample{scenario + "/large-body-blocked-log", "open_factory", float64(elapsed.Nanoseconds()) / 1e6, reply.Body.Len(), "ready"})
				mu.Unlock()
			}
			if retained := log.retained.Load(); retained <= 0 || retained > apiLogQueueBytes {
				t.Fatal("large-body logging byte admission out of bounds", retained)
			}
			for n := 0; n < 300; n++ {
				invoke("blocked-database-log", "open_factory", map[string]any{})
				invoke("blocked-database-log", "factory_state", args["factory_state"])
			}
			releaseLog()
			releaseDB()
			select {
			case err := <-writeDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("writer did not finish")
			}
			// Saturate all ordinary workers, then the bounded pending queue, before timing busy.
			release := make(chan struct{})
			entered := make(chan struct{}, ordinaryWorkers)
			for n := 0; n < ordinaryWorkers; n++ {
				submitUntilAvailable(t, f.server.operations, f.server.operations.instance, fmt.Sprint("block", n), "block", fmt.Sprint(n), false, 1, func(ctx context.Context) (any, error) {
					entered <- struct{}{}
					select {
					case <-release:
					case <-ctx.Done():
					}
					return nil, nil
				})
			}
			for n := 0; n < ordinaryWorkers; n++ {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("saturation workers did not start")
				}
			}
			for n := 0; n < pendingLimit; n++ {
				submitUntilAvailable(t, f.server.operations, f.server.operations.instance, fmt.Sprint("pending", n), "block", "blocked", false, 1, func(context.Context) (any, error) { return nil, nil })
			}
			for n := 0; n < 20; n++ {
				input := map[string]any{"id": "missing", "action": "acknowledge", "instanceId": f.server.operations.instance, "requestId": app.ID()}
				if out := invoke("saturated", "user_action", input); out["state"] != "busy" {
					t.Error("full queue did not return busy", out)
				}
			}
			close(release)
		})
	}
}
