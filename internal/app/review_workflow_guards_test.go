package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/prreview"
	"context"
	"encoding/base64"
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

type launchGuardWorkers struct {
	prreview.Workers
	reviews atomic.Int32
}

func (w *launchGuardWorkers) Review(ctx context.Context, binding prreview.Binding, handoff prreview.Handoff, correction string) ([]byte, error) {
	w.reviews.Add(1)
	return w.Workers.Review(ctx, binding, handoff, correction)
}

func TestReviewCaptureRejectsRemoteHeadDriftWithUnchangedSavedItem(t *testing.T) {
	a, item, workers, collector := retainedFixture(t)
	ctx := a.Runs.ctx
	guardWorkers := &launchGuardWorkers{Workers: workers}
	a.ReviewWorkers = guardWorkers
	run, err := a.Runs.PreparePRReview(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkout := collector.e.Checkout
	cmd := exec.Command("git", "-C", checkout, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.com", "commit", "--allow-empty", "-m", "Advance remote head")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	out, err := exec.Command("git", "-C", checkout, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	collector.e.Metadata.Head = strings.TrimSpace(string(out))
	collector.e.Decisions, err = prreview.PublishedDecisions(collector.e.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err = collector.e.Validate(); err != nil {
		t.Fatal("advanced remote fixture is invalid", err)
	}
	if err = collector.e.ValidateCheckout(ctx); err != nil {
		t.Fatal(err)
	}
	var savedItem domain.Item
	if err = a.Store.Get(ctx, "item", item.ID, &savedItem); err != nil {
		t.Fatal(err)
	}
	if run.SourceSnapshot == nil || run.SourceSnapshot.Head != item.Head || savedItem.Head != item.Head || collector.e.Metadata.Head == item.Head {
		t.Fatal("fixture did not preserve the accepted head while advancing remote evidence")
	}
	for _, resume := range []bool{false, true} {
		if _, err = a.Runs.Start(ctx, run.ID, resume); err != nil {
			t.Fatal(err)
		}
		var saved domain.Run
		waitFor(t, func() bool {
			if err := a.Store.Get(ctx, "run", run.ID, &saved); err != nil {
				t.Fatal(err)
			}
			return saved.Status == "failed" || saved.Status == "idle"
		})
		owner, unlock, err := a.Runs.lockRun(ctx, run.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		done := owner.reviewDone
		unlock()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			t.Fatal("rejected review execution did not stop")
		}
		if saved.Status != "failed" || !strings.Contains(saved.Error, "accepted launch") {
			t.Fatalf("remote drift did not reject the accepted launch: %+v", saved)
		}
		var retained retainedReview
		if err = a.Store.Get(ctx, "review-workflow", run.ID, &retained); err != nil {
			t.Fatal(err)
		}
		if collector.captures != 1 || workers.prepareCalls != 0 || guardWorkers.reviews.Load() != 0 || retained.Session.Preparation != nil || len(retained.Session.Reviews) != 0 || saved.ReviewArtifact != nil || saved.ThreadID != "" {
			t.Fatalf("remote drift reached preparation/review or retained a retargeted result: captures=%d preparations=%d reviews=%d run=%+v", collector.captures, workers.prepareCalls, guardWorkers.reviews.Load(), saved)
		}
	}
}

func TestDirectCommandReviewerRetainsExecutablePackageAfterSourceRemoval(t *testing.T) {
	a, _, _, _ := retainedFixture(t)
	dir := t.TempDir()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "scripts", "review.py")
	if err = os.MkdirAll(filepath.Dir(script), 0700); err != nil {
		t.Fatal(err)
	}
	const sibling = " \ttracked sibling \t"
	for name, body := range map[string]string{
		"scripts/review.py": fmt.Sprintf("#!%s\nimport helper,json,pathlib,sys\nassert json.load(open(sys.argv[1]))['contract']=='git-diff-v1'\nassert pathlib.Path(__file__).resolve().parent.parent.joinpath(%q).read_text()=='tracked package sibling'\nprint(json.dumps(helper.result))\n", python, sibling),
		"scripts/helper.py": "result={'findings':[]}\n",
		"pyproject.toml":    "[project]\nname='direct-fixture'\n",
		sibling:             "tracked package sibling",
	} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init"}, {"add", sibling, "scripts/helper.py", "pyproject.toml"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	if err = os.WriteFile(filepath.Join(dir, "owner-private.txt"), []byte("unrelated owner file"), 0600); err != nil {
		t.Fatal(err)
	}
	bindings, err := a.Runs.reviewBindings(a.Runs.ctx, []prreview.Reviewer{{ID: "direct", Kind: "command", Argv: []string{script, "{handoff}"}}})
	if err != nil {
		t.Fatal(err)
	}
	binding := bindings[0]
	if binding.Entrypoint != "bin/scripts/review.py" || binding.Reviewer.Argv[0] != "{package}/bin/scripts/review.py" || binding.Resources["bin/scripts/helper.py"] == "" {
		t.Fatalf("direct executable lost its package layout: entrypoint=%q argv=%v", binding.Entrypoint, binding.Reviewer.Argv)
	}
	for name := range binding.Resources {
		if strings.Contains(name, "owner-private") {
			t.Fatal("untracked owner file captured")
		}
	}
	if err = os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	output, err := (prreview.CommandWorkers{Root: t.TempDir()}).Review(a.Runs.ctx, binding, prreview.Handoff{Contract: "git-diff-v1", Evidence: prreview.Evidence{Checkout: t.TempDir()}}, "")
	if err != nil || !strings.Contains(string(output), "findings") {
		t.Fatal("retained direct executable cannot import its sibling after source removal", string(output), err)
	}
}

func TestRetainedReviewRevalidatesSourceLifecycleBeforeWork(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, state := range []string{"closed", "merged", "unavailable", "unknown"} {
			t.Run(fmt.Sprintf("resume=%t/%s", resume, state), func(t *testing.T) {
				a, item, workers, collector := retainedFixture(t)
				ctx := a.Runs.ctx
				guardWorkers := &launchGuardWorkers{Workers: workers}
				a.ReviewWorkers = guardWorkers
				run, err := a.Runs.PreparePRReview(ctx, item.ID)
				if err != nil {
					t.Fatal(err)
				}
				if resume {
					config, err := a.Config(ctx)
					if err != nil {
						t.Fatal(err)
					}
					var repo domain.Repository
					if err = a.Store.Get(ctx, "repository", item.Repository, &repo); err != nil {
						t.Fatal(err)
					}
					reviewers, rules := reviewSettings(config, repo)
					bindings, err := a.Runs.reviewBindings(ctx, reviewers)
					if err != nil {
						t.Fatal(err)
					}
					retained := retainedReview{Session: prreview.Session{ID: run.ID, Target: item.URL}, Bindings: bindings, Rules: rules, Configuration: prreview.Digest([]any{reviewers, rules, run.Profile, run.Skills})}
					if err = a.Store.Put(ctx, "review-workflow", run.ID, retained, ""); err != nil {
						t.Fatal(err)
					}
					run.Status = "interrupted"
					if err = a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
						t.Fatal(err)
					}
				}
				var current atomic.Value
				current.Store("open")
				var calls atomic.Int32
				entered, release := make(chan struct{}, 1), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/repos/owner/repo/pulls/2" {
						t.Error("unexpected source-validation request", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					calls.Add(1)
					value := current.Load().(string)
					if value != "open" {
						select {
						case entered <- struct{}{}:
						default:
						}
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
					}
					if value == "unavailable" {
						w.WriteHeader(503)
						return
					}
					merged := value == "merged"
					if merged {
						value = "closed"
					}
					fmt.Fprintf(w, `{"number":2,"state":%q,"merged":%t,"user":{"login":%q},"head":{"sha":%q}}`, value, merged, item.Author, item.Head)
				}))
				defer server.Close()
				defer unblock()
				live := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
				live.ConfigureUsage(nil, func(context.Context) (providers.GitHubPolicy, error) {
					return providers.GitHubPolicy{RequestsPerHour: 100}, nil
				})
				a.GitHub = providers.NewCachedGitHub(live)
				if _, err = a.GitHub.CurrentWorkItem(ctx, item); err != nil {
					t.Fatal("could not warm open-source cache", err)
				}
				current.Store(state)
				started := make(chan error, 1)
				go func() {
					_, err := a.Runs.Start(ctx, run.ID, resume)
					started <- err
				}()
				select {
				case err := <-started:
					if err != nil {
						t.Fatal("asynchronous source validation rejected admission", err)
					}
				case <-time.After(2 * time.Second):
					unblock()
					select {
					case <-started:
					case <-time.After(8 * time.Second):
						t.Fatal("blocked Start did not stop after source release")
					}
					t.Fatal("Start blocked on asynchronous source validation")
				}
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("retained start did not freshly validate its source")
				}
				unblock()
				var saved domain.Run
				waitFor(t, func() bool {
					if err := a.Store.Get(ctx, "run", run.ID, &saved); err != nil {
						t.Fatal(err)
					}
					return saved.Status == "terminated" || saved.Status == "failed" || saved.Status == "idle"
				})
				waitGuardReviewDone(t, a, run.ID)
				wantStatus, wantError := "terminated", "PR is "+state
				if state == "unavailable" || state == "unknown" {
					wantStatus, wantError = "failed", "cannot verify current work item"
				}
				if saved.Status != wantStatus || !strings.Contains(saved.Error, wantError) || saved.ResumePending || saved.ReviewComplete || saved.ReviewArtifact != nil || saved.ThreadID != "" {
					t.Fatalf("unusable source reached retained work: status=%s error=%q", saved.Status, saved.Error)
				}
				if saved.SourceSnapshot == nil || saved.SourceSnapshot.Head != item.Head || saved.Prompt != run.Prompt || saved.Worktree != run.Worktree || calls.Load() != 2 || collector.captures != 0 || workers.prepareCalls != 0 || guardWorkers.reviews.Load() != 0 {
					t.Fatalf("source check changed accepted identity or started child work: calls=%d captures=%d prepare=%d review=%d", calls.Load(), collector.captures, workers.prepareCalls, guardWorkers.reviews.Load())
				}
				var cached domain.Item
				if err = a.Store.Get(ctx, "item", item.ID, &cached); err != nil || cached.State != item.State || cached.Head != item.Head {
					t.Fatal("fixture unexpectedly updated saved source state", cached.State, err)
				}
			})
		}
	}
}

func waitGuardReviewDone(t *testing.T, a *App, id string) {
	t.Helper()
	owner, unlock, err := a.Runs.lockRun(a.Runs.ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	done := owner.reviewDone
	unlock()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("retained review execution did not stop")
	}
}

func TestReviewTerminationCancelsConcurrentStartAndResume(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume=%t", resume), func(t *testing.T) {
			a, item, workers, _ := retainedFixture(t)
			ctx := a.Runs.ctx
			workers.second = make(chan struct{})
			workers.release = make(chan struct{})
			run, err := a.Runs.PreparePRReview(ctx, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if resume {
				if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
					t.Fatal(err)
				}
				waitRetainedReviewValue(t, workers.second, "initial review worker")
				if err = a.Runs.Interrupt(ctx, run.ID); err != nil {
					t.Fatal(err)
				}
				waitGuardReviewDone(t, a, run.ID)
				workers.second = make(chan struct{})
				workers.once = sync.Once{}
			}

			owner, unlock, err := a.Runs.lockRun(ctx, run.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			observed, release := make(chan struct{}), make(chan struct{})
			observe := sync.OnceFunc(func() { close(observed) })
			unblock := sync.OnceFunc(func() { close(release) })
			owner.cancelMu.Lock()
			previousCancel := owner.cancel
			owner.cancel = func() {
				previousCancel()
				observe()
				<-release
			}
			owner.cancelMu.Unlock()
			unlock()

			// Hold termination after it captures the previous execution's handle.
			// Admission then replaces that handle before termination locks the owner.
			stopped := make(chan error, 1)
			terminationDone := make(chan struct{})
			t.Cleanup(func() {
				unblock()
				select {
				case <-terminationDone:
				case <-time.After(8 * time.Second):
					t.Error("source termination did not stop during cleanup")
				}
			})
			go func() {
				defer close(terminationDone)
				stopped <- a.Runs.terminateRun(ctx, run.ID, "source closed during admission")
			}()
			waitRetainedReviewValue(t, observed, "termination observing the previous lifetime")
			if _, err = a.Runs.Start(ctx, run.ID, resume); err != nil {
				t.Fatal(err)
			}
			waitRetainedReviewValue(t, workers.second, "replacement review worker")
			unblock()
			if err = waitRetainedReviewValue(t, stopped, "source termination"); err != nil {
				t.Fatal(err)
			}
			// Completion must follow cancellation without releasing the worker or
			// closing the manager, either of which could hide a stale handle.
			waitGuardReviewDone(t, a, run.ID)
			owner, unlock, err = a.Runs.lockRun(ctx, run.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			lifetimeErr := owner.ctx.Err()
			unlock()
			var saved domain.Run
			if err = a.Store.Get(ctx, "run", run.ID, &saved); err != nil {
				t.Fatal(err)
			}
			if lifetimeErr != context.Canceled || saved.Status != "terminated" || saved.ResumePending || !saved.InterruptRequested || saved.ReviewComplete || saved.ReviewArtifact != nil {
				t.Fatalf("termination missed the admitted execution: lifetime=%v run=%+v", lifetimeErr, saved)
			}
		})
	}
}

func TestCommandCaptureRejectsOversizeSelectedExecutableAndPackage(t *testing.T) {
	for _, packaged := range []bool{false, true} {
		t.Run(fmt.Sprintf("packaged=%t", packaged), func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "a-review")
			if err := os.WriteFile(script, []byte("x"), 0700); err != nil {
				t.Fatal(err)
			}
			if packaged {
				for _, args := range [][]string{{"init"}} {
					cmd := exec.Command("git", args...)
					cmd.Dir = dir
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatal(string(out), err)
					}
				}
			}
			if err := os.Truncate(script, (32<<20)+1); err != nil {
				t.Fatal(err)
			}
			if _, err := (&Manager{}).reviewBindings(context.Background(), []prreview.Reviewer{{ID: "oversize", Kind: "command", Argv: []string{script, "{handoff}"}}}); err == nil || !strings.Contains(err.Error(), "32 MiB") {
				t.Fatal("oversized selected executable was accepted", err)
			}
		})
	}
	// Keep the tracked sibling small in Git, then grow its working file sparsely.
	// Reading the selected byte first must reduce the shared package budget.
	dir := t.TempDir()
	script, sibling := filepath.Join(dir, "a-review"), filepath.Join(dir, "z-helper")
	for _, name := range []string{script, sibling} {
		if err := os.WriteFile(name, []byte("x"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init"}, {"add", "z-helper"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	if err := os.Truncate(sibling, 32<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Manager{}).reviewBindings(context.Background(), []prreview.Reviewer{{ID: "combined", Kind: "command", Argv: []string{script, "{handoff}"}}}); err == nil || !strings.Contains(err.Error(), "32 MiB") {
		t.Fatal("selected untracked executable was not counted with tracked siblings", err)
	}
}

func TestCommandCaptureRejectsNonregularPackageResources(t *testing.T) {
	const probeEnv = "CODE_FACTORY_CAPTURE_FIFO_PROBE"
	if script := os.Getenv(probeEnv); script != "" {
		bindings, err := (&Manager{}).reviewBindings(context.Background(), []prreview.Reviewer{{ID: "fifo", Kind: "command", Argv: []string{script, "{handoff}"}}})
		if err == nil || !strings.Contains(err.Error(), "invalid command reviewer package resource") || len(bindings) != 0 {
			t.Fatalf("FIFO package resource was not rejected: bindings=%v err=%v", bindings, err)
		}
		return
	}
	for _, marker := range []string{"package.json", "pyproject.toml"} {
		t.Run(marker, func(t *testing.T) {
			dir := t.TempDir()
			script, sibling := filepath.Join(dir, "review"), filepath.Join(dir, "z-helper")
			contents := map[string]string{"review": "#!/bin/sh\nexit 0\n", marker: "{}\n", "z-helper": "regular sibling"}
			for name, body := range contents {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Chmod(sibling, 0640); err != nil {
				t.Fatal(err)
			}
			reviewers := []prreview.Reviewer{{ID: "regular", Kind: "command", Argv: []string{script, "{handoff}"}}}
			bindings, err := (&Manager{}).reviewBindings(context.Background(), reviewers)
			if err != nil || len(bindings) != 1 {
				t.Fatal("regular non-Git package rejected", bindings, err)
			}
			binding := bindings[0]
			if len(binding.Resources) != len(contents) || binding.Entrypoint != "bin/review" || binding.Modes["bin/z-helper"] != 0640 {
				t.Fatalf("regular package lost its resources, entrypoint or mode: %+v", binding)
			}
			for name, body := range contents {
				if binding.Resources["bin/"+name] != base64.StdEncoding.EncodeToString([]byte(body)) {
					t.Fatalf("regular package resource %q changed", name)
				}
			}
			if err := os.Remove(sibling); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(sibling, 0600); err != nil {
				t.Fatal(err)
			}
			// Isolate a blocking open so the regression fails within a bound and
			// leaves no stuck capture goroutine or owner mutex in this process.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCommandCaptureRejectsNonregularPackageResources$")
			cmd.Env = append(os.Environ(), probeEnv+"="+script)
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("FIFO package capture blocked: %v\n%s", ctx.Err(), out)
			}
			if err != nil {
				t.Fatalf("FIFO package capture failed: %v\n%s", err, out)
			}
		})
	}
}

func TestCommandCaptureHonorsExactPackageBoundaryAndBoundedReads(t *testing.T) {
	t.Run("raw NUL-delimited tracked filenames", func(t *testing.T) {
		dir := t.TempDir()
		script := filepath.Join(dir, "review")
		const sibling = " \t\ntracked sibling \t\n"
		for _, name := range []string{"review", sibling, "owner-private.txt"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0700); err != nil {
				t.Fatal(err)
			}
		}
		for _, args := range [][]string{{"init"}, {"add", sibling}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatal(string(out), err)
			}
		}
		binding := prreview.Binding{Resources: map[string]string{}, Modes: map[string]uint32{}}
		if err := captureCommandPackage(dir, "bin", script, &binding); err != nil {
			t.Fatal(err)
		}
		if len(binding.Resources) != 2 || binding.Resources["bin/"+sibling] != base64.StdEncoding.EncodeToString([]byte(sibling)) || binding.Resources["bin/review"] == "" {
			t.Fatalf("raw tracked filename or private file boundary lost: resources=%v", binding.Resources)
		}
	})
	t.Run("exact 32 MiB including selected untracked entrypoint", func(t *testing.T) {
		dir := t.TempDir()
		script, sibling := filepath.Join(dir, "a-review"), filepath.Join(dir, "z-helper")
		for _, name := range []string{script, sibling} {
			if err := os.WriteFile(name, []byte("x"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		for _, args := range [][]string{{"init"}, {"add", "z-helper"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatal(string(out), err)
			}
		}
		if err := os.Truncate(sibling, (32<<20)-1); err != nil {
			t.Fatal(err)
		}
		binding := prreview.Binding{Resources: map[string]string{}, Modes: map[string]uint32{}}
		if err := captureCommandPackage(dir, "bin", script, &binding); err != nil {
			t.Fatal("exact command package limit rejected", err)
		}
		resources := binding.Resources
		if len(resources) != 2 || len(resources["bin/a-review"]) != base64.StdEncoding.EncodedLen(1) || len(resources["bin/z-helper"]) != base64.StdEncoding.EncodedLen((32<<20)-1) {
			t.Fatal("exact boundary omitted or truncated package resources")
		}
	})
	t.Run("read growth cannot consume beyond remaining budget plus sentinel", func(t *testing.T) {
		reader := strings.NewReader("123456789")
		if _, err := readCommandBytes(reader, 4); err == nil || reader.Len() != 4 {
			t.Fatal("growing input was accepted or read beyond the overflow sentinel", reader.Len(), err)
		}
		data, err := readCommandBytes(strings.NewReader("1234"), 4)
		if err != nil || string(data) != "1234" {
			t.Fatal("exact remaining read budget was rejected", string(data), err)
		}
	})
	t.Run("stat rejects before retaining executable bytes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "review")
		if err := os.WriteFile(path, []byte("12345"), 0700); err != nil {
			t.Fatal(err)
		}
		binding := prreview.Binding{Resources: map[string]string{}, Modes: map[string]uint32{}}
		if _, err := captureCommandResource(path, "bin/review", 4, &binding); err == nil || len(binding.Resources) != 0 || len(binding.Modes) != 0 {
			t.Fatal("oversized resource was retained", err)
		}
	})
}
