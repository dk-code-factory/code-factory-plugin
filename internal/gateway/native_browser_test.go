package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeBrowserOwnerApprovalPreservesOriginWithoutRequestLeak(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is unavailable for the native Chromium form regression")
	}
	playwright, err := filepath.Abs("../../web/node_modules/@playwright/test/index.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(playwright); err != nil {
		t.Skip("Playwright is not installed; run npm ci in web")
	}
	var private http.Handler
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This isolated test host models Tailscale Serve's authenticated identity
		// header; no real service, browser profile, or OAuth account is used.
		r.Header.Set("Tailscale-User-Login", "owner@example.com")
		private.ServeHTTP(w, r)
	}))
	defer server.Close()
	cfg := Config{PublicURL: "https://public.example/code-factory", ApprovalURL: server.URL, Owner: "owner@example.com", PublicListen: "127.0.0.1:8788", ApprovalListen: "127.0.0.1:8789", Daemon: "127.0.0.1:8787"}
	g, err := New(cfg, t.TempDir(), strings.Repeat("t", 64))
	if err != nil {
		t.Fatal(err)
	}
	g.state.Clients["native-test"] = client{ID: "native-test", Name: "Native browser fixture", Redirects: []string{"https://chatgpt.com/connector_platform_oauth_redirect"}}
	g.requests["native-request"] = grant{Client: "native-test", Redirect: "https://chatgpt.com/connector_platform_oauth_redirect", State: "native-state", Challenge: strings.Repeat("v", 43), Resource: g.resource(), Nonce: "native-nonce-must-not-enter-referer", Expires: time.Now().Add(time.Minute)}
	private = g.Private()
	script := filepath.Join(t.TempDir(), "native-approval.mjs")
	if err := os.WriteFile(script, []byte(nativeApprovalBrowserScript), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, script, playwright, server.URL)
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
		t.Skip("Chromium is not installed; run npx playwright install chromium in web")
	}
	if err != nil {
		t.Fatalf("native browser consent failed: %v\n%s", err, stderr.String())
	}
	var result struct {
		Status, DuplicateStatus, NullStatus, CrossStatus int
		Origin, Referer, CallbackOrigin, State           string
		CallbackHasCode                                  bool
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal("invalid native browser test report", err)
	}
	if result.Status != http.StatusSeeOther || result.Origin != server.URL || result.Referer != server.URL+"/" {
		t.Fatalf("native form origin/referrer or redirect incorrect: %+v", result)
	}
	if result.CallbackOrigin != "https://chatgpt.com" || result.State != "native-state" || !result.CallbackHasCode {
		t.Fatalf("native approval did not produce the bound OAuth callback: %+v", result)
	}
	if result.DuplicateStatus != http.StatusGone || result.NullStatus != http.StatusForbidden || result.CrossStatus != http.StatusForbidden {
		t.Fatalf("replay or invalid-origin approval was accepted: %+v", result)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.codes) != 1 || len(g.requests) != 0 {
		t.Fatal("native approval did not consume its request exactly once")
	}
}

const nativeApprovalBrowserScript = `import { existsSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
if (!existsSync(chromium.executablePath())) process.exit(77);
const base = process.argv[3];
const browser = await chromium.launch({headless: true, args: ['--no-sandbox']});
try {
  const context = await browser.newContext({ignoreHTTPSErrors: true});
  await context.route('**/*', async route => {
    const origin = new URL(route.request().url()).origin;
    if (origin === base) return route.continue();
    if (origin === 'https://chatgpt.com') return route.fulfill({status: 200, body: 'Fixture callback intercepted'});
    return route.abort();
  });
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  await page.goto(base + '/approve?request=native-request');
  const fields = await page.locator('form').evaluate(form => Object.fromEntries(new FormData(form)));
  const postResponse = page.waitForResponse(response => response.url() === base + '/approve' && response.request().method() === 'POST');
  await page.getByRole('button', {name: 'Connect Code Factory', exact: true}).click();
  const response = await postResponse;
  const headers = await response.request().allHeaders();
  await page.waitForURL(url => url.origin === 'https://chatgpt.com');
  const callback = new URL(page.url());
  const form = {...fields, decision: 'allow'};
  const duplicate = await context.request.post(base + '/approve', {headers: {Origin: base}, form, maxRedirects: 0});
  const nullOrigin = await context.request.post(base + '/approve', {headers: {Origin: 'null'}, form, maxRedirects: 0});
  const crossOrigin = await context.request.post(base + '/approve', {headers: {Origin: 'https://attacker.example'}, form, maxRedirects: 0});
  process.stdout.write(JSON.stringify({Status: response.status(), Origin: headers.origin, Referer: headers.referer,
    CallbackOrigin: callback.origin, State: callback.searchParams.get('state'), CallbackHasCode: !!callback.searchParams.get('code'),
    DuplicateStatus: duplicate.status(), NullStatus: nullOrigin.status(), CrossStatus: crossOrigin.status()}));
} finally { await browser.close(); }
`
