package pluginbundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// BrowserArchive targets a registered cloud MCP connection rather than asking a
// desktop executor to instantiate the package's bundled MCP server definition.
// The ID is supplied by the operator after real Work registration; never derive
// it from a Plugin_ package ID or fabricate one.
func (b *Bundle) BrowserArchive(id string) ([]byte, error) {
	id = strings.TrimPrefix(id, "plugin_")
	if !regexp.MustCompile(`^(asdk_app_|connector_|templated_apps_)[A-Za-z0-9][A-Za-z0-9_-]*$`).MatchString(id) {
		return nil, fmt.Errorf("an observed registered MCP app ID is required")
	}
	files := map[string][]byte{}
	for name, data := range b.files {
		if name != "mcp.json" && name != ".mcp.json" && name != "SHA256SUMS" {
			files[name] = data
		}
	}
	var manifest map[string]any
	if e := json.Unmarshal(files["plugin.json"], &manifest); e != nil {
		return nil, e
	}
	extension, ok := manifest["extensions"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing plugin extension")
	}
	openai, ok := extension["com.openai"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing OpenAI plugin extension")
	}
	openai["apps"] = "./.app.json"
	files["plugin.json"], _ = json.MarshalIndent(manifest, "", "  ")
	var compat map[string]any
	if e := json.Unmarshal(files[".codex-plugin/plugin.json"], &compat); e != nil {
		return nil, e
	}
	delete(compat, "mcpServers")
	compat["apps"] = "./.app.json"
	files[".codex-plugin/plugin.json"], _ = json.MarshalIndent(compat, "", "  ")
	files[".app.json"], _ = json.MarshalIndent(map[string]any{"apps": map[string]any{"code-factory": map[string]any{"id": id, "required": true}}}, "", "  ")
	files["README.md"] = []byte("Code Factory private browser package. Requires access to its registered MCP server and OAuth consent. Import in a supported private/workspace plugin flow; public submission uses With MCP. Package generation does not verify a Work installation.\n")
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var sums strings.Builder
	for _, name := range names {
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(files[name]), name)
	}
	files["SHA256SUMS"] = []byte(sums.String())
	names = append(names, "SHA256SUMS")
	sort.Strings(names)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		header.SetMode(0644)
		entry, e := writer.CreateHeader(header)
		if e != nil {
			return nil, e
		}
		if _, e = entry.Write(files[name]); e != nil {
			return nil, e
		}
	}
	if e := writer.Close(); e != nil {
		return nil, e
	}
	return buffer.Bytes(), nil
}

// RegisterBrowser exposes no unbound or placeholder download. A real registered
// connection must be recorded first; even then, Work must verify eligibility.
func (b *Bundle) RegisterBrowser(mux *http.ServeMux, lookup func() (string, error)) {
	mux.HandleFunc("GET /plugin-browser.zip", func(w http.ResponseWriter, r *http.Request) {
		id, e := lookup()
		if e != nil {
			http.Error(w, "Browser package needs a registered MCP app ID from ChatGPT (plugin_asdk_app_...). The Plugin_ package ID is not a server ID. Complete remote MCP registration first.", http.StatusConflict)
			return
		}
		archive, e := b.BrowserArchive(id)
		if e != nil {
			http.Error(w, "Recorded MCP app ID is invalid; correct the registered connection mapping", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="code-factory-browser-plugin-`+b.version+`.zip"`)
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(archive)))
		http.ServeContent(w, r, "code-factory-browser-plugin.zip", time.Time{}, bytes.NewReader(archive))
	})
}
