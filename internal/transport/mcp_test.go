package transport

import (
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
	"codefactory/internal/web"
	"context"
	"crypto/sha256"
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

func TestAuthenticatedMCPDiscoveryToolsAndUI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	db, e := store.Open(filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	vault, e := store.NewVault(db, dir)
	if e != nil {
		t.Fatal(e)
	}
	a := &app.App{Store: db, Vault: vault, DataDir: dir, GitHub: providers.NewGitHub()}
	a.Runs = app.NewManager(ctx, a, testutil.FakeCodex(t))
	defer a.Runs.Close()
	if e = a.Init(ctx); e != nil {
		t.Fatal(e)
	}
	svc := New(a, events.New(db, vault))
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(svc.Handler(token))
	defer server.Close()
	request := func(method string, params map[string]any, auth bool, version string) map[string]any {
		t.Helper()
		if params == nil {
			params = map[string]any{}
		}
		if version == "2026-07-28" {
			params["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": version, "io.modelcontextprotocol/clientInfo": map[string]string{"name": "integration-test", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		r, e := http.NewRequest("POST", server.URL+"/mcp", bytes.NewReader(b))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", version)
		r.Header.Set("Mcp-Method", method)
		if name, ok := params["name"].(string); ok {
			r.Header.Set("Mcp-Name", name)
		}
		if uri, ok := params["uri"].(string); ok {
			r.Header.Set("Mcp-Name", uri)
		}
		if auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		res, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		if !auth {
			if res.StatusCode != 401 {
				t.Fatal("unauthorized", res.StatusCode)
			}
			return nil
		}
		if res.StatusCode != 200 {
			t.Fatalf("%s HTTP %d: %s", method, res.StatusCode, data)
		}
		var out map[string]any
		if e = json.Unmarshal(data, &out); e != nil {
			t.Fatalf("%s: %s", e, data)
		}
		if out["error"] != nil {
			t.Fatalf("%s: %s", method, data)
		}
		return out["result"].(map[string]any)
	}
	request("tools/list", nil, false, "2026-07-28")
	d := request("server/discover", nil, true, "2026-07-28")
	if d["resultType"] != "complete" || d["capabilities"].(map[string]any)["events"] == nil {
		t.Fatal("events capability absent", d)
	}
	tools := request("tools/list", nil, true, "2026-07-28")["tools"].([]any)
	found := false
	for _, v := range tools {
		tool := v.(map[string]any)
		metadata := tool["_meta"].(map[string]any)
		if metadata["ui"].(map[string]any)["resourceUri"] != ResourceURI {
			t.Fatalf("tool %s does not reference current UI: %v", tool["name"], metadata)
		}
		if tool["name"] == "open_factory" && metadata["openai/outputTemplate"] != ResourceURI {
			t.Fatal("open_factory output template does not match current UI", metadata)
		}
		if tool["name"] == "set_jira_credential" || tool["name"] == "dismiss_question" {
			meta := tool["_meta"].(map[string]any)["ui"].(map[string]any)
			vis := meta["visibility"].([]any)
			if len(vis) != 1 || vis[0] != "app" {
				t.Fatal("credential tool visible to model")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing credential tool")
	}
	reportTools := map[string]bool{"report_issue": false, "add_issue_evidence": false, "get_issue_report": false, "list_issue_reports": false, "refresh_issue_reports": false, "resume_issue_report": false}
	for _, value := range tools {
		tool := value.(map[string]any)
		name := tool["name"].(string)
		if _, ok := reportTools[name]; ok {
			reportTools[name] = true
			visibility := tool["_meta"].(map[string]any)["ui"].(map[string]any)["visibility"].([]any)
			if len(visibility) != 2 {
				t.Fatal("report tool unavailable to chat and UI", name)
			}
		}
	}
	for name, present := range reportTools {
		if !present {
			t.Fatal("missing reporting tool", name)
		}
	}
	observation := map[string]any{"idempotencyKey": "mcp-observation", "observedAt": time.Now().UTC().Format(time.RFC3339), "surface": "Work", "expected": "Display response", "actual": "No response", "reproductionUnknown": "Observed once"}
	result := request("tools/call", map[string]any{"name": "report_issue", "arguments": observation}, true, "2026-07-28")
	report := result["structuredContent"].(map[string]any)["data"].(map[string]any)
	if report["processing"] != "queued" {
		t.Fatal("MCP submission did not persist queue", result)
	}
	id := report["id"].(string)
	result = request("tools/call", map[string]any{"name": "get_issue_report", "arguments": map[string]any{"id": id}}, true, "2026-07-28")
	if result["structuredContent"].(map[string]any)["data"].(map[string]any)["id"] != id {
		t.Fatal("MCP report read uses another ledger")
	}
	result = request("tools/call", map[string]any{"name": "add_issue_evidence", "arguments": map[string]any{"id": id, "idempotencyKey": "mcp-evidence", "text": "Work displayed an empty pane"}}, true, "2026-07-28")
	if result["isError"] == true {
		t.Fatal(result)
	}
	result = request("tools/call", map[string]any{"name": "list_issue_reports", "arguments": map[string]any{"status": "queued"}}, true, "2026-07-28")
	if len(result["structuredContent"].(map[string]any)["data"].(map[string]any)["items"].([]any)) != 1 {
		t.Fatal("MCP list lost retained observation")
	}

	for _, id := range []string{"r1", "r2"} {
		if err := db.Put(ctx, "run", id, domain.Run{ID: id, Status: "interrupted"}, ""); err != nil {
			t.Fatal(err)
		}
		if err := db.Put(ctx, "question-recovery", id, domain.QuestionRecovery{ID: id, RunID: id, Stage: "fallback", Question: "Which target?"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Put(ctx, "attention", "text", domain.Attention{ID: "text", RunID: "r1", Origin: "text", Status: "pending", RecoveryID: "r1"}, ""); err != nil {
		t.Fatal(err)
	}
	runReply := request("tools/call", map[string]any{"name": "read_run", "arguments": map[string]any{"id": "r1"}}, true, "2026-07-28")
	runData := runReply["structuredContent"].(map[string]any)["data"].(map[string]any)
	recoveries := runData["questionRecoveries"].([]any)
	if len(recoveries) != 1 || recoveries[0].(map[string]any)["runId"] != "r1" {
		t.Fatal("read_run leaked unrelated recovery")
	}
	dismissed := request("tools/call", map[string]any{"name": "dismiss_question", "arguments": map[string]any{"id": "text"}}, true, "2026-07-28")
	if dismissed["isError"] == true {
		t.Fatal(dismissed)
	}
	var text domain.Attention
	if err := db.Get(ctx, "attention", "text", &text); err != nil || text.Status != "dismissed" {
		t.Fatal("dismissal lost")
	}
	res := request("tools/call", map[string]any{"name": "read_snapshot", "arguments": map[string]any{}}, true, "2026-07-28")
	if res["isError"] == true || res["structuredContent"] == nil {
		t.Fatal(res)
	}
	snapshot := res["structuredContent"].(map[string]any)["data"].(map[string]any)
	if len(snapshot["questionRecoveries"].([]any)) != 2 {
		t.Fatal("snapshot omitted recoveries")
	}
	for name, args := range map[string]map[string]any{
		"save_repository": {"id": "pilot/local", "checkout": "", "base": "main"},
		"save_skill":      {"name": "pilot-skill", "content": "---\nname: pilot-skill\ndescription: Read-only pilot.\n---\n\nInspect only.\n"},
	} {
		result := request("tools/call", map[string]any{"name": name, "arguments": args}, true, "2026-07-28")
		if result["isError"] == true {
			t.Fatalf("UI settings input rejected: %s: %v", name, result)
		}
	}
	catalog := request("events/list", nil, true, "2026-07-28")
	if len(catalog["events"].([]any)) != 3 {
		t.Fatal(catalog)
	}
	resources := request("resources/list", nil, true, "2026-07-28")["resources"].([]any)
	if len(resources) != 1 || resources[0].(map[string]any)["uri"] != ResourceURI {
		t.Fatal("current UI resource advertisement", resources)
	}
	resource := request("resources/read", map[string]any{"uri": resources[0].(map[string]any)["uri"]}, true, "2026-07-28")
	contents := resource["contents"].([]any)[0].(map[string]any)
	if contents["text"] != web.HTML || contents["uri"] != ResourceURI || ResourceURI != fmt.Sprintf("ui://code-factory/inbox-%x.html", sha256.Sum256([]byte(web.HTML))) {
		t.Fatal("UI resource content does not match its advertised identity")
	}
	legacy := request("initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "compat-test", "version": "1"}}, true, "2025-11-25")
	if legacy["protocolVersion"] != "2025-11-25" {
		t.Fatal("legacy adapter", legacy)
	}
	request("tools/list", nil, true, "2025-11-25")
}
