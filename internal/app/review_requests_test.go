package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPreparePRReviewResolvesDefaultsAndStartsOnlyExplicitly(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	config, _ := a.Config(ctx)
	var repo domain.Repository
	_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
	skill := domain.Skill{Name: "review-check", Content: "---\nname: review-check\ndescription: Check review evidence.\n---\nInspect correctness.\n"}
	if err := a.Runs.SaveSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	for _, choice := range []string{"fallback", "system", "repository"} {
		t.Run(choice, func(t *testing.T) {
			want := "review"
			if choice != "fallback" {
				want = choice + "-review"
				p := domain.Profile{ID: want, Name: want, Prompt: "Review {{title}} using the chosen pipeline.", Skills: []string{"managed:review-check"}, Model: "configured-model", Effort: "high", Mode: "plan", Sandbox: "read-only", Approval: "on-request", Publishing: "ask"}
				if err := a.SaveProfile(ctx, p); err != nil {
					t.Fatal(err)
				}
				if choice == "system" {
					config.ReviewProfileID = want
				} else {
					repo.ReviewProfileID = want
				}
			}
			if err := a.SaveConfig(ctx, config); err != nil {
				t.Fatal(err)
			}
			if err := a.SaveRepository(ctx, repo); err != nil {
				t.Fatal(err)
			}
			before := a.Store.Latest(ctx)
			run, err := a.Runs.PreparePRReview(ctx, pr.ID)
			if err != nil {
				t.Fatal(err)
			}
			events, eventErr := a.Store.Events(ctx, before, 10)
			if eventErr != nil || len(events) != 1 || events[0].Kind != "run.prepared" || events[0].Entity != run.ID {
				t.Fatalf("prepared review event is missing: count=%d error=%v", len(events), eventErr)
			}
			var change map[string]string
			if err := json.Unmarshal(events[0].Data, &change); err != nil || len(change) != 2 || change["id"] != run.ID || change["status"] != "prepared" || len(events[0].Data) > 256 {
				t.Fatal("prepared update exposes private bundle data or lacks compact identity; bytes", len(events[0].Data))
			}
			if run.Status != "prepared" || run.Profile.ID != want || run.Base != pr.Head || run.AutomaticReview || run.ThreadID != "" || !strings.Contains(run.Prompt, pr.URL) || !strings.Contains(run.Prompt, pr.Head) || strings.Count(run.Prompt, repo.ReviewPrompt) != 1 {
				t.Fatal("review defaults, pinned target or preparation boundary lost", run)
			}
			if choice != "fallback" && (run.Profile.Model != "configured-model" || run.Profile.Effort != "high" || run.Profile.Mode != "plan") {
				t.Fatal("selected profile choices were overwritten")
			}
			if choice != "fallback" && (len(run.Skills) != 1 || run.Skills[0].Content != skill.Content || len(run.Skills[0].Files) != 1) {
				t.Fatal("configured review skill was not snapshotted")
			}
			if _, err := os.Stat(run.Worktree); !os.IsNotExist(err) {
				t.Fatalf("preparation created a workspace: %v", err)
			}
			if claimCount(t, a) != 0 {
				t.Fatal("manual preparation claimed an automatic review")
			}
			if choice == "fallback" {
				if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
					t.Fatal(err)
				}
				waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", run.ID, &run); return run.Status == "idle" })
				if _, err := a.Runs.Start(ctx, run.ID, false); err == nil {
					t.Fatal("repeated Start was accepted")
				}
			}
		})
	}
}

func TestPreparePRReviewRejectsInvalidTargetsAndUnsafePipelines(t *testing.T) {
	for _, mode := range []string{"issue", "missing-head", "missing-item", "missing-profile", "write-profile", "publishing-profile", "disabled-skill"} {
		t.Run(mode, func(t *testing.T) {
			a, pr := reviewFixture(t)
			ctx := a.Runs.ctx
			var p domain.Profile
			_ = a.Store.Get(ctx, "profile", "review", &p)
			switch mode {
			case "issue":
				pr.Kind = "issue"
			case "missing-head":
				pr.Head = ""
			case "missing-profile":
				var repo domain.Repository
				_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
				repo.ReviewProfileID = "unavailable"
				_ = a.Store.Put(ctx, "repository", repo.ID, repo, "")
			case "write-profile":
				p.Sandbox = "workspace-write"
			case "publishing-profile":
				p.Publishing = "automatic"
			case "disabled-skill":
				p.Skills = []string{"disabled"}
				_ = a.Store.Put(ctx, "skill", "disabled", domain.Skill{ID: "disabled", Name: "Disabled", Disabled: true}, "")
			}
			_ = a.Store.Put(ctx, "profile", p.ID, p, "")
			_ = a.Store.Put(ctx, "item", pr.ID, pr, "")
			if mode == "missing-item" {
				pr.ID = "unavailable#999"
			}
			before, _ := store.List[domain.Run](ctx, a.Store, "run")
			if _, err := a.Runs.PreparePRReview(ctx, pr.ID); err == nil {
				t.Fatal("invalid PR review preparation accepted")
			}
			after, _ := store.List[domain.Run](ctx, a.Store, "run")
			if len(before) != len(after) || claimCount(t, a) != 0 {
				t.Fatal("rejected preparation persisted work")
			}
		})
	}
}

func reviewFixture(t *testing.T) (*App, domain.Item) {
	t.Helper()
	t.Setenv("CF_FAKE_MODE", "complete")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	var repo domain.Repository
	_ = a.Store.Get(ctx, "repository", "owner/repo", &repo)
	// A local bare remote exercises the ordinary PR-head fetch without GitHub or a model.
	origin := t.TempDir()
	cmd := exec.Command("git", "clone", "--bare", repo.Checkout, origin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bare clone: %s %v", out, err)
	}
	cmd = exec.Command("git", "-C", repo.Checkout, "remote", "add", "origin", origin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("remote: %s %v", out, err)
	}
	headBytes, err := exec.Command("git", "-C", repo.Checkout, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	if out, err := exec.Command("git", "-C", origin, "update-ref", "refs/pull/2/head", head).CombinedOutput(); err != nil {
		t.Fatalf("pull ref: %s %v", out, err)
	}
	pr := item()
	pr.ID = "owner/repo#2"
	pr.Number = 2
	pr.Kind = "pr"
	pr.Author = "other"
	pr.ReviewRequests = []string{"me"}
	pr.Head = head
	pr.URL = "https://github.com/owner/repo/pull/2"
	pr.Revision = pr.ContentRevision()
	importOne(t, a, ctx, pr)
	repo.LastSync = time.Now().UTC()
	repo.ReviewPrompt = "Check repository-specific correctness and cite the diff."
	if err := a.SaveRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "identity", "github", domain.GitHubViewer{Login: "me", Teams: []string{"owner/core"}}, ""); err != nil {
		t.Fatal(err)
	}
	_ = run
	return a, pr
}
func claimCount(t *testing.T, a *App) int {
	t.Helper()
	claims, err := store.List[reviewClaim](a.Runs.ctx, a.Store, "review-claim")
	if err != nil {
		t.Fatal(err)
	}
	return len(claims)
}
func TestReviewPolicyInheritancePromptAndExactlyOnceAutomaticStart(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := context.WithoutCancel(a.Runs.ctx)
	if err := a.ProcessReviewRequests(ctx); err != nil {
		t.Fatal(err)
	}
	if claimCount(t, a) != 0 {
		t.Fatal("default queue started work")
	}
	config, _ := a.Config(ctx)
	config.AutoReview = true
	config.ReviewProfileID = "review"
	if err := a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		if err := a.ProcessReviewRequests(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if claimCount(t, a) != 1 {
		t.Fatal("polling duplicated automatic review")
	}
	claims, _ := store.List[reviewClaim](ctx, a.Store, "review-claim")
	var run domain.Run
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", claims[0].RunID, &run); return run.Status == "idle" })
	if !run.AutomaticReview || run.Profile.ID != "review" || run.Profile.Sandbox != "read-only" || run.Profile.Publishing != "ask" || run.Base != pr.Head || !strings.Contains(run.Prompt, "Repository review instructions:") || !strings.Contains(run.Prompt, "Check repository-specific correctness") || !strings.Contains(run.Prompt, pr.URL) {
		t.Fatal("review target, permissions or prompt were not retained", run)
	}
	a.Runs.Close()
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.ProcessReviewRequests(ctx); err != nil {
		t.Fatal(err)
	}
	if claimCount(t, a) != 1 {
		t.Fatal("restart replayed review")
	}
	// A re-request on the same head is a new episode, not a duplicate poll.
	pr.ReviewRequests = nil
	_ = a.Store.Put(ctx, "item", pr.ID, pr, "")
	_ = a.ProcessReviewRequests(ctx)
	pr.ReviewRequests = []string{"me"}
	_ = a.Store.Put(ctx, "item", pr.ID, pr, "")
	_ = a.ProcessReviewRequests(ctx)
	if claimCount(t, a) != 2 {
		t.Fatal("new request episode was ignored")
	}
}
func TestRepositoryQueueOverridePauseDraftAndStaleRequestsDoNotStart(t *testing.T) {
	for _, mode := range []string{"queue-override", "paused", "draft", "stale", "not-requested", "sync-error", "unsafe-profile", "agent-busy"} {
		t.Run(mode, func(t *testing.T) {
			a, pr := reviewFixture(t)
			ctx := a.Runs.ctx
			config, _ := a.Config(ctx)
			config.AutoReview = true
			_ = a.SaveConfig(ctx, config)
			var repo domain.Repository
			_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
			switch mode {
			case "queue-override":
				v := false
				repo.AutoReview = &v
			case "paused":
				repo.Paused = true
			case "draft":
				pr.Draft = true
			case "stale":
				repo.LastSync = time.Now().Add(-time.Hour)
			case "not-requested":
				pr.ReviewRequests = nil
			case "sync-error":
				repo.Error = "Access revoked"
			case "agent-busy":
				a.Runs.mu.Lock()
				a.Runs.live["other-work"] = &session{run: domain.Run{ID: "other-work", Status: "running"}}
				a.Runs.mu.Unlock()
			case "unsafe-profile":
				repo.ReviewProfileID = "implement"
			}
			_ = a.Store.Put(ctx, "repository", repo.ID, repo, "")
			_ = a.Store.Put(ctx, "item", pr.ID, pr, "")
			if err := a.ProcessReviewRequests(ctx); err != nil {
				t.Fatal(err)
			}
			if claimCount(t, a) != 0 {
				t.Fatal("ineligible review started")
			}
			if mode == "unsafe-profile" {
				_ = a.Store.Get(ctx, "repository", repo.ID, &repo)
				if repo.ReviewError == "" {
					t.Fatal("unsafe auto pipeline had no feedback")
				}
			}
		})
	}
}
func TestRepositoryAutoOverrideAndSystemPipelineDefaults(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	profile := domain.Profile{ID: "custom-review", Name: "Custom review", Prompt: "Review {{title}} with custom skill policy.", Sandbox: "read-only", Approval: "on-request", Publishing: "ask"}
	if err := a.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	config, _ := a.Config(ctx)
	config.ReviewProfileID = profile.ID
	config.AutoReview = false
	_ = a.SaveConfig(ctx, config)
	var repo domain.Repository
	_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
	v := true
	repo.AutoReview = &v
	if err := a.SaveRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := a.ProcessReviewRequests(ctx); err != nil {
		t.Fatal(err)
	}
	if claimCount(t, a) != 1 {
		t.Fatal("repo override did not launch")
	}
	claims, _ := store.List[reviewClaim](ctx, a.Store, "review-claim")
	var run domain.Run
	_ = a.Store.Get(ctx, "run", claims[0].RunID, &run)
	if run.Profile.ID != profile.ID || !strings.Contains(run.Prompt, "custom skill policy") {
		t.Fatal("system pipeline default ignored")
	}
}
func TestAutomaticStartRevalidatesPolicyAndMonitoringPreservesCheckpoint(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	config, _ := a.Config(ctx)
	config.AutoReview = true
	_ = a.SaveConfig(ctx, config)
	run, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: pr.ID, ProfileID: "review"})
	if err != nil {
		t.Fatal(err)
	}
	run.AutomaticReview = true
	_ = a.Store.Put(ctx, "run", run.ID, run, "")
	var repo domain.Repository
	_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
	before := repo.LastSync
	v := false
	repo.AutoReview = &v
	repo.ReviewPrompt = "Different repo review prompt"
	if err := a.SaveRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Runs.Start(ctx, run.ID, false); err == nil {
		t.Fatal("disabled policy launched prepared auto review")
	}
	_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
	if repo.LastSync != before {
		t.Fatal("monitoring edit reset sync checkpoint")
	}
}

func TestNewPRHeadGetsOneNewReviewAndExtraPromptIsNotDuplicated(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	config, _ := a.Config(ctx)
	config.AutoReview = true
	_ = a.SaveConfig(ctx, config)
	if err := a.ProcessReviewRequests(ctx); err != nil {
		t.Fatal(err)
	}
	claims, _ := store.List[reviewClaim](ctx, a.Store, "review-claim")
	var first domain.Run
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", claims[0].RunID, &first); return first.Status == "idle" })
	prepared, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: pr.ID, ProfileID: "review", Prompt: first.Prompt})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(prepared.Prompt, "Repository review instructions:") != 1 {
		t.Fatal("repository instructions duplicated")
	}
	var repo domain.Repository
	_ = a.Store.Get(ctx, "repository", pr.Repository, &repo)
	for _, args := range [][]string{{"-C", repo.Checkout, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "next-review-head"}, {"-C", repo.Checkout, "push", "origin", "HEAD:refs/pull/2/head"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("new head fixture: %s %v", out, err)
		}
	}
	head, err := exec.Command("git", "-C", repo.Checkout, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	pr.Head = strings.TrimSpace(string(head))
	_ = a.Store.Put(ctx, "item", pr.ID, pr, "")
	for n := 0; n < 3; n++ {
		if err := a.ProcessReviewRequests(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if claimCount(t, a) != 2 {
		t.Fatal("new head did not receive exactly one new review")
	}
}
