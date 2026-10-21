package gateway

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOwnerConsentPKCERotationPersistenceAndAuthenticatedProxy(t *testing.T) {
	ownerToken := strings.Repeat("z", 64)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+ownerToken {
			t.Error("owner token not supplied privately")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Tailscale-User-Login") != "" {
			t.Error("client identity leaked upstream")
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
	defer backend.Close()
	cfg := Config{PublicURL: "https://public.example", ApprovalURL: "https://private.example:8444", Owner: "owner@example.com", PublicListen: "127.0.0.1:8788", ApprovalListen: "127.0.0.1:8789", Daemon: strings.TrimPrefix(backend.URL, "http://")}
	dir := t.TempDir()
	g, e := New(cfg, dir, ownerToken)
	if e != nil {
		t.Fatal(e)
	}
	request := func(handler http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	pub, private := g.Public(), g.Private()
	if w := request(pub, "GET", "/mcp", "", nil); w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), "oauth-protected-resource/mcp") {
		t.Fatal("missing discovery challenge", w.Code)
	}
	if w := request(pub, "GET", "/approve", "", map[string]string{"Tailscale-User-Login": cfg.Owner}); w.Code != 404 {
		t.Fatal("public consent trusted forged identity")
	}
	if w := request(pub, "GET", "/mcp", "", map[string]string{"Authorization": "Bearer " + ownerToken}); w.Code != 401 {
		t.Fatal("local owner token accepted publicly")
	}
	bad := request(pub, "POST", "/register", `{"redirect_uris":["https://attacker.example/callback"]}`, nil)
	if bad.Code != 400 {
		t.Fatal("open redirect registered")
	}
	redirect := "https://chatgpt.com/connector_platform_oauth_redirect"
	other := "https://chatgpt.com/connector/oauth/other"
	w := request(pub, "POST", "/register", `{"client_name":"Work","redirect_uris":["`+redirect+`","`+other+`"],"token_endpoint_auth_method":"none"}`, nil)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var c client
	if e = json.Unmarshal(w.Body.Bytes(), &c); e != nil {
		t.Fatal(e)
	}
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	q := url.Values{"client_id": {c.ID}, "redirect_uri": {redirect}, "response_type": {"code"}, "state": {"work-state"}, "resource": {g.resource()}, "code_challenge_method": {"S256"}, "code_challenge": {challenge}}
	w = request(pub, "GET", "/authorize?"+q.Encode(), "", nil)
	if w.Code != 302 {
		t.Fatal(w.Body.String())
	}
	approval, _ := url.Parse(w.Header().Get("Location"))
	id := approval.Query().Get("request")
	if w = request(private, "GET", approval.RequestURI(), "", nil); w.Code != 403 {
		t.Fatal("anonymous private approval")
	}
	if w = request(private, "GET", approval.RequestURI(), "", map[string]string{"Tailscale-User-Login": "other@example.com"}); w.Code != 403 {
		t.Fatal("other tailnet user accepted")
	}
	if w = request(private, "GET", approval.RequestURI(), "", map[string]string{"Tailscale-User-Login": cfg.Owner}); w.Code != 200 {
		t.Fatal("owner cannot view consent", w.Body.String())
	}
	g.mu.Lock()
	nonce := g.requests[id].Nonce
	g.mu.Unlock()
	form := url.Values{"request": {id}, "nonce": {nonce}, "decision": {"allow"}}
	headers := map[string]string{"Tailscale-User-Login": cfg.Owner, "Origin": "https://attacker.example", "Content-Type": "application/x-www-form-urlencoded"}
	if w = request(private, "POST", "/approve", form.Encode(), headers); w.Code != 403 {
		t.Fatal("cross-origin consent")
	}
	headers["Origin"] = cfg.ApprovalURL
	w = request(private, "POST", "/approve", form.Encode(), headers)
	if w.Code != 303 {
		t.Fatal(w.Body.String())
	}
	callback, _ := url.Parse(w.Header().Get("Location"))
	code := callback.Query().Get("code")
	if code == "" || callback.Query().Get("state") != "work-state" || callback.Query().Get("iss") != cfg.PublicURL {
		t.Fatal("invalid callback")
	}
	if w = request(private, "POST", "/approve", form.Encode(), headers); w.Code != 410 {
		t.Fatal("consent replay accepted")
	}
	exchange := url.Values{"grant_type": {"authorization_code"}, "client_id": {c.ID}, "code": {code}, "redirect_uri": {other}, "code_verifier": {verifier}, "resource": {g.resource()}}
	postHeaders := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if w = request(pub, "POST", "/token", exchange.Encode(), postHeaders); w.Code != 400 {
		t.Fatal("code not bound to exact redirect")
	}
	exchange.Set("redirect_uri", redirect)
	exchange.Set("code_verifier", strings.Repeat("x", 64))
	if w = request(pub, "POST", "/token", exchange.Encode(), postHeaders); w.Code != 400 {
		t.Fatal("PKCE bypass")
	}
	exchange.Set("code_verifier", verifier)
	w = request(pub, "POST", "/token", exchange.Encode(), postHeaders)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var tokens map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &tokens)
	access := tokens["access_token"].(string)
	refresh := tokens["refresh_token"].(string)
	if w = request(pub, "POST", "/token", exchange.Encode(), postHeaders); w.Code != 400 {
		t.Fatal("code replay")
	}
	b, e := os.ReadFile(g.path)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{access, refresh, code, ownerToken, verifier} {
		if strings.Contains(string(b), secret) {
			t.Fatal("plaintext authorization secret persisted")
		}
	}
	info, _ := os.Stat(g.path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("public authorization state")
	}
	restarted, e := New(cfg, dir, ownerToken)
	if e != nil {
		t.Fatal(e)
	}
	pub = restarted.Public()
	w = request(pub, "POST", "/mcp", "{}", map[string]string{"Authorization": "Bearer " + access, "Cookie": "private=secret", "Tailscale-User-Login": "forged"})
	if w.Code != 200 || strings.Contains(w.Body.String(), ownerToken) {
		t.Fatal("authenticated proxy failed or disclosed token", w.Body.String())
	}
	rotate := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {c.ID}, "resource": {g.resource()}}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Go(func() { statuses <- request(pub, "POST", "/token", rotate.Encode(), postHeaders).Code })
	}
	wg.Wait()
	close(statuses)
	successes := 0
	for status := range statuses {
		if status == 200 {
			successes++
		} else if status != 400 {
			t.Fatal("unexpected rotation result", status)
		}
	}
	if successes != 1 {
		t.Fatal("refresh token replay", successes)
	}
	w = request(pub, "POST", "/revoke", url.Values{"token": {access}}.Encode(), postHeaders)
	if w.Code != 200 {
		t.Fatal("revoke failed")
	}
	if w = request(pub, "POST", "/mcp", "{}", map[string]string{"Authorization": "Bearer " + access}); w.Code != 401 {
		t.Fatal("revoked token accepted")
	}
	g.mu.Lock()
	g.requests["expired"] = grant{Expires: time.Now().Add(-time.Second)}
	g.mu.Unlock()
	if w = request(private, "GET", "/approve?request=expired", "", map[string]string{"Tailscale-User-Login": cfg.Owner}); w.Code != 410 {
		t.Fatal("expired consent accepted")
	}
}

func TestPublicDownloadsKeepMCPAndConsentProtected(t *testing.T) {
	cfg := Config{PublicURL: "https://plugin.example.ts.net:10000", ApprovalURL: "https://plugin.example.ts.net:8444", Owner: "owner@example.com", PublicListen: "127.0.0.1:8788", ApprovalListen: "127.0.0.1:8789", Daemon: "127.0.0.1:8787"}
	g, e := New(cfg, t.TempDir(), strings.Repeat("x", 64))
	if e != nil {
		t.Fatal(e)
	}
	for path, status := range map[string]int{"/": 200, "/plugin.zip": 200, "/plugin.json": 200, "/mcp.json": 200, "/assets/logo.svg": 200, "/mcp": 401, "/approve": 404, "/owner.token": 404, "/gateway-auth.json": 404} {
		w := httptest.NewRecorder()
		g.Public().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != status {
			t.Errorf("%s: got %d want %d", path, w.Code, status)
		}
		if strings.Contains(w.Body.String(), strings.Repeat("x", 64)) {
			t.Fatal("owner token disclosed")
		}
	}
}
