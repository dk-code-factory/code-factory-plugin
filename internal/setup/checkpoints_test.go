package setup

import (
	"codefactory/internal/buildinfo"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnershipRequiredAndVersionBoundEvidence(t *testing.T) {
	dir := t.TempDir()
	if RequireOwnedTunnel(dir) == nil {
		t.Fatal("unverified ownership accepted")
	}
	c := TunnelCheckpoint{ID: "tunnel_fixture", Organization: "org_fixture", Workspace: "private-workspace", OwnerOnly: true, Configured: true}
	if e := AtomicJSON(filepath.Join(dir, "tunnel.json"), c); e != nil {
		t.Fatal(e)
	}
	if e := RequireOwnedTunnel(dir); e != nil {
		t.Fatal(e)
	}
	if e := RecordEvidence(dir, "question.answer", Verified, "2026-10-19 fixture source reference"); e != nil {
		t.Fatal(e)
	}
	evidence, e := readEvidence(dir, "question.answer")
	if e != nil || evidence.Version != buildinfo.Version || evidence.At.IsZero() {
		t.Fatal("evidence is not bound to build and date", e)
	}
	if RecordEvidence(dir, "invented", Verified, "reference") == nil {
		t.Fatal("unknown evidence accepted")
	}
	info, e := os.Stat(filepath.Join(EvidenceDir(dir), "question.answer.json"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe evidence", e)
	}
}

func TestAppMappingSurvivesRelinking(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(installRoot(), "plugin")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "plugin.json"), []byte(`{"name":"code-factory","extensions":{"com.openai":{"onboardingSkill":"./skills/setup/SKILL.md"}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, ".app.json"), []byte(`{"apps":{"other":{"id":"connector_preserved"}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := LinkPlugin("plugin_asdk_app_observed"); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(root, ".app.json"))
	if e != nil || !strings.Contains(string(b), "connector_preserved") || !strings.Contains(string(b), "asdk_app_observed") {
		t.Fatal("mapping was lost", e)
	}
	if LinkPlugin("https://invalid.example") == nil {
		t.Fatal("invented mapping accepted")
	}
}

func TestDiagnosticsRefuseNonloopbackBeforeReadingToken(t *testing.T) {
	for _, address := range []string{"example.com:80", "0.0.0.0:8787", "192.168.1.1:8787"} {
		if _, e := FetchDiagnostics(context.Background(), t.TempDir(), address); e == nil {
			t.Fatal("unsafe destination", address)
		}
	}
}

func TestPersonalPluginRegistrationPreservesExistingCatalog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(installRoot(), "plugin")
	if e := AtomicJSON(filepath.Join(root, "plugin.json"), map[string]any{"name": "code-factory"}); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(home, ".agents", "plugins", "marketplace.json")
	if e := AtomicJSON(path, map[string]any{"name": "my-existing-plugins", "plugins": []any{map[string]any{"name": "other", "source": map[string]any{"source": "local", "path": "./other"}}}}); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		name, e := RegisterPersonalPlugin()
		if e != nil || name != "my-existing-plugins" {
			t.Fatal("registration failed", e)
		}
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var catalog struct {
		Name    string
		Plugins []struct {
			Name   string
			Source struct{ Path string }
		}
	}
	if e = json.Unmarshal(b, &catalog); e != nil {
		t.Fatal(e)
	}
	if len(catalog.Plugins) != 2 || catalog.Plugins[0].Name != "other" || catalog.Plugins[1].Source.Path != "./.local/lib/code-factory/plugin" {
		t.Fatal("catalog was replaced", string(b))
	}
}

func TestBundledEndpointTracksConfiguredTailscaleHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(installRoot(), "plugin")
	for _, file := range []string{"mcp.json", ".mcp.json"} {
		if e := AtomicJSON(filepath.Join(root, file), map[string]any{"mcpServers": map[string]any{"code-factory": map[string]any{"type": "streamable-http", "url": "https://old.example/mcp"}, "other": map[string]any{"url": "https://other.example/mcp"}}}); e != nil {
			t.Fatal(e)
		}
	}
	if e := SyncPluginEndpoint("https://new.ts.net:10000/mcp"); e != nil {
		t.Fatal(e)
	}
	for _, file := range []string{"mcp.json", ".mcp.json"} {
		b, e := os.ReadFile(filepath.Join(root, file))
		if e != nil || !strings.Contains(string(b), "https://new.ts.net:10000/mcp") || !strings.Contains(string(b), "https://other.example/mcp") {
			t.Fatal("endpoint update lost configuration", e)
		}
	}
}
