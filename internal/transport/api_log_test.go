package transport

import (
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/httpbudget"
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func apiLogFixture(t *testing.T) *Server {
	t.Helper()
	return apiLogFixtureForApp(t, &app.App{DataDir: t.TempDir()})
}

func apiLogFixtureForApp(t *testing.T, a *app.App) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{App: a, operations: &operationQueue{ctx: ctx}}
	t.Cleanup(func() {
		cancel()
		awaitTransportGroup(t, &s.apiLogWorkers, "direct API log workers")
		drainTransportBudget(t)
	})
	return s
}

type apiLogWarningHandler struct {
	slog.Handler
	warnings chan struct{}
}

func (h apiLogWarningHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "Code Factory API call log unavailable; requests continue without retry" {
		select {
		case h.warnings <- struct{}{}:
		default:
		}
	}
	return nil
}

func apiLogRecords(t *testing.T, path string, want int) []apiCallRecord {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			out := []apiCallRecord{}
			valid := true
			for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
				if len(line) == 0 {
					continue
				}
				var record apiCallRecord
				if json.Unmarshal(line, &record) != nil {
					valid = false
					break
				}
				out = append(out, record)
			}
			if valid && len(out) >= want {
				return out
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("API log records were not written")
	return nil
}

func TestAPILogRecordsMCPParametersMethodsFailuresAndRedactsSecrets(t *testing.T) {
	f := newTrackingFixture(t)
	call := func(name string, args map[string]any) {
		t.Helper()
		if _, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, name, args); err != nil {
			t.Fatal(err)
		}
	}
	call("factory_state", map[string]any{"state": "open", "queue": "review"})
	call("factory_state", map[string]any{"queue": "invalid"})
	call("answer_session_question", map[string]any{"token": "fixture-private-jira-value"})
	call("answer_session_question", map[string]any{"id": "missing-request", "answer": map[string]any{"answers": map[string]any{"q1": map[string]any{"answers": []string{"fixture-private-answer"}}}}})
	call("session_chat", map[string]any{"id": "missing-run", "text": "fixture-private-message"})
	// A URL parameter is useful, but its userinfo, query and fragment are not.
	call("factory_item", map[string]any{"target": "https://user:password@github.com/owner/repo/pull/1?access_token=fixture-query-secret#fixture-fragment"})
	call("factory_state", map[string]any{"API Key": "spaced-key-secret", "metadata": map[string]any{"custom": "metadata-secret"}})
	resource := map[string]any{"jsonrpc": "2.0", "id": 7, "method": "resources/read", "params": map[string]any{"uri": ResourceURI}}
	body, _ := json.Marshal(resource)
	req, _ := http.NewRequest("POST", f.http.URL+"/mcp", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	res, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	unauth, _ := http.NewRequest("POST", f.http.URL+"/mcp", strings.NewReader(`{"secret":"unauthorized-body-secret"}`))
	res, err = f.http.Client().Do(unauth)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	path := filepath.Join(f.a.DataDir, "logs", "api-calls.jsonl")
	records := apiLogRecords(t, path, 9)
	if len(records) != 9 {
		t.Fatal("request logging duplicated or omitted calls", len(records))
	}
	if records[0].API != "factory_state" || records[0].RPCMethod != "tools/call" || len(records[0].Parameters.(map[string]any)) != 0 || records[0].APIVersion != 2 || records[0].ResponseBytes == 0 || records[0].StartedAt.IsZero() || records[0].DurationMS < 0 {
		t.Fatal("missing API parameters/timing", records[0])
	}
	if records[7].API != "resources/read" || records[8].HTTPStatus != 401 {
		t.Fatal("resource or unauthorized request not recorded", records[7:])
	}
	data, _ := os.ReadFile(path)
	for _, secret := range []string{f.token, "fixture-private-jira-value", "fixture-private-answer", "fixture-private-message", "fixture-query-secret", "fixture-fragment", "password", "unauthorized-body-secret", "spaced-key-secret", "metadata-secret"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("API log leaked %q", secret)
		}
	}
	info, _ := os.Stat(path)
	dir, _ := os.Stat(filepath.Dir(path))
	if info.Mode().Perm() != 0600 || dir.Mode().Perm() != 0700 {
		t.Fatal("API logs are not private")
	}
}

func TestAPILogOwnerParametersTimingAndUnchangedRequestBody(t *testing.T) {
	s := apiLogFixture(t)
	const body = `{"action":"check","targets":["codex"],"token":"owner-body-secret"}`
	h := s.LogAPI("owner", "owner-auth-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil || string(got) != body {
			t.Error("logging changed request bytes", err)
		}
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/admin/dependencies/operations", strings.NewReader(body)))
	path := filepath.Join(s.App.DataDir, "logs", "api-calls.jsonl")
	record := apiLogRecords(t, path, 1)[0]
	params := record.Parameters.(map[string]any)["arguments"].(map[string]any)
	if record.API != "dependency_operations" || record.HTTPStatus != 202 || record.DurationMS < 20 || params["action"] != "check" || params["token"] != "[REDACTED]" {
		t.Fatal("owner call details are incomplete", record)
	}
	if w.Code != 202 {
		t.Fatal("logging changed the owner response")
	}
}

func TestAPILogRotationConcurrencyAndFailurePreserveAPI(t *testing.T) {
	previous, writer, flags := slog.Default(), stdlog.Writer(), stdlog.Flags()
	warnings := make(chan struct{}, 1)
	slog.SetDefault(slog.New(apiLogWarningHandler{slog.NewTextHandler(io.Discard, nil), warnings}))
	t.Cleanup(func() { slog.SetDefault(previous); stdlog.SetOutput(writer); stdlog.SetFlags(flags) })
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "api-calls.jsonl")
	log := &apiCallLog{path: path, maxBytes: 600}
	log.record(apiCallRecord{API: "seed", StartedAt: time.Now().UTC(), Parameters: map[string]any{}})
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 40; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			log.record(apiCallRecord{API: fmt.Sprint("api-", n), StartedAt: time.Now().UTC(), Parameters: map[string]any{"id": n}, HTTPStatus: 200})
		}(n)
	}
	wg.Wait()
	files, err := filepath.Glob(path + "*")
	if err != nil || len(files) != 4 {
		t.Fatal("rotation is not bounded", files, err)
	}
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil || info.Size() > 600 || info.Mode().Perm() != 0600 {
			t.Fatal("invalid retained log", file, info, err)
		}
		data, _ := os.ReadFile(file)
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			if !json.Valid(line) {
				t.Fatal("concurrent logging corrupted a JSONL record")
			}
		}
	}
	// Refuse a linked log path rather than changing unrelated owner files.
	select {
	case <-warnings:
		t.Fatal("ordinary rotation failed before the refusal fixture")
	default:
	}
	other := filepath.Join(t.TempDir(), "preserve.txt")
	if err := os.WriteFile(other, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	s := apiLogFixture(t)
	if err := os.Mkdir(filepath.Join(s.App.DataDir, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(s.App.DataDir, "logs", "api-calls.jsonl")); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.LogAPI("owner", "token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	awaitTransportSignal(t, warnings, "queued logging failure")
	data, _ := os.ReadFile(other)
	if w.Code != 204 || string(data) != "preserve" {
		t.Fatal("log failure changed API behavior or unrelated file")
	}
}

func TestAPILogBoundsMalformedBodiesAndPreservesPanic(t *testing.T) {
	s := apiLogFixture(t)
	for _, body := range []string{`{"method":"tools/call","params":{"token":"malformed-secret"`, strings.Repeat("oversized-private-payload", apiLogBodyLimit/8)} {
		w := httptest.NewRecorder()
		s.LogAPI("mcp", "token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n, err := io.Copy(io.Discard, r.Body)
			if err != nil || n != int64(len(body)) {
				t.Error("logging truncated the actual request", n, err)
			}
			w.WriteHeader(400)
		})).ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
	}
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		s.LogAPI("owner", "token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("private panic payload") })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/healthz", nil))
	}()
	path := filepath.Join(s.App.DataDir, "logs", "api-calls.jsonl")
	records := apiLogRecords(t, path, 3)
	data, _ := os.ReadFile(path)
	var panicRecord *apiCallRecord
	for i := range records {
		if records[i].Panicked {
			panicRecord = &records[i]
		}
	}
	if !panicked || panicRecord == nil || panicRecord.HTTPStatus != 0 || panicRecord.HTTPStatusKnown || panicRecord.HandlerStatus != 500 || len(data) > 3*(8<<10) {
		t.Fatal("panic handling or record bounds changed", records)
	}
	for _, private := range []string{"malformed-secret", "oversized-private-payload", "private panic payload"} {
		if bytes.Contains(data, []byte(private)) {
			t.Fatal("invalid request/panic text leaked", private)
		}
	}
}

func TestAPILogCapacityRejectionsReachDaemonAndExportedBoundary(t *testing.T) {
	s := apiLogFixture(t)
	release, entered := make(chan struct{}), make(chan struct{}, httpbudget.Capacity)
	var once sync.Once
	var wg sync.WaitGroup
	releaseWork := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		releaseWork()
		awaitTransportGroup(t, &wg, "capacity fixture responses")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := httpbudget.Drain(ctx); err != nil {
			t.Error("capacity fixture drain", err)
		}
	})
	blocker := httpbudget.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { entered <- struct{}{}; <-release }))
	for i := 0; i < httpbudget.Capacity; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			blocker.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		}()
	}
	for i := 0; i < httpbudget.Capacity; i++ {
		awaitTransportSignal(t, entered, "capacity fixture admission")
	}
	var calls atomic.Int32
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	mux := http.NewServeMux()
	mux.Handle("/healthz", s.LogAPI("owner", "private-token", next))
	mux.Handle("/mcp", s.LogAPI("mcp", "private-token", next))
	root := s.BoundHTTP(s.operations.ctx, mux)
	for _, row := range []struct {
		handler http.Handler
		path    string
	}{
		{root, "/healthz?token=private-query"}, {root, "/mcp?token=private-query"},
		{root, "/private-route?token=private-query"},
		{httpbudget.Wrap(s.LogAPI("owner", "private-token", next)), "/healthz"},
	} {
		w := httptest.NewRecorder()
		started := time.Now()
		row.handler.ServeHTTP(w, httptest.NewRequest("POST", row.path, strings.NewReader("private-body")))
		if w.Code != 503 || !strings.Contains(w.Body.String(), "request_not_started") || time.Since(started) > 100*time.Millisecond {
			t.Fatal("unbounded/dishonest capacity rejection", w.Code)
		}
	}
	records := apiLogRecords(t, filepath.Join(s.App.DataDir, "logs", "api-calls.jsonl"), 4)
	if calls.Load() != 0 || len(records) != 4 {
		t.Fatal("business started or duplicated rejection audit", calls.Load(), len(records))
	}
	seen := map[string]int{}
	for _, record := range records {
		seen[record.API]++
		if record.HTTPStatus != 503 || !record.HTTPStatusKnown || record.RejectionReason != "request_not_started" || record.RejectedCount != 1 || record.Coalesced || record.StartedAt.IsZero() {
			t.Fatal("missing truthful rejection diagnostic", record)
		}
	}
	if seen["healthz"] != 2 || seen["mcp_http"] != 1 || seen["owner_unknown"] != 1 {
		t.Fatal("missing bounded API identities", seen)
	}
	data, _ := os.ReadFile(filepath.Join(s.App.DataDir, "logs", "api-calls.jsonl"))
	for _, secret := range []string{"private-token", "private-query", "private-route", "private-body"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("rejection audit retained private request data", secret)
		}
	}
}

func TestAPILogQueueDropsAndRecordsOutcomeWithoutCommandPayload(t *testing.T) {
	s := apiLogFixture(t)
	sink := s.callLog()
	sink.mu.Lock()
	var once sync.Once
	unlock := func() { once.Do(sink.mu.Unlock) }
	defer unlock()
	h := s.LogAPI("mcp", "private-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		io.WriteString(w, `{"result":{"structuredContent":{"data":{"response":{"state":"queued"}}}}}`)
	}))
	invoke := func(name string) {
		body := fmt.Sprintf(`{"method":"tools/call","params":{"name":%q,"arguments":{"text":"private-command-body"}}}`, name)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
	}
	for n := 0; n < 300; n++ {
		invoke("fixture")
	}
	unlock()
	path := filepath.Join(s.App.DataDir, "logs", "api-calls.jsonl")
	apiLogRecords(t, path, 256)
	invoke("final")
	var final apiCallRecord
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		records := apiLogRecords(t, path, 257)
		for _, record := range records {
			if record.API == "final" {
				final = record
				break
			}
		}
		if final.API != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if final.DroppedLogs == 0 || final.Outcome != "queued" || final.APIVersion != 2 || final.ResponseBytes == 0 {
		t.Fatal("missing bounded logging evidence", final)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private-command-body")) {
		t.Fatal("command payload entered disk log")
	}
}

func TestAPILogWorkersFinishBeforeFixtureStorageDisposal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	s := &Server{App: &app.App{DataDir: dir}, operations: &operationQueue{ctx: ctx}}
	t.Cleanup(func() {
		cancel()
		awaitTransportGroup(t, &s.apiLogWorkers, "standalone API log workers")
		drainTransportBudget(t)
	})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	first, second := s.LogAPI("owner", "fixture", next), s.LogAPI("mcp", "fixture", next)
	invoke := func(h http.Handler) { h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/healthz", nil)) }
	invoke(first)
	invoke(second)
	apiLogRecords(t, filepath.Join(dir, "logs", "api-calls.jsonl"), 2)
	done := make(chan struct{})
	go func() { s.apiLogWorkers.Wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("active logger workers were not owned")
	case <-time.After(20 * time.Millisecond):
	}
	sink := s.callLog()
	sink.mu.Lock()
	var once sync.Once
	unlock := func() { once.Do(sink.mu.Unlock) }
	defer unlock()
	for range 100 {
		invoke(first)
		invoke(second)
	}
	cancel()
	unlock()
	awaitTransportSignal(t, done, "all Handler logger workers exiting")
	drainTransportBudget(t)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal("owned worker raced storage disposal", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("disposed logging storage recreated", err)
	}
}

func TestAPILogLargeRPCBacklogHasByteBoundAndReleasesReplayBodies(t *testing.T) {
	f := newTrackingFixture(t)
	sink := f.server.callLog()
	sink.mu.Lock()
	var once sync.Once
	unlock := func() { once.Do(sink.mu.Unlock) }
	defer unlock()
	handlers := []http.Handler{f.server.Handler(f.token), f.server.Handler(f.token)}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "unknown_tool", "arguments": map[string]any{"text": strings.Repeat("private-large-body", 55000)}}})
	if err != nil || len(body) >= rpcRequestLimit {
		t.Fatal("invalid near-limit RPC fixture", len(body), err)
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	for n := 0; n < 64; n++ {
		req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		reply := httptest.NewRecorder()
		handlers[n%len(handlers)].ServeHTTP(reply, req)
		if reply.Code == http.StatusServiceUnavailable && bytes.Contains(reply.Body.Bytes(), []byte("outcome_unknown")) {
			// Race instrumentation may exhaust the HTTP deadline while decoding
			// a near-limit body; wait for its owned handler before measuring heap.
			drainTransportBudget(t)
		} else if reply.Code != http.StatusOK || !bytes.Contains(reply.Body.Bytes(), []byte(`"error"`)) {
			t.Fatal("fixture did not reject unknown tool", reply.Code, reply.Body.String())
		}
	}
	drainTransportBudget(t)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc+32<<20 {
		t.Errorf("completed near-limit RPC logging retained more than32MiB: before%d after%d", before.HeapAlloc, after.HeapAlloc)
	}
	if retained := sink.retained.Load(); retained <= 0 || retained > apiLogQueueBytes {
		t.Errorf("shared backlog byte admission outside bound: %d", retained)
	}
	unlock()
	path := filepath.Join(f.a.DataDir, "logs", "api-calls.jsonl")
	apiLogRecords(t, path, 1)
	deadline := time.Now().Add(2 * time.Second)
	var dropped bool
	for time.Now().Before(deadline) {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"open_factory","arguments":{}}}`))
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		reply := httptest.NewRecorder()
		handlers[0].ServeHTTP(reply, req)
		if reply.Code != http.StatusOK {
			t.Fatal("post-backlog opener failed", reply.Code)
		}
		for _, record := range apiLogRecords(t, path, 1) {
			if record.API == "open_factory" && record.DroppedLogs > 0 {
				dropped = true
			}
		}
		if dropped {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !dropped {
		t.Error("large queued bodies did not consume a bounded log byte budget")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private-large-body")) {
		t.Error("raw command payload reached log")
	}
}
