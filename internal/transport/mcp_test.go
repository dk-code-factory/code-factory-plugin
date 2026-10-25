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
	reviewFound := false
	attentionFound := false
	for _, v := range tools {
		tool := v.(map[string]any)
		metadata := tool["_meta"].(map[string]any)
		if tool["name"] == "list_attention" {
			attentionFound = true
			vis := metadata["ui"].(map[string]any)["visibility"].([]any)
			annotations := tool["annotations"].(map[string]any)
			if len(vis) != 2 || vis[0] != "model" || vis[1] != "app" || annotations["readOnlyHint"] != true || annotations["openWorldHint"] != false {
				t.Fatal("question queue is not discoverable as a local read", tool)
			}
		}
		if tool["name"] == "prepare_pr_review" {
			reviewFound = true
			vis := metadata["ui"].(map[string]any)["visibility"].([]any)
			if len(vis) != 2 || vis[0] != "model" || vis[1] != "app" || !strings.Contains(tool["description"].(string), "start_run") {
				t.Fatal("PR review workflow is not discoverable to the model", tool)
			}
			schema := tool["inputSchema"].(map[string]any)
			if schema["properties"].(map[string]any)["target"] == nil || schema["properties"].(map[string]any)["itemId"] == nil {
				t.Fatal("preparation must accept targets and legacy itemId", schema)
			}
		}
		if tool["name"] == "read_pr_reviews" {
			vis := metadata["ui"].(map[string]any)["visibility"].([]any)
			if len(vis) != 2 || vis[0] != "model" || vis[1] != "app" || !strings.Contains(tool["description"].(string), "receipt") {
				t.Fatal("publication receipts not discoverable", tool)
			}
		}
		expectedURI := ResourceURI
		switch tool["name"] {
		case "read_plugin_admin", "manage_codex_plugins", "sync_repositories", "open_settings", "get_settings", "save_settings", "save_repository", "save_profile", "set_jira_credential", "discover_skills", "save_skill", "diagnostics":
			expectedURI = SettingsResourceURI
		}
		opens := tool["name"] == "open_factory" || tool["name"] == "open_settings"
		if opens && metadata["ui"].(map[string]any)["resourceUri"] != expectedURI {
			t.Fatalf("opener %s does not reference current UI: %v", tool["name"], metadata)
		}
		if !opens && (metadata["ui"].(map[string]any)["resourceUri"] != nil || metadata["openai/outputTemplate"] != nil) {
			t.Fatalf("non-opening tool %s would implicitly render a dashboard", tool["name"])
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
	if !found || !reviewFound || !attentionFound {
		t.Fatal("missing credential, PR review or attention queue tool")
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
	res := request("tools/call", map[string]any{"name": "read_snapshot", "arguments": map[string]any{"freshness": "view-first"}}, true, "2026-07-28")
	if res["isError"] == true || res["structuredContent"] == nil {
		t.Fatal(res)
	}
	after := db.Latest(ctx)
	readChanges := func(marker string) map[string]any {
		result := request("tools/call", map[string]any{"name": "read_changes", "arguments": map[string]any{"after": after, "limit": 1, "freshness": marker}}, true, "2026-07-28")
		if result["isError"] == true {
			t.Fatal("fresh read rejected", result)
		}
		return result["structuredContent"].(map[string]any)["data"].(map[string]any)
	}
	if len(readChanges("empty")["events"].([]any)) != 0 || db.Latest(ctx) != after {
		t.Fatal("read marker changed the journal")
	}
	if err := db.Write(ctx, func(tx *store.Tx) error {
		return tx.Event("fixture.external", "fixture", map[string]string{"status": "changed"})
	}); err != nil {
		t.Fatal(err)
	}
	if page := readChanges("after-external-change"); len(page["events"].([]any)) != 1 || page["cursor"].(float64) <= float64(after) {
		t.Fatal("fresh read lost unchanged-cursor external change", page)
	}
	for name, args := range map[string]map[string]any{
		"save_repository": {"id": "pilot/local", "checkout": "", "base": "main", "autoReview": nil, "reviewPrompt": "Check repository-specific invariants."},
		"save_skill":      {"name": "pilot-skill", "content": "---\nname: pilot-skill\ndescription: Read-only pilot.\n---\n\nInspect only.\n"},
	} {
		result := request("tools/call", map[string]any{"name": name, "arguments": args}, true, "2026-07-28")
		if result["isError"] == true {
			t.Fatalf("UI settings input rejected: %s: %v", name, result)
		}
	}
	var monitored domain.Repository
	if err := db.Get(ctx, "repository", "pilot/local", &monitored); err != nil {
		t.Fatal(err)
	}
	enabled := true
	monitored.AutoReview = &enabled
	monitored.ReviewPrompt = "Keep owner review instructions"
	monitored.ReviewProfileID = "review"
	if err := a.SaveRepository(ctx, monitored); err != nil {
		t.Fatal(err)
	}
	legacyRepo := request("tools/call", map[string]any{"name": "save_repository", "arguments": map[string]any{"id": "pilot/local", "checkout": "/tmp/pilot-legacy"}}, true, "2026-07-28")
	if legacyRepo["isError"] == true {
		t.Fatal(legacyRepo)
	}
	if err := db.Get(ctx, "repository", "pilot/local", &monitored); err != nil {
		t.Fatal(err)
	}
	if monitored.AutoReview == nil || !*monitored.AutoReview || monitored.ReviewPrompt != "Keep owner review instructions" || monitored.ReviewProfileID != "review" {
		t.Fatal("older widget overwrote monitoring fields")
	}
	inherit := request("tools/call", map[string]any{"name": "save_repository", "arguments": map[string]any{"id": "pilot/local", "autoReview": nil, "reviewPrompt": ""}}, true, "2026-07-28")
	if inherit["isError"] == true {
		t.Fatal(inherit)
	}
	monitored = domain.Repository{}
	_ = db.Get(ctx, "repository", "pilot/local", &monitored)
	if monitored.AutoReview != nil || monitored.ReviewPrompt != "" || monitored.Checkout != "/tmp/pilot-legacy" {
		t.Fatal("explicit inheritance/clearing was lost")
	}
	config, _ := a.Config(ctx)
	config.AutoReview = true
	config.ReviewProfileID = "review"
	config.PluginCheckMinutes = 60
	config.GitHubRequestsPerHour = 120
	reserve := 42
	config.GitHubReserve = &reserve
	config.GitHubPollSeconds = 300
	if err := a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	legacyConfig := map[string]any{"id": "settings", "priorityLabels": config.PriorityLabels, "assessmentPrompt": config.AssessmentPrompt, "notifications": config.Notifications, "policyVersion": config.PolicyVersion, "jiraUrl": config.JiraURL, "jiraEmail": config.JiraEmail}
	savedSettings := request("tools/call", map[string]any{"name": "save_settings", "arguments": legacyConfig}, true, "2026-07-28")
	if savedSettings["isError"] == true {
		t.Fatal(savedSettings)
	}
	config, _ = a.Config(ctx)
	if !config.AutoReview || config.ReviewProfileID != "review" || config.PluginCheckMinutes != 60 || config.GitHubRequestsPerHour != 120 || config.GitHubReserve == nil || *config.GitHubReserve != 42 || config.GitHubPollSeconds != 300 {
		t.Fatal("older settings form overwrote system review defaults")
	}
	admin := request("tools/call", map[string]any{"name": "read_plugin_admin", "arguments": map[string]any{}}, true, "2026-07-28")
	if admin["isError"] == true || admin["structuredContent"] == nil {
		t.Fatal("admin inventory read failed", admin)
	}
	invalid := request("tools/call", map[string]any{"name": "manage_codex_plugins", "arguments": map[string]any{"action": "install", "target": "--config=invalid"}}, true, "2026-07-28")
	if invalid["isError"] != true {
		t.Fatal("plugin selector injection accepted")
	}
	publication := domain.PRReviews{ItemID: "pilot/local#99", SyncedAt: time.Now().UTC(), LastAttempt: time.Now().UTC(), Reviews: []domain.PublishedReview{{ID: "9", URL: "https://github.com/pilot/local/pull/99#pullrequestreview-9", State: "COMMENTED", Author: "reviewer", Head: "reviewed-head", Body: "Concrete review", Comments: []domain.ReviewComment{{ID: "10", Path: "a.py", Line: 4, Body: "Verified defect"}}}}}
	_ = db.Put(ctx, "item", publication.ItemID, domain.Item{ID: publication.ItemID, Repository: "pilot/local", Number: 99, Kind: "pr"}, "")
	_ = db.Put(ctx, "pr-reviews", publication.ItemID, publication, "")
	_ = db.Put(ctx, "run", "publication-run", domain.Run{ID: "publication-run", ItemID: publication.ItemID, Status: "idle"}, "")
	for _, name := range []string{"read_pr_reviews", "read_run"} {
		id := publication.ItemID
		if name == "read_run" {
			id = "publication-run"
		}
		result := request("tools/call", map[string]any{"name": name, "arguments": map[string]any{"id": id}}, true, "2026-07-28")
		b, _ := json.Marshal(result["structuredContent"])
		if result["isError"] == true || !strings.Contains(string(b), "reviewed-head") || !strings.Contains(string(b), "Verified defect") || !strings.Contains(string(b), "pullrequestreview-9") {
			t.Fatal("model-visible publication evidence lost", name, result)
		}
	}
	if err := db.Write(ctx, func(tx *store.Tx) error {
		for _, row := range [][2]string{{"item", publication.ItemID}, {"pr-reviews", publication.ItemID}, {"run", "publication-run"}} {
			if err := tx.Delete(row[0], row[1]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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
	working := request("tools/call", map[string]any{"name": "list_inbox", "arguments": map[string]any{"queue": "working", "state": "all"}}, true, "2026-07-28")
	workingData := working["structuredContent"].(map[string]any)["data"].([]any)
	if len(workingData) != 1 || workingData[0].(map[string]any)["work"].(map[string]any)["tracked"] != true {
		t.Fatal("working projection missing retained work")
	}
	invalidQueue := request("tools/call", map[string]any{"name": "list_inbox", "arguments": map[string]any{"queue": "unknown"}}, true, "2026-07-28")
	if invalidQueue["isError"] != true {
		t.Fatal("unknown queue accepted")
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

	pr := domain.Item{ID: "pilot/local#8", Repository: monitored.ID, Kind: "pr", Author: "other", Head: strings.Repeat("b", 40), URL: "https://github.com/pilot/local/pull/8", Title: "Review fixture"}
	if err := db.Put(ctx, "identity", "github", domain.GitHubViewer{Login: "me"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "item", pr.ID, pr, ""); err != nil {
		t.Fatal(err)
	}
	prepared := request("tools/call", map[string]any{"name": "prepare_pr_review", "arguments": map[string]any{"itemId": pr.ID}}, true, "2026-07-28")
	if prepared["isError"] == true {
		t.Fatal("PR review preparation rejected", prepared)
	}
	run := prepared["structuredContent"].(map[string]any)["data"].(map[string]any)
	if run["status"] != "prepared" || run["base"] != pr.Head || run["profile"].(map[string]any)["id"] != "review" {
		t.Fatal("PR review did not return the prepared configured pipeline", run)
	}
	pr.Author = "ME"
	if err := db.Put(ctx, "item", pr.ID, pr, ""); err != nil {
		t.Fatal(err)
	}
	beforeOwnReview := db.Latest(ctx)
	for _, name := range []string{"prepare_pr_review", "prepare_launch"} {
		args := map[string]any{"itemId": pr.ID}
		if name == "prepare_launch" {
			args["profileId"] = "review"
		}
		rejected := request("tools/call", map[string]any{"name": name, "arguments": args}, true, "2026-07-28")
		if rejected["isError"] != true || !strings.Contains(fmt.Sprint(rejected), "cannot review your own pull request") {
			t.Fatal("MCP accepted own-PR review", name, rejected)
		}
	}
	rejectedStart := request("tools/call", map[string]any{"name": "start_run", "arguments": map[string]any{"id": run["id"]}}, true, "2026-07-28")
	if rejectedStart["isError"] != true || !strings.Contains(fmt.Sprint(rejectedStart), "cannot review your own pull request") || db.Latest(ctx) != beforeOwnReview {
		t.Fatal("MCP started own-PR review or changed retained state", rejectedStart)
	}
	badReview := request("tools/call", map[string]any{"name": "prepare_pr_review", "arguments": map[string]any{"itemId": "pilot/local#7"}}, true, "2026-07-28")
	if badReview["isError"] != true {
		t.Fatal("non-PR preparation accepted")
	}
	legacy := request("initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "compat-test", "version": "1"}}, true, "2025-11-25")
	if legacy["protocolVersion"] != "2025-11-25" {
		t.Fatal("legacy adapter", legacy)
	}
	request("tools/list", nil, true, "2025-11-25")
}

func TestDashboardSummariesOmitHistoryAndKeepFullItemDetail(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &app.App{Store: db}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	i := domain.Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "pr", Title: "Requested review", State: "open", Body: "Review context", Head: "abc", ReviewRequests: []string{"me"}, Activity: []domain.Activity{{ID: "comment:1", Body: strings.Repeat("history", 100000)}}, Checks: json.RawMessage(`{"large":"` + strings.Repeat("checks", 100000) + `"}`)}
	i.PR = &domain.PullRequest{ID: i.ID, CI: "success", Checks: []domain.Check{{Name: strings.Repeat("full check", 100000)}}}
	i.Revision = i.ContentRevision()
	linked := domain.Item{ID: "other/repo#2", Kind: "pr", PR: &domain.PullRequest{ID: "other/repo#2", CI: "success", Checks: i.PR.Checks}}
	if err := db.Put(ctx, "linked-pr", linked.ID, linked, ""); err != nil {
		t.Fatal(err)
	}
	link := domain.Relationship{ID: "linked", Source: i.ID, Target: linked.ID, Evidence: "https://github.com/other/repo/pull/2"}
	if err := db.Put(ctx, "relationship", link.ID, link, ""); err != nil {
		t.Fatal(err)
	}
	for kind, value := range map[string]any{"item": i, "user": domain.UserState{ID: i.ID}, "identity": domain.GitHubViewer{Login: "me"}} {
		id := i.ID
		if kind == "identity" {
			id = "github"
		}
		if err := db.Put(ctx, kind, id, value, ""); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{App: a}
	snap, err := s.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(snap)
	if err != nil || len(b) > 30000 {
		t.Fatal("dashboard carried item history", len(b), err)
	}
	entry := snap["inbox"].([]domain.InboxEntry)[0]
	if !app.ContainsQueue(entry, "review") || entry.Item.Body != i.Body || entry.Item.Revision != i.Revision || entry.Item.Head != i.Head {
		t.Fatal("summary lost review or launch context")
	}
	if len(entry.Item.PR.Checks) != 0 || entry.Item.PR.CI != "success" || len(entry.LinkedPullRequests) != 1 || len(entry.LinkedPullRequests[0].Checks) != 0 {
		t.Fatal("summary retained full check lists or lost status")
	}
	detail, err := s.detail(ctx, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	full := detail.(map[string]any)["item"].(domain.Item)
	if len(full.PR.Checks) != 1 || full.PR.Checks[0].Name != i.PR.Checks[0].Name || detail.(map[string]any)["linkedPullRequests"].([]domain.PullRequest)[0].Checks[0].Name != i.PR.Checks[0].Name {
		t.Fatal("full PR checks were truncated")
	}
	if len(full.Activity) != 1 || full.Activity[0].Body != i.Activity[0].Body || !bytes.Equal(full.Checks, i.Checks) {
		t.Fatal("full detail was truncated")
	}
}
