// Package pluginbundle serves only the embedded distributable plugin, never
// application data or an arbitrary filesystem directory.
package pluginbundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed dist/code-factory.zip
var packaged []byte

type Bundle struct {
	files    map[string][]byte
	archive  []byte
	checksum string
	version  string
	origin   string
}

// New binds both transport manifests to the running gateway's HTTPS base URL,
// including an optional canonical path prefix. The regenerated checksums
// describe these exact bytes.
func New(origin string) (*Bundle, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		u.RawPath != "" || strings.Contains(origin, "%") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(origin, "#") ||
		!regexp.MustCompile(`^(?:/[A-Za-z0-9_-]+)*$`).MatchString(u.Path) {
		return nil, fmt.Errorf("plugin host must be an HTTPS base URL with an optional canonical path prefix")
	}
	archive, e := zip.NewReader(bytes.NewReader(packaged), int64(len(packaged)))
	if e != nil {
		return nil, e
	}
	b := &Bundle{files: map[string][]byte{}, origin: origin}
	for _, file := range archive.File {
		if file.FileInfo().IsDir() || file.Name == "SHA256SUMS" {
			continue
		}
		reader, e := file.Open()
		if e != nil {
			return nil, e
		}
		data, e := io.ReadAll(io.LimitReader(reader, 2<<20))
		reader.Close()
		if e != nil {
			return nil, e
		}
		b.files[file.Name] = data
	}
	for _, name := range []string{"mcp.json", ".mcp.json"} {
		var m struct {
			Servers map[string]map[string]any `json:"mcpServers"`
		}
		if e = json.Unmarshal(b.files[name], &m); e != nil {
			return nil, e
		}
		if m.Servers["code-factory"] == nil {
			return nil, fmt.Errorf("missing plugin connection")
		}
		var value map[string]any
		if e = json.Unmarshal(b.files[name], &value); e != nil {
			return nil, e
		}
		value["mcpServers"].(map[string]any)["code-factory"].(map[string]any)["url"] = origin + "/mcp"
		b.files[name], e = json.MarshalIndent(value, "", "  ")
		if e != nil {
			return nil, e
		}
		b.files[name] = append(b.files[name], '\n')
	}
	var manifest struct{ Version string }
	if e = json.Unmarshal(b.files["plugin.json"], &manifest); e != nil || manifest.Version == "" {
		return nil, fmt.Errorf("invalid plugin manifest")
	}
	b.version = manifest.Version
	names := make([]string, 0, len(b.files))
	for name := range b.files {
		names = append(names, name)
	}
	sort.Strings(names)
	var sums strings.Builder
	for _, name := range names {
		sum := sha256.Sum256(b.files[name])
		fmt.Fprintf(&sums, "%x  %s\n", sum, name)
	}
	b.files["SHA256SUMS"] = []byte(sums.String())
	names = append(names, "SHA256SUMS")
	sort.Strings(names)
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		header.SetMode(0644)
		entry, e := writer.CreateHeader(header)
		if e != nil {
			return nil, e
		}
		if _, e = entry.Write(b.files[name]); e != nil {
			return nil, e
		}
	}
	if e = writer.Close(); e != nil {
		return nil, e
	}
	b.archive = buf.Bytes()
	sum := sha256.Sum256(b.archive)
	b.checksum = hex.EncodeToString(sum[:])
	return b, nil
}

func (b *Bundle) Register(mux *http.ServeMux, lookup func() (string, error)) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		registered := false
		if lookup != nil {
			if id, err := lookup(); err == nil {
				_, err = b.BrowserArchive(id)
				registered = err == nil
			}
		}
		_ = landing.Execute(w, map[string]any{"Version": b.version, "BaseURL": b.origin, "Endpoint": b.origin + "/mcp", "Registered": registered})
	})
	mux.HandleFunc("GET /plugin.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="code-factory-plugin-`+b.version+`.zip"`)
		http.ServeContent(w, r, "code-factory-plugin.zip", time.Time{}, bytes.NewReader(b.archive))
	})
	mux.HandleFunc("GET /plugin.zip.sha256", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "%s  code-factory-plugin-%s.zip\n", b.checksum, b.version)
	})
	mux.HandleFunc("GET /plugin-release.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "code-factory", "version": b.version, "downloadUrl": b.origin + "/plugin.zip", "sha256": b.checksum, "mcpUrl": b.origin + "/mcp", "authentication": "oauth", "workInstallation": "requires user installation and OAuth approval; hosted package alone does not install in Work"})
	})
	// Explicit public artifact list prevents accidentally publishing private files.
	for _, name := range []string{"plugin.json", "mcp.json", "assets/icon.svg", "assets/logo.svg"} {
		data := b.files[name]
		path := name
		mux.HandleFunc("GET /"+path, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(path, ".svg") {
				w.Header().Set("Content-Type", "image/svg+xml")
			} else {
				w.Header().Set("Content-Type", "application/json")
			}
			_, _ = w.Write(data)
		})
	}
}

var landing = template.Must(template.New("installation").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Code Factory</title><style>
body{font:17px/1.6 system-ui,sans-serif;max-width:760px;margin:60px auto;padding:24px;background:#f7faf7;color:#193e2c}header{display:flex;align-items:center;gap:18px}header img{width:64px}h1{margin:0}small{color:#55705e}.button{display:inline-block;background:#285d45;color:white;padding:12px 20px;border-radius:9px;text-decoration:none}section{background:white;border:1px solid #dbe6dd;padding:24px;border-radius:14px;margin:24px 0}code{overflow-wrap:anywhere;font-size:.9em}a{color:#285d45}li{margin:12px 0}</style></head><body>
<header><img src="{{.BaseURL}}/assets/logo.svg" alt=""><div><h1>Code Factory</h1><small>Plugin {{.Version}} · hosted on your machine through Tailscale</small></div></header>
<p>Your GitHub inbox and interactive local Codex agents. Includes the embedded inbox, OAuth MCP connection, and setup, daily-use and admin skills.</p>
{{if .Registered}}<p><a class="button" href="{{.BaseURL}}/plugin-browser.zip">Download browser package</a></p>
<p>Linked to your registered MCP connection. Import into a supported private plugin flow and approve OAuth when asked.</p>{{end}}
<section><h2>Connect in browser Work</h2><ol><li>In your existing signed-in ChatGPT browser tab, open <strong>Plugins</strong>, choose <strong>+ → Add custom MCP server</strong>, and name it <strong>Code Factory</strong>. Stay in that browser tab; this page does not launch or require the desktop app.</li>
<li>Use this server URL:<br><code>{{.Endpoint}}</code></li><li>Choose <strong>OAuth</strong>. Leave optional client ID and secret empty. Create the plugin and approve <strong>Connect Code Factory</strong> on the private Tailscale page. Your browser must be connected to the owner's tailnet.</li><li>Install Code Factory from Personal, open a new Work conversation, and ask <strong>Call Code Factory’s open_factory tool to show GitHub issues and pull requests</strong>.</li></ol>
<p>This connects the hosted tools and embedded UI. Installing the ZIP's bundled skills depends on your Work account's package-import support. A download link cannot install into your account automatically.</p></section>
<details><summary>Advanced packages & manifests</summary><section><h2>Portable plugin package</h2><p>For hosts that support portable plugin manifests or a local marketplace.</p><p><a href="{{.BaseURL}}/plugin.zip">Download portable plugin</a> · <a href="{{.BaseURL}}/plugin.zip.sha256">SHA256 checksum</a></p><p><a href="{{.BaseURL}}/plugin.json">Plugin manifest</a> · <a href="{{.BaseURL}}/mcp.json">MCP configuration</a> · <a href="{{.BaseURL}}/plugin-release.json">Release details</a></p></section></details>
<p><small>MCP calls require OAuth. Approval stays private to the Tailscale owner. Package downloads contain no local tokens, credentials, inbox data or run history.</small></p>
</body></html>`))
