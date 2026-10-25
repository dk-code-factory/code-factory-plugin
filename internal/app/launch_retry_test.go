package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type launchRetrySource struct {
	providers.GitHubSource
	item           domain.Item
	syncFails      atomic.Bool
	currentEntered chan struct{}
	currentRelease chan struct{}
	currentOnce    sync.Once
}

func (s *launchRetrySource) CurrentWorkItem(ctx context.Context, item domain.Item) (domain.Item, error) {
	if s.currentEntered != nil {
		s.currentOnce.Do(func() { close(s.currentEntered) })
		select {
		case <-s.currentRelease:
		case <-ctx.Done():
			return domain.Item{}, ctx.Err()
		}
	}
	return s.GitHubSource.CurrentWorkItem(ctx, item)
}

func (s *launchRetrySource) User(context.Context) (string, error) { return "me", nil }
func (s *launchRetrySource) SyncIndex(context.Context, domain.Repository, bool) ([]domain.Item, time.Time, error) {
	if s.syncFails.Load() {
		return nil, time.Time{}, errors.New("fixture repository sync failed")
	}
	return []domain.Item{s.item}, time.Now().UTC(), nil
}
func (s *launchRetrySource) PullRequest(context.Context, string, int) (domain.Item, error) {
	return s.item, nil
}

func (s *launchRetrySource) PublishedReviews(context.Context, domain.Item) ([]domain.PublishedReview, time.Time, error) {
	return nil, time.Now().UTC(), nil
}

type launchRetryFixture struct {
	a      *App
	pr     domain.Item
	run    domain.Run
	github *providers.GitHub
	source *launchRetrySource
	budget atomic.Int32
	calls  atomic.Int32
	live   atomic.Value
	record string
	status atomic.Int32
}

func deferredReviewFixture(t *testing.T, automatic bool) *launchRetryFixture {
	t.Helper()
	a, pr := reviewFixture(t)
	t.Setenv("CF_FAKE_MODE", "hold")
	f := &launchRetryFixture{a: a, pr: pr, record: filepath.Join(t.TempDir(), "calls.jsonl")}
	f.live.Store(pr)
	f.budget.Store(1)
	f.status.Store(http.StatusOK)
	t.Setenv("CF_FAKE_RECORD", f.record)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.URL.Path == "/user" {
			fmt.Fprint(w, `{"login":"me"}`)
			return
		}
		number := 2
		if r.URL.Path == "/repos/owner/repo/pulls/3" {
			number = 3
		} else if r.URL.Path != "/repos/owner/repo/pulls/2" {
			t.Errorf("unexpected HTTP request %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(int(f.status.Load()))
		item := f.live.Load().(domain.Item)
		reviewers := []map[string]string{}
		for _, login := range item.ReviewRequests {
			reviewers = append(reviewers, map[string]string{"login": login})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"number": number, "title": "PR", "state": item.State, "draft": item.Draft, "user": map[string]string{"login": item.Author}, "head": map[string]string{"sha": item.Head}, "requested_reviewers": reviewers})
	}))
	t.Cleanup(server.Close)
	f.github = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	f.github.ConfigureUsage(a.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: int(f.budget.Load())}, nil
	})
	f.source = &launchRetrySource{GitHubSource: f.github, item: pr}
	a.GitHub = f.source
	ctx := a.Runs.ctx
	if _, err := f.github.User(ctx); err != nil {
		t.Fatal(err)
	}
	if automatic {
		c, _ := a.Config(ctx)
		c.AutoReview = true
		if err := a.SaveConfig(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := a.ProcessReviewRequests(ctx); err != nil {
			t.Fatal(err)
		}
		claims, _ := store.List[reviewClaim](ctx, a.Store, "review-claim")
		if len(claims) != 1 {
			t.Fatal("expected exactly one claim", claims)
		}
		if err := a.Store.Get(ctx, "run", claims[0].RunID, &f.run); err != nil {
			t.Fatal(err)
		}
	} else {
		var err error
		f.run, err = a.Runs.PreparePRReview(ctx, pr.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Runs.Start(ctx, f.run.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", f.run.ID, &f.run); return f.run.Status == "deferred" })
	usage, err := f.github.UsageStatus(ctx)
	if err != nil || !f.run.LaunchRetryAt.Equal(usage.NextRequest) || !f.run.LaunchRetryAt.After(time.Now()) || !f.run.ResumePending || f.run.ThreadID != "" || f.run.TurnID != "" || !strings.Contains(f.run.Error, "Code Factory GitHub budget reached") {
		t.Fatalf("launch was not durably deferred: %+v usage=%+v err=%v", f.run, usage, err)
	}
	if f.calls.Load() != 1 {
		t.Fatal("budget preflight performed HTTP", f.calls.Load())
	}
	if _, err := os.Stat(f.record); !os.IsNotExist(err) {
		t.Fatal("deferred launch started an agent", err)
	}
	if _, err := os.Stat(f.run.Worktree); !os.IsNotExist(err) {
		t.Fatal("deferred launch created workspace", err)
	}
	return f
}
func (f *launchRetryFixture) expire(t *testing.T) {
	t.Helper()
	if err := f.a.Store.Get(context.Background(), "run", f.run.ID, &f.run); err != nil {
		t.Fatal(err)
	}
	f.run.LaunchRetryAt = time.Now().UTC().Add(-time.Second)
	if err := f.a.Store.Put(context.Background(), "run", f.run.ID, f.run, ""); err != nil {
		t.Fatal(err)
	}
}
func (f *launchRetryFixture) restart(t *testing.T) {
	t.Helper()
	waitLaunchRetry(t, f.a.Runs.Close)
	f.a.Runs = NewManager(context.Background(), f.a, testutil.FakeCodex(t))
	if err := f.a.Runs.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func (f *launchRetryFixture) assertNoAgent(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(f.record); !os.IsNotExist(err) {
		t.Fatal("unexpected provider invocation", err)
	}
}
func (f *launchRetryFixture) assertSingleLaunch(t *testing.T) {
	t.Helper()
	waitFor(t, func() bool {
		_ = f.a.Store.Get(context.Background(), "run", f.run.ID, &f.run)
		return f.run.Status == "running" && f.run.TurnID != ""
	})
	calls := recordedCalls(t, f.record)
	if len(calls) != 2 || calls[0].Method != "thread/start" || calls[1].Method != "turn/start" {
		t.Fatalf("accepted launch replayed or resumed; recorded calls=%d", len(calls))
	}
	if !f.run.LaunchRetryAt.IsZero() || f.run.Error != "" || !f.run.ResumePending {
		t.Fatal("retry metadata was not cleared", f.run)
	}
	runs, err := store.List[domain.Run](context.Background(), f.a.Store, "run")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range runs {
		if r.ItemID == f.pr.ID {
			n++
		}
	}
	if n != 1 {
		t.Fatal("retry created duplicate runs", n)
	}
	if f.run.AutomaticReview && claimCount(t, f.a) != 1 {
		t.Fatal("retry created duplicate automatic claim")
	}
}

func TestDeferredReviewLaunchRespectsDeadlineAndRetriesExactlyOnce(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprint(automatic), func(t *testing.T) {
			f := deferredReviewFixture(t, automatic)
			ctx := context.Background()
			f.budget.Store(100)
			if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
				t.Fatal(err)
			}
			if f.calls.Load() != 1 {
				t.Fatal("retry ignored persisted deadline")
			}
			f.assertNoAgent(t)
			// A successful repository poll must preserve the accepted run/claim.
			if err := f.a.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			f.expire(t)
			var wg sync.WaitGroup
			for n := 0; n < 12; n++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
						t.Error(err)
					}
				}()
			}
			waitLaunchRetry(t, func() { wg.Wait() })
			f.assertSingleLaunch(t)
			if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
				t.Fatal(err)
			}
			if err := f.a.ProcessReviewRequests(ctx); err != nil {
				t.Fatal(err)
			}
			f.assertSingleLaunch(t)
		})
	}
}

func TestDeferredReviewLaunchRecoversBeforeAndAfterDeadline(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, expired := range []bool{false, true} {
			t.Run(fmt.Sprintf("automatic=%t/expired=%t", automatic, expired), func(t *testing.T) {
				f := deferredReviewFixture(t, automatic)
				f.budget.Store(100)
				if expired {
					f.expire(t)
				}
				f.restart(t)
				if !expired {
					if f.calls.Load() != 1 {
						t.Fatal("restart ignored retry deadline")
					}
					f.assertNoAgent(t)
					f.expire(t)
					if err := f.a.Runs.RetryDeferredLaunches(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				f.assertSingleLaunch(t)
			})
		}
	}
}

func TestDeferredReviewLaunchRetriesWhenRepositorySyncFails(t *testing.T) {
	f := deferredReviewFixture(t, true)
	f.budget.Store(100)
	f.expire(t)
	f.source.syncFails.Store(true)
	var original domain.Repository
	if err := f.a.Store.Get(context.Background(), "repository", f.pr.Repository, &original); err != nil {
		t.Fatal(err)
	}
	original.LastSync = time.Now().UTC().Add(-time.Hour)
	if err := f.a.Store.Put(context.Background(), "repository", original.ID, original, ""); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.a.syncLoop(ctx, make(chan time.Time)) }()
	t.Cleanup(func() { cancel(); waitLaunchRetry(t, func() { <-done }) })
	f.assertSingleLaunch(t)
	var repo domain.Repository
	if err := f.a.Store.Get(ctx, "repository", f.pr.Repository, &repo); err != nil || repo.Error == "" {
		t.Fatal("test did not exercise failed repository sync", repo, err)
	}
}

func TestDeferredReviewLaunchRevalidatesLiveTargetAndPolicy(t *testing.T) {
	for _, change := range []string{"head", "closed", "draft", "withdrawn", "disabled", "paused", "profile", "author", "permanent-preflight"} {
		t.Run(change, func(t *testing.T) {
			f := deferredReviewFixture(t, true)
			ctx := context.Background()
			fresh := f.pr
			switch change {
			case "head":
				fresh.Head = "different-head"
			case "closed":
				fresh.State = "closed"
			case "draft":
				fresh.Draft = true
			case "withdrawn":
				fresh.ReviewRequests = nil
			case "author":
				fresh.Author = "me"
			case "disabled":
				c, _ := f.a.Config(ctx)
				c.AutoReview = false
				if err := f.a.SaveConfig(ctx, c); err != nil {
					t.Fatal(err)
				}
			case "paused":
				var repo domain.Repository
				_ = f.a.Store.Get(ctx, "repository", f.pr.Repository, &repo)
				repo.Paused = true
				if err := f.a.SaveRepository(ctx, repo); err != nil {
					t.Fatal(err)
				}
			case "profile":
				var p domain.Profile
				_ = f.a.Store.Get(ctx, "profile", "review", &p)
				p.Prompt += " changed"
				if err := f.a.SaveProfile(ctx, p); err != nil {
					t.Fatal(err)
				}
			case "permanent-preflight":
				f.status.Store(503)
			}
			f.live.Store(fresh)
			f.budget.Store(100)
			f.expire(t)
			if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
				t.Fatal(err)
			}
			if change == "disabled" || change == "paused" {
				_ = f.a.Store.Get(ctx, "run", f.run.ID, &f.run)
				if f.run.Status != "deferred" || f.calls.Load() != 1 {
					t.Fatal("paused or disabled automatic launch was attempted", f.run, f.calls.Load())
				}
			} else {
				waitFor(t, func() bool {
					_ = f.a.Store.Get(ctx, "run", f.run.ID, &f.run)
					return f.run.Status == "failed" || f.run.Status == "terminated"
				})
				if f.run.ResumePending || !f.run.LaunchRetryAt.IsZero() {
					t.Fatal("terminal rejection retained retry authority", f.run)
				}
				if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
					t.Fatal(err)
				}
			}
			f.assertNoAgent(t)
			if claimCount(t, f.a) != 1 {
				t.Fatal("revalidation changed automatic claim")
			}
		})
	}
}

func TestDeferredLaunchInterruptAndCleanupCancelAttachedAndDetachedRuns(t *testing.T) {
	for _, detached := range []bool{false, true} {
		for _, cleanup := range []bool{false, true} {
			t.Run(fmt.Sprintf("detached=%t/cleanup=%t", detached, cleanup), func(t *testing.T) {
				f := deferredReviewFixture(t, false)
				if detached {
					f.restart(t)
				}
				var err error
				if cleanup {
					err = f.a.Runs.Cleanup(context.Background(), f.run.ID)
				} else {
					err = f.a.Runs.Interrupt(context.Background(), f.run.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				_ = f.a.Store.Get(context.Background(), "run", f.run.ID, &f.run)
				if f.run.ResumePending || !f.run.InterruptRequested || !f.run.LaunchRetryAt.IsZero() || f.run.Status == "deferred" {
					t.Fatal("cancel did not clear accepted launch", f.run)
				}
				f.budget.Store(100)
				f.restart(t)
				if err := f.a.Runs.RetryDeferredLaunches(context.Background()); err != nil {
					t.Fatal(err)
				}
				f.assertNoAgent(t)
			})
		}
	}
}

func TestLegacyBudgetReviewFailureRecoveryIsExactAndPreAgentOnly(t *testing.T) {
	const prefix = "cannot verify current work item; work remains stopped: "
	for _, which := range []string{"budget", "reserve", "rate", "permanent", "thread", "turn", "interrupted", "non-review", "nil-review", "nil-custom-configured", "explicit-false-review", "explicit-true-custom", "agent-error", "lookalike"} {
		t.Run(which, func(t *testing.T) {
			f := deferredReviewFixture(t, false)
			waitLaunchRetry(t, f.a.Runs.Close)
			r := f.run
			r.Status = "failed"
			r.ResumePending = false
			r.LaunchRetryAt = time.Time{}
			r.Error = prefix + "Code Factory GitHub budget reached; synchronization will retry"
			eligible := true
			switch which {
			case "reserve":
				r.Error = prefix + "GitHub request reserve reached; synchronization will retry"
			case "rate":
				r.Error = prefix + "GitHub rate limit; synchronization will retry"
			case "permanent":
				r.Error = prefix + "GitHub unavailable (HTTP 503)"
				eligible = false
			case "thread":
				r.ThreadID = "already-started"
				eligible = false
			case "turn":
				r.TurnID = "already-started"
				eligible = false
			case "interrupted":
				r.InterruptRequested = true
				eligible = false
			case "non-review":
				intent := false
				r.PRReview = &intent
				r.Profile.ID = "investigate"
				eligible = false
			case "nil-review":
				r.PRReview = nil
			case "nil-custom-configured":
				r.PRReview = nil
				r.Profile.ID = "custom"
				config, err := f.a.Config(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				config.ReviewProfileID = "custom"
				if err := f.a.Store.Put(context.Background(), "config", "settings", config, ""); err != nil {
					t.Fatal(err)
				}
				eligible = false
			case "explicit-false-review":
				intent := false
				r.PRReview = &intent
				eligible = false
			case "explicit-true-custom":
				r.Profile.ID = "custom"
			case "agent-error":
				r.Error = "Code Factory GitHub budget reached; synchronization will retry"
				eligible = false
			case "lookalike":
				r.Error += " after agent failure"
				eligible = false
			}
			if err := f.a.Store.Put(context.Background(), "run", r.ID, r, ""); err != nil {
				t.Fatal(err)
			}
			f.a.Runs = NewManager(context.Background(), f.a, testutil.FakeCodex(t))
			if err := f.a.Runs.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			_ = f.a.Store.Get(context.Background(), "run", r.ID, &r)
			if eligible {
				usage, err := f.github.UsageStatus(context.Background())
				if err != nil || r.Status != "deferred" || !r.ResumePending || !r.LaunchRetryAt.Equal(usage.NextRequest) {
					t.Fatal("legacy failure was not durably recovered", r, usage, err)
				}
				f.assertNoAgent(t)
				f.budget.Store(100)
				f.expire(t)
				if err := f.a.Runs.RetryDeferredLaunches(context.Background()); err != nil {
					t.Fatal(err)
				}
				f.assertSingleLaunch(t)
			} else if r.Status != "failed" || r.ResumePending {
				t.Fatal("unrelated failure gained retry authority", r)
			}
			if !eligible {
				f.assertNoAgent(t)
			}
		})
	}
}

func TestDeferredLaunchProjectionRetainsWorkingQueueAndRetryDeadline(t *testing.T) {
	f := deferredReviewFixture(t, false)
	entries, err := f.a.Inbox(context.Background(), "open")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, entry := range entries {
		if entry.Item.ID == f.pr.ID {
			found = true
			if entry.Work.Status != "Waiting for GitHub" || !ContainsQueue(entry, "working") {
				t.Fatal("deferred launch disappeared from working queue", entry)
			}
		}
	}
	if !found {
		t.Fatal("review item absent")
	}
	if got := runSummary(f.run)["launchRetryAt"]; got != f.run.LaunchRetryAt {
		t.Fatal("tracking omitted retry deadline", got)
	}
	if err := f.a.Runs.Interrupt(context.Background(), f.run.ID); err != nil {
		t.Fatal(err)
	}
	entries, err = f.a.Inbox(context.Background(), "open")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Item.ID == f.pr.ID && entry.Work.Status == "Waiting for GitHub" {
			t.Fatal("cancel retained waiting projection")
		}
	}
}

func TestDeferredLaunchRepeatedBudgetDenialKeepsAcceptedRun(t *testing.T) {
	f := deferredReviewFixture(t, true)
	f.expire(t)
	if err := f.a.Runs.RetryDeferredLaunches(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = f.a.Store.Get(context.Background(), "run", f.run.ID, &f.run)
		return f.run.Status == "deferred"
	})
	usage, err := f.github.UsageStatus(context.Background())
	if err != nil || !f.run.LaunchRetryAt.Equal(usage.NextRequest) || !f.run.ResumePending || f.calls.Load() != 1 {
		t.Fatal("renewed budget denial lost deadline or performed HTTP", f.run, usage, err)
	}
	f.assertNoAgent(t)
	if claimCount(t, f.a) != 1 {
		t.Fatal("renewed deferral changed review claim")
	}
}

func TestManualDeferredReviewRevalidatesPinnedHeadAndClosedSource(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(fmt.Sprint(closed), func(t *testing.T) {
			f := deferredReviewFixture(t, false)
			fresh := f.pr
			if closed {
				fresh.State = "closed"
			} else {
				fresh.Head = "changed-head"
			}
			f.live.Store(fresh)
			f.budget.Store(100)
			f.expire(t)
			if err := f.a.Runs.RetryDeferredLaunches(context.Background()); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool {
				_ = f.a.Store.Get(context.Background(), "run", f.run.ID, &f.run)
				return f.run.Status == "failed" || f.run.Status == "terminated"
			})
			f.assertNoAgent(t)
			if f.run.ResumePending || !f.run.LaunchRetryAt.IsZero() {
				t.Fatal("stale or closed manual review remained retryable", f.run)
			}
		})
	}
}

func TestDeferredLaunchCancelAndCloseDuringSourceVerification(t *testing.T) {
	for _, action := range []string{"interrupt", "close", "disable", "pause"} {
		t.Run(action, func(t *testing.T) {
			f := deferredReviewFixture(t, true)
			f.budget.Store(100)
			f.expire(t)
			f.source.currentEntered = make(chan struct{})
			f.source.currentRelease = make(chan struct{})
			if err := f.a.Runs.RetryDeferredLaunches(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-f.source.currentEntered:
			case <-time.After(3 * time.Second):
				t.Fatal("retry did not begin source verification")
			}
			var closer chan struct{}
			switch action {
			case "interrupt":
				if err := f.a.Runs.Interrupt(context.Background(), f.run.ID); err != nil {
					t.Fatal(err)
				}
			case "close":
				closer = make(chan struct{})
				go func() { waitLaunchRetry(t, f.a.Runs.Close); close(closer) }()
				waitFor(t, func() bool { return f.a.Runs.ctx.Err() != nil })
			case "disable":
				c, _ := f.a.Config(context.Background())
				c.AutoReview = false
				if err := f.a.SaveConfig(context.Background(), c); err != nil {
					t.Fatal(err)
				}
			case "pause":
				var repo domain.Repository
				_ = f.a.Store.Get(context.Background(), "repository", f.pr.Repository, &repo)
				repo.Paused = true
				if err := f.a.SaveRepository(context.Background(), repo); err != nil {
					t.Fatal(err)
				}
			}
			close(f.source.currentRelease)
			if closer != nil {
				waitLaunchRetry(t, func() { <-closer })
			} else {
				live, err := f.a.Runs.get(f.run.ID)
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-live.started:
				case <-time.After(3 * time.Second):
					t.Fatal("cancelled retry did not finish")
				}
			}
			f.assertNoAgent(t)
			if action == "interrupt" || action == "close" {
				if _, err := os.Stat(f.run.Worktree); !os.IsNotExist(err) {
					t.Fatal("cancelled launch created workspace", err)
				}
			}
			if err := f.a.Runs.RetryDeferredLaunches(context.Background()); err != nil && action != "close" {
				t.Fatal(err)
			}
			f.assertNoAgent(t)
		})
	}
}

func TestDeferredLaunchDoesNotRetryProviderExecutionFailures(t *testing.T) {
	f := deferredReviewFixture(t, false)
	f.budget.Store(100)
	f.expire(t)
	if err := f.a.Runs.RetryDeferredLaunches(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertSingleLaunch(t)
	live, err := f.a.Runs.get(f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Even a typed rate limit after provider startup is an execution failure,
	// because replay could repeat work or an uncertain external publication.
	f.a.Runs.fail(live, &providers.RateLimitError{Until: time.Now().UTC().Add(time.Hour)})
	if err := f.a.Runs.RetryDeferredLaunches(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = f.a.Store.Get(context.Background(), "run", f.run.ID, &f.run)
	if f.run.Status != "failed" || f.run.ResumePending || !f.run.LaunchRetryAt.IsZero() {
		t.Fatal("provider failure gained retry authority", f.run)
	}
	if calls := recordedCalls(t, f.record); len(calls) != 2 {
		t.Fatal("provider failure was replayed", len(calls))
	}
}

func TestDeferredAutomaticLaunchesSerializeAndContinueAfterCompletion(t *testing.T) {
	f := deferredReviewFixture(t, true)
	ctx := context.Background()
	other := f.pr
	other.ID, other.Number, other.URL = "owner/repo#3", 3, "https://github.com/owner/repo/pull/3"
	importOne(t, f.a, ctx, other)
	var repo domain.Repository
	if err := f.a.Store.Get(ctx, "repository", other.Repository, &repo); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo.Checkout, "push", "origin", other.Head+":refs/pull/3/head").CombinedOutput(); err != nil {
		t.Fatalf("local PR head: %s %v", out, err)
	}
	if err := f.a.ProcessReviewRequests(ctx); err != nil {
		t.Fatal(err)
	}
	claims, err := store.List[reviewClaim](ctx, f.a.Store, "review-claim")
	if err != nil || len(claims) != 2 {
		t.Fatal("second review was not accepted", claims, err)
	}
	var second domain.Run
	for _, claim := range claims {
		if claim.ItemID == other.ID {
			if err := f.a.Store.Get(ctx, "run", claim.RunID, &second); err != nil {
				t.Fatal(err)
			}
		}
	}
	waitFor(t, func() bool { _ = f.a.Store.Get(ctx, "run", second.ID, &second); return second.Status == "deferred" })
	f.expire(t)
	second.LaunchRetryAt = f.run.LaunchRetryAt
	if err := f.a.Store.Put(ctx, "run", second.ID, second, ""); err != nil {
		t.Fatal(err)
	}
	f.budget.Store(100)
	var wg sync.WaitGroup
	for n := 0; n < 10; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	waitLaunchRetry(t, func() { wg.Wait() })
	var firstRunning, waiting domain.Run
	waitFor(t, func() bool {
		_ = f.a.Store.Get(ctx, "run", f.run.ID, &f.run)
		_ = f.a.Store.Get(ctx, "run", second.ID, &second)
		if f.run.Status == "running" && f.run.TurnID != "" && second.Status == "deferred" {
			firstRunning, waiting = f.run, second
			return true
		}
		if second.Status == "running" && second.TurnID != "" && f.run.Status == "deferred" {
			firstRunning, waiting = second, f.run
			return true
		}
		return false
	})
	if calls := recordedCalls(t, f.record); len(calls) != 2 {
		t.Fatal("deferred automatic reviews started concurrently", len(calls))
	}
	live, err := f.a.Runs.get(firstRunning.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]string{"id": firstRunning.TurnID, "status": "completed"}})})
	if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = f.a.Store.Get(ctx, "run", waiting.ID, &waiting)
		return waiting.Status == "running" && waiting.TurnID != ""
	})
	if calls := recordedCalls(t, f.record); len(calls) != 4 {
		t.Fatal("waiting automatic review was lost or repeated", len(calls))
	}
	if claimCount(t, f.a) != 2 {
		t.Fatal("retry changed automatic claims")
	}
}

// Bound joins as well as assertions: a serialization or shutdown regression must
// fail locally instead of preventing the test from ever reaching waitFor.
func waitLaunchRetry(t *testing.T, join func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); join() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("launch retry operation did not finish")
	}
}

func TestDeferredAutomaticLaunchRejectsSupersededRequestEpisode(t *testing.T) {
	f := deferredReviewFixture(t, true)
	ctx := context.Background()
	oldID := f.run.ID
	withdrawn := f.pr
	withdrawn.ReviewRequests = nil
	importOne(t, f.a, ctx, withdrawn)
	if err := f.a.ProcessReviewRequests(ctx); err != nil {
		t.Fatal(err)
	}
	importOne(t, f.a, ctx, f.pr)
	if err := f.a.ProcessReviewRequests(ctx); err != nil {
		t.Fatal(err)
	}
	claims, err := store.List[reviewClaim](ctx, f.a.Store, "review-claim")
	if err != nil || len(claims) != 2 || claims[0].ID == claims[1].ID || claims[0].RunID == claims[1].RunID {
		t.Fatal("request episodes did not retain distinct claims", claims, err)
	}
	var current domain.Run
	for _, claim := range claims {
		if claim.RunID != oldID {
			if err := f.a.Store.Get(ctx, "run", claim.RunID, &current); err != nil {
				t.Fatal(err)
			}
		}
	}
	waitFor(t, func() bool { _ = f.a.Store.Get(ctx, "run", current.ID, &current); return current.Status == "deferred" })
	// Neither episode may ignore its saved deadline during a daemon restart.
	f.restart(t)
	f.assertNoAgent(t)
	f.expire(t)
	f.budget.Store(100)
	if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = f.a.Store.Get(ctx, "run", oldID, &f.run); return f.run.Status == "failed" })
	if f.run.ResumePending || !f.run.LaunchRetryAt.IsZero() || f.run.ThreadID != "" {
		t.Fatal("superseded claim retained startup authority", f.run)
	}
	f.assertNoAgent(t)
	current.LaunchRetryAt = time.Now().UTC().Add(-time.Second)
	if err := f.a.Store.Put(ctx, "run", current.ID, current, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = f.a.Store.Get(ctx, "run", current.ID, &current)
		return current.Status == "running" && current.TurnID != ""
	})
	if calls := recordedCalls(t, f.record); len(calls) != 2 {
		t.Fatal("superseded episode executed", len(calls))
	}
	before := string(raw(f.run))
	if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.a.Store.Get(ctx, "run", oldID, &f.run); err != nil || string(raw(f.run)) != before {
		t.Fatal("terminal superseded run changed", err)
	}
}

func TestDeferredLaunchPollDoesNotDecodeUnusedRetainedPayloads(t *testing.T) {
	f := deferredReviewFixture(t, false)
	ctx := context.Background()
	// Wrong-shaped sentinels make a full Run decode fail. SQLite must omit all
	// saved bundle/context payloads before decoding even when no run is due.
	retained := map[string]any{"id": "retained", "status": "idle", "skills": "not decoded", "profile": "not decoded", "prompt": []int{1}, "reviewPromptSnapshot": []int{1}, "sourceSnapshot": "not decoded"}
	if err := f.a.Store.Put(ctx, "run", "retained", retained, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
		t.Fatal("poll decoded an unused saved payload", err)
	}
	var after map[string]any
	if err := f.a.Store.Get(ctx, "run", "retained", &after); err != nil || string(raw(after)) != string(raw(retained)) {
		t.Fatal("projection overwrote a retained record", err)
	}
	// A due candidate must be reread in full before its startup is persisted.
	var saved domain.Run
	if err := f.a.Store.Get(ctx, "run", f.run.ID, &saved); err != nil {
		t.Fatal(err)
	}
	f.expire(t)
	f.budget.Store(100)
	if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = f.a.Store.Get(ctx, "run", f.run.ID, &f.run)
		return f.run.Status == "running" && f.run.TurnID != ""
	})
	if string(raw(saved.Profile)) != string(raw(f.run.Profile)) || string(raw(saved.Skills)) != string(raw(f.run.Skills)) || saved.Prompt != f.run.Prompt || saved.ReviewPromptSnapshot != f.run.ReviewPromptSnapshot || f.run.SourceSnapshot == nil {
		t.Fatal("projected candidate lost its accepted payload")
	}
}

func TestFreshAutomaticStartAndDeferredRetryShareIdleSlot(t *testing.T) {
	f := deferredReviewFixture(t, true)
	ctx := context.Background()
	other := f.pr
	other.ID, other.Number, other.URL = "owner/repo#3", 3, "https://github.com/owner/repo/pull/3"
	importOne(t, f.a, ctx, other)
	var repo domain.Repository
	if err := f.a.Store.Get(ctx, "repository", other.Repository, &repo); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo.Checkout, "push", "origin", other.Head+":refs/pull/3/head").CombinedOutput(); err != nil {
		t.Fatalf("local PR head: %s %v", out, err)
	}
	f.expire(t)
	f.budget.Store(100)
	// This is the fresh ProcessReviewRequests critical section after busy() and
	// acceptance, before Start. There is no production hook at that internal gap.
	f.a.autoReviewMu.Lock()
	locked := true
	defer func() {
		if locked {
			f.a.autoReviewMu.Unlock()
		}
	}()
	if f.a.Runs.busy() {
		t.Fatal("fixture is not idle")
	}
	fresh, err := f.a.Runs.Prepare(ctx, LaunchInput{ItemID: other.ID, ProfileID: "review"})
	if err != nil {
		t.Fatal(err)
	}
	fresh.AutomaticReview = true
	state := reviewRequestState{ID: other.ID, Active: true, Head: other.Head, Generation: 1}
	claim := reviewClaim{ID: reviewClaimID(other.ID, other.Head, state.Generation), ItemID: other.ID, RunID: fresh.ID, Head: other.Head, Status: "claimed"}
	if err := f.a.Store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.Put("review-request", state.ID, state); err != nil {
			return err
		}
		if err := tx.Put("review-claim", claim.ID, claim); err != nil {
			return err
		}
		return tx.Put("run", fresh.ID, fresh)
	}); err != nil {
		t.Fatal(err)
	}
	f.source.currentEntered = make(chan struct{})
	f.source.currentRelease = make(chan struct{})
	release := sync.OnceFunc(func() { close(f.source.currentRelease) })
	defer release()
	retryDone := make(chan error, 1)
	go func() { retryDone <- f.a.Runs.RetryDeferredLaunches(ctx) }()
	// A bounded negative check gives the competing retry an opportunity to enter
	// the real source barrier; successful behavior is also proved by the startup
	// and eventual-progress assertions below.
	select {
	case <-f.source.currentEntered:
		t.Error("retry took the idle slot during fresh automatic acceptance")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := f.a.Runs.Start(ctx, fresh.ID, false); err != nil {
		t.Fatal("fresh accepted claim was stranded", err)
	}
	select {
	case <-f.source.currentEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("fresh accepted review did not reach preflight")
	}
	f.a.autoReviewMu.Unlock()
	locked = false
	release()
	waitLaunchRetry(t, func() {
		if err := <-retryDone; err != nil {
			t.Error(err)
		}
	})
	waitFor(t, func() bool {
		_ = f.a.Store.Get(ctx, "run", fresh.ID, &fresh)
		return fresh.Status == "running" && fresh.TurnID != ""
	})
	if err := f.a.Store.Get(ctx, "run", f.run.ID, &f.run); err != nil || f.run.Status != "deferred" || !f.run.ResumePending {
		t.Fatal("accepted retry was lost or started concurrently", f.run, err)
	}
	if calls := recordedCalls(t, f.record); len(calls) != 2 {
		t.Fatal("fresh and retry agents overlapped", len(calls))
	}
	live, err := f.a.Runs.get(fresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.a.Runs.onMessage(live, codex.Message{Method: "turn/completed", Params: raw(map[string]any{"turn": map[string]string{"id": fresh.TurnID, "status": "completed"}})})
	if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = f.a.Store.Get(ctx, "run", f.run.ID, &f.run)
		return f.run.Status == "running" && f.run.TurnID != ""
	})
	if calls := recordedCalls(t, f.record); len(calls) != 4 || claimCount(t, f.a) != 2 {
		t.Fatal("accepted claims did not each execute exactly once", len(calls))
	}
}

func TestAutomaticRequestPollSharesAcceptanceCriticalSection(t *testing.T) {
	f := deferredReviewFixture(t, true)
	ctx := context.Background()
	withdrawn := f.pr
	withdrawn.ReviewRequests = nil
	importOne(t, f.a, ctx, withdrawn)
	f.a.autoReviewMu.Lock()
	locked := true
	defer func() {
		if locked {
			f.a.autoReviewMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- f.a.ProcessReviewRequests(ctx) }()
	select {
	case err := <-done:
		t.Fatal("request poll bypassed fresh/retry serialization", err)
	case <-time.After(100 * time.Millisecond):
	}
	var state reviewRequestState
	if err := f.a.Store.Get(ctx, "review-request", f.pr.ID, &state); err != nil || !state.Active {
		t.Fatal("request episode changed outside the critical section", state, err)
	}
	f.a.autoReviewMu.Unlock()
	locked = false
	waitLaunchRetry(t, func() {
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	if err := f.a.Store.Get(ctx, "review-request", f.pr.ID, &state); err != nil || state.Active || claimCount(t, f.a) != 1 {
		t.Fatal("real request poll did not update the same episode", state, err)
	}
	f.assertNoAgent(t)
}

func TestAcceptedRetrySurvivesRestartBeforeProviderHandoff(t *testing.T) {
	for _, stage := range []string{"source", "verified-source"} {
		for _, cache := range []string{"stale", "error"} {
			t.Run(stage+"/"+cache, func(t *testing.T) {
				f := deferredReviewFixture(t, true)
				ctx := context.Background()
				f.budget.Store(100)
				f.expire(t)
				var repo domain.Repository
				if err := f.a.Store.Get(ctx, "repository", f.pr.Repository, &repo); err != nil {
					t.Fatal(err)
				}
				if cache == "error" {
					repo.Error = "fixture sync unavailable"
				} else {
					repo.LastSync = time.Now().UTC().Add(-time.Hour)
					item := f.pr
					item.ReviewObservedAt = repo.LastSync
					if err := f.a.Store.Put(ctx, "item", item.ID, item, ""); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.a.Store.Put(ctx, "repository", repo.ID, repo, ""); err != nil {
					t.Fatal(err)
				}
				var entered string
				if stage == "source" {
					f.source.currentEntered, f.source.currentRelease = make(chan struct{}), make(chan struct{})
				} else {
					root := t.TempDir()
					entered = filepath.Join(root, "entered")
					fifo := filepath.Join(root, "release")
					if err := syscall.Mkfifo(fifo, 0600); err != nil {
						t.Fatal(err)
					}
					wrapper := filepath.Join(root, "codex")
					script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n : > %q\n read release < %q\nfi\nexec %q \"$@\"\n", entered, fifo, f.a.Runs.Binary)
					// Keep a shell builtin blocked after live verification, before
					// workspace/sandbox preparation and app-server creation.
					if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
						t.Fatal(err)
					}
					f.a.Runs.Binary = wrapper
				}
				if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
					t.Fatal(err)
				}
				if stage == "source" {
					select {
					case <-f.source.currentEntered:
					case <-time.After(3 * time.Second):
						t.Fatal("source verification did not start")
					}
				} else {
					waitFor(t, func() bool { _, err := os.Stat(entered); return err == nil })
				}
				waitLaunchRetry(t, f.a.Runs.Close)
				f.assertNoAgent(t)
				if stage == "source" {
					close(f.source.currentRelease)
					f.source.currentEntered = nil
				}
				if err := f.a.Store.Get(ctx, "run", f.run.ID, &f.run); err != nil {
					t.Fatal(err)
				}
				if f.run.LaunchRetryAt.IsZero() || !f.run.ResumePending {
					t.Fatal("shutdown lost accepted pre-provider retry origin", f.run.Status)
				}
				// Capacity recovery remains serialized even across manager replacement.
				f.a.Runs = NewManager(ctx, f.a, testutil.FakeCodex(t))
				f.a.Runs.live["occupied"] = &session{run: domain.Run{ID: "occupied", Status: "running"}}
				if err := f.a.Runs.Recover(ctx); err != nil {
					t.Fatal(err)
				}
				f.assertNoAgent(t)
				if err := f.a.Store.Get(ctx, "run", f.run.ID, &f.run); err != nil || f.run.Status != "deferred" {
					t.Fatal("accepted retry was not retained for capacity", f.run.Status, err)
				}
				f.a.Runs.mu.Lock()
				delete(f.a.Runs.live, "occupied")
				f.a.Runs.mu.Unlock()
				if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
					t.Fatal(err)
				}
				f.assertSingleLaunch(t)
			})
		}
	}
}

func TestDueAutomaticRetryRejectsBusyBeforeBundleDecode(t *testing.T) {
	f := deferredReviewFixture(t, true)
	ctx := context.Background()
	f.expire(t)
	if err := f.a.Store.Get(ctx, "run", f.run.ID, &f.run); err != nil {
		t.Fatal(err)
	}
	saved := f.run
	var heavy map[string]any
	if err := json.Unmarshal(raw(saved), &heavy); err != nil {
		t.Fatal(err)
	}
	heavy["skills"] = strings.Repeat("unused accepted bundle ", 1<<18)
	heavy["profile"] = "wrong-shaped unused profile"
	if err := f.a.Store.Put(ctx, "run", saved.ID, heavy, ""); err != nil {
		t.Fatal(err)
	}
	f.a.Runs.mu.Lock()
	f.a.Runs.live["occupied"] = &session{run: domain.Run{ID: "occupied", Status: "running"}}
	f.a.Runs.mu.Unlock()
	f.a.autoReviewMu.Lock()
	_, _, err := f.a.Runs.startDeferredLaunch(ctx, saved)
	f.a.autoReviewMu.Unlock()
	if err == nil || err.Error() != "automatic review is waiting for active work" {
		t.Fatal("busy admission decoded an unused accepted bundle", err)
	}
	if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
		t.Fatal(err)
	}
	var after map[string]any
	if err := f.a.Store.Get(ctx, "run", saved.ID, &after); err != nil || string(raw(after)) != string(raw(heavy)) {
		t.Fatal("busy poll rewrote a projected candidate", err)
	}
	// Restore the valid accepted bundle; its full profile/context must survive
	// the eventual admitted claim, rather than the projected snapshot.
	if err := f.a.Store.Put(ctx, "run", saved.ID, saved, ""); err != nil {
		t.Fatal(err)
	}
	f.a.Runs.mu.Lock()
	delete(f.a.Runs.live, "occupied")
	f.a.Runs.mu.Unlock()
	f.budget.Store(100)
	if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
		t.Fatal(err)
	}
	f.assertSingleLaunch(t)
	if string(raw(saved.Profile)) != string(raw(f.run.Profile)) || string(raw(saved.Skills)) != string(raw(f.run.Skills)) || saved.Prompt != f.run.Prompt || saved.ReviewPromptSnapshot != f.run.ReviewPromptSnapshot || f.run.SourceSnapshot == nil {
		t.Fatal("admitted retry lost accepted payload")
	}
}

func TestAcceptedRetryStopsAcrossUncertainProviderCreation(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprint(disconnect), func(t *testing.T) {
			f := deferredReviewFixture(t, true)
			ctx := context.Background()
			f.budget.Store(100)
			f.expire(t)
			root := t.TempDir()
			entered, fifo := filepath.Join(root, "created"), filepath.Join(root, "release")
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			wrapper := filepath.Join(root, "codex")
			script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "app-server" ]; then
 echo created >> %q
 read release < %q
fi
exec %q "$@"
`, entered, fifo, f.a.Runs.Binary)
			if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			f.a.Runs.Binary = wrapper
			if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { _, err := os.Stat(entered); return err == nil })
			if err := f.a.Store.Get(ctx, "run", f.run.ID, &f.run); err != nil {
				t.Fatal(err)
			}
			if !f.run.LaunchRetryAt.IsZero() || f.run.ResumePending || f.run.ThreadID != "" {
				t.Fatal("provider created before durable non-replay boundary", f.run.Status)
			}
			if disconnect {
				live, err := f.a.Runs.get(f.run.ID)
				if err != nil {
					t.Fatal(err)
				}
				f.a.Runs.onMessage(live, codex.Message{Method: "process/closed"})
			}
			waitLaunchRetry(t, f.a.Runs.Close)
			f.a.Runs = NewManager(ctx, f.a, testutil.FakeCodex(t))
			if err := f.a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			if err := f.a.Runs.RetryDeferredLaunches(ctx); err != nil {
				t.Fatal(err)
			}
			if err := f.a.Store.Get(ctx, "run", f.run.ID, &f.run); err != nil {
				t.Fatal(err)
			}
			if f.run.Status != "interrupted" || f.run.ResumePending || !f.run.LaunchRetryAt.IsZero() {
				t.Fatal("uncertain startup regained retry authority", f.run.Status)
			}
			f.assertNoAgent(t)
			created, err := os.ReadFile(entered)
			if err != nil || string(created) != `created
` {
				t.Fatal("uncertain provider creation was replayed", err)
			}
			if claimCount(t, f.a) != 1 {
				t.Fatal("uncertain startup replaced its accepted claim")
			}
		})
	}
}
