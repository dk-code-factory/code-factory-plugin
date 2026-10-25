package transport

import (
	"bytes"
	"codefactory/internal/app"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

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
	call("list_inbox", map[string]any{"state": "open", "queue": "review"})
	call("list_inbox", map[string]any{"queue": "invalid"})
	call("set_jira_credential", map[string]any{"token": "fixture-private-jira-value"})
	call("answer_request", map[string]any{"id": "missing-request", "answer": map[string]any{"answers": map[string]any{"q1": map[string]any{"answers": []string{"fixture-private-answer"}}}}})
	call("send_message", map[string]any{"id": "missing-run", "text": "fixture-private-message"})
	// A URL parameter is useful, but its userinfo, query and fragment are not.
	call("track_work", map[string]any{"target": "https://user:password@github.com/owner/repo/pull/1?access_token=fixture-query-secret#fixture-fragment"})
	call("list_inbox", map[string]any{"API Key": "spaced-key-secret", "metadata": map[string]any{"custom": "metadata-secret"}})
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
	if records[0].API != "list_inbox" || records[0].RPCMethod != "tools/call" || records[0].Parameters.(map[string]any)["queue"] != "review" || records[0].StartedAt.IsZero() || records[0].DurationMS < 0 {
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
	s := &Server{App: &app.App{DataDir: t.TempDir()}}
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

func TestAPILogActualOwnerHandlerRecordsRejectedInput(t *testing.T) {
	f := newTrackingFixture(t)
	request := httptest.NewRequest("POST", "/admin/dependencies/operations", strings.NewReader(`{"id":"invalid","action":"check","targets":["unknown"]}`))
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.server.DependencyHandler(f.token).ServeHTTP(w, request)
	if w.Code != 400 {
		t.Fatal("owner validation changed", w.Code)
	}
	record := apiLogRecords(t, filepath.Join(f.a.DataDir, "logs", "api-calls.jsonl"), 1)[0]
	if record.API != "dependency_operations" || record.HTTPStatus != 400 || record.Parameters.(map[string]any)["arguments"].(map[string]any)["id"] != "invalid" {
		t.Fatal("owner rejection was not logged", record)
	}
}

func TestAPILogRotationConcurrencyAndFailurePreserveAPI(t *testing.T) {
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
	other := filepath.Join(t.TempDir(), "preserve.txt")
	if err := os.WriteFile(other, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{App: &app.App{DataDir: t.TempDir()}}
	if err := os.Mkdir(filepath.Join(s.App.DataDir, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(s.App.DataDir, "logs", "api-calls.jsonl")); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.LogAPI("owner", "token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	data, _ := os.ReadFile(other)
	if w.Code != 204 || string(data) != "preserve" {
		t.Fatal("log failure changed API behavior or unrelated file")
	}
}

func TestAPILogBoundsMalformedBodiesAndPreservesPanic(t *testing.T) {
	s := &Server{App: &app.App{DataDir: t.TempDir()}}
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
	if !panicked || !records[2].Panicked || records[2].HTTPStatus != 500 || len(data) > 3*(8<<10) {
		t.Fatal("panic handling or record bounds changed", records)
	}
	for _, private := range []string{"malformed-secret", "oversized-private-payload", "private panic payload"} {
		if bytes.Contains(data, []byte(private)) {
			t.Fatal("invalid request/panic text leaked", private)
		}
	}
}
