package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/prreview"
	"codefactory/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitRetainedReviewValue[T any](t *testing.T, ch <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

type retainedCollector struct {
	validateHook func()
	invalid      bool
	e            prreview.Evidence
	mu           sync.Mutex
	captures     int
}

func (f *retainedCollector) Capture(context.Context, string, string) (prreview.Evidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captures++
	return f.e, nil
}
func (f *retainedCollector) ValidateCurrent(context.Context, prreview.Evidence) error {
	f.mu.Lock()
	invalid, hook := f.invalid, f.validateHook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if invalid {
		return errors.New("remote evidence changed")
	}
	return nil
}

type retainedWorkers struct {
	ignoreCancellation bool
	analysis           prreview.Analysis
	second             chan struct{}
	release            chan struct{}
	once               sync.Once
	prepareCalls       int
	reviewCalls        int
	completionCalls    int
	secondErr          error
	findings           bool
}

func (w *retainedWorkers) Prepare(_ context.Context, in prreview.PreparationInput, _ string) ([]byte, error) {
	w.prepareCalls++
	a := w.analysis
	if len(in.Answers) > 0 {
		a.Questions = append([]prreview.Question{}, a.Questions...)
		for i := range a.Questions {
			a.Questions[i].Status = "resolved"
			a.Questions[i].Resolution = "Owner selected narrow scope"
		}
	}
	return json.Marshal(a)
}
func (*retainedWorkers) MaterialChoice(context.Context, prreview.PreparationInput, prreview.Analysis) (prreview.MaterialCheck, error) {
	return prreview.MaterialCheck{Required: false, Rationale: "Independent material scope assessment", Evidence: []string{"pr/body"}}, nil
}
func (w *retainedWorkers) Review(ctx context.Context, b prreview.Binding, _ prreview.Handoff, _ string) ([]byte, error) {
	w.reviewCalls++
	if b.Reviewer.ID == "two" && w.secondErr != nil {
		return nil, w.secondErr
	}
	if b.Reviewer.ID == "two" && w.second != nil {
		w.once.Do(func() { close(w.second) })
		if w.ignoreCancellation {
			<-w.release
		} else {
			select {
			case <-w.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	if w.findings && b.Reviewer.ID == "one" {
		return []byte(`{"findings":[{"severity":"Major","path":"a.go","line":2,"message":"Lost state","fix":"Retain state"}]}`), nil
	}
	return []byte(`{"findings":[]}`), nil
}
func (w *retainedWorkers) Completion(context.Context, prreview.Preparation, prreview.Result) (prreview.CompletionAssessment, error) {
	w.completionCalls++
	a := prreview.CompletionAssessment{Criteria: map[string]prreview.CompletionCheck{}}
	for _, k := range []string{"scope", "remediation_depth", "placement", "test_sufficiency", "cumulative_complexity"} {
		a.Criteria[k] = prreview.CompletionCheck{Verified: true, Rationale: "Compared code and actual assertions with published scope", Evidence: []string{"pr/body"}}
	}
	return a, nil
}
func retainedFixture(t *testing.T) (*App, domain.Item, *retainedWorkers, *retainedCollector) {
	t.Helper()
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	var repo domain.Repository
	_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
	data, err := os.ReadFile("../prreview/testdata/parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Patch    string            `json:"patch"`
		Analysis prreview.Analysis `json:"analysis"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", repo.Checkout, "rev-parse", "HEAD^{tree}").Output()
	if err != nil {
		t.Fatal(err)
	}
	e := prreview.Evidence{Metadata: prreview.Metadata{URL: pr.URL, NodeID: "PR_fixture", Base: pr.Head, Head: pr.Head, Tree: strings.TrimSpace(string(out)), Body: pr.Body, Author: pr.Author, ChangedFiles: 1}, Files: []prreview.File{{Path: "a.go", Additions: 2, Deletions: 1, Status: "modified"}}, Patch: fixture.Patch, Checkout: repo.Checkout, Repository: filepath.Join(t.TempDir(), "source"), Scope: map[string]json.RawMessage{}, Sources: map[string]string{"pr/body": pr.Body}}
	e.Decisions, err = prreview.PublishedDecisions(e.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	skillDir := t.TempDir()
	path := filepath.Join(skillDir, "SKILL.md")
	if err = os.WriteFile(path, []byte("---\nname: fixture\ndescription: Review fixture\n---\nReview pinned evidence."), 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Put(ctx, "skill", "fixture", domain.Skill{ID: "fixture", Name: "fixture", Path: path}, ""); err != nil {
		t.Fatal(err)
	}
	config, _ := a.Config(ctx)
	config.Reviewers = []prreview.Reviewer{{ID: "one", Kind: "skill", SkillID: "fixture"}, {ID: "two", Kind: "skill", SkillID: "fixture"}}
	if err = a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	workers := &retainedWorkers{analysis: fixture.Analysis}
	collector := &retainedCollector{e: e}
	a.ReviewEngine = nil
	a.ReviewWorkers = workers
	a.ReviewCollector = collector
	return a, pr, workers, collector
}

type unavailableReviewEngine struct{ prreview.FixtureAdapter }

func (unavailableReviewEngine) Check(context.Context) error {
	return errors.New("missing " + prreview.OrderedIteration)
}
func TestFreshReviewDependencyDiagnosticAndLaunchPolicySnapshot(t *testing.T) {
	a, pr, _, collector := retainedFixture(t)
	ctx := a.Runs.ctx
	a.ReviewEngine = unavailableReviewEngine{}
	on, off := true, false
	c, _ := a.Config(ctx)
	c.AutoPostReview = true
	if err := a.SaveConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	var repo domain.Repository
	_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
	repo.AutoPostReview = &off
	if err := a.SaveRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveReviewPolicy(ctx, pr.ID, &on); err != nil {
		t.Fatal(err)
	}
	run, err := a.Runs.PreparePRReviewPolicy(ctx, pr.ID, &off)
	if err != nil {
		t.Fatal(err)
	}
	if run.ReviewWorkflow != prreview.Contract || run.EffectiveAutoPostReview.Source != "launch" || run.EffectiveAutoPostReview.Enabled {
		t.Fatal(run)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err == nil || !strings.Contains(err.Error(), prreview.OrderedIteration) {
		t.Fatal(err)
	}
	var saved domain.Run
	_ = a.Store.Get(ctx, "run", run.ID, &saved)
	if saved.Status != "prepared" || saved.ThreadID != "" || collector.captures != 0 {
		t.Fatal("dependency failure started work", saved)
	}
	if _, err = a.SaveReviewPolicy(ctx, pr.ID, nil); err != nil {
		t.Fatal(err)
	}
	policy, err := a.ReviewPostingPolicy(ctx, pr.ID, nil)
	if err != nil || policy.Source != "repository" || policy.Enabled {
		t.Fatal(policy, err)
	}
}
func TestRetainedReviewChildCompletionAndRestartReuse(t *testing.T) {
	a, pr, workers, _ := retainedFixture(t)
	ctx := a.Runs.ctx
	workers.second = make(chan struct{})
	workers.release = make(chan struct{})
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitRetainedReviewValue(t, workers.second, "second reviewer")
	var saved domain.Run
	_ = a.Store.Get(ctx, "run", run.ID, &saved)
	if saved.Status != "running" || saved.ReviewComplete || saved.ReviewArtifact != nil || saved.ThreadID != "" {
		t.Fatal("child completion completed parent", saved)
	}
	if err = a.Runs.Interrupt(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	a.Runs.Close()
	a.Runs = NewManager(context.Background(), a, "")
	if err = a.Runs.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(workers.release)
	if _, err = a.Runs.Start(context.Background(), run.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(context.Background(), "run", run.ID, &saved)
		return saved.Status == "idle" || saved.Status == "failed"
	})
	if saved.Status != "idle" || !saved.ReviewComplete || saved.ReviewArtifact == nil || saved.ReviewResult == nil || len(saved.ReviewResult.Reused) != 1 || saved.ReviewResult.Reused[0] != "one" {
		t.Fatalf("retained restart: %+v", saved)
	}
}
func TestRetainedReviewQuestionsSurviveRestartAndAnswerOnce(t *testing.T) {
	a, pr, workers, _ := retainedFixture(t)
	workers.analysis.Questions = []prreview.Question{{ID: "scope", Status: "open", Material: true, Statements: "Published scope conflicts with discussion", AffectedWork: "Review placement", Recommendation: "Keep narrow scope", ProposedChanges: "Select review scope", Options: []prreview.Option{{Choice: "Narrow", Benefits: "Limited changes", Costs: "Deferred work"}, {Choice: "Wide", Benefits: "More coverage", Costs: "More complexity"}}, Evidence: []string{"pr/body"}}}
	run, err := a.Runs.PreparePRReview(a.Runs.ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(a.Runs.ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	var at domain.Attention
	waitFor(t, func() bool {
		rows, _ := store.List[domain.Attention](context.Background(), a.Store, "attention")
		if len(rows) == 1 {
			at = rows[0]
			return at.Status == "pending"
		}
		return false
	})
	a.Runs.Close()
	a.Runs = NewManager(context.Background(), a, "")
	if err = a.Runs.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Get(context.Background(), "attention", at.ID, &at); err != nil || at.Status != "pending" || at.Origin != "workflow" {
		t.Fatal("workflow question lost", at, err)
	}
	answer := json.RawMessage(`{"answers":{"scope":{"answers":["Narrow"]}}}`)
	if err = a.Runs.Answer(context.Background(), at.ID, answer); err != nil {
		t.Fatal(err)
	}
	if err = a.Runs.Answer(context.Background(), at.ID, answer); err == nil {
		t.Fatal("duplicate answer accepted")
	}
	var saved domain.Run
	waitFor(t, func() bool {
		_ = a.Store.Get(context.Background(), "run", run.ID, &saved)
		return saved.Status == "idle" || saved.Status == "failed"
	})
	if saved.Status != "idle" || !saved.ReviewComplete {
		t.Fatal(saved)
	}
}
func TestOneTimeDKImportPreservesFactoryValuesAndReportsConflicts(t *testing.T) {
	a, pr, _, _ := retainedFixture(t)
	ctx := a.Runs.ctx
	root := t.TempDir()
	folder := filepath.Join(root, prreview.Hash([]byte("remote:github.com/" + pr.Repository))[:24])
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "reviewers.json"), []byte(`{"reviewers":[{"command":["/bin/echo","{handoff}"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "rules.json"), []byte(`{"rules":[{"text":"Use comment reviews."}],"conflicts":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var repo domain.Repository
	_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
	existing := "Keep Factory rules"
	repo.ReviewRules = &existing
	if err := a.SaveRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	imported, err := a.importDKReviews(ctx, pr.Repository, root)
	if err != nil {
		t.Fatal(err)
	}
	if !imported.Completed || len(imported.Imported) != 1 || len(imported.Conflicts) != 1 {
		t.Fatal(imported)
	}
	_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
	if *repo.ReviewRules != existing || repo.Reviewers == nil || len(*repo.Reviewers) != 1 {
		t.Fatal(repo)
	}
	_ = os.Remove(filepath.Join(folder, "reviewers.json"))
	again, err := a.importDKReviews(ctx, pr.Repository, root)
	if err != nil || prreview.Digest(again) != prreview.Digest(imported) {
		t.Fatal("import repeated", again, err)
	}
}
func TestOneTimeGlobalDKImportUsesUserRulesAndPreservesConflicts(t *testing.T) {
	for _, mode := range []string{"import", "DK conflict", "Factory settings"} {
		t.Run(mode, func(t *testing.T) {
			a, ctx := fixture(t)
			root := t.TempDir()
			user := filepath.Join(root, "user")
			if err := os.Mkdir(user, 0700); err != nil {
				t.Fatal(err)
			}
			rules := `{"rules":[{"text":"Review private changes.","scope":"review"}],"conflicts":[]}`
			if mode == "DK conflict" {
				rules = `{"rules":[{"text":"Review private changes."}],"conflicts":[{"text":"Unresolved private rule"}]}`
			}
			for path, data := range map[string]string{
				filepath.Join(root, "reviewers.json"): `{"reviewers":[{"command":["/bin/echo","{handoff}"]}]}`,
				filepath.Join(root, "rules.json"):     `{"rules":[{"text":"Wrong root rules."}],"conflicts":[]}`,
				filepath.Join(user, "rules.json"):     rules,
			} {
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			config, err := a.Config(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "Factory settings" {
				config.Reviewers = []prreview.Reviewer{{ID: "factory", Kind: "command", Argv: []string{"/bin/true", "{handoff}"}}}
				config.ReviewRules = "Keep Factory rules"
				if err := a.SaveConfig(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			imported, err := a.importDKReviews(ctx, "", root)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := a.Config(ctx)
			if err != nil || !imported.Completed || imported.Scope != "global" {
				t.Fatal("global import failed", imported, err)
			}
			switch mode {
			case "import":
				if strings.Join(imported.Imported, ",") != "reviewers,reviewRules" || len(imported.Conflicts) != 0 || saved.ReviewRules != "[review] Review private changes.\n" || !saved.ReviewRulesConfigured {
					t.Fatal("global user rules not imported", imported, saved)
				}
			case "DK conflict":
				if strings.Join(imported.Imported, ",") != "reviewers" || len(imported.Conflicts) != 1 || !strings.Contains(imported.Conflicts[0], "explicit reconciliation") || saved.ReviewRules != "" || saved.ReviewRulesConfigured {
					t.Fatal("unresolved global rules imported", imported, saved)
				}
			case "Factory settings":
				if len(imported.Imported) != 0 || strings.Join(imported.Conflicts, ",") != "Code Factory global reviewers preserved,Code Factory global review rules preserved" || prreview.Digest(saved.Reviewers) != prreview.Digest(config.Reviewers) || saved.ReviewRules != config.ReviewRules {
					t.Fatal("Factory global settings replaced", imported, saved)
				}
			}
			if mode != "Factory settings" && (len(saved.Reviewers) != 1 || strings.Join(saved.Reviewers[0].Argv, ",") != "/bin/echo,{handoff}" || !saved.ReviewersConfigured) {
				t.Fatal("global root reviewer defaults not imported", saved)
			}
			data, err := os.ReadFile(filepath.Join(user, "rules.json"))
			if err != nil || string(data) != rules {
				t.Fatal("private DK rules modified", string(data), err)
			}
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			again, err := a.importDKReviews(ctx, "", root)
			if err != nil || prreview.Digest(again) != prreview.Digest(imported) {
				t.Fatal("global import repeated after source removal", again, err)
			}
			unchanged, err := a.Config(ctx)
			if err != nil || prreview.Digest(unchanged) != prreview.Digest(saved) {
				t.Fatal("repeated global import changed settings", unchanged, err)
			}
		})
	}
}
func TestAutomaticReviewUsesNewWorkflowAndNeverNativeFallback(t *testing.T) {
	a, pr, _, collector := retainedFixture(t)
	ctx := a.Runs.ctx
	a.ReviewEngine = unavailableReviewEngine{}
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
		t.Fatal(claims)
	}
	var run domain.Run
	_ = a.Store.Get(ctx, "run", claims[0].RunID, &run)
	if run.ItemID != pr.ID || run.ReviewWorkflow != prreview.Contract || run.ThreadID != "" || collector.captures != 0 || claims[0].Status != "blocked" {
		t.Fatal("automatic review bypassed engine boundary", run, claims)
	}
	var op prreview.Operation
	if err := a.Store.Get(ctx, "review-publication", "forged", &op); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}

type retainedPostRemote struct {
	fail      error
	lost      bool
	snapshot  prreview.Snapshot
	calls     int
	mutations int
	hook      func(int)
}

func (r *retainedPostRemote) Snapshot(context.Context, string) (prreview.Snapshot, error) {
	if r.fail != nil {
		return prreview.Snapshot{}, r.fail
	}
	r.calls++
	if r.hook != nil {
		r.hook(r.calls)
	}
	b, _ := json.Marshal(r.snapshot)
	var out prreview.Snapshot
	_ = json.Unmarshal(b, &out)
	return out, nil
}
func (r *retainedPostRemote) Submit(_ context.Context, _ string, p prreview.Payload) (string, error) {
	r.mutations++
	v := p.Variables.Input
	r.snapshot.Reviews = append(r.snapshot.Reviews, prreview.RemoteReview{ID: "receipt", URL: r.snapshot.Metadata.URL + "#receipt", Author: r.snapshot.Author, Head: v.Head, State: "COMMENTED", Body: v.Body, Threads: v.Threads})
	if r.lost {
		return "", errors.New("fixture lost response after remote submission")
	}
	return "receipt", nil
}

// Like ReviewGitHub.ReadEvidence, the source includes submitted reviews in scope;
// the same fake remote actually appends the approved review during Submit.
type retainedReviewSource struct {
	fixture *retainedCollector
	remote  *retainedPostRemote
}

func (s retainedReviewSource) ReadEvidence(ctx context.Context, target string) (prreview.Evidence, error) {
	live, err := s.remote.Snapshot(ctx, target)
	if err != nil {
		return prreview.Evidence{}, err
	}
	data, _ := json.Marshal(s.fixture.e)
	var evidence prreview.Evidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return evidence, err
	}
	evidence.Metadata = live.Metadata
	evidence.Scope["pr-reviews"], err = json.Marshal(live.Reviews)
	return evidence, err
}

type scopedRetainedCollector struct {
	prreview.GitCollector
	fixture *retainedCollector
}

func (c scopedRetainedCollector) Capture(ctx context.Context, target, root string) (prreview.Evidence, error) {
	return c.fixture.Capture(ctx, target, root)
}
func TestAutomaticPostingRechecksConcurrentPolicyAndLaunchPrecedence(t *testing.T) {
	for _, mode := range []string{"disabled-during-review", "disabled-before-submit", "launch-true", "launch-false"} {
		t.Run(mode, func(t *testing.T) {
			a, pr, workers, collector := retainedFixture(t)
			ctx := a.Runs.ctx
			workers.findings = true
			workers.second = make(chan struct{})
			workers.release = make(chan struct{})
			remote := &retainedPostRemote{snapshot: prreview.Snapshot{Metadata: collector.e.Metadata, Patch: collector.e.Patch, Author: "me", Reviews: []prreview.RemoteReview{}}}
			a.ReviewRemote = remote
			c, _ := a.Config(ctx)
			c.AutoPostReview = true
			if err := a.SaveConfig(ctx, c); err != nil {
				t.Fatal(err)
			}
			on, off := true, false
			var launch *bool
			if mode == "launch-true" {
				launch = &on
			}
			if mode == "launch-false" {
				launch = &off
			}
			run, err := a.Runs.PreparePRReviewPolicy(ctx, pr.ID, launch)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			waitRetainedReviewValue(t, workers.second, "second reviewer")
			if mode == "disabled-during-review" || mode == "launch-true" {
				if _, err = a.SaveReviewPolicy(ctx, pr.ID, &off); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "disabled-before-submit" {
				remote.hook = func(n int) {
					if n == 2 {
						if _, err := a.SaveReviewPolicy(context.Background(), pr.ID, &off); err != nil {
							t.Error(err)
						}
					}
				}
			}
			close(workers.release)
			var saved domain.Run
			waitFor(t, func() bool {
				_ = a.Store.Get(context.Background(), "run", run.ID, &saved)
				return saved.Status == "idle" || saved.Status == "failed"
			})
			if saved.Status != "idle" || !saved.ReviewComplete {
				t.Fatalf("review result lost: %+v", saved)
			}
			if mode == "launch-true" {
				if saved.PostingState != "verified" || saved.PostingReceipt == nil || saved.PostingReceipt.Comments != 1 || remote.mutations != 1 {
					t.Fatalf("launch override lost: %+v", saved)
				}
			} else if remote.mutations != 0 || saved.PostingReceipt != nil {
				t.Fatal("policy change or explicit false ignored", saved, remote.mutations)
			}
			if mode == "disabled-before-submit" && saved.PostingState != "disabled" {
				t.Fatal("concurrent policy rejection not visible", saved)
			}
			if _, err = a.PostRetainedReview(context.Background(), PostRetainedReviewInput{RunID: saved.ID, Artifact: *saved.ReviewArtifact, Target: "https://github.com/other/repo/pull/1"}); err == nil {
				t.Fatal("target mismatch accepted")
			}
		})
	}
}

func TestReviewInterruptResumeJoinsOldExecution(t *testing.T) {
	a, pr, workers, _ := retainedFixture(t)
	ctx := a.Runs.ctx
	workers.second = make(chan struct{})
	workers.release = make(chan struct{})
	workers.ignoreCancellation = true
	release := sync.OnceFunc(func() { close(workers.release) })
	t.Cleanup(func() {
		release()
		waitLaunchRetry(t, a.Runs.Close)
	})
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitRetainedReviewValue(t, workers.second, "second reviewer")
	interrupted := make(chan error, 1)
	go func() { interrupted <- a.Runs.Interrupt(ctx, run.ID) }()
	waitFor(t, func() bool {
		var current domain.Run
		_ = a.Store.Get(ctx, "run", run.ID, &current)
		return current.InterruptRequested
	})
	select {
	case err := <-interrupted:
		t.Fatalf("interrupt returned before worker joined: %v", err)
	default:
	}
	if _, err = a.Runs.Start(ctx, run.ID, true); err == nil || !strings.Contains(err.Error(), "still stopping") {
		t.Fatal("replacement overlapped old worker", err)
	}
	release()
	if err := waitRetainedReviewValue(t, interrupted, "joined interrupt"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		owner, _ := a.Runs.get(run.ID)
		owner.mu.Lock()
		defer owner.mu.Unlock()
		select {
		case <-owner.reviewResult.done:
			return true
		default:
			return false
		}
	})
	if _, err = a.Runs.QueueMessage(ctx, run.ID, "Review an additional subsystem", ""); err == nil {
		t.Fatal("new engineering instructions reused the old review")
	}
	delivery, err := a.Runs.QueueMessage(ctx, run.ID, "continue", "")
	if err != nil || delivery.State != "delivered" {
		t.Fatal("continuation control did not resume the retained workflow", delivery, err)
	}
	var saved domain.Run
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &saved)
		return saved.Status == "idle" || saved.Status == "failed"
	})
	if saved.Status != "idle" || !saved.ReviewComplete {
		t.Fatal(saved)
	}
}
func TestCommandReviewerCapturesTrackedPackageAndPreservesStandaloneScope(t *testing.T) {
	a, _, _, _ := retainedFixture(t)
	ctx := a.Runs.ctx
	dir := t.TempDir()
	script := filepath.Join(dir, "review.py")
	helper := filepath.Join(dir, "helper.py")
	_ = os.WriteFile(script, []byte("import helper,json,sys\nassert json.load(open(sys.argv[1]))['contract']=='git-diff-v1'\nprint(json.dumps(helper.result))\n"), 0600)
	_ = os.WriteFile(helper, []byte("result={'findings':[]}\n"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname='fixture'\n"), 0600)
	for _, args := range [][]string{{"init"}, {"add", "helper.py", "pyproject.toml"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(b), err)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "owner-private.txt"), []byte("unrelated private value"), 0600)
	bindings, err := a.Runs.reviewBindings(ctx, []prreview.Reviewer{{ID: "command", Kind: "command", Argv: []string{"python3", script, "{handoff}"}}})
	if err != nil {
		t.Fatal(err)
	}
	b := bindings[0]
	for path := range b.Resources {
		if strings.Contains(path, "owner-private") {
			t.Fatal("unrelated owner file captured")
		}
	}
	_ = os.Remove(script)
	_ = os.Remove(helper)
	output, err := (prreview.CommandWorkers{Root: t.TempDir()}).Review(ctx, b, prreview.Handoff{Contract: "git-diff-v1", Evidence: prreview.Evidence{Checkout: t.TempDir()}}, "")
	if err != nil || !strings.Contains(string(output), "findings") {
		t.Fatal("retained command resources unavailable after source deletion", string(output), err)
	}
	standalone := t.TempDir()
	entry := filepath.Join(standalone, "single.py")
	_ = os.WriteFile(entry, []byte("print('{}')"), 0600)
	_ = os.WriteFile(filepath.Join(standalone, "private.txt"), []byte("owner data"), 0600)
	bindings, err = a.Runs.reviewBindings(ctx, []prreview.Reviewer{{ID: "single", Kind: "command", Argv: []string{"python3", entry, "{handoff}"}}})
	if err != nil {
		t.Fatal(err)
	}
	for path := range bindings[0].Resources {
		if strings.Contains(path, "private.txt") {
			t.Fatal("standalone entrypoint captured its parent directory")
		}
	}
}

func TestReviewRestartResumesCompletedPublicationTail(t *testing.T) {
	a, pr, workers, collector := retainedFixture(t)
	ctx := a.Runs.ctx
	workers.findings = true
	config, _ := a.Config(ctx)
	config.AutoPostReview = true
	if err := a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	remote := &retainedPostRemote{fail: errors.New("publication unavailable"), snapshot: prreview.Snapshot{Metadata: collector.e.Metadata, Patch: collector.e.Patch, Author: "me", Reviews: []prreview.RemoteReview{}}}
	a.ReviewRemote = remote
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	var saved domain.Run
	waitFor(t, func() bool {
		_ = a.Store.Get(context.Background(), "run", run.ID, &saved)
		return saved.Status == "failed"
	})
	if !saved.ReviewComplete || saved.ReviewArtifact == nil {
		t.Fatal("completed review lost on publication failure", saved)
	}
	a.Runs.Close()
	a.Runs = NewManager(context.Background(), a, "")
	remote.fail = nil
	if err = a.Runs.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(context.Background(), run.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(context.Background(), "run", run.ID, &saved)
		return saved.Status == "idle" || saved.Status == "failed"
	})
	a.Runs.Close()
	var retained retainedReview
	_ = a.Store.Get(context.Background(), "review-workflow", run.ID, &retained)
	if saved.Status != "idle" || saved.PostingState != "verified" || saved.PostingReceipt == nil || remote.mutations != 1 || workers.prepareCalls != 1 || len(retained.Session.Results) != 1 {
		t.Fatal("publication tail reran review or lost receipt", saved, retained.Session.Stage)
	}
}

func TestReviewRestartDoesNotPromoteOlderArtifactBeforeEngineeringCompletion(t *testing.T) {
	a, pr, workers, _ := retainedFixture(t)
	ctx := context.Background()
	workers.secondErr = errors.New("fixture reviewer unavailable")
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	var saved domain.Run
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &saved)
		return saved.Status == "failed"
	})
	a.Runs.Close()
	if saved.ReviewComplete || saved.ReviewArtifact == nil || saved.ReviewResult == nil || saved.ReviewResult.Complete || workers.completionCalls != 0 {
		t.Fatal("initial incomplete review was not retained", saved)
	}
	oldArtifact := *saved.ReviewArtifact
	workers.secondErr = nil
	dbURL := url.URL{Scheme: "file", Path: filepath.Join(a.DataDir, "state.db"), RawQuery: "_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", dbURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER fail_review_aggregate BEFORE UPDATE ON entities WHEN NEW.kind = 'review-workflow' AND json_extract(NEW.data, '$.session.stage') = 'validate-and-aggregate' BEGIN SELECT RAISE(ABORT, 'fixture failed aggregate checkpoint'); END`); err != nil {
		t.Fatal(err)
	}
	a.Runs = NewManager(ctx, a, "")
	if err = a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &saved)
		return saved.Status == "failed"
	})
	a.Runs.Close()
	if saved.ReviewComplete || saved.ReviewArtifact == nil || *saved.ReviewArtifact != oldArtifact || saved.ReviewResult == nil || saved.ReviewResult.Complete || workers.completionCalls != 0 || !strings.Contains(saved.Error, "fixture failed aggregate checkpoint") {
		t.Fatal("failed aggregate promoted the older incomplete result", saved)
	}
	if _, err = a.PostRetainedReview(ctx, PostRetainedReviewInput{RunID: run.ID, Artifact: oldArtifact}); err == nil {
		t.Fatal("older incomplete artifact became publishable")
	}
	if _, err = db.Exec(`DROP TRIGGER fail_review_aggregate`); err != nil {
		t.Fatal(err)
	}
	a.Runs = NewManager(ctx, a, "")
	if err = a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &saved)
		return saved.Status == "idle" || saved.Status == "failed"
	})
	a.Runs.Close()
	if saved.Status != "idle" || !saved.ReviewComplete || saved.ReviewArtifact == nil || *saved.ReviewArtifact == oldArtifact || saved.ReviewResult == nil || !saved.ReviewResult.Complete || workers.completionCalls != 1 || workers.prepareCalls != 1 || workers.reviewCalls != 3 {
		t.Fatal("successful retry did not complete from saved reviewer checkpoints", saved, workers.reviewCalls, workers.completionCalls)
	}
}

func TestReviewRestartFinalizesSavedFinishedSessionWithoutReplay(t *testing.T) {
	a, pr, workers, collector := retainedFixture(t)
	ctx := context.Background()
	workers.findings = true
	remote := &retainedPostRemote{snapshot: prreview.Snapshot{Metadata: collector.e.Metadata, Patch: collector.e.Patch, Author: "me", Reviews: []prreview.RemoteReview{}}}
	a.ReviewRemote = remote
	dbURL := url.URL{Scheme: "file", Path: filepath.Join(a.DataDir, "state.db"), RawQuery: "_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", dbURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Lose only parent finalization after the real finished-session save. This
	// leaves the same durable boundary as process loss before that final write.
	if _, err = db.Exec(`CREATE TRIGGER lose_review_finalization BEFORE UPDATE ON entities WHEN NEW.kind = 'run' AND json_extract(NEW.data, '$.reviewStage') = 'finished' AND json_extract(NEW.data, '$.status') != 'running' BEGIN SELECT RAISE(ABORT, 'fixture lost parent finalization'); END`); err != nil {
		t.Fatal(err)
	}
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	var saved domain.Run
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &saved)
		return saved.ReviewStage == "finished"
	})
	waitGuardReviewDone(t, a, run.ID)
	owner, _ := a.Runs.get(run.ID)
	owner.mu.Lock()
	finalErr := owner.reviewResult.err
	owner.mu.Unlock()
	var joined retainedReview
	if err = a.Store.Get(ctx, "review-workflow", run.ID, &joined); err != nil || joined.PendingWorker != nil || finalErr == nil || !strings.Contains(finalErr.Error(), "fixture lost parent finalization") {
		t.Fatal("verified workers lost their durable proof or finalization error", joined, finalErr, err)
	}
	a.Runs.Close()
	if err = a.Store.Get(ctx, "run", run.ID, &saved); err != nil || saved.Status != "running" || !saved.ReviewComplete || saved.ReviewArtifact == nil {
		t.Fatal("lost finalization boundary not retained", saved, err)
	}
	ref := *saved.ReviewArtifact
	if _, err = db.Exec(`DROP TRIGGER lose_review_finalization`); err != nil {
		t.Fatal(err)
	}
	// A policy enabled after the saved finish cannot authorize recovery to post.
	config, _ := a.Config(ctx)
	config.AutoPostReview = true
	if err = a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.Runs = NewManager(ctx, a, "")
	if err = a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Get(ctx, "run", run.ID, &saved); err != nil || saved.Status != "interrupted" || !saved.ReviewComplete {
		t.Fatal("restart lost saved completion", saved, err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &saved)
		return saved.Status == "idle" || saved.Status == "failed"
	})
	a.Runs.Close()
	var retained retainedReview
	if err = a.Store.Get(ctx, "review-workflow", run.ID, &retained); err != nil {
		t.Fatal(err)
	}
	if saved.Status != "idle" || !saved.ReviewComplete || saved.ReviewArtifact == nil || *saved.ReviewArtifact != ref || saved.ReviewResult == nil || !saved.ReviewResult.Complete || workers.prepareCalls != 1 || workers.reviewCalls != 2 || collector.captures != 1 || remote.calls != 0 || remote.mutations != 0 || len(retained.Session.Results) != 1 || retained.Session.Stage != "finished" {
		t.Fatal("finished-session recovery lost completion or replayed work", saved, retained.Session, workers.reviewCalls, remote.calls)
	}
	if _, err = a.PostRetainedReview(ctx, PostRetainedReviewInput{RunID: run.ID, Artifact: ref}); err != nil || remote.mutations != 1 {
		t.Fatal("recovered artifact cannot be posted explicitly", err, remote.mutations)
	}
}

func TestReviewRestartAcceptsOwnPublicationInCurrentScope(t *testing.T) {
	for _, boundary := range []string{"finished", "uncertain-tail", "submitting-tail"} {
		t.Run(boundary, func(t *testing.T) {
			a, pr, workers, collector := retainedFixture(t)
			ctx := context.Background()
			workers.findings = true
			collector.e.Scope["pr-reviews"] = json.RawMessage(`[]`)
			collector.e.Sources["issues/pr-reviews"] = "[]"
			remote := &retainedPostRemote{lost: boundary == "uncertain-tail", snapshot: prreview.Snapshot{Metadata: collector.e.Metadata, Patch: collector.e.Patch, Author: "me", Reviews: []prreview.RemoteReview{}}}
			a.ReviewRemote = remote
			a.ReviewCollector = scopedRetainedCollector{GitCollector: prreview.GitCollector{Remote: retainedReviewSource{collector, remote}}, fixture: collector}
			config, _ := a.Config(ctx)
			config.AutoPostReview = true
			if err := a.SaveConfig(ctx, config); err != nil {
				t.Fatal(err)
			}
			dbURL := url.URL{Scheme: "file", Path: filepath.Join(a.DataDir, "state.db"), RawQuery: "_pragma=busy_timeout(5000)"}
			db, err := sql.Open("sqlite", dbURL.String())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			predicate := `NEW.kind = 'run' AND json_extract(NEW.data, '$.reviewStage') = 'finished' AND json_extract(NEW.data, '$.status') != 'running'`
			if boundary == "uncertain-tail" {
				predicate = `NEW.kind = 'review-workflow' AND json_extract(NEW.data, '$.session.stage') = 'finished'`
			} else if boundary == "submitting-tail" {
				predicate = `NEW.kind = 'review-publication' AND json_extract(NEW.data, '$.status') = 'uncertain'`
			}
			if _, err = db.Exec(`CREATE TRIGGER lose_publication_finalization BEFORE UPDATE ON entities WHEN ` + predicate + ` BEGIN SELECT RAISE(ABORT, 'fixture lost finalization after submission'); END`); err != nil {
				t.Fatal(err)
			}
			run, err := a.Runs.PreparePRReview(ctx, pr.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			var saved domain.Run
			waitFor(t, func() bool {
				_ = a.Store.Get(ctx, "run", run.ID, &saved)
				return saved.ReviewStage == "finished" || saved.Status == "failed"
			})
			a.Runs.Close()
			if err = a.Store.Get(ctx, "run", run.ID, &saved); err != nil || !saved.ReviewComplete || saved.ReviewArtifact == nil || remote.mutations != 1 || saved.PostingOperation == "" {
				t.Fatal("submitted crash boundary not retained", saved, remote.mutations, err)
			}
			if boundary == "finished" && (saved.Status != "running" || saved.PostingState != "verified") || boundary == "uncertain-tail" && (saved.Status != "failed" || saved.PostingState != "uncertain") || boundary == "submitting-tail" && (saved.Status != "failed" || saved.PostingState != "submitting") {
				t.Fatal("wrong publication recovery boundary", saved)
			}
			ref := *saved.ReviewArtifact
			if _, err = db.Exec(`DROP TRIGGER lose_publication_finalization`); err != nil {
				t.Fatal(err)
			}
			a.Runs = NewManager(ctx, a, "")
			if err = a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = a.Runs.Start(ctx, run.ID, true); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool {
				_ = a.Store.Get(ctx, "run", run.ID, &saved)
				return saved.Status == "idle" || saved.Status == "failed"
			})
			a.Runs.Close()
			if saved.Status != "idle" || !saved.ReviewComplete || saved.ReviewArtifact == nil || *saved.ReviewArtifact != ref || saved.PostingState != "verified" || saved.PostingReceipt == nil || remote.mutations != 1 || workers.prepareCalls != 1 || workers.reviewCalls != 2 || collector.captures != 1 {
				t.Fatal("own publication blocked recovery or replayed work", saved, remote.mutations, workers.reviewCalls)
			}
			var artifact prreview.Artifact
			if err := a.reviewArtifacts().Load(ref, &artifact); err != nil || len(artifact.Result.Findings) != 1 || artifact.Result.Findings[0].Message != "Lost state" || saved.ReviewResult == nil || saved.ReviewResult.FindingCount == nil || *saved.ReviewResult.FindingCount != 1 {
				t.Fatal("compact summary lost private publication findings", saved.ReviewResult, artifact.Result, err)
			}
			var savedBytes json.RawMessage
			if err := a.Store.Get(ctx, "run", run.ID, &savedBytes); err != nil || strings.Contains(string(savedBytes), "Lost state") || strings.Contains(string(savedBytes), `"findings"`) {
				t.Fatal("new run persisted finding prose", err)
			}
		})
	}
}

func TestTrackingExposesRetainedReviewCompletenessAndOwnReceipt(t *testing.T) {
	a, pr, _, _ := retainedFixture(t)
	ctx := a.Runs.ctx
	a.GitHub = nil
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "idle"
	run.ReviewComplete = true
	run.ReviewStage = "finished"
	run.PostingState = "verified"
	run.PostingOperation = "operation"
	run.PostingReceipt = &prreview.Receipt{ID: "receipt", URL: pr.URL + "#receipt", Head: pr.Head, Comments: 2}
	if err = a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	out, err := a.TrackWork(ctx, TrackInput{Target: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	summary := out["currentRun"].(map[string]any)["review"].(map[string]any)
	if summary["complete"] != true || summary["postingState"] != "verified" || summary["receipt"].(*prreview.Receipt).ID != "receipt" {
		t.Fatal("tracking lost review authority or receipt", summary)
	}
}

func TestReviewAnswerRejectsChangedRemoteEvidenceBeforeClaim(t *testing.T) {
	a, pr, workers, collector := retainedFixture(t)
	ctx := a.Runs.ctx
	workers.analysis.Questions = []prreview.Question{{ID: "scope", Status: "open", Material: true, Statements: "Scope conflict", AffectedWork: "Review scope", Recommendation: "Narrow", ProposedChanges: "Choose scope", Options: []prreview.Option{{Choice: "Narrow", Benefits: "Bounded", Costs: "Deferred"}, {Choice: "Wide", Benefits: "Coverage", Costs: "Complexity"}}, Evidence: []string{"pr/body"}}}
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	var at domain.Attention
	waitFor(t, func() bool {
		rows, _ := store.List[domain.Attention](ctx, a.Store, "attention")
		if len(rows) == 1 {
			at = rows[0]
			return at.Status == "pending"
		}
		return false
	})
	collector.mu.Lock()
	collector.invalid = true
	collector.mu.Unlock()
	if err = a.Runs.Answer(ctx, at.ID, json.RawMessage(`{"answers":{"scope":{"answers":["Narrow"]}}}`)); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatal("stale answer accepted", err)
	}
	if err = a.Store.Get(ctx, "attention", at.ID, &at); err != nil || at.Status != "pending" {
		t.Fatal("stale answer was durably claimed", at, err)
	}
}

func TestReviewAnswerDoesNotClaimAcrossConcurrentRuleChange(t *testing.T) {
	a, pr, workers, collector := retainedFixture(t)
	ctx := a.Runs.ctx
	workers.analysis.Questions = []prreview.Question{{ID: "scope", Status: "open", Material: true, Statements: "Scope conflict", AffectedWork: "Review scope", Recommendation: "Narrow", ProposedChanges: "Choose scope", Options: []prreview.Option{{Choice: "Narrow", Benefits: "Bounded", Costs: "Deferred"}, {Choice: "Wide", Benefits: "Coverage", Costs: "Complexity"}}, Evidence: []string{"pr/body"}}}
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	var at domain.Attention
	waitFor(t, func() bool {
		rows, _ := store.List[domain.Attention](ctx, a.Store, "attention")
		if len(rows) == 1 {
			at = rows[0]
			return at.Status == "pending"
		}
		return false
	})
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	var once sync.Once
	collector.mu.Lock()
	collector.validateHook = func() { once.Do(func() { close(entered) }); <-release }
	collector.mu.Unlock()
	answered := make(chan error, 1)
	answerDone := make(chan struct{})
	t.Cleanup(func() {
		unblock()
		waitRetainedReviewValue(t, answerDone, "answer goroutine shutdown")
	})
	go func() {
		defer close(answerDone)
		answered <- a.Runs.Answer(ctx, at.ID, json.RawMessage(`{"answers":{"scope":{"answers":["Narrow"]}}}`))
	}()
	waitRetainedReviewValue(t, entered, "answer validation hook")
	config, _ := a.Config(ctx)
	config.ReviewRules = "New governing rule"
	if err = a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err = waitRetainedReviewValue(t, answered, "answer result"); err == nil || !strings.Contains(err.Error(), "configuration changed") {
		t.Fatal("answer consumed across rule change", err)
	}
	if err = a.Store.Get(ctx, "attention", at.ID, &at); err != nil || at.Status != "pending" {
		t.Fatal("obsolete answer resolved attention", at, err)
	}
}
