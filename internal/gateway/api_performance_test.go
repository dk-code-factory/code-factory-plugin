package gateway

import (
	"encoding/json"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var apiPerformance = flag.Bool("api-performance", false, "run isolated API latency measurements")
var gatewaySamples struct {
	sync.Mutex
	Rows []gatewaySample
}

type gatewaySample struct {
	Method  string  `json:"method"`
	Path    string  `json:"path"`
	Status  int     `json:"status"`
	Outcome string  `json:"outcome"`
	MS      float64 `json:"ms"`
}

func recordGatewayTiming(t *testing.T, method, path string, status int, started time.Time) {
	t.Helper()
	if !*apiPerformance {
		return
	}
	elapsed := time.Since(started)
	// Strip one-time authorization/approval data from retained measurements.
	path = strings.SplitN(path, "?", 2)[0]
	gatewaySamples.Lock()
	gatewaySamples.Rows = append(gatewaySamples.Rows, gatewaySample{Method: method, Path: path, Status: status, MS: float64(elapsed) / float64(time.Millisecond), Outcome: gatewayOutcome(status)})
	gatewaySamples.Unlock()
	if elapsed > 100*time.Millisecond {
		t.Errorf("%s %s exceeded 100 ms: %s", method, path, elapsed)
	}
}
func gatewayOutcome(status int) string {
	if status == 503 {
		return "truthful_rejection"
	}
	if status == 202 {
		return "accepted"
	}
	if status >= 200 && status < 400 {
		return "success"
	}
	return "protocol_rejection"
}
func TestGatewayAPILatency(t *testing.T) {
	if !*apiPerformance {
		t.Skip("isolated API performance gate")
	}
	defer func() {
		path := filepath.Join("..", "..", "verification-results", "gateway-latency.json")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Error(err)
			return
		}
		gatewaySamples.Lock()
		defer gatewaySamples.Unlock()
		data, err := json.MarshalIndent(map[string]any{"limit_ms": 100, "samples": gatewaySamples.Rows}, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Error(err)
		}
	}()
	// Existing successful OAuth fixtures assert actual consent, exact PKCE,
	// token exchange/rotation/revocation and authenticated upstream proxying.
	t.Run("oauth", TestOwnerConsentPKCERotationPersistenceAndAuthenticatedProxy)
	t.Run("downloads", TestPublicDownloadsKeepMCPAndConsentProtected)
	t.Run("delayed_finite_dependencies", TestGatewayFiniteResponseContentionAndDelayedProxy)
	t.Run("delayed_registration_mapping", TestGatewayDelayedRegistrationMappingRemainsFinite)
	t.Run("delayed_audit", TestGatewayAuditKeepsActualWireStatusWithSlowSink)
	t.Run("uncertain_refresh", TestOAuthDeadlineDoesNotReplayConsumedRefresh)
	t.Run("stream_handshake", TestGatewaySSERequiresFirstDataAndKeepsAcknowledgedStream)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		io.WriteString(w, `{"accepted":true}`)
	}))
	defer backend.Close()
	for _, prefix := range []string{"", "/factory"} {
		cfg := Config{PublicURL: "https://public.example" + prefix, ApprovalURL: "https://private.example:8444", Owner: "owner@example.com", PublicListen: "127.0.0.1:8788", ApprovalListen: "127.0.0.1:8789", Daemon: strings.TrimPrefix(backend.URL, "http://")}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "registered-app.json"), []byte(`{"id":"asdk_app_latency_fixture"}`), 0600); err != nil {
			t.Fatal(err)
		}
		g, err := New(cfg, dir, strings.Repeat("f", 64))
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(g.Public())
		defer server.Close()
		server.Client().Timeout = 2 * time.Second
		paths := []string{"/", "/plugin.zip", "/plugin.json", "/mcp.json", "/assets/icon.svg", "/assets/logo.svg", "/healthz", "/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp", "/plugin.zip.sha256", "/plugin-release.json", "/plugin-browser.zip", "/authorize"}
		if prefix != "" {
			for _, path := range append([]string(nil), paths...) {
				paths = append(paths, prefix+path)
			}
			paths = append(paths, "/.well-known/oauth-authorization-server"+prefix, "/.well-known/oauth-protected-resource"+prefix+"/mcp")
		}
		seen := map[string]bool{}
		for n := 0; n < 20; n++ {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, path := range paths {
					// Invalid authorization probes consume the production limiter;
					// one sample per registered GET/HEAD variant covers this path.
					if n > 0 && strings.HasSuffix(path, "/authorize") {
						continue
					}
					request, _ := http.NewRequest(method, server.URL+path, nil)
					started := time.Now()
					response, err := server.Client().Do(request)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(response.Body)
					response.Body.Close()
					recordGatewayTiming(t, method, path, response.StatusCode, started)
					wantStatus := http.StatusOK
					if strings.HasSuffix(path, "/authorize") {
						wantStatus = http.StatusBadRequest
					}
					if err != nil || response.StatusCode != wantStatus || response.Header.Get("Content-Type") == "" {
						t.Fatal("public API variant failed", method, path, response.StatusCode, err, string(body))
					}
					if method == http.MethodHead && len(body) != 0 {
						t.Fatal("HEAD returned response body", path)
					}
					if method == http.MethodGet {
						if len(body) == 0 {
							t.Fatal("GET returned no representation", path)
						}
						if strings.Contains(path, "/.well-known/") {
							var metadata map[string]any
							if err := json.Unmarshal(body, &metadata); err != nil {
								t.Fatal(err)
							}
							key, want := "resource", g.resource()
							if strings.Contains(path, "oauth-authorization-server") {
								key, want = "issuer", cfg.PublicURL
							}
							if metadata[key] != want {
								t.Fatal("metadata lost configured prefix", method, path, metadata)
							}
						}
					}
					seen[method+" "+path] = true
				}
			}
		}
		request, _ := http.NewRequest("POST", server.URL+"/github/webhook", strings.NewReader(`{}`))
		started := time.Now()
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		recordGatewayTiming(t, "POST", "/github/webhook", response.StatusCode, started)
		if err != nil || response.StatusCode != http.StatusAccepted || string(body) != `{"accepted":true}` {
			t.Fatal("webhook proxy acknowledgement changed", response.StatusCode, err)
		}
		// Derive completeness from every public mux registration, including the
		// bundle's generated artifact routes and conditional metadata aliases.
		for _, pattern := range registeredGatewayPatterns(t, prefix) {
			method, path, qualified := strings.Cut(pattern, " ")
			if !qualified {
				method, path = "", pattern
			}
			if method == "GET" {
				for _, variant := range []string{"GET", "HEAD"} {
					if !seen[variant+" "+path] {
						t.Errorf("registered route %s %s has no timed scenario", variant, path)
					}
				}
				continue
			}
			covered := false
			gatewaySamples.Lock()
			for _, sample := range gatewaySamples.Rows {
				if sample.Path == path && (method == "" || sample.Method == method) {
					covered = true
				}
			}
			gatewaySamples.Unlock()
			if !covered {
				t.Errorf("registered route %s has no timed scenario", pattern)
			}
		}
	}
}

// Source registration is the oracle because net/http does not expose a mux's
// registered patterns. Fail on unknown route expressions instead of omitting one.
func registeredGatewayPatterns(t *testing.T, prefix string) []string {
	t.Helper()
	patterns := []string{}
	for _, path := range []string{"gateway.go", "../pluginbundle/hosting.go", "../pluginbundle/browser.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string][]string{}
		var values func(ast.Expr) []string
		values = func(expression ast.Expr) []string {
			switch expression := expression.(type) {
			case *ast.BasicLit:
				if expression.Kind == token.STRING {
					v, err := strconv.Unquote(expression.Value)
					if err == nil {
						return []string{v}
					}
				}
			case *ast.Ident:
				return names[expression.Name]
			case *ast.SelectorExpr:
				if owner, ok := expression.X.(*ast.Ident); ok && owner.Name == "g" && expression.Sel.Name == "publicPrefix" {
					return []string{prefix}
				}
			case *ast.BinaryExpr:
				if expression.Op == token.ADD {
					var result []string
					for _, left := range values(expression.X) {
						for _, right := range values(expression.Y) {
							result = append(result, left+right)
						}
					}
					return result
				}
			case *ast.CompositeLit:
				var result []string
				for _, element := range expression.Elts {
					result = append(result, values(element)...)
				}
				return result
			}
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.RangeStmt:
				if name, ok := node.Value.(*ast.Ident); ok {
					names[name.Name] = values(node.X)
				}
			case *ast.AssignStmt:
				if len(node.Lhs) == 1 && len(node.Rhs) == 1 {
					if name, ok := node.Lhs[0].(*ast.Ident); ok {
						names[name.Name] = values(node.Rhs[0])
					}
				}
			case *ast.CallExpr:
				method, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || (method.Sel.Name != "Handle" && method.Sel.Name != "HandleFunc") {
					return true
				}
				mux, ok := method.X.(*ast.Ident)
				if !ok || mux.Name != "mux" {
					return true
				}
				routes := values(node.Args[0])
				if len(routes) == 0 {
					t.Fatalf("unhandled route registration expression in %s", path)
				}
				for _, route := range routes {
					patterns = append(patterns, strings.ReplaceAll(route, "{$}", ""))
				}
			}
			return true
		})
	}
	return patterns
}
