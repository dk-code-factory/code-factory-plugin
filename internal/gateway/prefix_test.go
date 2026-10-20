package gateway

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPrefixedGatewayOAuthAndProxy(t *testing.T) {
	for _, stripped := range []bool{false, true} {
		name := "complete public paths"
		if stripped {
			name = "Tailscale stripped paths"
		}
		t.Run(name, func(t *testing.T) {
			ownerToken := strings.Repeat("o", 64)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer "+ownerToken {
					t.Error("prefixed request was not normalized and authenticated for the daemon")
				}
				writeJSON(w, 200, map[string]bool{"ok": true})
			}))
			defer backend.Close()
			cfg := Config{PublicURL: "https://public.example/code-factory", ApprovalURL: "https://private.example:8444", Owner: "owner@example.com", PublicListen: "127.0.0.1:8788", ApprovalListen: "127.0.0.1:8789", Daemon: strings.TrimPrefix(backend.URL, "http://")}
			g, err := New(cfg, t.TempDir(), ownerToken)
			if err != nil {
				t.Fatal(err)
			}
			pub, private := g.Public(), g.Private()
			request := func(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				for k, v := range headers {
					r.Header.Set(k, v)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			publicPath := func(path string) string {
				if stripped {
					return path
				}
				return "/code-factory" + path
			}
			for _, path := range []string{"/.well-known/oauth-protected-resource/code-factory/mcp", "/code-factory/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource/mcp"} {
				w := request(pub, "GET", path, "", nil)
				var metadata struct {
					Resource string   `json:"resource"`
					Servers  []string `json:"authorization_servers"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &metadata); err != nil || w.Code != 200 || metadata.Resource != cfg.PublicURL+"/mcp" || len(metadata.Servers) != 1 || metadata.Servers[0] != cfg.PublicURL {
					t.Fatal("incorrect prefixed resource metadata", path, w.Code)
				}
			}
			for _, path := range []string{"/.well-known/oauth-authorization-server/code-factory", "/code-factory/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server"} {
				w := request(pub, "GET", path, "", nil)
				var metadata map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &metadata); err != nil || w.Code != 200 || metadata["issuer"] != cfg.PublicURL {
					t.Fatal("incorrect prefixed issuer metadata", path, w.Code)
				}
				for key, suffix := range map[string]string{"authorization_endpoint": "/authorize", "token_endpoint": "/token", "registration_endpoint": "/register", "revocation_endpoint": "/revoke"} {
					if metadata[key] != cfg.PublicURL+suffix {
						t.Fatal("OAuth endpoint lost public prefix", key)
					}
				}
			}
			w := request(pub, "GET", publicPath("/mcp"), "", nil)
			if w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), cfg.PublicURL+"/.well-known/oauth-protected-resource/mcp") {
				t.Fatal("prefixed protected resource omitted discovery challenge")
			}
			for _, path := range []string{"/approve", "/owner.token", "/gateway-auth.json", "/code-factory-else/mcp"} {
				if w := request(pub, "GET", publicPath(path), "", map[string]string{"Tailscale-User-Login": cfg.Owner}); w.Code != 404 {
					t.Fatal("public prefix exposed a private or unrelated path", path, w.Code)
				}
			}
			redirect := "https://chatgpt.com/connector_platform_oauth_redirect"
			w = request(pub, "POST", publicPath("/register"), `{"redirect_uris":["`+redirect+`"],"token_endpoint_auth_method":"none"}`, nil)
			var c client
			if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || w.Code != 201 || c.ID == "" {
				t.Fatal("prefixed registration failed", w.Code)
			}
			verifier := strings.Repeat("v", 64)
			sum := sha256.Sum256([]byte(verifier))
			q := url.Values{"client_id": {c.ID}, "redirect_uri": {redirect}, "response_type": {"code"}, "resource": {g.resource()}, "scope": {"code-factory"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "state": {"prefixed-state"}}
			w = request(pub, "GET", publicPath("/authorize?")+q.Encode(), "", nil)
			approval, err := url.Parse(w.Header().Get("Location"))
			if err != nil || w.Code != 302 || approval.Scheme+"://"+approval.Host != cfg.ApprovalURL || approval.Path != "/approve" {
				t.Fatal("public prefix changed private owner approval", w.Code)
			}
			if w := request(private, "GET", approval.RequestURI(), "", nil); w.Code != 403 {
				t.Fatal("anonymous approval accepted")
			}
			w = request(private, "GET", approval.RequestURI(), "", map[string]string{"Tailscale-User-Login": cfg.Owner})
			nonce := consentNonce(w.Body.String())
			if w.Code != 200 || nonce == "" || !strings.Contains(w.Body.String(), `action="/approve"`) {
				t.Fatal("private approval form was changed by public prefix")
			}
			form := url.Values{"request": {approval.Query().Get("request")}, "nonce": {nonce}, "decision": {"allow"}}
			headers := map[string]string{"Origin": cfg.ApprovalURL, "Tailscale-User-Login": cfg.Owner, "Content-Type": "application/x-www-form-urlencoded"}
			w = request(private, "POST", "/approve", form.Encode(), headers)
			callback, err := url.Parse(w.Header().Get("Location"))
			if err != nil || w.Code != 303 || callback.Query().Get("iss") != cfg.PublicURL || callback.Query().Get("state") != "prefixed-state" {
				t.Fatal("prefixed issuer or OAuth state lost in callback")
			}
			exchange := url.Values{"grant_type": {"authorization_code"}, "client_id": {c.ID}, "redirect_uri": {redirect}, "code": {callback.Query().Get("code")}, "code_verifier": {verifier}, "resource": {"https://public.example/mcp"}}
			formHeaders := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
			if w := request(pub, "POST", publicPath("/token"), exchange.Encode(), formHeaders); w.Code != 400 {
				t.Fatal("unprefixed resource accepted for a prefixed authorization")
			}
			exchange.Set("resource", g.resource())
			w = request(pub, "POST", publicPath("/token"), exchange.Encode(), formHeaders)
			// expires_in is numeric, so decode only the needed field.
			var token struct {
				Access string `json:"access_token"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil || w.Code != 200 || token.Access == "" {
				t.Fatal("prefixed PKCE token exchange failed", w.Code)
			}
			w = request(pub, "POST", publicPath("/mcp"), "{}", map[string]string{"Authorization": "Bearer " + token.Access})
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
				t.Fatal("prefixed authenticated MCP forwarding failed", w.Code)
			}
		})
	}
}

func consentNonce(page string) string {
	const start = `name="nonce" value="`
	_, tail, found := strings.Cut(page, start)
	if !found {
		return ""
	}
	value, _, _ := strings.Cut(tail, `"`)
	return value
}

func TestGatewayBaseURLValidation(t *testing.T) {
	for _, raw := range []string{"https://host.example", "https://host.example/code-factory", "https://host.example/team/code-factory_2"} {
		if _, ok := validGatewayURL(raw, true); !ok {
			t.Error("valid public base rejected", raw)
		}
	}
	for _, raw := range []string{"http://host.example/base", "https://host.example/", "https://host.example/base/", "https://host.example//base", "https://host.example/base/../other", "https://host.example/./base", "https://host.example/base%2Fother", "https://host.example/%62ase", "https://host.example/base?", "https://host.example/base?x=y", "https://host.example/base#", "https://host.example/base#fragment", "https://user:secret@host.example/base", "https://host.example:/base", "https:///base", "https://host.example/base\\other"} {
		if _, ok := validGatewayURL(raw, true); ok {
			t.Error("unsafe or noncanonical public base accepted", raw)
		}
	}
	if _, ok := validGatewayURL("https://host.example/approve", false); ok {
		t.Fatal("approval URL allowed a path")
	}
	a, _ := url.Parse("https://HOST.example/code-factory")
	b, _ := url.Parse("https://host.example:443")
	if !sameHTTPSOrigin(a, b) {
		t.Fatal("public prefix allowed equivalent public and private origins")
	}
	leadingZeroPort, _ := url.Parse("https://host.example:0443")
	if !sameHTTPSOrigin(a, leadingZeroPort) {
		t.Fatal("equivalent explicit port bypassed separate approval origin")
	}
	cfg := Config{PublicURL: a.String(), ApprovalURL: b.String(), Owner: "owner@example.com", PublicListen: "127.0.0.1:8788", ApprovalListen: "127.0.0.1:8789", Daemon: "127.0.0.1:8787"}
	if _, err := New(cfg, t.TempDir(), strings.Repeat("x", 64)); err == nil {
		t.Fatal("public and private endpoints accepted the same HTTPS origin")
	}
}
