// Package gateway provides a single-owner OAuth/PKCE boundary for Tailscale
// Funnel. MCP is public but authenticated; consent is only on Tailscale Serve.
package gateway

import (
	"codefactory/internal/pluginbundle"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type Config struct {
	PublicURL      string `json:"publicUrl"`
	ApprovalURL    string `json:"approvalUrl"`
	Owner          string `json:"owner"`
	PublicListen   string `json:"publicListen"`
	ApprovalListen string `json:"approvalListen"`
	Daemon         string `json:"daemon"`
}
type client struct {
	ID        string   `json:"client_id"`
	Redirects []string `json:"redirect_uris"`
	Name      string   `json:"client_name"`
	Method    string   `json:"token_endpoint_auth_method"`
}
type grant struct {
	Client, Redirect, State, Challenge, Resource, Nonce string
	Expires                                             time.Time
}
type credential struct {
	Client, Challenge, Resource, Redirect string
	Expires                               time.Time
	Used                                  bool
}
type state struct {
	Clients map[string]client
	Tokens  map[string]credential
	Refresh map[string]credential
}
type Gateway struct {
	cfg           Config
	path          string
	ownerToken    string
	mu            sync.Mutex
	state         state
	requests      map[string]grant
	codes         map[string]credential
	registration  *rate.Limiter
	authorization *rate.Limiter
	proxy         *httputil.ReverseProxy
	bundle        *pluginbundle.Bundle
	publicPrefix  string
}

var challengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
var publicPathPattern = regexp.MustCompile(`^(?:/[A-Za-z0-9_-]+)*$`)

func validGatewayURL(raw string, allowPath bool) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return nil, false
	}
	if (!allowPath && u.Path != "") || !publicPathPattern.MatchString(u.Path) {
		return nil, false
	}
	return u, true
}

func sameHTTPSOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if u.Port() == "" {
			return "443"
		}
		p := strings.TrimLeft(u.Port(), "0")
		if p == "" {
			return "0"
		}
		return p
	}
	return strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func opaque() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func New(cfg Config, dir, token string) (*Gateway, error) {
	publicURL, publicOK := validGatewayURL(cfg.PublicURL, true)
	approvalURL, approvalOK := validGatewayURL(cfg.ApprovalURL, false)
	if !publicOK || !approvalOK {
		return nil, fmt.Errorf("gateway public URL must be a canonical HTTPS base and approval URL an HTTPS origin")
	}
	for _, address := range []string{cfg.PublicListen, cfg.ApprovalListen, cfg.Daemon} {
		h, _, e := net.SplitHostPort(address)
		if e != nil || net.ParseIP(h) == nil || !net.ParseIP(h).IsLoopback() {
			return nil, fmt.Errorf("gateway listeners and daemon must use loopback IPs")
		}
	}
	if cfg.PublicListen == cfg.ApprovalListen || sameHTTPSOrigin(publicURL, approvalURL) || cfg.Owner == "" || len(token) < 32 {
		return nil, fmt.Errorf("separate public/private listeners and an owner identity are required")
	}
	g := &Gateway{cfg: cfg, publicPrefix: publicURL.Path, path: filepath.Join(dir, "gateway-auth.json"), ownerToken: token, requests: map[string]grant{}, codes: map[string]credential{}, registration: rate.NewLimiter(rate.Every(10*time.Second), 10), authorization: rate.NewLimiter(rate.Every(time.Second), 20)}
	g.state = state{Clients: map[string]client{}, Tokens: map[string]credential{}, Refresh: map[string]credential{}}
	if b, e := os.ReadFile(g.path); e == nil {
		if e = json.Unmarshal(b, &g.state); e != nil {
			return nil, fmt.Errorf("invalid gateway authorization state")
		}
		if g.state.Clients == nil || g.state.Tokens == nil || g.state.Refresh == nil {
			return nil, fmt.Errorf("incomplete gateway authorization state")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	bundle, e := pluginbundle.New(cfg.PublicURL)
	if e != nil {
		return nil, e
	}
	g.bundle = bundle
	target, _ := url.Parse("http://" + cfg.Daemon)
	g.proxy = &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.Host = target.Host
		pr.Out.Header.Set("Authorization", "Bearer "+g.ownerToken)
		pr.Out.Header.Del("Cookie")
		pr.Out.Header.Del("Tailscale-User-Login")
		pr.Out.Header.Del("Forwarded")
		pr.Out.Header.Del("X-Forwarded-For")
		pr.Out.Header.Del("X-Forwarded-Host")
		pr.Out.Header.Del("X-Forwarded-Proto")
	}, ErrorHandler: proxyError}
	return g, nil
}
func (g *Gateway) save() error {
	if e := os.MkdirAll(filepath.Dir(g.path), 0700); e != nil {
		return e
	}
	b, e := json.Marshal(g.state)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(g.path), ".auth-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), g.path)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

const gatewayCSP = "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'"

func harden(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", gatewayCSP)
		next.ServeHTTP(w, r)
	})
}
func (g *Gateway) resource() string { return g.cfg.PublicURL + "/mcp" }
func (g *Gateway) Public() http.Handler {
	mux := http.NewServeMux()
	lookupRegistration := func() (string, error) {
		var mapping struct {
			ID string `json:"id"`
		}
		b, e := os.ReadFile(filepath.Join(filepath.Dir(g.path), "registered-app.json"))
		if e != nil {
			return "", e
		}
		if e = json.Unmarshal(b, &mapping); e != nil {
			return "", e
		}
		return mapping.ID, nil
	}
	g.bundle.Register(mux, lookupRegistration)
	g.bundle.RegisterBrowser(mux, lookupRegistration)
	resourceMetadata := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"resource": g.resource(), "authorization_servers": []string{g.cfg.PublicURL}, "scopes_supported": []string{"code-factory"}})
	}
	authorizationMetadata := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"issuer": g.cfg.PublicURL, "authorization_endpoint": g.cfg.PublicURL + "/authorize", "token_endpoint": g.cfg.PublicURL + "/token", "registration_endpoint": g.cfg.PublicURL + "/register", "revocation_endpoint": g.cfg.PublicURL + "/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"code-factory"}, "authorization_response_iss_parameter_supported": true})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", resourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", resourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", authorizationMetadata)
	if g.publicPrefix != "" {
		// RFC 8414 and RFC 9728 insert the well-known component at the
		// origin root, before the issuer or protected-resource path.
		mux.HandleFunc("GET /.well-known/oauth-authorization-server"+g.publicPrefix, authorizationMetadata)
		mux.HandleFunc("GET /.well-known/oauth-protected-resource"+g.publicPrefix+"/mcp", resourceMetadata)
	}
	mux.HandleFunc("POST /register", g.register)
	mux.HandleFunc("GET /authorize", g.authorize)
	mux.HandleFunc("POST /token", g.token)
	mux.HandleFunc("POST /revoke", g.revoke)
	mux.HandleFunc("/mcp", g.mcp)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "verified", "transport": "tailscale"})
	})
	return harden(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Tailscale Serve can strip its mount prefix before proxying. Also
		// accept the complete public path for direct reverse proxies. Exact
		// local routes win if a chosen prefix overlaps a built-in route.
		_, pattern := mux.Handler(r)
		if pattern == "" && g.publicPrefix != "" && (r.URL.Path == g.publicPrefix || strings.HasPrefix(r.URL.Path, g.publicPrefix+"/")) {
			r = r.Clone(r.Context())
			r.URL.Path = strings.TrimPrefix(r.URL.Path, g.publicPrefix)
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
			r.URL.RawPath = ""
		}
		mux.ServeHTTP(w, r)
	}))
}
func validRedirect(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host != "chatgpt.com" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return false
	}
	return u.Path == "/connector_platform_oauth_redirect" || regexp.MustCompile(`^/connector/oauth/[A-Za-z0-9_-]+$`).MatchString(u.Path)
}
func (g *Gateway) register(w http.ResponseWriter, r *http.Request) {
	if !g.registration.Allow() {
		failure(w, 429, "slow_down")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var c client
	if json.NewDecoder(r.Body).Decode(&c) != nil || len(c.Redirects) == 0 || len(c.Redirects) > 5 || len(c.Name) > 200 || (c.Method != "" && c.Method != "none") {
		failure(w, 400, "invalid_client_metadata")
		return
	}
	for _, uri := range c.Redirects {
		if !validRedirect(uri) {
			failure(w, 400, "invalid_redirect_uri")
			return
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.state.Clients) >= 128 {
		failure(w, 429, "registration_capacity_reached")
		return
	}
	c.ID = opaque()
	c.Method = "none"
	g.state.Clients[c.ID] = c
	if e := g.save(); e != nil {
		delete(g.state.Clients, c.ID)
		failure(w, 500, "server_error")
		return
	}
	writeJSON(w, 201, c)
}
func (g *Gateway) prune() {
	now := time.Now()
	for k, v := range g.requests {
		if now.After(v.Expires) {
			delete(g.requests, k)
		}
	}
	for k, v := range g.codes {
		if now.After(v.Expires) {
			delete(g.codes, k)
		}
	}
	for k, v := range g.state.Tokens {
		if now.After(v.Expires) {
			delete(g.state.Tokens, k)
		}
	}
	for k, v := range g.state.Refresh {
		if now.After(v.Expires) {
			delete(g.state.Refresh, k)
		}
	}
}
func (g *Gateway) authorize(w http.ResponseWriter, r *http.Request) {
	if !g.authorization.Allow() {
		failure(w, 429, "slow_down")
		return
	}
	q := r.URL.Query()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prune()
	c, ok := g.state.Clients[q.Get("client_id")]
	matched := false
	for _, uri := range c.Redirects {
		matched = matched || uri == q.Get("redirect_uri")
	}
	if !ok || !matched || q.Get("response_type") != "code" || q.Get("resource") != g.resource() || q.Get("code_challenge_method") != "S256" || !challengePattern.MatchString(q.Get("code_challenge")) || len(q.Get("state")) > 2048 || (q.Get("scope") != "" && q.Get("scope") != "code-factory") {
		failure(w, 400, "invalid_request")
		return
	}
	if len(g.requests) >= 64 {
		failure(w, 429, "slow_down")
		return
	}
	id := opaque()
	g.requests[id] = grant{Client: c.ID, Redirect: q.Get("redirect_uri"), State: q.Get("state"), Challenge: q.Get("code_challenge"), Resource: g.resource(), Nonce: opaque(), Expires: time.Now().Add(5 * time.Minute)}
	http.Redirect(w, r, g.cfg.ApprovalURL+"/approve?request="+url.QueryEscape(id), http.StatusFound)
}

var consent = template.Must(template.New("consent").Parse(`<!doctype html><html><meta charset="utf-8"><title>Connect Code Factory</title><style>body{font:17px system-ui;max-width:560px;margin:60px auto;padding:24px}button{padding:12px;margin:10px}</style><h1>Connect Code Factory</h1><p>Signed in through your private Tailscale connection.</p><p>Allow {{.Name}} ({{.Redirect}}) to access your repository inbox and control local Codex runs as this computer's owner?</p><form method="post" action="/approve"><input type="hidden" name="request" value="{{.ID}}"><input type="hidden" name="nonce" value="{{.Nonce}}"><button name="decision" value="allow">Connect Code Factory</button><button name="decision" value="deny">Cancel</button></form></html>`))

func (g *Gateway) Private() http.Handler {
	return harden(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/approve" {
			http.NotFound(w, r)
			return
		}
		// Only the Serve listener trusts this header. Public/Funnel never does.
		if r.Header.Get("Tailscale-User-Login") != g.cfg.Owner {
			http.Error(w, "This connection is limited to the configured Tailscale owner", 403)
			return
		}
		id := r.URL.Query().Get("request")
		if r.Method == "POST" {
			if r.Header.Get("Origin") != g.cfg.ApprovalURL {
				http.Error(w, "Invalid approval origin", 403)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if r.ParseForm() != nil {
				http.Error(w, "Invalid approval", 400)
				return
			}
			id = r.PostForm.Get("request")
		} else if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		g.prune()
		grant, ok := g.requests[id]
		if !ok {
			http.Error(w, "Connection request expired; reconnect in Work", 410)
			return
		}
		if r.Method == "GET" {
			// Native browser form POSTs can send Origin: null under no-referrer.
			// Keep only the origin so strict CSRF validation still works without
			// disclosing the approval request or nonce in Referer headers.
			w.Header().Set("Referrer-Policy", "strict-origin")
			// Chromium also applies form-action to the OAuth redirect. The
			// registered and allowlisted callback is the only external target.
			w.Header().Set("Content-Security-Policy", strings.Replace(gatewayCSP, "form-action 'self';", "form-action 'self' "+grant.Redirect+";", 1))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = consent.Execute(w, map[string]string{"ID": id, "Nonce": grant.Nonce, "Name": g.state.Clients[grant.Client].Name, "Redirect": grant.Redirect})
			return
		}
		if subtle.ConstantTimeCompare([]byte(grant.Nonce), []byte(r.PostForm.Get("nonce"))) != 1 {
			http.Error(w, "Invalid approval session", 403)
			return
		}
		decision := r.PostForm.Get("decision")
		if decision != "allow" && decision != "deny" {
			http.Error(w, "Invalid decision", 400)
			return
		}
		delete(g.requests, id)
		u, _ := url.Parse(grant.Redirect)
		q := u.Query()
		q.Set("state", grant.State)
		q.Set("iss", g.cfg.PublicURL)
		if decision == "allow" {
			code := opaque()
			g.codes[hash(code)] = credential{Client: grant.Client, Redirect: grant.Redirect, Challenge: grant.Challenge, Resource: grant.Resource, Expires: time.Now().Add(time.Minute)}
			q.Set("code", code)
		} else {
			q.Set("error", "access_denied")
		}
		u.RawQuery = q.Encode()
		http.Redirect(w, r, u.String(), http.StatusSeeOther)
	}))
}
func (g *Gateway) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if r.ParseForm() != nil {
		failure(w, 400, "invalid_request")
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prune()
	var cred credential
	var key string
	refresh := r.PostForm.Get("grant_type") == "refresh_token"
	if refresh {
		key = hash(r.PostForm.Get("refresh_token"))
		cred = g.state.Refresh[key]
	} else if r.PostForm.Get("grant_type") == "authorization_code" {
		key = hash(r.PostForm.Get("code"))
		cred = g.codes[key]
	} else {
		failure(w, 400, "unsupported_grant_type")
		return
	}
	if cred.Client == "" || cred.Used || cred.Client != r.PostForm.Get("client_id") || time.Now().After(cred.Expires) || r.PostForm.Get("resource") != cred.Resource {
		failure(w, 400, "invalid_grant")
		return
	}
	if !refresh {
		verifier := r.PostForm.Get("code_verifier")
		sum := sha256.Sum256([]byte(verifier))
		challenge := base64.RawURLEncoding.EncodeToString(sum[:])
		redirectOK := cred.Redirect == r.PostForm.Get("redirect_uri")
		// Codes are tied to the exact redirect approved for this authorization.
		if !verifierPattern.MatchString(verifier) || subtle.ConstantTimeCompare([]byte(challenge), []byte(cred.Challenge)) != 1 || !redirectOK {
			failure(w, 400, "invalid_grant")
			return
		}
	}
	if len(g.state.Tokens) >= 256 || len(g.state.Refresh) >= 256 {
		failure(w, 429, "token_capacity_reached")
		return
	}
	access, renewal := opaque(), opaque()
	g.state.Tokens[hash(access)] = credential{Client: cred.Client, Resource: cred.Resource, Expires: time.Now().Add(time.Hour)}
	g.state.Refresh[hash(renewal)] = credential{Client: cred.Client, Resource: cred.Resource, Expires: time.Now().Add(30 * 24 * time.Hour)}
	if refresh {
		delete(g.state.Refresh, key)
	} else {
		delete(g.codes, key)
	}
	if e := g.save(); e != nil {
		delete(g.state.Tokens, hash(access))
		delete(g.state.Refresh, hash(renewal))
		failure(w, 500, "server_error")
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": access, "refresh_token": renewal, "token_type": "Bearer", "expires_in": 3600, "scope": "code-factory"})
}
func (g *Gateway) revoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil {
		failure(w, 400, "invalid_request")
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	key := hash(r.PostForm.Get("token"))
	delete(g.state.Tokens, key)
	delete(g.state.Refresh, key)
	if g.save() != nil {
		failure(w, 500, "server_error")
		return
	}
	w.WriteHeader(200)
}
func (g *Gateway) mcp(w http.ResponseWriter, r *http.Request) {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	g.mu.Lock()
	cred, ok := g.state.Tokens[hash(got)]
	authorized := ok && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && cred.Resource == g.resource() && time.Now().Before(cred.Expires)
	g.mu.Unlock()
	if !authorized {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+g.cfg.PublicURL+`/.well-known/oauth-protected-resource/mcp", scope="code-factory"`)
		failure(w, 401, "invalid_token")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	g.proxy.ServeHTTP(w, r)
}
func (g *Gateway) Serve(ctx context.Context) error {
	// Bind both before exposing either handler, so partial startup fails closed.
	public, e := net.Listen("tcp", g.cfg.PublicListen)
	if e != nil {
		return e
	}
	defer public.Close()
	private, e := net.Listen("tcp", g.cfg.ApprovalListen)
	if e != nil {
		return e
	}
	defer private.Close()
	p := &http.Server{Handler: audit("public", g.Public()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	a := &http.Server{Handler: audit("approval", g.Private()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10}
	errs := make(chan error, 2)
	go func() { errs <- p.Serve(public) }()
	go func() { errs <- a.Serve(private) }()
	select {
	case <-ctx.Done():
	case e = <-errs:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.Shutdown(shutdown)
	_ = a.Shutdown(shutdown)
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
