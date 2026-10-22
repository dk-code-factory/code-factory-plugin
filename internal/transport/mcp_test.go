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
		expectedURI := ResourceURI
		switch tool["name"] {
		case "open_settings", "get_settings", "save_settings", "save_repository", "save_profile", "set_jira_credential", "discover_skills", "save_skill", "diagnostics":
			expectedURI = SettingsResourceURI
		}
		if metadata["ui"].(map[string]any)["resourceUri"] != expectedURI {
			t.Fatalf("tool %s does not reference current UI: %v", tool["name"], metadata)
		}
		if (tool["name"] == "open_factory" || tool["name"] == "open_settings") && metadata["openai/outputTemplate"] != expectedURI {
			t.Fatal("open_factory output template does not match current UI", metadata)
		}
		if tool["name"] == "set_jira_credential" {
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
	res := request("tools/call", map[string]any{"name": "read_snapshot", "arguments": map[string]any{}}, true, "2026-07-28")
	if res["isError"] == true || res["structuredContent"] == nil {
		t.Fatal(res)
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
	if len(resources) != 2 {
		t.Fatal("expected separate inbox and settings resources", resources)
	}
	expected := map[string]string{ResourceURI: web.HTML, SettingsResourceURI: web.SettingsHTML()}
	for _, value := range resources {
		uri := value.(map[string]any)["uri"].(string)
		html, ok := expected[uri]
		if !ok {
			t.Fatal("unrecognized advertised UI", uri)
		}
		resource := request("resources/read", map[string]any{"uri": uri}, true, "2026-07-28")
		contents := resource["contents"].([]any)[0].(map[string]any)
		if contents["text"] != html || contents["uri"] != uri {
			t.Fatal("UI identity/body mismatch")
		}
	}
	if ResourceURI != fmt.Sprintf("ui://code-factory/inbox-%x.html", sha256.Sum256([]byte(web.HTML))) || SettingsResourceURI != fmt.Sprintf("ui://code-factory/settings-%x.html", sha256.Sum256([]byte(web.SettingsHTML()))) {
		t.Fatal("incorrect content identity")
	}
	for _, seed := range []struct {
		kind, id string
		value    any
	}{
		{"item", "pilot/local#7", domain.Item{ID: "pilot/local#7", State: "open", Title: "Unrelated task fixture"}},
		{"user", "pilot/local#7", domain.UserState{}},
		{"run", "fixture-run", domain.Run{ID: "fixture-run", ItemID: "pilot/local#7", Status: "interrupted", Prompt: "Unrelated task fixture"}},
		{"attention", "fixture-attention", domain.Attention{ID: "fixture-attention", RunID: "fixture-run", Status: "pending", Params: json.RawMessage(`{}`)}},
	} {
		if err := a.Store.Put(ctx, seed.kind, seed.id, seed.value, "fixture.changed"); err != nil {
			t.Fatal(err)
		}
	}
	taskSnapshot := request("tools/call", map[string]any{"name": "read_snapshot", "arguments": map[string]any{}}, true, "2026-07-28")["structuredContent"].(map[string]any)["data"].(map[string]any)
	for _, key := range []string{"inbox", "runs", "attention"} {
		if len(taskSnapshot[key].([]any)) != 1 {
			t.Fatal("task fixture missing", key)
		}
	}
	settings := request("tools/call", map[string]any{"name": "open_settings", "arguments": map[string]any{}}, true, "2026-07-28")
	if settings["isError"] == true || settings["structuredContent"] == nil {
		t.Fatal("settings entry failed", settings)
	}
	settingsData := settings["structuredContent"].(map[string]any)["data"].(map[string]any)
	for _, key := range []string{"inbox", "runs", "attention"} {
		if len(settingsData[key].([]any)) != 0 {
			t.Fatal("settings entry disclosed unrelated task data", key)
		}
	}
	if len(settingsData["repositories"].([]any)) == 0 || settingsData["config"] == nil {
		t.Fatal("settings configuration data missing")
	}

	legacy := request("initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "compat-test", "version": "1"}}, true, "2025-11-25")
	if legacy["protocolVersion"] != "2025-11-25" {
		t.Fatal("legacy adapter", legacy)
	}
	request("tools/list", nil, true, "2025-11-25")
}
