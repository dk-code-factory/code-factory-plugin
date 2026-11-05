package factory

import (
	byteslib "bytes"
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSignedWebhookCommitsInboxNotificationWithoutProviderRead(t *testing.T) {
	for _, matching := range []bool{false, true} {
		t.Run(fmt.Sprint(matching), func(t *testing.T) {
			s, rpc, _ := fixture(t)
			ctx := context.Background()
			c, e := s.Settings(ctx)
			must(t, e)
			if !matching {
				c.Rules = nil
			}
			must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
			vault, e := store.NewVault(s.Store, s.App.DataDir)
			must(t, e)
			notifications := 0
			ev := events.New(s.Store, vault)
			ev.Client = &http.Client{Transport: eventTransport(func(r *http.Request) (*http.Response, error) {
				body, e := io.ReadAll(r.Body)
				if e != nil {
					return nil, e
				}
				var p map[string]any
				if e = json.Unmarshal(body, &p); e != nil {
					return nil, e
				}
				reply := "{}"
				if p["type"] == "verification" {
					reply = fmt.Sprintf(`{"challenge":%q}`, p["challenge"])
				} else {
					notifications++
					if p["name"] != "inbox.changed" || strings.Contains(string(body), "private-webhook-body") {
						t.Error("invalid inbox notification", string(body))
					}
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(reply))}, nil
			})}
			_, e = ev.Subscribe(ctx, events.SubscribeInput{Name: "inbox.changed", Delivery: events.Delivery{Mode: "webhook", URL: "https://callback.example/events", Secret: "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))}})
			must(t, e)
			body := bytes(map[string]any{"repository": map[string]string{"full_name": "o/r"}, "action": "synchronize", "pull_request": map[string]any{"number": 1, "title": "new source", "body": "private-webhook-body", "state": "open", "head": map[string]string{"sha": "new-head"}}})
			handler := s.Webhook(func(context.Context) (string, error) { return "fixture-secret", nil })
			send := func(id string) {
				t.Helper()
				req := httptest.NewRequest("POST", "/github", bytespkg(body))
				req.Header.Set("X-GitHub-Delivery", id)
				req.Header.Set("X-GitHub-Event", "pull_request")
				mac := hmac.New(sha256.New, []byte("fixture-secret"))
				_, _ = mac.Write(body)
				req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != 202 {
					t.Fatal("signed webhook rejected", w.Code, w.Body.String())
				}
			}
			send("first")
			must(t, s.Poll(ctx))
			must(t, ev.Tick(ctx))
			if notifications != 1 {
				t.Fatal("webhook did not emit inbox subscription", notifications)
			}
			var saved domain.Item
			must(t, s.Store.Get(ctx, "item", "o/r#1", &saved))
			if saved.Head != "new-head" || saved.Body != "private-webhook-body" {
				t.Fatal("notification preceded source commit", saved)
			}
			send("first")
			send("same-source-new-delivery")
			must(t, s.Poll(ctx))
			must(t, ev.Tick(ctx))
			if notifications != 1 {
				t.Fatal("duplicate/no-change delivery emitted extra inbox notifications", notifications)
			}
			sessions, e := s.Sessions(ctx)
			must(t, e)
			if (!matching && len(sessions) != 0) || len(rpc.inputs) != 0 {
				t.Fatal("notification read launched work", sessions)
			}
		})
	}
}
func bytespkg(b []byte) io.Reader { return byteslib.NewReader(b) }

type pollSnapshots struct {
	providers.GitHubSource
	items map[string]domain.Item
}

func (g *pollSnapshots) ItemDetail(_ context.Context, i domain.Item) (domain.Item, error) {
	v, ok := g.items[i.ID]
	if !ok {
		return v, fmt.Errorf("missing fixture %s", i.ID)
	}
	return v, nil
}

func TestPollingDiscoversPeerChangesWhileNativeAttachmentIsHeld(t *testing.T) {
	for _, mode := range []string{"unchanged-issue", "changed-pr", "detail-refresh", "initial-baseline", "busy-webhook"} {
		t.Run(mode, func(t *testing.T) {
			s, rpc, clock := fixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, e := s.Settings(ctx)
			must(t, e)
			c.Repositories = append(c.Repositories, Repository{ID: "z/r"})
			c.Rules = []Rule{{ID: "all", Repositories: []string{"*"}, Types: []string{"pull_request"}, Instructions: "handle current change"}}
			must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
			must(t, s.projectSettings(ctx))
			for _, repo := range []string{"o/r", "z/r"} {
				must(t, s.Store.Put(ctx, "repository", repo, domain.Repository{ID: repo, Imported: true}, ""))
				must(t, s.Store.Put(ctx, "factory-baseline", repo, map[string]bool{"ready": true}, ""))
			}
			snapshots := map[string]domain.Item{}
			for _, id := range []string{"o/r#1", "o/r#2", "z/r#1"} {
				i := domain.Item{ID: id, Repository: strings.Split(id, "#")[0], Number: 1, Kind: "pr", State: "open", Head: "old", DetailSyncedAt: clock.Add(-time.Hour)}
				if id == "o/r#2" {
					i.Number = 2
				}
				if id == "o/r#1" && mode == "unchanged-issue" {
					i.Kind = "issue"
				}
				i.Revision = i.ContentRevision()
				must(t, s.Store.Put(ctx, "item", id, i, ""))
				must(t, s.Store.Put(ctx, "user", id, domain.UserState{ID: id}, ""))
				must(t, s.Observe(ctx, observations(i), true))
				must(t, s.Store.Put(ctx, "factory-item-observed", id, map[string]string{"revision": i.Revision}, ""))
				snapshots[id] = i
			}
			if mode == "initial-baseline" {
				must(t, s.Store.Put(ctx, "factory-baseline", "o/r", map[string]any{"ready": false, "items": []string{"o/r#1"}, "cursor": 0}, ""))
				s.App.GitHub = &pollSnapshots{items: snapshots}
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			s.Workspace = func(ctx context.Context, v Session) (string, error) {
				if v.ID == "o/r#1" {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
				return s.Dir, nil
			}
			finished := make(chan error, 1)
			go func() { _, e := s.Chat(ctx, "o/r#1", "slow attachment", nil); finished <- e }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("attachment did not enter")
			}
			for _, id := range []string{"o/r#2", "z/r#1"} {
				i := snapshots[id]
				i.Head = "new-" + id
				i.Revision = i.ContentRevision()
				snapshots[id] = i
				if mode != "detail-refresh" {
					must(t, s.Store.Put(ctx, "item", id, i, ""))
				}
			}
			if mode == "detail-refresh" {
				s.App.GitHub = &pollSnapshots{items: snapshots}
			}
			if mode == "changed-pr" {
				i := snapshots["o/r#1"]
				i.Head = "changed-busy"
				i.Revision = i.ContentRevision()
				must(t, s.Store.Put(ctx, "item", i.ID, i, ""))
			}
			if mode == "busy-webhook" {
				d := delivery{ID: "busy-native", Type: "pull_request", State: "pending", At: *clock, Body: bytes(map[string]any{"repository": map[string]string{"full_name": "o/r"}, "action": "synchronize", "pull_request": map[string]any{"number": 1, "state": "open", "head": map[string]string{"sha": "changed-busy"}}})}
				must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
			}
			pollCtx, pollCancel := context.WithTimeout(ctx, time.Second)
			defer pollCancel()
			polled := make(chan error, 1)
			go func() { polled <- s.Poll(pollCtx) }()
			select {
			case e := <-polled:
				must(t, e)
			case <-pollCtx.Done():
				t.Fatal("busy conversation blocked independent polling ingestion", mode)
			}
			// These events were first discovered by Poll, not pre-observed by the test.
			for _, id := range []string{"o/r#2", "z/r#1"} {
				v, e := s.Session(ctx, id)
				must(t, e)
				found := 0
				for _, p := range v.Pending {
					if p.Event.Facet == "head" && p.Event.Context.Head == "new-"+id {
						found++
					}
				}
				if found != 1 || !v.Due.Equal(clock.Add(120*time.Second)) {
					t.Fatal("peer discovery/quiet clock delayed", mode, id, v)
				}
			}
			if mode == "busy-webhook" {
				var d delivery
				must(t, s.Store.Get(ctx, "factory-delivery", "busy-native", &d))
				if d.State != "pending" || d.Error != "" || !d.RetryAt.IsZero() {
					t.Fatal("busy webhook lost/retry-backed-off", d)
				}
			}
			stopped, stop := context.WithCancel(ctx)
			stop()
			if e := s.Poll(stopped); !errors.Is(e, context.Canceled) {
				t.Fatal("cancelled poll did not stop", e)
			}
			unblock()
			select {
			case e := <-finished:
				must(t, e)
			case <-ctx.Done():
				t.Fatal("attachment did not finish")
			}
			must(t, s.Poll(ctx))
			must(t, s.Poll(ctx))
			if mode == "changed-pr" || mode == "busy-webhook" {
				v := get(t, s)
				heads := 0
				for _, p := range v.Pending {
					if p.Event.Facet == "head" {
						heads++
					}
				}
				if heads != 1 {
					t.Fatal("deferred busy change lost or duplicated", mode, v.Pending)
				}
			}
			if mode == "initial-baseline" {
				v := get(t, s)
				if len(v.Pending) != 0 {
					t.Fatal("deferred initial baseline launched historical work", v.Pending)
				}
			}
			if len(rpc.inputs) != 1 {
				t.Fatal("polling launched independent agents", len(rpc.inputs))
			}
		})
	}
}

func localGit(t *testing.T, ctx context.Context, dir string, args ...string) string {
	t.Helper()
	a := append([]string{"git"}, args...)
	out, e := run(ctx, dir, a...)
	must(t, e)
	return out
}
func localCheckout(t *testing.T, ctx context.Context) string {
	t.Helper()
	dir := t.TempDir()
	localGit(t, ctx, dir, "init", "-b", "main")
	localGit(t, ctx, dir, "config", "user.name", "Fixture")
	localGit(t, ctx, dir, "config", "user.email", "fixture@example.invalid")
	must(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("base\n"), 0600))
	localGit(t, ctx, dir, "add", "tracked.txt")
	localGit(t, ctx, dir, "commit", "-m", "fixture")
	return dir
}

func TestInterruptedRealWorktreeCreationResumesRegisteredDirtyWorkspace(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	checkout := localCheckout(t, ctx)
	origin := t.TempDir()
	localGit(t, ctx, origin, "init", "--bare")
	localGit(t, ctx, checkout, "remote", "add", "origin", origin)
	localGit(t, ctx, checkout, "push", "origin", "HEAD:refs/pull/1/head")
	c, e := s.Settings(ctx)
	must(t, e)
	c.Repositories[0].Checkout = checkout
	must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
	planned := filepath.Join(s.Dir, "worktrees", digest("o/r#1")[:24])
	realGit, e := exec.LookPath("git")
	must(t, e)
	testBinary, e := os.Executable()
	must(t, e)
	var before, after atomic.Bool
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var intent Session
		if e := s.Store.Get(ctx, "factory-session", "o/r#1", &intent); e != nil || intent.Worktree != planned || intent.WorktreeReady || intent.WorktreeGitDir == "" {
			t.Error("missing durable intent at Git boundary", r.URL.Path, intent, e)
			http.Error(w, "intent unavailable", 500)
			return
		}
		_, e := os.Stat(filepath.Join(planned, ".git"))
		if r.URL.Path == "/before" {
			if !os.IsNotExist(e) {
				t.Error("before probe followed worktree creation", e)
				http.Error(w, "late probe", 500)
				return
			}
			before.Store(true)
		} else if r.URL.Path == "/after" {
			if e != nil {
				t.Error("after probe preceded worktree creation", e)
				http.Error(w, "early probe", 500)
				return
			}
			after.Store(true)
		} else {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(204)
	}))
	defer probe.Close()
	bin := t.TempDir()
	wrapper := `#!/bin/sh
if [ "$1" = worktree ] && [ "$2" = add ]; then
 exec "$CODE_FACTORY_FIXTURE_TEST_BINARY" -test.run '^TestWorkspaceGitProbeProcess$' -- "$@"
fi
exec "$CODE_FACTORY_FIXTURE_REAL_GIT" "$@"
`
	must(t, os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0700))
	t.Setenv("CODE_FACTORY_FIXTURE_GIT_PROBE", "1")
	t.Setenv("CODE_FACTORY_FIXTURE_TEST_BINARY", testBinary)
	t.Setenv("CODE_FACTORY_FIXTURE_REAL_GIT", realGit)
	t.Setenv("CODE_FACTORY_FIXTURE_GIT_URL", probe.URL)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s.Workspace = s.workspace
	if _, e = s.Chat(ctx, "o/r#1", "first", nil); e == nil {
		t.Fatal("interruption not stimulated")
	}
	if !before.Load() || !after.Load() {
		t.Fatal("actual before/after Git side-effect probes did not finish")
	}
	v := get(t, s)
	if v.Worktree != planned || v.WorktreeReady || len(rpc.inputs) != 0 {
		t.Fatal("interrupted workspace lost its plan", v)
	}
	must(t, os.WriteFile(filepath.Join(planned, "tracked.txt"), []byte("retained dirty work\n"), 0600))
	localGit(t, ctx, checkout, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "unavailable"))
	c.Repositories[0].Checkout = localCheckout(t, ctx)
	must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
	s.Close()
	resumed := New(ctx, s.App, "unused")
	resumed.Now = s.Now
	resumed.Start = func(context.Context, Agent, func(codex.Message)) (RPC, error) { return rpc, nil }
	t.Cleanup(resumed.Close)
	must(t, resumed.Init(ctx))
	v, e = resumed.Chat(ctx, "o/r#1", "resume retained work", nil)
	must(t, e)
	if v.Worktree != planned || !v.WorktreeReady || len(rpc.inputs) != 1 {
		t.Fatal("registered workspace not reused", v)
	}
	content, e := os.ReadFile(filepath.Join(planned, "tracked.txt"))
	must(t, e)
	if string(content) != "retained dirty work\n" {
		t.Fatal("recovery reset retained work")
	}
	if strings.Count(localGit(t, ctx, checkout, "worktree", "list", "--porcelain"), "worktree "+planned) != 1 {
		t.Fatal("duplicate registered worktree")
	}
	_, e = resumed.Control(ctx, v.ID, "interrupt")
	must(t, e)
	if _, e = resumed.Control(ctx, v.ID, "cleanup"); e == nil {
		t.Fatal("dirty recovered worktree cleanup accepted")
	}
	must(t, os.WriteFile(filepath.Join(planned, "tracked.txt"), []byte("base\n"), 0600))
	_, e = resumed.Control(ctx, v.ID, "cleanup")
	must(t, e)
	v, e = resumed.Session(ctx, v.ID)
	must(t, e)
	if v.Execution != "cleaned" || v.ThreadID == "" || v.WorktreeReady {
		t.Fatal("explicit cleanup did not retain retired conversation", v)
	}
}

func TestWorkspaceRecoveryRefusesForeignPathsAndAllowsMissingIntentCleanup(t *testing.T) {
	for _, mode := range []string{"foreign-directory", "registered-wrong-branch", "foreign-repository", "branch-only-intent"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _ := fixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			checkout := localCheckout(t, ctx)
			c, e := s.Settings(ctx)
			must(t, e)
			c.Repositories[0].Checkout = checkout
			must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
			var item domain.Item
			must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
			item.Kind = "issue"
			must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
			v := newSession(item.ID, "item", item.ID, item.Repository, Agent{}, s.Now())
			p := filepath.Join(s.Dir, "worktrees", digest(v.ID)[:24])
			must(t, os.MkdirAll(filepath.Dir(p), 0700))
			if mode == "foreign-directory" {
				must(t, os.MkdirAll(p, 0700))
				must(t, os.WriteFile(filepath.Join(p, "unrelated"), []byte("preserve"), 0600))
			} else if mode == "registered-wrong-branch" {
				localGit(t, ctx, checkout, "worktree", "add", "-b", "unrelated", p, "HEAD")
			} else if mode == "foreign-repository" {
				foreign := localCheckout(t, ctx)
				localGit(t, ctx, foreign, "worktree", "add", "-b", "code-factory/"+digest(v.ID)[:24], p, "HEAD")
			} else {
				localGit(t, ctx, checkout, "branch", "code-factory/"+digest(v.ID)[:24])
			}
			v.Worktree = p
			v.WorktreeGitDir = localGit(t, ctx, checkout, "rev-parse", "--path-format=absolute", "--git-common-dir")
			must(t, s.save(ctx, v))
			if _, e = s.workspace(ctx, v); e == nil {
				t.Fatal("unverified workspace/branch adopted", mode)
			}
			if mode != "branch-only-intent" {
				if _, e = s.Control(ctx, v.ID, "cleanup"); e == nil {
					t.Fatal("explicit cleanup removed unverified intent path", mode)
				}
				if _, e = os.Stat(p); e != nil {
					t.Fatal("foreign intent path lost", mode, e)
				}
			}
			if mode == "foreign-directory" {
				b, e := os.ReadFile(filepath.Join(p, "unrelated"))
				must(t, e)
				if string(b) != "preserve" {
					t.Fatal("foreign path changed")
				}
			} else if mode == "registered-wrong-branch" {
				if localGit(t, ctx, p, "branch", "--show-current") != "unrelated" {
					t.Fatal("foreign branch changed")
				}
			} else if mode == "branch-only-intent" {
				_, e = s.Control(ctx, v.ID, "cleanup")
				must(t, e)
				v = get(t, s)
				if v.Execution != "cleaned" {
					t.Fatal("missing planned workspace cannot be explicitly retired")
				}
				localGit(t, ctx, checkout, "show-ref", "--verify", "refs/heads/code-factory/"+digest(v.ID)[:24])
			}
		})
	}
}

// This subprocess is the fixture's git worktree-add executable, never a model.
// It checks the durable intent before the real Git side effect, then fails after
// successful creation to reproduce the exact interruption window.
func TestWorkspaceGitProbeProcess(t *testing.T) {
	if os.Getenv("CODE_FACTORY_FIXTURE_GIT_PROBE") != "1" {
		return
	}
	args := []string{}
	for i, a := range os.Args {
		if a == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	probe := func(stage string) {
		client := &http.Client{Timeout: 2 * time.Second}
		r, e := client.Get(os.Getenv("CODE_FACTORY_FIXTURE_GIT_URL") + "/" + stage)
		if e != nil {
			os.Exit(77)
		}
		_ = r.Body.Close()
		if r.StatusCode != 204 {
			os.Exit(78)
		}
	}
	probe("before")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CODE_FACTORY_FIXTURE_REAL_GIT"), args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e := cmd.Run(); e != nil {
		os.Exit(79)
	}
	probe("after")
	os.Exit(23)
}
