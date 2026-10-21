package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func LinkPlugin(id string) error {
	id = strings.TrimPrefix(id, "plugin_")
	if !regexp.MustCompile(`^(asdk_app_|connector_|templated_apps_)[A-Za-z0-9_-]+$`).MatchString(id) {
		return fmt.Errorf("provide the observed registered app ID")
	}
	root := filepath.Join(installRoot(), "plugin")
	var apps map[string]any
	path := filepath.Join(root, ".app.json")
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		apps = map[string]any{"apps": map[string]any{}}
	} else if e != nil {
		return e
	} else if e = json.Unmarshal(b, &apps); e != nil {
		return e
	}
	mapping, ok := apps["apps"].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid installed app mapping")
	}
	mapping["code-factory"] = map[string]any{"id": id, "required": true}
	if e = AtomicJSON(path, apps); e != nil {
		return e
	}
	if e = linkManifest(root); e != nil {
		return e
	}
	// The cloud package identity (Plugin_...) is deliberately never accepted here.
	// Keep a separate nonsecret mapping for the gateway's linked browser download.
	if installed, e := ReadInstallation(); e == nil {
		return AtomicJSON(filepath.Join(installed.Runtime.DataDir, "registered-app.json"), map[string]string{"id": id})
	} else if !os.IsNotExist(e) {
		return e
	}
	return nil
}

// RegisterPersonalPlugin makes the installed package discoverable without editing
// unrelated plugin choices. Local paths resolve from the user's marketplace root.
func RegisterPersonalPlugin() (string, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	root := filepath.Join(installRoot(), "plugin")
	var plugin map[string]any
	b, e := os.ReadFile(filepath.Join(root, "plugin.json"))
	if e != nil {
		return "", e
	}
	if e = json.Unmarshal(b, &plugin); e != nil {
		return "", e
	}
	if plugin["name"] != "code-factory" {
		return "", fmt.Errorf("installed Code Factory plugin is missing")
	}
	path := filepath.Join(home, ".agents", "plugins", "marketplace.json")
	marketplace := map[string]any{"name": "code-factory-personal", "interface": map[string]any{"displayName": "Code Factory personal"}, "plugins": []any{}}
	if b, e = os.ReadFile(path); e == nil {
		if e = json.Unmarshal(b, &marketplace); e != nil {
			return "", e
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	name, ok := marketplace["name"].(string)
	if !ok || name == "" {
		return "", fmt.Errorf("invalid personal marketplace name")
	}
	entries, ok := marketplace["plugins"].([]any)
	if !ok {
		return "", fmt.Errorf("invalid personal marketplace plugin list")
	}
	relative, e := filepath.Rel(home, root)
	if e != nil || !filepath.IsLocal(relative) {
		return "", fmt.Errorf("plugin must be contained within the personal marketplace root")
	}
	source := "./" + filepath.ToSlash(relative)
	entry := map[string]any{"name": "code-factory", "source": map[string]any{"source": "local", "path": source}, "policy": map[string]any{"installation": "AVAILABLE", "authentication": "ON_INSTALL"}, "category": "Productivity"}
	found := false
	for i, raw := range entries {
		old, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("invalid personal marketplace entry")
		}
		if old["name"] != "code-factory" {
			continue
		}
		prior, ok := old["source"].(map[string]any)
		if !ok || prior["source"] != "local" || prior["path"] != source {
			return "", fmt.Errorf("another Code Factory entry already exists in the personal marketplace; inspect it before replacing")
		}
		entries[i] = entry
		found = true
	}
	if !found {
		entries = append(entries, entry)
	}
	marketplace["plugins"] = entries
	if e = AtomicJSON(path, marketplace); e != nil {
		return "", e
	}
	return name, nil
}

// SyncPluginEndpoint keeps bundled transport metadata aligned with the installed
// Tailscale host, including its compatibility configuration.
func SyncPluginEndpoint(endpoint string) error {
	root := filepath.Join(installRoot(), "plugin")
	for _, name := range []string{"mcp.json", ".mcp.json"} {
		path := filepath.Join(root, name)
		b, e := os.ReadFile(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		var value map[string]any
		if e = json.Unmarshal(b, &value); e != nil {
			return e
		}
		servers, ok := value["mcpServers"].(map[string]any)
		if !ok {
			return fmt.Errorf("invalid MCP server configuration")
		}
		server, ok := servers["code-factory"].(map[string]any)
		if !ok {
			return fmt.Errorf("missing Code Factory MCP server")
		}
		server["url"] = endpoint
		if e = AtomicJSON(path, value); e != nil {
			return e
		}
	}
	return nil
}
