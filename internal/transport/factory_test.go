package transport

import (
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/factory"
	"codefactory/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFactoryPublicInterfacesRetirePipelines(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	vault, err := store.NewVault(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &app.App{Store: db, Vault: vault, DataDir: dir}
	if err = a.InitCore(ctx); err != nil {
		t.Fatal(err)
	}
	f := factory.New(ctx, a, "unused")
	defer f.Close()
	if err = f.Init(ctx); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(NewFactory(a, f, events.New(db, vault)).Handler(strings.Repeat("a", 64)))
	defer s.Close()
	request := func(method string, params map[string]any) map[string]any {
		t.Helper()
		params["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientInfo": map[string]string{"name": "fixture", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req, _ := http.NewRequest("POST", s.URL+"/mcp", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Mcp-Method", method)
		if name, ok := params["name"].(string); ok {
			req.Header.Set("Mcp-Name", name)
		}
		res, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var value struct {
			Result map[string]any
			Error  any
		}
		if err = json.NewDecoder(res.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		if value.Error != nil {
			t.Fatal(value.Error)
		}
		return value.Result
	}
	catalog := request("tools/list", map[string]any{})
	names := map[string]bool{}
	for _, raw := range catalog["tools"].([]any) {
		name := raw.(map[string]any)["name"].(string)
		names[name] = true
	}
	for _, name := range []string{"session_chat", "session_control", "session_questions", "answer_session_question", "configuration_chat", "confirm_configuration", "factory_events", "user_action", "link_items"} {
		if !names[name] {
			t.Fatalf("missing %s", name)
		}
	}
	for _, name := range []string{"prepare_launch", "prepare_pr_review", "start_run", "save_profile", "post_retained_review", "save_settings", "report_issue"} {
		if names[name] {
			t.Fatalf("retired interface %s remains public", name)
		}
	}
	result := request("tools/call", map[string]any{"name": "factory_state", "arguments": map[string]any{"freshness": "state"}})
	if result["isError"] == true {
		t.Fatal(result)
	}
	result = request("tools/call", map[string]any{"name": "factory_events", "arguments": map[string]any{"kind": "events", "offset": 0, "limit": 50, "freshness": "archive"}})
	if result["isError"] == true {
		t.Fatal("bounded archive rejected bridge freshness", result)
	}
	result = request("tools/call", map[string]any{"name": "session_transcript", "arguments": map[string]any{"id": "missing", "after": 0, "freshness": "page"}})
	if result["isError"] == true {
		t.Fatal(result)
	}
	result = request("tools/call", map[string]any{"name": "answer_session_question", "arguments": map[string]any{"id": "missing", "answer": map[string]any{"answers": map[string]any{}}}})
	if result["isError"] != true || !strings.Contains(string(mustJSON(result)), "record not found") {
		t.Fatal("answer object failed schema validation instead of reaching the question handler", result)
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestFactoryStatePagesCompactDataAndExplicitDelivery(t *testing.T) {
	f := newTrackingFixture(t)
	ctx := f.ctx
	const marker = "large-retained-body-never-transfer-in-state"
	body := strings.Repeat(marker, 220)
	err := f.a.Store.Write(ctx, func(tx *store.Tx) error {
		for n := 0; n < 1000; n++ {
			id := fmt.Sprintf("o/r#%04d", n)
			item := domain.Item{ID: id, Repository: "o/r", Number: n, Kind: "issue", State: "open", Title: fmt.Sprintf("Item%04d", n), Body: body, Revision: fmt.Sprint(n), Updated: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)}
			if n == 777 {
				item.Body += " unique-body-search"
			}
			if err := tx.Put("item", id, item); err != nil {
				return err
			}
			if err := tx.Put("user", id, domain.UserState{ID: id, New: n == 777, ReadRevision: fmt.Sprint(n)}); err != nil {
				return err
			}
		}
		session := factory.Session{ID: "o/r#0777", Kind: "item", ItemID: "o/r#0777", Execution: "interrupted", Hold: true, Error: "context deadline exceeded", BatchID: "uncertain-batch", Pending: []factory.Pending{{Event: factory.Event{Context: domain.Item{Body: body}}}}}
		if err := tx.Put("factory-session", session.ID, session); err != nil {
			return err
		}
		if err := tx.Put("factory-batch", "uncertain-batch", factory.Batch{State: "uncertain", Context: domain.Item{Body: body}}); err != nil {
			return err
		}
		for n := 0; n < 60; n++ {
			id := fmt.Sprintf("proposal%03d", n)
			if err := tx.Put("factory-proposal", id, factory.Proposal{ID: id, State: "applied", Diff: marker, Created: time.Date(2026, 11, 5, 0, 0, n, 0, time.UTC)}); err != nil {
				return err
			}
		}
		for n := 0; n < 40; n++ {
			id := fmt.Sprintf("question%03d", n)
			if err := tx.Put("factory-question", id, factory.Question{ID: id, SessionID: session.ID, Status: "answered", Params: json.RawMessage(`{"message":"` + marker + `"}`), Created: time.Date(2026, 11, 5, 0, 0, n, 0, time.UTC)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page, failed := f.call(t, "factory_state", map[string]any{"view": "issues", "limit": 50, "offset": 50, "filter": "all"})
	if failed {
		t.Fatal(page)
	}
	if len(page["inbox"].([]any)) != 50 || page["inboxTotal"] != float64(1000) {
		t.Fatal("inbox was not paged", page["inboxTotal"])
	}
	wire := mustJSON(page)
	if bytes.Contains(wire, []byte(marker)) || len(wire) > 100*1024 {
		t.Fatal("recurring state contains retained bodies/proposals", len(wire))
	}
	if _, ok := page["settings"]; ok {
		t.Fatal("settings leaked into unrelated view")
	}
	if len(page["questions"].([]any)) > 25 {
		t.Fatal("historical questions unbounded")
	}
	searched, failed := f.call(t, "factory_state", map[string]any{"view": "issues", "search": "unique-body-search", "filter": "new"})
	if failed {
		t.Fatal(searched)
	}
	entries := searched["inbox"].([]any)
	if len(entries) != 1 || searched["inboxTotal"] != float64(1) || entries[0].(map[string]any)["item"].(map[string]any)["id"] != "o/r#0777" {
		t.Fatal("body search/new filter did not precede paging")
	}
	sessions := searched["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatal("selected summary missing")
	}
	session := sessions[0].(map[string]any)
	if session["pendingCount"] != float64(1) || session["deliveryState"] != "uncertain" {
		t.Fatal("structured queue/delivery state missing", session)
	}
	if bytes.Contains(mustJSON(searched), []byte(marker)) {
		t.Fatal("search returned full body")
	}
	detail, failed := f.call(t, "factory_item", map[string]any{"id": "o/r#0777"})
	if failed || !strings.Contains(detail["item"].(map[string]any)["body"].(string), marker) {
		t.Fatal("full source detail was lost")
	}
	mutation, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, "user_action", map[string]any{"id": "o/r#0777", "action": "priority", "priority": 2})
	if err != nil || mutation["isError"] == true {
		t.Fatal("priority mutation failed", err, mutation)
	}
	conversation, failed := f.call(t, "factory_state", map[string]any{"view": "conversation", "itemId": "o/r#0777"})
	if failed {
		t.Fatal(conversation)
	}
	selectedEntry := conversation["selectedEntry"].(map[string]any)
	if selectedEntry["user"].(map[string]any)["override"] != float64(2) || selectedEntry["priority"] != float64(2) || len(conversation["inbox"].([]any)) != 0 {
		t.Fatal("conversation lost persisted priority override")
	}
	if bytes.Contains(mustJSON(conversation), []byte(marker)) {
		t.Fatal("conversation refresh transferred retained bodies")
	}
	config, failed := f.call(t, "factory_state", map[string]any{"view": "configuration"})
	if failed {
		t.Fatal(config)
	}
	if config["settings"] == nil || len(config["proposals"].([]any)) != 50 || len(config["inbox"].([]any)) != 0 {
		t.Fatal("configuration projection not bounded/scoped")
	}
	_, failed = f.call(t, "factory_state", map[string]any{"view": "issues", "limit": 201})
	if !failed {
		t.Fatal("unbounded page accepted")
	}
}

func TestFactoryItemReadsRequireDisplayedRevisionAcknowledgmentAndVersions(t *testing.T) {
	f := newTrackingFixture(t)
	ctx := f.ctx
	item := domain.Item{ID: "o/r#1", Repository: "o/r", Kind: "issue", Number: 1, State: "open", Title: "Saved source", Body: "first body"}
	item.Revision = item.ContentRevision()
	for _, i := range []domain.Item{item, {ID: "o/r#2", Repository: "o/r", Kind: "pr", Number: 2, State: "open", Title: "Linked PR", Revision: "linked-r1"}} {
		if err := f.a.Store.Put(ctx, "item", i.ID, i, ""); err != nil {
			t.Fatal(err)
		}
		if err := f.a.Store.Put(ctx, "user", i.ID, domain.UserState{ID: i.ID, New: true}, ""); err != nil {
			t.Fatal(err)
		}
	}
	stateVersion := func() string {
		t.Helper()
		v, failed := f.call(t, "factory_state", map[string]any{"view": "conversation", "itemId": item.ID})
		if failed {
			t.Fatal(v)
		}
		return v["selectedEntry"].(map[string]any)["detailVersion"].(string)
	}
	firstVersion := stateVersion()
	detail, failed := f.call(t, "factory_item", map[string]any{"id": item.ID})
	if failed || detail["detailVersion"] != firstVersion {
		t.Fatal("detail and compact projection disagree", detail)
	}
	var user domain.UserState
	if err := f.a.Store.Get(ctx, "user", item.ID, &user); err != nil {
		t.Fatal(err)
	}
	if user.ReadRevision != "" {
		t.Fatal("fetch acknowledged an undisplayed/lost response")
	}
	oldRevision := item.Revision
	item.Body = "new saved body"
	item.Revision = item.ContentRevision()
	if err := f.a.Store.Put(ctx, "item", item.ID, item, ""); err != nil {
		t.Fatal(err)
	}
	newVersion := stateVersion()
	if newVersion == firstVersion {
		t.Fatal("same-item source change did not invalidate detail")
	}
	acknowledge := func(revision string) {
		t.Helper()
		r, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, "user_action", map[string]any{"id": item.ID, "action": "read", "revision": revision})
		if err != nil || r["isError"] == true {
			t.Fatal("read acknowledgment failed", err, r)
		}
	}
	acknowledge(oldRevision)
	if err := f.a.Store.Get(ctx, "user", item.ID, &user); err != nil {
		t.Fatal(err)
	}
	if user.ReadRevision != oldRevision || user.ReadRevision == item.Revision {
		t.Fatal("stale displayed revision marked unseen current content read")
	}
	link := domain.Relationship{ID: "link", Source: item.ID, Target: "o/r#2", Kind: "implements", Evidence: "saved relationship", Manual: true}
	if err := f.a.Store.Put(ctx, "relationship", link.ID, link, ""); err != nil {
		t.Fatal(err)
	}
	linkedVersion := stateVersion()
	if linkedVersion == newVersion {
		t.Fatal("relationship-only change did not invalidate detail")
	}
	current, failed := f.call(t, "factory_item", map[string]any{"id": item.ID})
	if failed || current["detailVersion"] != linkedVersion || len(current["relationships"].([]any)) != 1 {
		t.Fatal("relationship detail/version mismatch")
	}
	if err := f.a.Store.Get(ctx, "user", item.ID, &user); err != nil {
		t.Fatal(err)
	}
	if user.ReadRevision != oldRevision {
		t.Fatal("second fetch consumed current unread state")
	}
	acknowledge(item.Revision)
	if err := f.a.Store.Get(ctx, "user", item.ID, &user); err != nil {
		t.Fatal(err)
	}
	if user.ReadRevision != item.Revision {
		t.Fatal("accepted exact revision was not recorded")
	}
}

func TestFactoryStateVersionSkipsIdleProjectionAndTracksMetadata(t *testing.T) {
	f := newTrackingFixture(t)
	ctx := context.Background()
	full, failed := f.call(t, "factory_state", map[string]any{"view": "issues"})
	if failed {
		t.Fatal(full)
	}
	version := full["version"].(float64)
	idle, failed := f.call(t, "factory_state", map[string]any{"view": "issues", "sinceVersion": version})
	if failed || idle["unchanged"] != true || len(idle) != 2 {
		t.Fatal("idle projection was rebuilt", idle)
	}
	for _, kind := range []string{"factory-session", "factory-question", "factory-proposal", "factory-settings", "factory-batch"} {
		// Test actual metadata-only writes. These never need retained contents in the cursor.
		value := map[string]any{"id": "metadata", "execution": "idle", "status": "answered", "state": "applied", "pending": []any{}}
		if err := f.a.Store.Put(ctx, kind, "metadata", value, ""); err != nil {
			t.Fatal(err)
		}
		current, err := f.a.Store.UILatest(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if float64(current) <= version {
			t.Fatal("metadata update invisible", kind)
		}
		version = float64(current)
	}
	// A matching version must return before scanning/decoding an inbox. The invalid
	// title is an intentional sentinel for the otherwise expensive projection read.
	if err := f.a.Store.Put(ctx, "item", "projection-sentinel", map[string]any{"id": "projection-sentinel", "title": map[string]string{"invalid": "would fail domain decoding"}}, ""); err != nil {
		t.Fatal(err)
	}
	cursor, err := f.a.Store.UILatest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	idle, failed = f.call(t, "factory_state", map[string]any{"view": "issues", "sinceVersion": cursor})
	if failed || idle["unchanged"] != true {
		t.Fatal("idle request traversed invalid projection", idle)
	}
	_, failed = f.call(t, "factory_state", map[string]any{"view": "issues"})
	if !failed {
		t.Fatal("projection sentinel did not exercise a failing full decode")
	}
	if err = f.a.Store.Write(ctx, func(tx *store.Tx) error { return tx.Delete("item", "projection-sentinel") }); err != nil {
		t.Fatal(err)
	}
	full, failed = f.call(t, "factory_state", map[string]any{"view": "issues", "sinceVersion": cursor})
	if failed || full["unchanged"] == true || full["version"].(float64) <= float64(cursor) {
		t.Fatal("live mutation did not refresh", full)
	}
	version = full["version"].(float64)
	if err = f.a.Store.Write(ctx, func(tx *store.Tx) error {
		return tx.Event("session.output", "metadata", map[string]string{"delta": "stream"})
	}); err != nil {
		t.Fatal(err)
	}
	idle, failed = f.call(t, "factory_state", map[string]any{"view": "issues", "sinceVersion": version})
	if failed || idle["unchanged"] != true {
		t.Fatal("transcript forced full inbox rebuild", idle)
	}
}

func TestFactoryStateReusesInboxAcrossQueriesAndRefreshesAfterMutation(t *testing.T) {
	f := newTrackingFixture(t)
	i := domain.Item{ID: "o/r#1", Kind: "issue", State: "open", Title: "Original", Revision: "v1"}
	if err := f.a.Store.Put(f.ctx, "item", i.ID, i, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.a.Store.Put(f.ctx, "user", i.ID, domain.UserState{ID: i.ID}, ""); err != nil {
		t.Fatal(err)
	}
	_, failed := f.call(t, "factory_state", map[string]any{"view": "issues"})
	if failed {
		t.Fatal("could not warm projection")
	}
	// Bypass invalidation only to install a sentinel: any repeated full inbox
	// decode now fails, while filtering/paging a retained projection still works.
	db, err := sql.Open("sqlite", filepath.Join(f.a.DataDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(f.ctx, `UPDATE entities SET data=json_set(data,'$.title',json('{"invalid":"projection reread"}')) WHERE kind='item' AND id=?`, i.ID); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		{"view": "issues", "filter": "all", "offset": 1},
		{"view": "issues", "search": "Original"},
		{"view": "working"},
		{"view": "conversation", "itemId": i.ID},
	} {
		page, failed := f.call(t, "factory_state", args)
		if failed {
			t.Fatal("query change rebuilt unchanged inbox", args, page)
		}
	}
	i.Title, i.Revision = "Updated", "v2"
	if err = f.a.Store.Put(f.ctx, "item", i.ID, i, ""); err != nil {
		t.Fatal(err)
	}
	page, failed := f.call(t, "factory_state", map[string]any{"view": "issues"})
	if failed || page["inbox"].([]any)[0].(map[string]any)["item"].(map[string]any)["title"] != "Updated" {
		t.Fatal("source mutation did not refresh projection", page)
	}
}

func TestFactoryEventsProjectsBoundedPagesBeforeArchiveDecode(t *testing.T) {
	f := newTrackingFixture(t)
	ctx := f.ctx
	const secret = "raw-archive-never-transfer"
	huge := strings.Repeat(secret, 4096)
	err := f.a.Store.Write(ctx, func(tx *store.Tx) error {
		for n := 0; n < 205; n++ {
			id := fmt.Sprintf("row-%03d", n)
			for _, kind := range []string{"factory-event", "factory-delivery", "factory-batch"} {
				v := map[string]any{"id": id, "type": "check_run", "repository": "o/r", "details": strings.Repeat("界", 1000), "body": huge, "context": map[string]string{"body": huge}, "events": []any{map[string]string{"body": huge}}, "actions": []any{map[string]string{"instructions": huge}}, "state": "delivered"}
				if err := tx.Put(kind, id, v); err != nil {
					return err
				}
			}
		}
		values := []string{}
		for i := 0; i < 30; i++ {
			values = append(values, strings.Repeat("界", 200))
		}
		rules := []map[string]any{}
		for n := 0; n < 205; n++ {
			rules = append(rules, map[string]any{"id": fmt.Sprintf("rule-%03d", n), "repositories": values, "types": values, "actions": values, "skills": values, "instructions": huge, "disabled": n%2 == 1})
		}
		return tx.Put("factory-settings", "effective", map[string]any{"rules": rules})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"events", "deliveries", "batches", "rules"} {
		read := func(in factoryEventsInput) map[string]any {
			t.Helper()
			result, e := factoryEvents(ctx, f.a, in)
			if e != nil {
				t.Fatal(e)
			}
			return result.(map[string]any)
		}
		first := read(factoryEventsInput{Kind: kind})
		rows := first["rows"].([]map[string]any)
		if first["limit"] != 50 || first["total"] != 205 || len(rows) != 50 {
			t.Fatal("archive default page unbounded", kind, first)
		}
		want := "row-204"
		if kind == "rules" {
			want = "rule-000"
		}
		if rows[0]["id"] != want {
			t.Fatal("unstable archive order", kind, rows[0])
		}
		encoded := string(mustJSON(first))
		if strings.Contains(encoded, secret) || strings.Contains(encoded, "instructions") || strings.Contains(encoded, "context") || len(encoded) > 500000 {
			t.Fatal("archive exposed retained payload or oversized page", kind, len(encoded))
		}
		if kind == "events" {
			p := rows[0]["preview"].(map[string]any)
			if len(p["text"].(string)) > 512 || p["originalBytes"].(float64) != 3000 || p["truncated"] != true {
				t.Fatal("preview not bounded by encoded bytes", p)
			}
		}
		if kind == "rules" {
			r := rows[0]
			if r["index"].(float64) != 0 || r["enabled"] != true || r["skillsCount"].(float64) != 30 || len(r["skills"].([]any)) != 16 {
				t.Fatal("rule order/count projection", r)
			}
			for _, v := range r["skills"].([]any) {
				if len(v.(string)) > 128 {
					t.Fatal("array element escaped UTF8 byte limit")
				}
			}
		}
		last := read(factoryEventsInput{Kind: kind, Offset: 200, Limit: 999})
		if last["limit"] != 200 || len(last["rows"].([]map[string]any)) != 5 {
			t.Fatal("archive max/offset not bounded", kind, last)
		}
		empty := read(factoryEventsInput{Kind: kind, Offset: 1000, Limit: 50})
		if len(empty["rows"].([]map[string]any)) != 0 || empty["total"] != 205 {
			t.Fatal("archive empty page lost total", kind)
		}
	}
	if _, e := factoryEvents(ctx, f.a, factoryEventsInput{Kind: "invalid"}); e == nil {
		t.Fatal("invalid archive kind accepted")
	}
	if _, e := factoryEvents(ctx, f.a, factoryEventsInput{Offset: -1}); e == nil {
		t.Fatal("negative offset accepted")
	}
	sessions, e := f.a.Store.List(ctx, "factory-session")
	if e != nil || len(sessions) != 0 {
		t.Fatal("archive reads launched work", e)
	}
}
