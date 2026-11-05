package factory

import (
	"codefactory/internal/app"
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRPC struct {
	mu          sync.Mutex
	methods     []string
	inputs      []json.RawMessage
	tools       []json.RawMessage
	writes      []json.RawMessage
	replies     []json.RawMessage
	fail        string
	history     []json.RawMessage
	native      map[string]any
	nativeByCWD map[string]map[string]any
	onClose     func()
}

func (f *fakeRPC) Call(_ context.Context, method string, params, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.methods = append(f.methods, method)
	if method == f.fail {
		return fmt.Errorf("response lost")
	}
	value := any(map[string]any{})
	switch method {
	case "thread/start", "thread/resume":
		value = map[string]any{"thread": map[string]any{"id": "thread-1"}}
		if method == "thread/start" {
			f.tools = append(f.tools, bytes(params))
		}
	case "turn/start", "turn/steer":
		f.inputs = append(f.inputs, bytes(params))
		value = map[string]any{"turn": map[string]any{"id": fmt.Sprintf("turn-%d", len(f.inputs))}}
	case "config/read":
		value = map[string]any{"config": map[string]any{"profiles": map[string]any{"native": map[string]any{"model": "fixture-model"}}}}
		if f.native != nil {
			value = f.native
		}
		if f.nativeByCWD != nil {
			var p struct{ CWD string }
			_ = json.Unmarshal(bytes(params), &p)
			if config, ok := f.nativeByCWD[p.CWD]; ok {
				value = config
			}
		}
	case "config/batchWrite":
		f.writes = append(f.writes, bytes(params))
	case "skills/list":
		value = map[string]any{"data": []any{map[string]any{"skills": []any{map[string]any{"name": "any-installed-skill", "path": "/installed/SKILL.md", "enabled": true}}}}}
	case "thread/items/list":
		value = map[string]any{"data": f.history}
	}
	if out != nil {
		return json.Unmarshal(bytes(value), out)
	}
	return nil
}
func (f *fakeRPC) Reply(_ json.RawMessage, v any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, bytes(v))
	return nil
}
func (f *fakeRPC) Reject(_ json.RawMessage, _ string) error { return nil }
func (f *fakeRPC) Close() error {
	if f.onClose != nil {
		f.onClose()
	}
	return nil
}
func fixture(t *testing.T) (*Service, *fakeRPC, *time.Time) {
	return fixtureWithStoreFile(t, "state.db")
}
func fixtureWithStoreFile(t *testing.T, stateFile string) (*Service, *fakeRPC, *time.Time) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, stateFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := &app.App{Store: db, DataDir: dir}
	if err = a.InitCore(ctx); err != nil {
		t.Fatal(err)
	}
	s := New(ctx, a, "unused")
	t.Cleanup(s.Close)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	f := &fakeRPC{}
	s.Start = func(context.Context, Agent, func(codex.Message)) (RPC, error) { return f, nil }
	s.Workspace = func(context.Context, Session) (string, error) { return dir, nil }
	if err = s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	c := Defaults()
	c.Repositories = []Repository{{ID: "o/r"}}
	c.Rules = []Rule{{ID: "first", Repositories: []string{"*"}, Types: []string{"pull_request", "issue_comment"}, Instructions: "first action", Skills: []string{"any-installed-skill"}}, {ID: "second", Repositories: []string{"o/r"}, Types: []string{"pull_request"}, Instructions: "second action"}}
	if err = s.Store.Put(ctx, "factory-settings", "effective", c, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.projectSettings(ctx); err != nil {
		t.Fatal(err)
	}
	item := domain.Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "pr", State: "open", Head: "head-1", Title: "current PR"}
	item.Revision = item.ContentRevision()
	if err = s.Store.Put(ctx, "item", item.ID, item, ""); err != nil {
		t.Fatal(err)
	}
	must(t, s.Store.Put(ctx, "user", item.ID, domain.UserState{ID: item.ID, Actions: []domain.Action{}}, ""))
	return s, f, &now
}
func incoming(s *Service, facet, value string) Event {
	var item domain.Item
	_ = s.Store.Get(context.Background(), "item", "o/r#1", &item)
	return event(item, "pull_request", "synchronize", facet, value)
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func get(t *testing.T, s *Service) Session {
	t.Helper()
	v, e := s.Session(context.Background(), "o/r#1")
	must(t, e)
	return v
}

func TestQuietPeriodResetAndOrderedActions(t *testing.T) {
	s, f, now := fixture(t)
	ctx := context.Background()
	must(t, s.Observe(ctx, []Event{incoming(s, "head", "a")}, false))
	*now = now.Add(119 * time.Second)
	must(t, s.Tick(ctx))
	if len(f.inputs) != 0 {
		t.Fatal("sent before quiet period")
	}
	must(t, s.Observe(ctx, []Event{incoming(s, "head", "b")}, false))
	*now = now.Add(119 * time.Second)
	must(t, s.Tick(ctx))
	if len(f.inputs) != 0 {
		t.Fatal("timer did not reset")
	}
	*now = now.Add(time.Second)
	must(t, s.Tick(ctx))
	if len(f.inputs) != 1 {
		t.Fatalf("turns %d", len(f.inputs))
	}
	v := get(t, s)
	var b Batch
	must(t, s.Store.Get(ctx, "factory-batch", v.BatchID, &b))
	if len(b.Events) != 2 || len(b.Actions) != 4 || b.Actions[0].RuleID != "first" || b.Actions[1].RuleID != "second" || b.State != "delivered" {
		t.Fatalf("batch %#v", b)
	}
	if !strings.Contains(string(f.inputs[0]), "any-installed-skill") {
		t.Fatal("arbitrary installed skill was not sent")
	}
}
func TestConcurrentFirstEventsOneCanonicalSession(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if e := s.Observe(ctx, []Event{incoming(s, fmt.Sprint(i), fmt.Sprint(i))}, false); e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	ss, e := s.Sessions(ctx)
	must(t, e)
	if len(ss) != 1 || len(ss[0].Pending) != 20 {
		t.Fatalf("sessions %#v", ss)
	}
}
func TestBaselineAndPollingOverlap(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	e := incoming(s, "head", "a")
	must(t, s.Observe(ctx, []Event{e}, true))
	ss, err := s.Sessions(ctx)
	must(t, err)
	if len(ss) != 0 {
		t.Fatal("baseline launched historical work")
	}
	e.Value = "new"
	e.Source = "webhook"
	must(t, s.Observe(ctx, []Event{e}, false))
	e.Source = "poll"
	must(t, s.Observe(ctx, []Event{e}, false))
	if len(get(t, s).Pending) != 1 {
		t.Fatal("poll duplicated webhook")
	}
}
func TestChatFlushBusySteerHoldAndRetainedReuse(t *testing.T) {
	s, f, now := fixture(t)
	ctx := context.Background()
	must(t, s.Observe(ctx, []Event{incoming(s, "head", "a")}, false))
	_, err := s.Chat(ctx, "o/r#1", "start now", nil)
	must(t, err)
	if len(f.inputs) != 1 {
		t.Fatal("chat did not bypass timer")
	}
	must(t, s.Observe(ctx, []Event{incoming(s, "head", "b")}, false))
	*now = now.Add(5 * time.Minute)
	must(t, s.Tick(ctx))
	if len(f.inputs) != 1 {
		t.Fatal("automatic batch interrupted active turn")
	}
	_, err = s.Chat(ctx, "o/r#1", "steer", nil)
	must(t, err)
	if !contains(f.methods, "turn/steer") || len(get(t, s).Pending) != 0 {
		t.Fatal("steer omitted queued events")
	}
	v := get(t, s)
	s.handle(v.ID, v.Epoch, codex.Message{Method: "turn/completed", Params: bytes(map[string]any{"turn": map[string]any{"id": v.TurnID, "status": "completed"}})})
	_, err = s.Control(ctx, v.ID, "interrupt")
	must(t, err)
	must(t, s.Observe(ctx, []Event{incoming(s, "head", "c")}, false))
	*now = now.Add(5 * time.Minute)
	must(t, s.Tick(ctx))
	if len(f.inputs) != 2 {
		t.Fatal("explicit interruption did not hold queue")
	}
	_, err = s.Control(ctx, v.ID, "resume")
	must(t, err)
	count := 0
	for _, m := range f.methods {
		if m == "thread/start" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("retained thread was recreated")
	}
}
func TestStatusToolBlockingAndNativeAnswers(t *testing.T) {
	s, f, _ := fixture(t)
	ctx := context.Background()
	v, err := s.Chat(ctx, "o/r#1", "start", nil)
	must(t, err)
	call := func(state string, questions []string) {
		s.handle(v.ID, v.Epoch, codex.Message{ID: bytes("rpc-1"), Method: "item/tool/call", Params: bytes(map[string]any{"threadId": v.ThreadID, "tool": "code_factory_report_status", "arguments": map[string]any{"state": state, "summary": "summary", "questions": questions}})})
	}
	call("blocked", nil)
	if len(f.replies) != 1 {
		t.Fatal("invalid blocked status not rejected")
	}
	call("blocked", []string{"Choose the scope"})
	if len(f.replies) != 1 {
		t.Fatal("blocked tool response was not suspended")
	}
	v = get(t, s)
	if v.Reported != "blocked" || v.Execution != "waiting" {
		t.Fatal(v)
	}
	_, err = s.Chat(ctx, v.ID, "bypass question", nil)
	if err == nil {
		t.Fatal("blocking request bypassed")
	}
	qs, err := s.Questions(ctx)
	must(t, err)
	must(t, s.Answer(ctx, qs[0].ID, bytes(map[string]any{"answer": "approved scope"})))
	if len(f.replies) != 2 {
		t.Fatal("answer not delivered")
	}
	if s.Answer(ctx, qs[0].ID, bytes(map[string]any{"answer": "again"})) == nil {
		t.Fatal("duplicate answer accepted")
	}
	call("done", nil)
	v = get(t, s)
	s.handle(v.ID, v.Epoch, codex.Message{Method: "turn/completed", Params: bytes(map[string]any{"turn": map[string]any{"id": v.TurnID, "status": "completed"}})})
	v = get(t, s)
	if v.Reported != "done" || v.Execution != "idle" {
		t.Fatal(v)
	}
}
func TestRestartExpiresIdentitiesAndNeverReplaysUncertainBatch(t *testing.T) {
	s, f, _ := fixture(t)
	ctx := context.Background()
	f.fail = "turn/start"
	_, err := s.Chat(ctx, "o/r#1", "start", nil)
	if err == nil {
		t.Fatal("expected uncertain send")
	}
	v := get(t, s)
	q := Question{ID: "native", SessionID: v.ID, Epoch: v.Epoch, RequestID: bytes(1), Status: "pending"}
	must(t, s.Store.Put(ctx, "factory-question", q.ID, q, ""))
	must(t, s.Init(ctx))
	qs, e := s.Questions(ctx)
	must(t, e)
	if qs[0].Status != "stale" {
		t.Fatal(qs)
	}
	f.fail = ""
	before := len(f.inputs)
	_, err = s.Control(ctx, v.ID, "resume")
	if err == nil || len(f.inputs) != before {
		t.Fatal("uncertain send replayed")
	}
	f.history = []json.RawMessage{bytes(map[string]any{"type": "userMessage", "text": "[code-factory-batch:" + v.BatchID + "]"})}
	_, err = s.Control(ctx, v.ID, "resume")
	must(t, err)
}
func TestConfigurationConfirmationStalenessAndSafeBoundary(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	c, err := s.Settings(ctx)
	must(t, err)
	c.QuietSeconds = 7
	p, err := s.Propose(ctx, &c, nil)
	must(t, err)
	actual, _ := s.Settings(ctx)
	if actual.QuietSeconds != 120 || p.Diff == "" {
		t.Fatal("proposal applied without confirmation")
	}
	v, err := s.Chat(ctx, "o/r#1", "busy", nil)
	must(t, err)
	p, err = s.Confirm(ctx, p.ID, true)
	must(t, err)
	if p.State != "confirmed" {
		t.Fatal("active work did not defer configuration")
	}
	s.handle(v.ID, v.Epoch, codex.Message{Method: "turn/completed", Params: bytes(map[string]any{"turn": map[string]any{"id": v.TurnID, "status": "completed"}})})
	must(t, s.Tick(ctx))
	actual, _ = s.Settings(ctx)
	if actual.QuietSeconds != 7 || len(actual.Rules) != 2 {
		t.Fatal("confirmed settings did not preserve unrelated rules")
	}
	p, err = s.Propose(ctx, &c, nil)
	must(t, err)
	actual.QuietSeconds = 8
	must(t, s.Store.Put(ctx, "factory-settings", "effective", actual, ""))
	if _, err = s.Confirm(ctx, p.ID, true); err == nil {
		t.Fatal("stale proposal accepted")
	}
}
func TestMissingSkillsAndProfilesAreVisible(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	must(t, s.Observe(ctx, []Event{incoming(s, "head", "a")}, false))
	v := get(t, s)
	v.Agent.Profile = "missing"
	must(t, s.save(ctx, v))
	if _, e := s.Chat(ctx, v.ID, "start", nil); e == nil || !strings.Contains(e.Error(), "profile") {
		t.Fatal(e)
	}
	v = get(t, s)
	v.Agent = Agent{}
	v.Pending[0].Actions[0].Skills = []string{"missing"}
	must(t, s.save(ctx, v))
	if _, e := s.Chat(ctx, v.ID, "start", nil); e == nil || !strings.Contains(e.Error(), "skill") {
		t.Fatal(e)
	}
}
func TestWebhookSignaturePersistenceRedelivery(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	h := s.Webhook(func(context.Context) (string, error) { return "private-fixture-key", nil })
	body := `{"action":"synchronize","repository":{"full_name":"o/r"},"pull_request":{"number":1,"state":"open","head":{"sha":"new"}}}`
	sendAs := func(payload, kind, signature string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/github/webhook", strings.NewReader(payload))
		r.Header.Set("X-GitHub-Delivery", "d1")
		r.Header.Set("X-GitHub-Event", kind)
		r.Header.Set("X-Hub-Signature-256", signature)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	send := func(signature string) *httptest.ResponseRecorder { return sendAs(body, "pull_request", signature) }
	if send("sha256=bad").Code != 401 {
		t.Fatal("bad signature accepted")
	}
	mac := hmac.New(sha256.New, []byte("private-fixture-key"))
	_, _ = mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if send(sig).Code != 202 {
		t.Fatal("valid delivery rejected")
	}
	var d delivery
	must(t, s.Store.Get(ctx, "factory-delivery", "d1", &d))
	if d.State != "pending" {
		t.Fatal("delivery was not persisted before acknowledgement")
	}
	if send(sig).Code != 202 {
		t.Fatal("redelivery rejected")
	}
	original := d
	for _, collision := range []struct{ body, kind string }{{strings.Replace(body, "new", "other", 1), "pull_request"}, {body, "issue_comment"}} {
		mac := hmac.New(sha256.New, []byte("private-fixture-key"))
		_, _ = mac.Write([]byte(collision.body))
		if sendAs(collision.body, collision.kind, "sha256="+hex.EncodeToString(mac.Sum(nil))).Code != 503 {
			t.Fatal("different digest reused a delivery ID")
		}
		must(t, s.Store.Get(ctx, "factory-delivery", "d1", &d))
		if d.Digest != original.Digest || d.Type != original.Type || string(d.Body) != string(original.Body) || d.State != "pending" {
			t.Fatal("collision replaced retained delivery")
		}
	}
	if send(sig).Code != 202 {
		t.Fatal("valid redelivery rejected after collision")
	}
	must(t, s.Poll(ctx))
	if len(get(t, s).Pending) != 1 {
		t.Fatal("redelivery duplicated event")
	}
}

func TestNativeAppServerQuestionAndAnswer(t *testing.T) {
	s, _, _ := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := nativeStarter(testutil.FakeCodex(t))
	// The service normally retains a process beyond one request. This fixture's
	// entire process and startup handshake share the test's bounded lifetime.
	s.Start = func(_ context.Context, agent Agent, handler func(codex.Message)) (RPC, error) {
		return start(ctx, agent, handler)
	}
	_, err := s.Chat(ctx, "o/r#1", "Inspect the fixture", nil)
	must(t, err)
	deadline, _ := ctx.Deadline()
	var qs []Question
	for time.Now().Before(deadline) {
		qs, err = s.Questions(ctx)
		must(t, err)
		if len(qs) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(qs) != 1 {
		t.Fatal("native request missing")
	}
	must(t, s.Answer(ctx, qs[0].ID, bytes(map[string]any{"answers": map[string]any{"scope": map[string]any{"answers": []string{"Focused"}}}})))
	for time.Now().Before(deadline) {
		v := get(t, s)
		if v.Execution == "idle" {
			page, e := s.Store.SessionEvents(ctx, v.ID, 0, 100)
			must(t, e)
			if !strings.Contains(string(bytes(page)), "Answer received. Task complete.") {
				t.Fatal(page)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	v := get(t, s)
	t.Fatalf("native turn did not finish: %#v", v)
}

func TestRepositoryQuietOverrideAndIdleWithoutStatus(t *testing.T) {
	s, f, now := fixture(t)
	ctx := context.Background()
	settings, _ := s.Settings(ctx)
	seconds := 3
	settings.Repositories[0].QuietSeconds = &seconds
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	must(t, s.Observe(ctx, []Event{incoming(s, "head", "a")}, false))
	*now = now.Add(3 * time.Second)
	must(t, s.Tick(ctx))
	if len(f.inputs) != 1 {
		t.Fatal("repository override ignored")
	}
	v := get(t, s)
	s.handle(v.ID, v.Epoch, codex.Message{Method: "turn/completed", Params: bytes(map[string]any{"turn": map[string]string{"id": v.TurnID, "status": "completed"}})})
	v = get(t, s)
	if v.Execution != "idle" || v.Reported != "" {
		t.Fatal("turn completion implied done")
	}
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", v.ItemID, &item))
	item.State = "merged"
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	_, err := s.Chat(ctx, item.ID, "Inspect retained work after merge", nil)
	must(t, err)
	if get(t, s).ThreadID != v.ThreadID {
		t.Fatal("merge discarded retained conversation")
	}
}
func TestNativeConfigurationLayerVersionsPreserveUnrelatedSettings(t *testing.T) {
	s, f, _ := fixture(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "config.toml")
	f.native = map[string]any{"config": map[string]any{"model": "old", "unrelated": "keep"}, "layers": []any{map[string]any{"name": map[string]any{"type": "user", "file": file}, "version": "v1", "config": map[string]any{"model": "old", "unrelated": "keep"}}}}
	n := NativeEdit{CWD: s.Dir, File: file, Version: "v1", Edits: []ConfigEdit{{KeyPath: "model", Value: "new", MergeStrategy: "replace"}}}
	p, err := s.Propose(ctx, nil, &n)
	must(t, err)
	if contains(f.methods, "config/batchWrite") {
		t.Fatal("proposal wrote native settings")
	}
	if !strings.Contains(p.Diff, "unrelated") {
		t.Fatal("diff omitted actual layers")
	}
	p, err = s.Confirm(ctx, p.ID, true)
	must(t, err)
	if p.State != "applied" || !contains(f.methods, "config/batchWrite") {
		t.Fatal("native write did not follow confirmation")
	}
	if len(f.writes) != 1 {
		t.Fatal("native edit was not written exactly once")
	}
	var write struct {
		File    string       `json:"filePath"`
		Version string       `json:"expectedVersion"`
		Edits   []ConfigEdit `json:"edits"`
		Reload  bool         `json:"reloadUserConfig"`
	}
	must(t, json.Unmarshal(f.writes[0], &write))
	if write.File != file || write.Version != "v1" || write.Reload || len(write.Edits) != 1 || write.Edits[0].KeyPath != "model" || write.Edits[0].Value != "new" || write.Edits[0].MergeStrategy != "replace" {
		t.Fatal("native write was not scoped to the confirmed file, version and one key", string(f.writes[0]))
	}
	p, err = s.Propose(ctx, nil, &n)
	must(t, err)
	f.native["config"].(map[string]any)["unrelated"] = "owner edit"
	if _, err = s.Confirm(ctx, p.ID, true); err == nil {
		t.Fatal("stale native layering was accepted")
	}
}
func TestSecretAnswerRedactsSplitStreamingAndRetainedOutput(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	vault, err := store.NewVault(s.Store, s.Dir)
	must(t, err)
	s.App.Vault = vault
	v, err := s.Chat(ctx, "o/r#1", "start", nil)
	must(t, err)
	s.handle(v.ID, v.Epoch, codex.Message{ID: bytes(7), Method: "item/tool/requestUserInput", Params: bytes(map[string]any{"threadId": v.ThreadID, "questions": []any{map[string]any{"id": "secret", "question": "Private input", "isSecret": true}}})})
	qs, _ := s.Questions(ctx)
	must(t, s.Answer(ctx, qs[0].ID, bytes(map[string]any{"answers": map[string]any{"secret": map[string]any{"answers": []string{"sensitive-fixture-value"}}}})))
	for _, delta := range []string{"prefix sensitive-", "fixture-value suffix"} {
		s.handle(v.ID, v.Epoch, codex.Message{Method: "item/agentMessage/delta", Params: bytes(map[string]any{"threadId": v.ThreadID, "itemId": "message", "delta": delta})})
	}
	s.handle(v.ID, v.Epoch, codex.Message{Method: "item/completed", Params: bytes(map[string]any{"threadId": v.ThreadID, "item": map[string]string{"id": "message", "type": "agentMessage", "text": "completed sensitive-fixture-value"}})})
	s.handle(v.ID, v.Epoch, codex.Message{ID: bytes(8), Method: "item/tool/requestUserInput", Params: bytes(map[string]any{"threadId": v.ThreadID, "turnId": v.TurnID, "questions": []any{map[string]any{"id": "echo", "question": "Reconfirm sensitive-fixture-value", "options": []any{map[string]string{"label": "Use sensitive-fixture-value"}}}}})})
	records, e := s.Store.List(ctx, "factory-question")
	must(t, e)
	if strings.Contains(string(bytes(records)), "sensitive-fixture-value") {
		t.Fatal("native request stored plaintext secret")
	}
	qs, e = s.Questions(ctx)
	must(t, e)
	var echo Question
	for _, q := range qs {
		if q.Status == "pending" {
			echo = q
		}
	}
	must(t, s.Answer(ctx, echo.ID, bytes(map[string]any{"answers": map[string]any{"echo": map[string]any{"answers": []string{"Use [redacted]"}}}})))
	var delivered struct {
		Answers map[string]struct{ Answers []string }
	}
	must(t, json.Unmarshal(rpc.replies[len(rpc.replies)-1], &delivered))
	if got := delivered.Answers["echo"].Answers; len(got) != 1 || got[0] != "Use sensitive-fixture-value" {
		t.Fatal("masked native choice was not restored for the original request")
	}
	s.handle(v.ID, v.Epoch, codex.Message{Method: "process/closed"})
	s.Close()
	resumed := New(ctx, s.App, "unused")
	defer resumed.Close()
	next := &fakeRPC{}
	resumed.Start = func(context.Context, Agent, func(codex.Message)) (RPC, error) { return next, nil }
	must(t, resumed.Init(ctx))
	continued, e := resumed.Chat(ctx, v.ID, "resume", nil)
	must(t, e)
	foundResume := false
	for _, method := range next.methods {
		if method == "thread/resume" {
			foundResume = true
		}
		if method == "thread/start" {
			t.Fatal("retained secret session was replaced")
		}
	}
	if !foundResume {
		t.Fatal("native history was not resumed")
	}
	resumed.handle(continued.ID, continued.Epoch, codex.Message{Method: "item/completed", Params: bytes(map[string]any{"threadId": continued.ThreadID, "item": map[string]string{"id": "after-restart", "type": "agentMessage", "text": "resumed sensitive-fixture-value"}})})
	page, err := s.Store.SessionEvents(ctx, v.ID, 0, 100)
	must(t, err)
	out := string(bytes(page))
	if strings.Contains(out, "sensitive-") || !strings.Contains(out, "[redacted]") {
		t.Fatal("secret leaked into transcript", out)
	}
}

type fixtureGitHub struct {
	providers.GitHubSource
	items map[int]domain.Item
}

func (g *fixtureGitHub) PullRequest(_ context.Context, _ string, n int) (domain.Item, error) {
	i, ok := g.items[n]
	if !ok {
		return i, fmt.Errorf("unknown fixture PR")
	}
	return i, nil
}
func (g *fixtureGitHub) ItemDetail(_ context.Context, i domain.Item) (domain.Item, error) {
	return g.PullRequest(context.Background(), i.Repository, i.Number)
}
func TestCIWebhookAssociatesEveryCurrentHeadPRAndPollDeduplicates(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	settings, _ := s.Settings(ctx)
	settings.Rules[0].Types = append(settings.Rules[0].Types, "ci")
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	items := map[int]domain.Item{}
	for _, number := range []int{1, 2} {
		i := domain.Item{ID: fmt.Sprintf("o/r#%d", number), Repository: "o/r", Number: number, Kind: "pr", Head: "shared", State: "open", PR: &domain.PullRequest{Head: "shared", CI: "success", Checks: []domain.Check{{Name: "build", Conclusion: "success"}}}}
		items[number] = i
		must(t, s.Store.Put(ctx, "item", i.ID, i, ""))
	}
	s.App.GitHub = &fixtureGitHub{items: items}
	d := delivery{ID: "ci", Type: "check_run", State: "pending", At: s.Now(), Body: bytes(map[string]any{"repository": map[string]string{"full_name": "o/r"}, "check_run": map[string]any{"head_sha": "shared", "pull_requests": []any{map[string]int{"number": 1}, map[string]int{"number": 2}}}})}
	must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
	must(t, s.processDelivery(ctx, d))
	ss, err := s.Sessions(ctx)
	must(t, err)
	if len(ss) != 2 {
		t.Fatalf("CI associated %d sessions", len(ss))
	}
	for _, v := range ss {
		if len(v.Pending) != 1 || v.Pending[0].Event.Type != "ci" || v.Pending[0].Event.Context.PR != nil {
			t.Fatal(v)
		}
		var details []json.RawMessage
		must(t, json.Unmarshal(v.Pending[0].Event.Details, &details))
		if len(details) != 3 || string(details[1]) != `"success"` {
			t.Fatal("CI delta omitted status", details)
		}
		must(t, s.Observe(ctx, observations(items[v.Pending[0].Event.Context.Number]), false))
	}
	for _, v := range ss {
		current, e := s.Session(ctx, v.ID)
		must(t, e)
		ci := 0
		for _, p := range current.Pending {
			if p.Event.Type == "ci" {
				ci++
			}
		}
		if ci != 1 {
			t.Fatal("poll duplicated CI webhook")
		}
	}
}

func TestPriorityBookkeepingReusesThreadAndRejectsStaleAssessment(t *testing.T) {
	s, f, _ := fixture(t)
	ctx := context.Background()
	settings, _ := s.Settings(ctx)
	settings.PrioritySkill = "any-installed-skill"
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	must(t, s.projectSettings(ctx))
	must(t, s.AssessNext(ctx))
	v, err := s.Session(ctx, "priority:o/r")
	must(t, err)
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	config, err := s.App.Config(ctx)
	must(t, err)
	args := map[string]any{"itemId": item.ID, "revision": "stale", "context": s.App.AssessmentContext(ctx, item), "policy": config.PolicyVersion, "priority": 1, "rationale": "Regression evidence"}
	_, _, err = s.dynamic(ctx, &v, "code_factory_record_priority", bytes(args), codex.Message{})
	if err == nil {
		t.Fatal("stale assessment accepted")
	}
	args["revision"] = item.Revision
	_, _, err = s.dynamic(ctx, &v, "code_factory_record_priority", bytes(args), codex.Message{})
	must(t, err)
	s.handle(v.ID, v.Epoch, codex.Message{Method: "turn/completed", Params: bytes(map[string]any{"turn": map[string]string{"id": v.TurnID, "status": "completed"}})})
	must(t, s.AssessNext(ctx))
	if len(f.inputs) != 1 {
		t.Fatal("fresh assessment was not cached")
	}
	item.Title = "Updated urgency"
	item.Revision = item.ContentRevision()
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	must(t, s.AssessNext(ctx))
	starts := 0
	for _, m := range f.methods {
		if m == "thread/start" {
			starts++
		}
	}
	if starts != 1 || len(f.inputs) != 2 {
		t.Fatal("bookkeeping thread was not reused")
	}
}
func TestNativeAgentDefinitionUsesNativeTOMLAndProfile(t *testing.T) {
	s, f, _ := fixture(t)
	file := filepath.Join(t.TempDir(), "agent.toml")
	must(t, os.WriteFile(file, []byte("model = 'agent-model'\ndeveloper_instructions = 'native instruction'\n"), 0600))
	f.native = map[string]any{"config": map[string]any{"profiles": map[string]any{"custom": map[string]any{"model": "profile-model", "model_reasoning_effort": "medium"}}, "agents": map[string]any{"chosen": map[string]any{"config_file": file}}}}
	config, err := nativeOverrides(context.Background(), f, s.Dir, Agent{Profile: "custom", Definition: "chosen"})
	must(t, err)
	if config["model"] != "agent-model" || config["model_reasoning_effort"] != "medium" || config["developer_instructions"] != "native instruction" {
		t.Fatal(config)
	}
	ctx := context.Background()
	_, err = s.Chat(ctx, "o/r#1", "start with native instructions", &Agent{Profile: "custom", Definition: "chosen"})
	must(t, err)
	var params struct {
		DeveloperInstructions string `json:"developerInstructions"`
	}
	must(t, json.Unmarshal(f.tools[0], &params))
	if params.DeveloperInstructions != "native instruction\n\n"+guidance {
		t.Fatalf("native agent instructions were replaced: %q", params.DeveloperInstructions)
	}
	f.native = map[string]any{"config": map[string]any{"developer_instructions": "project instructions"}}
	config, err = nativeOverrides(ctx, f, s.Dir, Agent{})
	must(t, err)
	if config["developer_instructions"] != "project instructions" {
		t.Fatal("effective native project instructions were dropped")
	}
}
func TestRetainedWorktreeCleanupRefusesDirtyChanges(t *testing.T) {
	s, f, _ := fixture(t)
	ctx := context.Background()
	repo := t.TempDir()
	_, err := run(ctx, repo, "git", "init", "-b", "main")
	must(t, err)
	_, err = run(ctx, repo, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	must(t, err)
	settings, _ := s.Settings(ctx)
	settings.Repositories[0].Checkout = repo
	settings.Repositories[0].Base = "main"
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	item.Kind = "issue"
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	s.Workspace = s.workspace
	v, err := s.Chat(ctx, item.ID, "start", nil)
	must(t, err)
	sentinel := filepath.Join(v.Worktree, "user-work.txt")
	must(t, os.WriteFile(sentinel, []byte("keep this work"), 0600))
	_, err = s.Control(ctx, v.ID, "interrupt")
	must(t, err)
	if _, err = s.Control(ctx, v.ID, "cleanup"); err == nil {
		t.Fatal("dirty worktree was removed")
	}
	if _, err = os.Stat(sentinel); err != nil {
		t.Fatal("user work was lost")
	}
	must(t, os.Remove(sentinel))
	q := Question{ID: "cleanup-question", SessionID: v.ID, Epoch: v.Epoch, Status: "pending"}
	must(t, s.Store.Put(ctx, "factory-question", q.ID, q, ""))
	closed := make(chan struct{})
	f.onClose = func() {
		go func() {
			s.handle(v.ID, v.Epoch, codex.Message{Method: "turn/completed", Params: bytes(map[string]any{"turn": map[string]any{"id": v.TurnID, "status": "completed"}})})
			s.handle(v.ID, v.Epoch, codex.Message{Method: "process/closed"})
			close(closed)
		}()
	}
	_, err = s.Control(ctx, v.ID, "cleanup")
	must(t, err)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close callback did not finish")
	}
	retained := get(t, s)
	if retained.ThreadID != v.ThreadID || retained.Execution != "cleaned" || retained.Epoch != "" {
		t.Fatal("cleanup discarded history or delayed callbacks revived the session")
	}
	must(t, s.Store.Get(ctx, "factory-question", q.ID, &q))
	if q.Status != "stale" {
		t.Fatal("cleanup retained a live request")
	}
	if _, err = s.Chat(ctx, v.ID, "resume removed worktree", nil); err == nil || !strings.Contains(err.Error(), "cleaned") {
		t.Fatal("cleaned worktree resumed")
	}
}

func TestAnswerFlushesPendingEventsBeforeQuietDeadline(t *testing.T) {
	s, f, now := fixture(t)
	ctx := context.Background()
	v, err := s.Chat(ctx, "o/r#1", "start", nil)
	must(t, err)
	s.handle(v.ID, v.Epoch, codex.Message{ID: bytes("question"), Method: "item/tool/requestUserInput", Params: bytes(map[string]any{"threadId": v.ThreadID, "turnId": v.TurnID, "questions": []any{map[string]any{"id": "scope", "question": "Choose scope", "isOther": true}}})})
	*now = now.Add(time.Second)
	must(t, s.Observe(ctx, []Event{incoming(s, "head", "next")}, false))
	if !get(t, s).Due.After(*now) {
		t.Fatal("test did not queue before quiet deadline")
	}
	qs, err := s.Questions(ctx)
	must(t, err)
	must(t, s.Answer(ctx, qs[0].ID, bytes(map[string]any{"answers": map[string]any{"scope": map[string]any{"answers": []string{"Focused"}}}})))
	v = get(t, s)
	if len(v.Pending) != 0 || len(f.inputs) != 2 || f.methods[len(f.methods)-1] != "turn/steer" {
		t.Fatal("human answer failed to bypass quiet timer", v, f.methods)
	}
}

func TestInitialDetailedImportBaselinesHistoryAndLaterThreadChanges(t *testing.T) {
	s, _, now := fixture(t)
	ctx := context.Background()
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.Rules[0].Types = append(settings.Rules[0].Types, "pull_request_review_thread")
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	var i domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &i))
	i.Activity = []domain.Activity{{ID: "comment:old", Kind: "comment", Body: "before initial synchronization", Updated: "2026-01-01"}}
	i.ReviewThreads = []domain.ReviewThread{{ID: "thread-old", Resolved: false}}
	s.App.GitHub = &fixtureGitHub{items: map[int]domain.Item{1: i}}
	var repo domain.Repository
	must(t, s.Store.Get(ctx, "repository", "o/r", &repo))
	repo.Imported = true
	must(t, s.Store.Put(ctx, "repository", repo.ID, repo, ""))
	must(t, s.Poll(ctx))
	sessions, err := s.Sessions(ctx)
	must(t, err)
	if len(sessions) != 0 {
		t.Fatal("initial detailed import launched history", sessions)
	}
	must(t, s.Poll(ctx))
	sessions, err = s.Sessions(ctx)
	must(t, err)
	if len(sessions) != 0 {
		t.Fatal("poll replayed initial comments", sessions)
	}
	*now = now.Add(time.Minute)
	i.ReviewThreads[0].Resolved = true
	i.DetailSyncedAt = time.Time{}
	s.App.GitHub.(*fixtureGitHub).items[1] = i
	must(t, s.Poll(ctx))
	must(t, s.Poll(ctx))
	v := get(t, s)
	threadEvents := 0
	for _, p := range v.Pending {
		if p.Event.Type == "pull_request_review_thread" {
			threadEvents++
			if p.Event.Action != "resolved" {
				t.Fatal(p)
			}
		}
	}
	if threadEvents != 1 {
		t.Fatal("thread change was lost or duplicated", v.Pending)
	}
}

func TestNativeChoicesApprovalAmendmentsAndPermissionDenials(t *testing.T) {
	amendment := map[string]any{"acceptWithExecpolicyAmendment": map[string]any{"execpolicy_amendment": []string{"git", "status"}}}
	q := Question{Method: "item/commandExecution/requestApproval", Params: bytes(map[string]any{"availableDecisions": []any{"accept", "decline", amendment}})}
	must(t, validateResponse(q, bytes(map[string]any{"decision": amendment})))
	if validateResponse(q, bytes(map[string]any{"decision": "acceptForSession"})) == nil {
		t.Fatal("unoffered authorization accepted")
	}
	q = Question{Method: "item/tool/requestUserInput", Params: bytes(map[string]any{"questions": []any{map[string]any{"id": "q", "options": []any{map[string]string{"label": "Focused"}}, "isOther": false}}})}
	if validateResponse(q, bytes(map[string]any{"answers": map[string]any{"q": map[string]any{"answers": []string{"unoffered"}}}})) == nil {
		t.Fatal("unoffered native choice accepted")
	}
	must(t, validateResponse(q, bytes(map[string]any{"answers": map[string]any{"q": map[string]any{"answers": []string{"Focused"}}}})))
	q = Question{Method: "item/permissions/requestApproval", Params: bytes(map[string]any{"permissions": map[string]any{"network": map[string]bool{"enabled": true}}})}
	must(t, validateResponse(q, bytes(map[string]any{"permissions": map[string]any{"network": map[string]bool{"enabled": false}}, "scope": "turn"})))
	if validateResponse(q, bytes(map[string]any{"permissions": map[string]any{"fileSystem": map[string]any{"write": []string{"/unrequested"}}}})) == nil {
		t.Fatal("unrequested filesystem grant accepted")
	}
}

func TestExplicitRetryRequiresSuccessfulHistoryReconciliation(t *testing.T) {
	for _, mode := range []string{"unavailable", "delivered", "absent"} {
		t.Run(mode, func(t *testing.T) {
			s, f, now := fixture(t)
			ctx := context.Background()
			must(t, s.Observe(ctx, []Event{incoming(s, "head", "a")}, false))
			*now = now.Add(120 * time.Second)
			f.fail = "turn/start"
			must(t, s.Tick(ctx))
			v := get(t, s)
			f.fail = ""
			if mode == "unavailable" {
				f.fail = "thread/items/list"
			}
			if mode == "delivered" {
				f.history = []json.RawMessage{bytes(map[string]any{"text": "[code-factory-batch:" + v.BatchID + "]"})}
			}
			err := s.ResolveDelivery(ctx, v.ID, "retry")
			v = get(t, s)
			if mode == "absent" {
				must(t, err)
				if len(v.Pending) != 1 || !v.Hold {
					t.Fatal("confirmed absent delivery was not held for explicit continuation")
				}
			} else if err == nil || len(v.Pending) != 0 {
				t.Fatal("retry queued work without proving non-delivery")
			}
			if len(f.inputs) != 0 {
				t.Fatal("resolution repeated an uncertain send")
			}
		})
	}
}

func TestDiscussionWebhooksKeepNewerPRContext(t *testing.T) {
	for _, kind := range []string{"pull_request_review", "pull_request_review_comment", "pull_request_review_thread"} {
		t.Run(kind, func(t *testing.T) {
			s, _, now := fixture(t)
			ctx := context.Background()
			var current domain.Item
			must(t, s.Store.Get(ctx, "item", "o/r#1", &current))
			current.Updated = now.Add(time.Hour)
			current.Head = "latest-head"
			current.Labels = []string{"current-label"}
			must(t, s.Store.Put(ctx, "item", current.ID, current, ""))
			body := map[string]any{"action": "created", "repository": map[string]any{"full_name": "o/r"}, "pull_request": map[string]any{"number": 1, "state": "open", "title": "stale title", "updated_at": now.Add(-time.Hour).Format(time.RFC3339), "head": map[string]any{"sha": "old-head"}}, "comment": map[string]any{"id": 7, "body": "new discussion", "updated_at": now.Format(time.RFC3339)}, "review": map[string]any{"id": 8, "body": "review", "state": "approved", "submitted_at": now.Format(time.RFC3339)}, "thread": map[string]any{"id": 9, "node_id": "thread-9", "is_resolved": true}}
			d := delivery{ID: "discussion", Type: kind, Body: bytes(body), State: "pending", At: *now}
			must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
			must(t, s.processDelivery(ctx, d))
			var actual domain.Item
			must(t, s.Store.Get(ctx, "item", current.ID, &actual))
			if actual.Head != current.Head || actual.Title != current.Title || len(actual.Labels) != 1 || actual.Labels[0] != "current-label" {
				t.Fatal("late discussion snapshot regressed current PR context")
			}
			es, err := store.List[Event](ctx, s.Store, "factory-event")
			must(t, err)
			if len(es) != 1 || es[0].Context.Head != current.Head {
				t.Fatal("discussion event used stale PR context")
			}
		})
	}
}
