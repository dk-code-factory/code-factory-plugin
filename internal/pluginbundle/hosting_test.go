package pluginbundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestHostedPackageIsCompleteBoundToHostAndChecksummed(t *testing.T) {
	for _, origin := range []string{"https://pilot.example.ts.net:10000", "https://pilot.example.ts.net/code-factory"} {
		t.Run(origin, func(t *testing.T) {
			bundle, e := New(origin)
			if e != nil {
				t.Fatal(e)
			}
			mux := http.NewServeMux()
			bundle.Register(mux, nil)
			get := func(path string) *httptest.ResponseRecorder {
				t.Helper()
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				return w
			}
			page := get("/")
			if page.Code != 200 || !strings.Contains(page.Body.String(), origin+"/mcp") || !strings.Contains(page.Body.String(), "/plugin.zip") {
				t.Fatal("missing hosted installation page")
			}
			links := regexp.MustCompile(`(?:href|src)="([^"]+)"`).FindAllStringSubmatch(page.Body.String(), -1)
			if len(links) != 6 {
				t.Fatal("unexpected public landing link count", len(links))
			}
			for _, link := range links {
				if !strings.HasPrefix(link[1], origin+"/") {
					t.Fatal("landing link escaped configured base URL", link[1])
				}
			}
			for _, asset := range []string{"/plugin.json", "/mcp.json", "/assets/icon.svg", "/assets/logo.svg"} {
				if response := get(asset); response.Code != 200 || response.Body.Len() == 0 {
					t.Fatal("public artifact unavailable", asset)
				}
			}
			downloaded := get("/plugin.zip")
			if downloaded.Code != 200 || downloaded.Header().Get("Content-Type") != "application/zip" || !strings.HasPrefix(downloaded.Header().Get("Content-Disposition"), "attachment;") {
				t.Fatal("package download invalid")
			}
			checksum := get("/plugin.zip.sha256")
			sum := fmt.Sprintf("%x", sha256.Sum256(downloaded.Body.Bytes()))
			if !strings.HasPrefix(checksum.Body.String(), sum+"  ") {
				t.Fatal("public checksum mismatch")
			}
			var release struct{ SHA256, DownloadURL, MCPURL string }
			if e = json.Unmarshal(get("/plugin-release.json").Body.Bytes(), &release); e != nil || release.SHA256 != sum || release.DownloadURL != origin+"/plugin.zip" || release.MCPURL != origin+"/mcp" {
				t.Fatal("release metadata mismatch", e)
			}
			archive, e := zip.NewReader(bytes.NewReader(downloaded.Body.Bytes()), int64(downloaded.Body.Len()))
			if e != nil {
				t.Fatal(e)
			}
			files := map[string][]byte{}
			for _, file := range archive.File {
				r, e := file.Open()
				if e != nil {
					t.Fatal(e)
				}
				b, e := io.ReadAll(r)
				r.Close()
				if e != nil {
					t.Fatal(e)
				}
				files[file.Name] = b
			}
			for _, path := range []string{"plugin.json", "mcp.json", ".mcp.json", ".codex-plugin/plugin.json", "skills/code-factory/SKILL.md", "skills/code-factory-setup/SKILL.md", "skills/code-factory-admin/SKILL.md", "assets/logo.svg", "SHA256SUMS"} {
				if len(files[path]) == 0 {
					t.Fatal("missing plugin component", path)
				}
			}
			for _, name := range []string{"mcp.json", ".mcp.json"} {
				if !bytes.Contains(files[name], []byte(origin+"/mcp")) {
					t.Fatal("package endpoint differs from hosted gateway")
				}
			}
			for _, line := range strings.Split(strings.TrimSpace(string(files["SHA256SUMS"])), "\n") {
				parts := strings.SplitN(line, "  ", 2)
				if len(parts) != 2 || fmt.Sprintf("%x", sha256.Sum256(files[parts[1]])) != parts[0] {
					t.Fatal("internal checksum mismatch", line)
				}
			}
			for _, path := range []string{"/owner.token", "/gateway-auth.json", "/runtime.json", "/.app.json", "/skills/code-factory/SKILL.md", "/data/state.db"} {
				if get(path).Code != 404 {
					t.Fatal("non-public artifact exposed", path)
				}
			}
			again, e := New(origin)
			if e != nil || !bytes.Equal(bundle.archive, again.archive) {
				t.Fatal("hosted package is not deterministic", e)
			}
		})
	}
}

func TestHostedPackageRejectsUnsafeBaseURLs(t *testing.T) {
	for _, base := range []string{
		"http://pilot.ts.net/code-factory", "https://", "https://user:secret@pilot.ts.net/code-factory",
		"https://pilot.ts.net/code-factory?key=secret", "https://pilot.ts.net/code-factory?",
		"https://pilot.ts.net/code-factory#fragment", "https://pilot.ts.net/code-factory#",
		"https://pilot.ts.net/../code-factory", "https://pilot.ts.net/code-factory/..", "https://pilot.ts.net/code-factory/.",
		"https://pilot.ts.net//code-factory", "https://pilot.ts.net/code-factory/", "https://pilot.ts.net/",
		"https://pilot.ts.net/%63ode-factory", "https://pilot.ts.net/code%2ffactory", "https://pilot.ts.net/%2e%2e/code-factory",
	} {
		t.Run(base, func(t *testing.T) {
			if _, e := New(base); e == nil {
				t.Fatal("unsafe or noncanonical base URL accepted")
			}
		})
	}
}

func TestBrowserPackageRequiresRealAppMappingAndOmitsBundledMCP(t *testing.T) {
	bundle, e := New("https://pilot.ts.net:10000")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bundle.BrowserArchive("Plugin_e162fcc74c788191975d60b6f80137f2"); e == nil {
		t.Fatal("package ID accepted as remote app")
	}
	mux := http.NewServeMux()
	bundle.RegisterBrowser(mux, func() (string, error) { return "", fmt.Errorf("no registered connection") })
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/plugin-browser.zip", nil))
	if w.Code != 409 || w.Header().Get("Content-Disposition") != "" {
		t.Fatal("unbound browser ZIP offered")
	}
	mux = http.NewServeMux()
	bundle.RegisterBrowser(mux, func() (string, error) { return "plugin_asdk_app_observed", nil })
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/plugin-browser.zip", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	archive, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if e != nil {
		t.Fatal(e)
	}
	files := map[string][]byte{}
	for _, file := range archive.File {
		r, e := file.Open()
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		files[file.Name] = data
	}
	if files["mcp.json"] != nil || files[".mcp.json"] != nil {
		t.Fatal("browser artifact includes desktop MCP launcher")
	}
	var m map[string]any
	if e = json.Unmarshal(files[".codex-plugin/plugin.json"], &m); e != nil {
		t.Fatal(e)
	}
	if m["apps"] != "./.app.json" || m["mcpServers"] != nil {
		t.Fatal("incorrect browser wiring")
	}
	var apps struct {
		Apps map[string]struct {
			ID       string
			Required bool
		}
	}
	if e = json.Unmarshal(files[".app.json"], &apps); e != nil || apps.Apps["code-factory"].ID != "asdk_app_observed" || !apps.Apps["code-factory"].Required {
		t.Fatal("wrong registered mapping", e)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(files["SHA256SUMS"])), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || fmt.Sprintf("%x", sha256.Sum256(files[parts[1]])) != parts[0] {
			t.Fatal("browser checksum mismatch")
		}
	}
}

func TestLandingPrioritizesOnlyValidRegisteredBrowserPackage(t *testing.T) {
	bundle, err := New("https://pilot.ts.net/code-factory")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "Plugin_package", "asdk_app_observed"} {
		t.Run(id, func(t *testing.T) {
			mux := http.NewServeMux()
			bundle.Register(mux, func() (string, error) { return id, nil })
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			body := w.Body.String()
			primary := `class="button" href="https://pilot.ts.net/code-factory/plugin-browser.zip"`
			if strings.Contains(body, primary) != (id == "asdk_app_observed") {
				t.Fatal("incorrect primary download", body)
			}
			if !strings.Contains(body, `<details><summary>Advanced packages &amp; manifests</summary>`) && !strings.Contains(body, `<details><summary>Advanced packages & manifests</summary>`) {
				t.Fatal("missing collapsed advanced packages")
			}
			if strings.Index(body, "Advanced packages") > strings.Index(body, `href="https://pilot.ts.net/code-factory/plugin.zip"`) {
				t.Fatal("portable package outside advanced section")
			}
			if id != "asdk_app_observed" && strings.Contains(body, "/plugin-browser.zip") {
				t.Fatal("unbound browser download offered")
			}
			if !strings.Contains(body, "Connect in browser Work") || !strings.Contains(body, "OAuth") {
				t.Fatal("connection steps missing")
			}
		})
	}
}
