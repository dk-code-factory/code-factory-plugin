package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewStartUsesActiveGitHubPrincipal(t *testing.T) {
	for _, failure := range []string{"self", "unavailable", "empty", "scope-change"} {
		t.Run(failure, func(t *testing.T) {
			a, pr := reviewFixture(t)
			ctx := a.Runs.ctx
			run, err := a.Runs.PreparePRReview(ctx, pr.ID)
			if err != nil {
				t.Fatal(err)
			}
			var principal atomic.Value
			principal.Store("me")
			var fail atomic.Bool
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/repos/owner/repo/pulls/2" {
					fmt.Fprintf(w, `{"number":2,"title":"PR","state":"open","user":{"login":%q},"head":{"sha":%q}}`, pr.Author, pr.Head)
					return
				}
				if r.URL.Path != "/user" {
					t.Error("unexpected execution identity request", r.URL.Path)
				}
				calls.Add(1)
				if fail.Load() {
					switch failure {
					case "unavailable":
						w.WriteHeader(503)
						return
					case "empty":
						fmt.Fprint(w, `{"login":""}`)
						return
					case "scope-change":
						principal.Store("changed-during-read")
					}
				}
				login := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				_ = json.NewEncoder(w).Encode(map[string]string{"login": login})
			}))
			defer server.Close()
			live := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return principal.Load().(string), nil }}
			live.ConfigureUsage(nil, func(context.Context) (providers.GitHubPolicy, error) {
				return providers.GitHubPolicy{RequestsPerHour: 100}, nil
			})
			a.GitHub = providers.NewCachedGitHub(live)
			if who, err := a.GitHub.User(ctx); err != nil || who != "me" {
				t.Fatal("could not warm identity cache", who, err)
			}
			fail.Store(true)
			if failure == "self" {
				principal.Store(pr.Author)
			}
			if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal("saved eligible proposal was not queued", err)
			}
			waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", run.ID, &run); return run.Status == "failed" })
			if calls.Load() != 2 || run.ThreadID != "" || run.ResumePending {
				t.Fatal("execution reused cached identity or launched rejected work", calls.Load(), run.Status)
			}
			if _, err := os.Stat(run.Worktree); !os.IsNotExist(err) {
				t.Fatal("rejected active identity created a workspace", err)
			}
		})
	}
}

func TestReviewExecutionRechecksPrincipalAfterStart(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	var principal atomic.Value
	principal.Store("me")
	var users atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			users.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"login": principal.Load().(string)})
		case "/repos/owner/repo/pulls/2":
			// The authenticated account changes after the Start checkpoint but
			// before execution receives its fresh source observation.
			principal.Store(pr.Author)
			fmt.Fprintf(w, `{"number":2,"title":"PR","state":"open","user":{"login":%q},"head":{"sha":%q}}`, pr.Author, pr.Head)
		default:
			t.Error("unexpected preflight request", r.URL.Path)
		}
	}))
	defer server.Close()
	live := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return principal.Load().(string), nil }}
	live.ConfigureUsage(nil, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 100}, nil
	})
	a.GitHub = providers.NewCachedGitHub(live)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal("initial nonowner principal was rejected", err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", run.ID, &run); return run.Status == "failed" })
	if users.Load() != 0 || run.ThreadID != "" || run.ResumePending || !strings.Contains(run.Error, "GitHub identity changed") {
		t.Fatal("execution trusted a source from the changed principal", users.Load(), run.Status, run.Error)
	}
	if _, err := os.Stat(run.Worktree); !os.IsNotExist(err) {
		t.Fatal("changed execution principal created workspace", err)
	}
}

func TestReviewExecutionReadsFreshPrincipalWithoutSavedLogin(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	var users atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			login := "me"
			if users.Add(1) > 1 {
				login = pr.Author
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"login": login})
		case "/repos/owner/repo/pulls/2":
			// Start has queued the saved proposal. Remove the synchronized
			// identity before background execution validates the live principal.
			if err := a.Store.Write(r.Context(), func(tx *store.Tx) error { return tx.Delete("identity", "github") }); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			fmt.Fprintf(w, `{"number":2,"title":"PR","state":"open","user":{"login":%q},"head":{"sha":%q}}`, pr.Author, pr.Head)
		default:
			t.Error("unexpected preflight request", r.URL.Path)
		}
	}))
	defer server.Close()
	live := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	live.ConfigureUsage(nil, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 100}, nil
	})
	a.GitHub = providers.NewCachedGitHub(live)
	if who, err := a.GitHub.User(ctx); err != nil || who != "me" {
		t.Fatal("could not warm the previous principal", who, err)
	}
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", run.ID, &run); return run.Status == "failed" })
	if users.Load() != 2 || !strings.Contains(run.Error, "cannot review your own pull request") || run.ThreadID != "" {
		t.Fatal("execution used cached or saved principal", users.Load(), run.Error)
	}
	var saved domain.GitHubViewer
	if err := a.Store.Get(ctx, "identity", "github", &saved); err != store.ErrNotFound {
		t.Fatal("probe retained a saved login", err)
	}
	if _, err := os.Stat(run.Worktree); !os.IsNotExist(err) {
		t.Fatal("self-review execution created a workspace", err)
	}
}

func TestReviewStartReturnsWhileActiveIdentityReadIsBlocked(t *testing.T) {
	a, pr := reviewFixture(t)
	t.Setenv("CF_FAKE_MODE", "hold")
	ctx := a.Runs.ctx
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `{"login":"me"}`)
			return
		}
		fmt.Fprintf(w, `{"number":2,"title":"PR","state":"open","user":{"login":%q},"head":{"sha":%q}}`, pr.Author, pr.Head)
	}))
	defer server.Close()
	defer unblock()
	live := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	live.ConfigureUsage(nil, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 100}, nil
	})
	a.GitHub = providers.NewCachedGitHub(live)
	accepted := make(chan error, 1)
	go func() { _, err := a.Runs.Start(ctx, run.ID, false); accepted <- err }()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Start waited for active identity network validation")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("queued review did not reach identity preflight")
	}
	if _, err := os.Stat(run.Worktree); !os.IsNotExist(err) {
		t.Fatal("blocked identity created a workspace", err)
	}
	unblock()
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &run)
		return run.Status == "running" && run.ThreadID != ""
	})
}

func TestNonReviewPreparationAndStartDoNotReadPrincipal(t *testing.T) {
	a, pr := reviewFixture(t)
	t.Setenv("CF_FAKE_MODE", "hold")
	ctx := a.Runs.ctx
	pr.Author = "me"
	if err := a.Store.Put(ctx, "item", pr.ID, pr, ""); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.URL.Path != "/repos/owner/repo/pulls/2" {
			t.Error("nonreview work requested active principal", r.URL.Path)
		}
		fmt.Fprintf(w, `{"number":2,"title":"PR","state":"open","user":{"login":"me"},"head":{"sha":%q}}`, pr.Head)
	}))
	defer server.Close()
	live := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	live.ConfigureUsage(nil, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 100}, nil
	})
	a.GitHub = providers.NewCachedGitHub(live)
	run, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: pr.ID, ProfileID: "investigate"})
	if err != nil || reads.Load() != 0 {
		t.Fatal("preparation made a provider request", err, reads.Load())
	}
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &run)
		return run.Status == "running" && run.ThreadID != ""
	})
	if reads.Load() != 1 {
		t.Fatal("nonreview Start read principal rather than source only", reads.Load())
	}
}

func TestNonReviewRunRetainsIntentAfterReviewDefaultChanges(t *testing.T) {
	for _, scope := range []string{"system", "repository"} {
		t.Run(scope, func(t *testing.T) {
			a, pr := reviewFixture(t)
			t.Setenv("CF_FAKE_MODE", "hold")
			ctx := a.Runs.ctx
			pr.Author = "me"
			if err := a.Store.Put(ctx, "item", pr.ID, pr, ""); err != nil {
				t.Fatal(err)
			}
			run, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: pr.ID, ProfileID: "investigate"})
			if err != nil {
				t.Fatal(err)
			}
			original := run
			if scope == "system" {
				config, _ := a.Config(ctx)
				config.ReviewProfileID = run.Profile.ID
				err = a.SaveConfig(ctx, config)
			} else {
				var repo domain.Repository
				_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
				repo.ReviewProfileID = run.Profile.ID
				err = a.SaveRepository(ctx, repo)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Store.Get(ctx, "run", run.ID, &run); err != nil {
				t.Fatal(err)
			}
			currentItemProvider(t, a, fmt.Sprintf(`{"number":2,"title":"PR","state":"open","user":{"login":"me"},"head":{"sha":%q}}`, pr.Head), 200)
			if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal("new default reclassified retained nonreview work", err)
			}
			waitFor(t, func() bool {
				_ = a.Store.Get(ctx, "run", run.ID, &run)
				return run.Status == "running" && run.ThreadID != ""
			})
			thread := run.ThreadID
			if err := a.Runs.Interrupt(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", run.ID, &run); return run.Status == "interrupted" })
			if _, err := a.Runs.Start(ctx, run.ID, true); err != nil {
				t.Fatal("new default blocked retained nonreview continuation", err)
			}
			waitFor(t, func() bool {
				_ = a.Store.Get(ctx, "run", run.ID, &run)
				return run.Status == "running" && run.TurnID != ""
			})
			if run.ThreadID != thread || run.Worktree != original.Worktree || run.Prompt != original.Prompt || string(raw(run.Profile)) != string(raw(original.Profile)) {
				t.Fatal("default change replaced retained work or authority")
			}
		})
	}
}

func TestPRReviewIntentJSONCompatibility(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	pr.Author = "me"
	config, _ := a.Config(ctx)
	config.ReviewProfileID = "investigate"
	if err := a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, encoded, profile string
		known, review, blocked bool
	}{
		{"missing-standard", `{}`, "review", false, false, true},
		{"missing-configured", `{}`, "investigate", false, false, true},
		{"old-true", `{"prReview":true}`, "old-review", true, true, true},
		{"explicit-false", `{"prReview":false}`, "investigate", true, false, false},
		{"automatic", `{"prReview":false,"automaticReview":true}`, "investigate", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var run domain.Run
			if err := json.Unmarshal([]byte(tc.encoded), &run); err != nil {
				t.Fatal(err)
			}
			run.Profile.ID = tc.profile
			var encoded map[string]json.RawMessage
			if err := json.Unmarshal(raw(run), &encoded); err != nil {
				t.Fatal(err)
			}
			value, known := encoded["prReview"]
			if known != tc.known || known && string(value) != fmt.Sprint(tc.review) {
				t.Fatal("review intent lost JSON presence or boolean value", string(value), known)
			}
			var legacy struct {
				PRReview bool `json:"prReview"`
			}
			if err := json.Unmarshal(raw(run), &legacy); err != nil || legacy.PRReview != tc.review {
				t.Fatal("legacy boolean consumer lost compatibility", err)
			}
			err := validatePRReviewRun(func(kind, id string, value any) error { return a.Store.Get(ctx, kind, id, value) }, run, pr)
			if (err != nil) != tc.blocked || err != nil && !strings.Contains(err.Error(), "cannot review your own pull request") {
				t.Fatal("review intent changed eligibility", err)
			}
		})
	}
}

func TestManualPRReviewRejectsOwnPullRequests(t *testing.T) {
	for _, route := range []string{"dedicated", "generic", "configured", "unknown-viewer", "unknown-author"} {
		t.Run(route, func(t *testing.T) {
			a, pr := reviewFixture(t)
			ctx := a.Runs.ctx
			pr.Author = "ME"
			want := "cannot review your own pull request"
			if route == "unknown-viewer" {
				if err := a.Store.Put(ctx, "identity", "github", domain.GitHubViewer{}, ""); err != nil {
					t.Fatal(err)
				}
				want = "cannot verify PR review eligibility"
			}
			if route == "unknown-author" {
				pr.Author = ""
				want = "cannot verify PR review eligibility"
			}
			if err := a.Store.Put(ctx, "item", pr.ID, pr, ""); err != nil {
				t.Fatal(err)
			}
			profile := "review"
			if route == "configured" {
				var p domain.Profile
				_ = a.Store.Get(ctx, "profile", profile, &p)
				p.ID = "custom-review"
				if err := a.SaveProfile(ctx, p); err != nil {
					t.Fatal(err)
				}
				c, _ := a.Config(ctx)
				c.ReviewProfileID = p.ID
				if err := a.SaveConfig(ctx, c); err != nil {
					t.Fatal(err)
				}
				profile = p.ID
			}
			before := a.Store.Latest(ctx)
			var err error
			if route == "generic" || route == "configured" {
				_, err = a.Runs.Prepare(ctx, LaunchInput{ItemID: pr.ID, ProfileID: profile})
			} else {
				_, err = a.Runs.PreparePRReview(ctx, pr.ID)
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("review should be rejected with %q: %v", want, err)
			}
			if a.Store.Latest(ctx) != before {
				t.Fatal("rejected review wrote an event")
			}
			runs, err := store.List[domain.Run](ctx, a.Store, "run")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range runs {
				if r.ItemID == pr.ID {
					t.Fatal("rejected review persisted a run")
				}
			}
			// Other work on the owner's PR remains available.
			if _, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: pr.ID, ProfileID: "investigate"}); err != nil {
				t.Fatal("investigation of own PR was blocked", err)
			}
		})
	}
}

func TestManualPRReviewRetainsEligibilityAfterDefaultChanges(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	var p domain.Profile
	_ = a.Store.Get(ctx, "profile", "review", &p)
	p.ID = "old-review"
	if err := a.SaveProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	c, _ := a.Config(ctx)
	c.ReviewProfileID = p.ID
	if err := a.SaveConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	r, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.ReviewProfileID = "review"
	if err := a.SaveConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	pr.Author = "me"
	if err := a.Store.Put(ctx, "item", pr.ID, pr, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Runs.Start(ctx, r.ID, false); err == nil || !strings.Contains(err.Error(), "cannot review your own pull request") {
		t.Fatal("changed default bypassed retained review eligibility", err)
	}
}

func TestManualPRReviewChecksFreshAuthorBeforeProvider(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	r, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentItemProvider(t, a, fmt.Sprintf(`{"number":2,"title":"PR","state":"open","user":{"login":"me"},"head":{"sha":%q}}`, pr.Head), 200)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", r.ID, &r)
		return r.Status == "failed"
	})
	if !strings.Contains(r.Error, "cannot review your own pull request") || r.ThreadID != "" || r.ResumePending {
		t.Fatal("fresh author check did not stop the review", r.Error)
	}
	if _, err := os.Stat(r.Worktree); !os.IsNotExist(err) {
		t.Fatal("fresh self review created a workspace", err)
	}
}

func TestManualPRReviewRechecksAuthorBeforeStart(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepared", true: "legacy"}[legacy], func(t *testing.T) {
			a, pr := reviewFixture(t)
			ctx := a.Runs.ctx
			r, err := a.Runs.PreparePRReview(ctx, pr.ID)
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				// Construct an older snapshot without any new eligibility metadata.
				r = domain.Run{ID: r.ID, ItemID: r.ItemID, Profile: r.Profile, Status: "prepared", Worktree: r.Worktree}
				if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Store.Put(ctx, "identity", "github", domain.GitHubViewer{Login: "OTHER"}, ""); err != nil {
				t.Fatal(err)
			}
			before := a.Store.Latest(ctx)
			if _, err := a.Runs.Start(ctx, r.ID, false); err == nil || !strings.Contains(err.Error(), "cannot review your own pull request") {
				t.Fatal("prepared self review was accepted", err)
			}
			if a.Store.Latest(ctx) != before {
				t.Fatal("rejected start changed run state")
			}
			if _, err := os.Stat(r.Worktree); !os.IsNotExist(err) {
				t.Fatal("rejected start created a workspace", err)
			}
		})
	}
}

func TestManualPRReviewExplicitBaseKeepsAcceptedHead(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			a, pr := reviewFixture(t)
			ctx := a.Runs.ctx
			var repo domain.Repository
			if err := a.Store.Get(ctx, "repository", pr.Repository, &repo); err != nil {
				t.Fatal(err)
			}
			if _, err := command(ctx, repo.Checkout, "rev-parse", "--verify", "main^{commit}"); err != nil {
				t.Fatal(err)
			}
			run, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: pr.ID, ProfileID: "review", Base: "main"})
			if err != nil {
				t.Fatal(err)
			}
			if run.Base != "main" || run.SourceSnapshot == nil || run.SourceSnapshot.Head != pr.Head {
				t.Fatal("checkout base replaced accepted review head", run)
			}
			if changed {
				pr.Head = "new-head"
			}
			if err := a.Store.Put(ctx, "item", pr.ID, pr, ""); err != nil {
				t.Fatal(err)
			}
			_, err = a.Runs.Start(ctx, run.ID, false)
			if changed {
				if err == nil || !strings.Contains(err.Error(), "PR head changed") {
					t.Fatal("changed accepted head was not rejected", err)
				}
				return
			}
			if err != nil {
				t.Fatal("custom checkout base rejected unchanged PR head", err)
			}
			waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", run.ID, &run); return run.Status == "idle" })
			if run.Base != "main" || run.ThreadID == "" {
				t.Fatal("custom checkout was not used", run)
			}
		})
	}
}

func TestLegacyReviewHeadStillUsesPinnedBase(t *testing.T) {
	a, pr := reviewFixture(t)
	for _, snapshot := range []*domain.Item{nil, {Head: ""}} {
		review := true
		run := domain.Run{PRReview: &review, Base: pr.Head, SourceSnapshot: snapshot}
		get := func(kind, id string, out any) error { return a.Store.Get(a.Runs.ctx, kind, id, out) }
		if err := validatePRReviewRun(get, run, pr); err != nil {
			t.Fatal(err)
		}
		run.Base = "other-head"
		if err := validatePRReviewRun(get, run, pr); err == nil {
			t.Fatal("legacy accepted-head pin was weakened")
		}
	}
}
