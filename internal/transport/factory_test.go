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
	"os"
	"path/filepath"
	"regexp"
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
	server := NewFactory(a, f, events.New(db, vault))
	t.Cleanup(func() {
		awaitTransportGroup(t, &server.apiLogWorkers, "public-interface log workers")
		drainTransportBudget(t)
	})
	s := httptest.NewServer(server.Handler(strings.Repeat("a", 64)))
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
	assertOperationClassifiersMatchCatalog(t, catalog)
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
	if result["isError"] != true || !strings.Contains(string(mustJSON(result)), "requestId") {
		t.Fatal("answer submission did not require an operation identity", result)
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func assertOperationClassifiersMatchCatalog(t *testing.T, catalog map[string]any) {
	t.Helper()
	classifiers := map[string]map[string]bool{}
	pattern := regexp.MustCompile(`(?s)const\s+operationTools\s*=\s*new Set\(\[(.*?)\]\)`)
	for _, path := range []string{"../../web/src/bridge.ts", "../../web/tests/wrapper-fixtures.ts"} {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		matches := pattern.FindAllSubmatch(source, -1)
		if len(matches) != 1 {
			t.Fatalf("cannot inspect the actual operation classifier in %s", path)
		}
		var names []string
		if err := json.Unmarshal([]byte("["+strings.TrimRight(string(matches[0][1]), " \t\r\n,")+"]"), &names); err != nil {
			t.Fatal("operation classifier must expose its static tool names", path, err)
		}
		classifiers[path] = map[string]bool{}
		for _, name := range names {
			if classifiers[path][name] {
				t.Fatal("duplicate operation classifier name", path, name)
			}
			classifiers[path][name] = true
		}
	}
	registered := map[string]bool{}
	for _, raw := range catalog["tools"].([]any) {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		registered[name] = true
		required := map[string]bool{}
		fields, _ := tool["inputSchema"].(map[string]any)["required"].([]any)
		for _, field := range fields {
			required[field.(string)] = true
		}
		readOnly := false
		if annotations, ok := tool["annotations"].(map[string]any); ok {
			readOnly = annotations["readOnlyHint"] == true
		}
		wireOperation := required["requestId"] && required["instanceId"] && !readOnly
		if wireOperation != isOperationTool(name) {
			t.Fatal("server operation classification contradicts its exposed identity schema", name)
		}
		for path, names := range classifiers {
			if names[name] != wireOperation {
				t.Fatal("client/fixture classification differs from authenticated server contract", path, name)
			}
		}
	}
	for path, names := range classifiers {
		for name := range names {
			if !registered[name] {
				t.Fatal("operation classifier refers to an unregistered tool", path, name)
			}
		}
	}
}

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
	mutation, failed := f.call(t, "user_action", map[string]any{"id": "o/r#0777", "action": "priority", "priority": 2})
	if failed {
		t.Fatal("priority mutation failed", mutation)
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
		r, failed := f.call(t, "user_action", map[string]any{"id": item.ID, "action": "read", "revision": revision})
		if failed {
			t.Fatal("read acknowledgment failed", r)
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

func TestFactoryStateReopenScopesVersionToDaemonInstance(t *testing.T) {
	dir := t.TempDir()
	first := newTrackingFixtureAt(t, dir)
	item := domain.Item{ID: "o/r#restart", Kind: "issue", State: "open", Title: "Before restart", Revision: "before"}
	if err := first.a.Store.Put(first.ctx, "item", item.ID, item, ""); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 100; n++ {
		if err := first.a.Store.Put(first.ctx, "user", item.ID, domain.UserState{}, ""); err != nil {
			t.Fatal(err)
		}
	}
	before, failed := first.call(t, "factory_state", map[string]any{"view": "issues"})
	if failed {
		t.Fatal(before)
	}
	oldVersion, oldInstance := before["version"].(float64), first.server.operations.instance
	first.close()
	second := newTrackingFixtureAt(t, dir)
	if second.server.operations.instance == oldInstance {
		t.Fatal("reopen did not establish a new daemon identity")
	}
	item.Title, item.Revision = "Saved after restart", "after"
	if err := second.a.Store.Put(second.ctx, "item", item.ID, item, ""); err != nil {
		t.Fatal(err)
	}
	read := func(namespace string) map[string]any {
		t.Helper()
		value, failed := second.call(t, "factory_state", map[string]any{"view": "issues", "sinceVersion": oldVersion, "sinceInstanceId": namespace})
		if failed || value["unchanged"] == true || value["inbox"].([]any)[0].(map[string]any)["item"].(map[string]any)["title"] != item.Title {
			t.Fatal("old or absent namespace suppressed different saved data", value)
		}
		return value
	}
	after := read(oldInstance)
	if after["version"].(float64) >= oldVersion {
		t.Fatal("fixture did not exercise a lower restarted counter")
	}
	for after["version"].(float64) < oldVersion {
		if err := second.a.Store.Put(second.ctx, "user", item.ID, domain.UserState{}, ""); err != nil {
			t.Fatal(err)
		}
		value, failed := second.call(t, "factory_state", map[string]any{"view": "issues"})
		if failed {
			t.Fatal(value)
		}
		if value["version"].(float64) <= after["version"].(float64) {
			t.Fatal("saved user mutation did not advance the collision fixture")
		}
		after = value
	}
	if after["version"] != oldVersion {
		t.Fatal("fixture did not exercise equal counters across daemon instances")
	}
	read(oldInstance)
	read("")
	idle, failed := second.call(t, "factory_state", map[string]any{"view": "issues", "sinceVersion": oldVersion, "sinceInstanceId": second.server.operations.instance})
	if failed || idle["unchanged"] != true || len(idle) != 2 {
		t.Fatal("current instance lost its unchanged optimization", idle)
	}
}

func TestProjectionDependencyFailureIsVisibleAndRecoversAfterRepair(t *testing.T) {
	for _, name := range []string{"factory_item", "factory_state"} {
		for _, shape := range []string{"cold-id", "warm-source", "warm-target"} {
			t.Run(name+"/"+shape, func(t *testing.T) {
				f := newTrackingFixture(t)
				item := domain.Item{ID: "o/r#1", Repository: "o/r", Kind: "issue", State: "open", Title: "Saved source", Revision: "v1"}
				for _, source := range []domain.Item{item, {ID: "o/r#2", Repository: "o/r", Kind: "pr", State: "open", Title: "Linked PR", Revision: "linked-v1"}} {
					if err := f.a.Store.Put(f.ctx, "item", source.ID, source, ""); err != nil {
						t.Fatal(err)
					}
					if err := f.a.Store.Put(f.ctx, "user", source.ID, domain.UserState{ID: source.ID, New: true}, ""); err != nil {
						t.Fatal(err)
					}
				}
				args := map[string]any{"id": item.ID}
				if name == "factory_state" {
					args = map[string]any{"view": "conversation", "itemId": item.ID}
				}
				if shape != "cold-id" {
					if value, failed := f.call(t, name, args); failed {
						t.Fatal("could not prepare valid source", value)
					}
				}
				broken := map[string]any{"id": 7, "source": item.ID, "target": "o/r#2"}
				if shape == "warm-source" {
					broken = map[string]any{"id": "broken", "source": 7, "target": item.ID}
				}
				if shape == "warm-target" {
					broken = map[string]any{"id": "broken", "source": item.ID, "target": 7}
				}
				if err := f.a.Store.Put(f.ctx, "relationship", "broken", broken, ""); err != nil {
					t.Fatal(err)
				}
				failure, failed := f.call(t, name, args)
				revision, hasRevision := failure["revision"].(float64)
				if !failed || failure["state"] != "failed" || !strings.Contains(fmt.Sprint(failure["error"]), "cannot unmarshal number") || !hasRevision || revision == 0 {
					t.Fatal("dependency failure did not reach caller at saved revision", failure)
				}
				link := domain.Relationship{ID: "broken", Source: item.ID, Target: "o/r#2", Kind: "implements", Manual: true}
				if err := f.a.Store.Put(f.ctx, "relationship", link.ID, link, ""); err != nil {
					t.Fatal(err)
				}
				value, failed := f.call(t, name, args)
				if failed {
					t.Fatal("repaired dependencies did not produce current source", value)
				}
				source := value
				if name == "factory_state" {
					source = value["selectedEntry"].(map[string]any)
				}
				if source["item"].(map[string]any)["title"] != item.Title {
					t.Fatal("repaired source changed", value)
				}
				if name == "factory_item" && len(value["relationships"].([]any)) != 1 {
					t.Fatal("repaired relationship missing", value)
				}
			})
		}
	}
}

func TestRelationshipCASInvalidatesPreparedSelectedReads(t *testing.T) {
	for _, name := range []string{"factory_item", "factory_state"} {
		t.Run(name, func(t *testing.T) {
			f := newTrackingFixture(t)
			for _, id := range []string{"a", "b", "c", "d"} {
				item := domain.Item{ID: id, Repository: "o/r", Kind: "issue", State: "open", Title: id, Revision: "v1"}
				if err := f.a.Store.Put(f.ctx, "item", id, item, ""); err != nil {
					t.Fatal(err)
				}
				if err := f.a.Store.Put(f.ctx, "user", id, domain.UserState{ID: id, New: true}, ""); err != nil {
					t.Fatal(err)
				}
			}
			read := func(id string) string {
				t.Helper()
				args := map[string]any{"id": id}
				if name == "factory_state" {
					args = map[string]any{"view": "conversation", "itemId": id}
				}
				value, failed := f.call(t, name, args)
				if failed {
					t.Fatal("selected read failed", value)
				}
				if name == "factory_state" {
					value = value["selectedEntry"].(map[string]any)
				}
				return value["detailVersion"].(string)
			}
			originalA, originalC := read("a"), read("c")
			first := domain.Relationship{ID: "link", Source: "a", Target: "b", Kind: "implements", Manual: true}
			if changed, err := f.a.Store.CompareAndSwap(f.ctx, "relationship", first.ID, nil, first); err != nil || !changed {
				t.Fatal("CAS insert failed", changed, err)
			}
			insertedA := read("a")
			if insertedA == originalA || read("c") != originalC {
				t.Fatal("CAS insert did not refresh the selected endpoint")
			}
			var previous json.RawMessage
			if err := f.a.Store.Get(f.ctx, "relationship", first.ID, &previous); err != nil {
				t.Fatal(err)
			}
			next := first
			next.Source = "c"
			next.Target = "d"
			if changed, err := f.a.Store.CompareAndSwap(f.ctx, "relationship", first.ID, previous, next); err != nil || !changed {
				t.Fatal("CAS move failed", changed, err)
			}
			if read("a") == insertedA || read("c") == originalC {
				t.Fatal("CAS move left an old or new endpoint stale")
			}
		})
	}
}

func TestFactoryStateVersionSkipsIdleAndRejectsUnpreparedData(t *testing.T) {
	f := newTrackingFixture(t)
	ctx := context.Background()
	full, failed := f.call(t, "factory_state", map[string]any{"view": "issues"})
	if failed {
		t.Fatal(full)
	}
	version := full["version"].(float64)
	idle, failed := f.call(t, "factory_state", map[string]any{"view": "issues", "sinceVersion": version, "sinceInstanceId": f.server.operations.instance})
	if failed || idle["unchanged"] != true || len(idle) != 2 {
		t.Fatal("idle projection was rebuilt", idle)
	}
	if err := f.a.Store.Put(ctx, "item", "projection-sentinel", map[string]any{"id": "projection-sentinel", "title": map[string]string{"invalid": "must not serve old data"}}, ""); err != nil {
		t.Fatal(err)
	}
	_, failed = f.call(t, "factory_state", map[string]any{"view": "issues", "sinceVersion": version, "sinceInstanceId": f.server.operations.instance})
	if !failed {
		t.Fatal("unprepared revision appeared current")
	}
	if err := f.a.Store.Write(ctx, func(tx *store.Tx) error { return tx.Delete("item", "projection-sentinel") }); err != nil {
		t.Fatal(err)
	}
	full, failed = f.call(t, "factory_state", map[string]any{"view": "issues", "sinceVersion": version, "sinceInstanceId": f.server.operations.instance})
	if failed || full["unchanged"] == true || full["version"].(float64) <= version {
		t.Fatal("live mutation did not refresh", full)
	}
	version = full["version"].(float64)
	if err := f.a.Store.Write(ctx, func(tx *store.Tx) error {
		return tx.Event("session.output", "metadata", map[string]string{"delta": "stream"})
	}); err != nil {
		t.Fatal(err)
	}
	idle, failed = f.call(t, "factory_state", map[string]any{"view": "issues", "sinceVersion": version, "sinceInstanceId": f.server.operations.instance})
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

func TestProjectionReadIdentifiersBoundBeforeCacheAdmission(t *testing.T) {
	f := newTrackingFixture(t)
	handler := f.server.Handler(f.token)
	for _, example := range []struct{ name, field string }{{"factory_item", "id"}, {"session_transcript", "id"}, {"factory_state", "itemId"}} {
		for _, id := range []string{strings.Repeat("x", 1025), strings.Repeat("界", 342)} {
			args := map[string]any{example.field: id}
			if example.name == "factory_state" {
				args["view"] = "conversation"
			}
			body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": example.name, "arguments": args}})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+f.token)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			r.Header.Set("MCP-Protocol-Version", "2025-11-25")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			var wire struct {
				Error  json.RawMessage `json:"error"`
				Result map[string]any  `json:"result"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil || w.Code != http.StatusOK || w.Body.Len() > responseLimit {
				t.Fatalf("selector rejection failed: tool=%s bytes=%d status=%d err=%v", example.name, w.Body.Len(), w.Code, err)
			}
			// ASCII is rejected by the SDK schema; multibyte values within its
			// rune ceiling reach the explicit UTF-8-byte admission validation.
			if len(wire.Error) == 0 && wire.Result["isError"] != true {
				t.Errorf("oversized %s.%s entered projection admission", example.name, example.field)
			}
			if !bytes.Contains(w.Body.Bytes(), []byte("1024")) {
				t.Errorf("selector rejection lost the explicit bound: tool=%s", example.name)
			}
		}
	}
	f.server.projections.mu.Lock()
	entries := len(f.server.projections.entries)
	f.server.projections.mu.Unlock()
	if entries != 0 {
		t.Error("rejected selectors retained projection entries", entries)
	}
	f.server.operations.mu.Lock()
	operations := len(f.server.operations.operations)
	f.server.operations.mu.Unlock()
	if operations != 0 {
		t.Error("read rejection admitted work", operations)
	}
	for _, id := range []string{strings.Repeat("x", 1024), strings.Repeat("界", 341) + "x"} {
		body := strings.Repeat("complete source 界<&", 10000)
		if err := f.a.Store.Put(f.ctx, "item", id, domain.Item{ID: id, Kind: "issue", State: "open", Title: "boundary identity", Revision: "r1", Body: body}, ""); err != nil {
			t.Fatal(err)
		}
		value, failed := f.call(t, "factory_item", map[string]any{"id": id})
		if failed {
			t.Fatal("exact boundary identifier rejected", value)
		}
		item := value["item"].(map[string]any)
		if item["id"] != id || item["body"] != body {
			t.Fatal("identifier or full source silently truncated")
		}
		if _, failed := f.call(t, "session_transcript", map[string]any{"id": id}); failed {
			t.Fatal("boundary transcript selector rejected")
		}
		// An arbitrary synthetic ID need not satisfy conversation domain rules;
		// this assertion checks identifier admission, independently of preparation.
		result, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, "factory_state", map[string]any{"view": "conversation", "itemId": id})
		if err != nil || result["isError"] == true {
			t.Fatal("boundary conversation selector rejected before preparation", err, result)
		}
	}
}
