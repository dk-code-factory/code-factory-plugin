package factory

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebhookRegistrationFailureRetainsPollingAndRetries(t *testing.T) {
	s, _, now := fixture(t)
	ctx := context.Background()
	must(t, os.WriteFile(filepath.Join(s.Dir, "tailscale.json"), []byte(`{"publicUrl":"https://plugin.example/code-factory"}`), 0600))
	denied, calls, posts := true, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.URL.Path, "/hooks") {
			if denied {
				w.WriteHeader(403)
				fmt.Fprint(w, `{"message":"private-provider-secret"}`)
				return
			}
			if r.Method == "GET" {
				fmt.Fprint(w, `[]`)
				return
			}
			posts++
			var input struct{ Config map[string]string }
			must(t, json.NewDecoder(r.Body).Decode(&input))
			secret, err := ReadWebhookSecret(s.Dir)
			must(t, err)
			if input.Config["secret"] != secret || input.Config["url"] != "https://plugin.example/code-factory/github/webhook" {
				t.Error("secret or prefix mismatch")
			}
			fmt.Fprintf(w, `{"id":42,"config":{"url":%q}}`, input.Config["url"])
			return
		}
		if r.URL.Path == "/user" {
			fmt.Fprint(w, `{"login":"fixture"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/issues") {
			fmt.Fprint(w, `[{"number":1,"title":"API PR status","state":"closed","head":{"sha":"polled-head"},"pull_request":{}}]`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/pulls") || r.URL.Path == "/notifications" {
			fmt.Fprint(w, `[]`)
			return
		}
		t.Error("unexpected polling endpoint", r.URL)
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	s.App.GitHub = providers.NewCachedGitHub(&providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }})
	must(t, s.ReconcileWebhooks(ctx))
	var failed webhookRecord
	must(t, s.Store.Get(ctx, "factory-webhook", "o/r", &failed))
	if failed.State != "polling" || !strings.Contains(failed.Error, "API polling") || failed.RetryAt != now.Add(15*time.Minute) || strings.Contains(failed.Error, "private-provider-secret") {
		t.Fatal(failed)
	}
	info, err := os.Stat(filepath.Join(s.Dir, "github-webhook-secret"))
	must(t, err)
	if info.Mode().Perm() != 0600 {
		t.Fatal("secret is not private")
	}
	// Setup failure does not pause registration or the actual API sync path.
	must(t, s.App.Sync(ctx))
	var repo domain.Repository
	must(t, s.Store.Get(ctx, "repository", "o/r", &repo))
	if !repo.Imported || repo.Paused || repo.Error != "" {
		t.Fatal("webhook failure blocked polling", repo)
	}
	var polled domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &polled))
	if polled.State != "closed" || polled.Title != "API PR status" || polled.Head != "polled-head" {
		t.Fatal("API polling did not refresh PR status", polled)
	}
	before := calls
	must(t, s.ReconcileWebhooks(ctx))
	if calls != before {
		t.Fatal("failure retried before deadline")
	}
	denied = false
	*now = failed.RetryAt
	// A new service recovers retained failure and retries without registration.
	restarted := New(ctx, s.App, "unused")
	defer restarted.Close()
	restarted.Now = s.Now
	must(t, restarted.ReconcileWebhooks(ctx))
	var success webhookRecord
	must(t, s.Store.Get(ctx, "factory-webhook", "o/r", &success))
	if success.State != "configured" || success.Error != "" || success.HookID != 42 || posts != 1 {
		t.Fatal(success, posts)
	}
	before = calls
	must(t, restarted.ReconcileWebhooks(ctx))
	if calls != before {
		t.Fatal("successful setup repeated")
	}
}

func TestWebhookMissingGatewayAndUnsafeSecretFallBack(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	must(t, s.ReconcileWebhooks(ctx))
	var status webhookRecord
	must(t, s.Store.Get(ctx, "factory-webhook", "o/r", &status))
	if status.State != "polling" || !strings.Contains(status.Error, "public gateway") {
		t.Fatal(status)
	}
	must(t, os.WriteFile(filepath.Join(s.Dir, "tailscale.json"), []byte(`{"publicUrl":"https://plugin.example/code-factory"}`), 0600))
	path := filepath.Join(s.Dir, "github-webhook-secret")
	must(t, os.WriteFile(path, []byte("keep-private"), 0644))
	must(t, s.ReconcileWebhooks(ctx))
	must(t, s.Store.Get(ctx, "factory-webhook", "o/r", &status))
	if status.State != "polling" || !strings.Contains(status.Error, "secret") {
		t.Fatal(status)
	}
	b, err := os.ReadFile(path)
	must(t, err)
	if string(b) != "keep-private" {
		t.Fatal("unsafe secret was overwritten")
	}
	must(t, os.Remove(path))
	outside := filepath.Join(t.TempDir(), "secret")
	must(t, os.WriteFile(outside, []byte("existing"), 0600))
	must(t, os.Symlink(outside, path))
	if _, err = ensureWebhookSecret(s.Dir); err == nil {
		t.Fatal("symlink secret accepted")
	}
}

func TestConfirmedRepositoryRegistrationAutomaticallyProvisionsWebhook(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.Repositories, settings.Rules = nil, nil
	p, err := s.Propose(ctx, &settings, nil)
	must(t, err)
	_, err = s.Confirm(ctx, p.ID, true)
	must(t, err)
	must(t, os.WriteFile(filepath.Join(s.Dir, "tailscale.json"), []byte(`{"publicUrl":"https://plugin.example/code-factory"}`), 0600))
	created := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `[]`)
			return
		}
		fmt.Fprint(w, `{"id":42,"config":{"url":"https://plugin.example/code-factory/github/webhook"}}`)
		created <- struct{}{}
	}))
	defer server.Close()
	s.App.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	done := make(chan struct{})
	go func() { defer close(done); s.Loop(ctx) }()
	waitStopped := func() {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("service loop did not stop after cancellation")
		}
	}
	defer func() { cancel(); waitStopped() }()
	settings.Repositories = []Repository{{ID: "o/r"}}
	p, err = s.Propose(ctx, &settings, nil)
	must(t, err)
	_, err = s.Confirm(ctx, p.ID, true)
	must(t, err)
	select {
	case <-created:
	case <-time.After(5 * time.Second):
		t.Fatal("confirmed registration did not automatically provision webhook")
	}
	cancel()
	waitStopped()
	if len(rpc.inputs) != 0 {
		t.Fatal("registration launched a native turn")
	}
}

func TestWebhookSetupTimeoutReleasesProviderForPolling(t *testing.T) {
	s, _, now := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	must(t, os.WriteFile(filepath.Join(s.Dir, "tailscale.json"), []byte(`{"publicUrl":"https://plugin.example/code-factory"}`), 0600))
	server, apiCalls := webhookPollingFixture(t)
	started := make(chan struct{})
	var authCalls atomic.Int32
	s.App.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(ctx context.Context) (string, error) {
		if authCalls.Add(1) == 1 {
			close(started)
			<-ctx.Done() // Model gh auth token stalling before any HTTP timeout.
			return "", ctx.Err()
		}
		return "fixture", nil
	}}
	// Retained work uses the ordinary API PR/CI polling path, sharing the exact
	// provider whose dispatch lock is held by the stalled setup authentication.
	must(t, s.Store.Put(ctx, "relationship", "watch", domain.Relationship{ID: "watch", Source: "o/r#1", Target: "o/r#1", Manual: true}, ""))
	done := make(chan error, 1)
	go func() { done <- s.reconcileWebhooks(ctx, 100*time.Millisecond) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("webhook authentication did not start")
	}
	must(t, s.App.RefreshPullRequests(ctx))
	select {
	case err := <-done:
		must(t, err)
	case <-ctx.Done():
		t.Fatal("webhook timeout did not release reconciliation")
	}
	if ctx.Err() != nil || s.Context().Err() != nil {
		t.Fatal("polling required daemon cancellation")
	}
	var polled domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &polled))
	if polled.Title != "API after stalled auth" || polled.State != "closed" || polled.Head != "polled-head" || polled.PR == nil || polled.PR.CI != "success" || polled.PR.Error != "" || len(polled.PR.Checks) != 1 {
		t.Fatal("API polling did not refresh PR and CI", polled)
	}
	var failed webhookRecord
	must(t, s.Store.Get(ctx, "factory-webhook", "o/r", &failed))
	if failed.State != "polling" || !strings.Contains(failed.Error, "API polling remains active") || failed.RetryAt != now.Add(15*time.Minute) {
		t.Fatal("expired setup lost durable fallback", failed)
	}
	beforeAuth, beforeAPI := authCalls.Load(), apiCalls.Load()
	must(t, s.ReconcileWebhooks(ctx))
	if authCalls.Load() != beforeAuth || apiCalls.Load() != beforeAPI {
		t.Fatal("expired setup retried before its retained deadline")
	}
}

func TestWebhookSetupTimeoutWhilePollingOwnsProvider(t *testing.T) {
	s, _, now := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	must(t, os.WriteFile(filepath.Join(s.Dir, "tailscale.json"), []byte(`{"publicUrl":"https://plugin.example/code-factory"}`), 0600))
	server, apiCalls := webhookPollingFixture(t)
	authStarted, releaseAuth := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseAuth) })
	var authCalls atomic.Int32
	g := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Now: func() time.Time { return *now }, Token: func(ctx context.Context) (string, error) {
		if authCalls.Add(1) == 1 {
			close(authStarted)
			select {
			case <-releaseAuth:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return "fixture-secret-token", nil
	}}
	g.ConfigureUsage(s.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 100}, nil
	})
	s.App.GitHub = g
	must(t, s.Store.Put(ctx, "relationship", "watch", domain.Relationship{ID: "watch", Source: "o/r#1", Target: "o/r#1", Manual: true}, ""))
	pollDone, pollStopped := make(chan error, 1), make(chan struct{})
	go func() { defer close(pollStopped); pollDone <- s.App.RefreshPullRequests(ctx) }()
	var setupStopped chan struct{}
	defer func() {
		release()
		cancel()
		for _, stopped := range []chan struct{}{pollStopped, setupStopped} {
			if stopped == nil {
				continue
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Error("provider fixture did not stop")
			}
		}
	}()
	select {
	case <-authStarted:
	case <-ctx.Done():
		t.Fatal("ordinary polling did not acquire provider authentication")
	}
	setupDone := make(chan error, 1)
	setupStopped = make(chan struct{})
	go func() { defer close(setupStopped); setupDone <- s.reconcileWebhooks(ctx, 100*time.Millisecond) }()
	select {
	case err := <-setupDone:
		must(t, err)
	case <-time.After(time.Second):
		t.Fatal("setup did not expire while ordinary authentication retained ownership")
	}
	// Observe persistence BEFORE releasing the unrelated authentication owner.
	var failed webhookRecord
	must(t, s.Store.Get(ctx, "factory-webhook", "o/r", &failed))
	if failed.State != "polling" || failed.At != *now || failed.RetryAt != now.Add(15*time.Minute) || !strings.Contains(failed.Error, "API polling remains active") || strings.Contains(failed.Error, "fixture-secret") || strings.Contains(failed.Error, "context deadline") {
		t.Fatal("queued setup lost sanitized durable fallback", failed)
	}
	select {
	case <-pollStopped:
		t.Fatal("setup canceled unrelated polling")
	default:
	}
	if ctx.Err() != nil || s.Context().Err() != nil || authCalls.Load() != 1 || apiCalls.Load() != 0 {
		t.Fatal("expired admission performed work or canceled its owner", authCalls.Load(), apiCalls.Load())
	}
	must(t, s.ReconcileWebhooks(ctx))
	usage, err := g.UsageStatus(ctx)
	must(t, err)
	if authCalls.Load() != 1 || apiCalls.Load() != 0 || usage.RequestsLastHour != 0 {
		t.Fatal("expired setup retried or charged an undispatched request", usage)
	}
	release()
	select {
	case err := <-pollDone:
		must(t, err)
	case <-ctx.Done():
		t.Fatal("ordinary polling did not resume")
	}
	var polled domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &polled))
	if polled.Title != "API after stalled auth" || polled.State != "closed" || polled.Head != "polled-head" || polled.PR == nil || polled.PR.CI != "success" || polled.PR.Error != "" || len(polled.PR.Checks) != 1 {
		t.Fatal("ordinary PR/CI polling lost useful results", polled)
	}
	usage, err = g.UsageStatus(ctx)
	must(t, err)
	if usage.RequestsLastHour != int(apiCalls.Load()) || apiCalls.Load() == 0 || ctx.Err() != nil || s.Context().Err() != nil {
		t.Fatal("polling lost accounting or required daemon cancellation", usage, apiCalls.Load())
	}
}

func webhookPollingFixture(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var apiCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		switch r.URL.Path {
		case "/repos/o/r/pulls/1":
			fmt.Fprint(w, `{"number":1,"title":"API after stalled auth","state":"closed","head":{"sha":"polled-head"}}`)
		case "/repos/o/r/commits/polled-head/check-runs":
			fmt.Fprint(w, `{"total_count":1,"check_runs":[{"id":1,"name":"fixture-ci","status":"completed","conclusion":"success"}]}`)
		case "/repos/o/r/commits/polled-head/statuses":
			fmt.Fprint(w, `[]`)
		case "/graphql":
			fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"headRefOid":"polled-head","reviewDecision":"APPROVED","statusCheckRollup":{"state":"SUCCESS"},"latestOpinionatedReviews":{"nodes":[],"pageInfo":{}},"closingIssuesReferences":{"nodes":[],"pageInfo":{}},"reviewThreads":{"nodes":[],"pageInfo":{}}}}}}`)
		default:
			t.Error("unexpected API endpoint", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, &apiCalls
}
