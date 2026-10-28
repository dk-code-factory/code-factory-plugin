package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/prreview"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func configureSingleCodex(t *testing.T, a *App, ctx context.Context) {
	t.Helper()
	if err := a.Runs.SaveSkill(ctx, domain.Skill{Name: "dk-pr-iter", Content: "---\nname: dk-pr-iter\ndescription: Fixture iteration skill.\n---\nReview the target using the retained session.\n"}); err != nil {
		t.Fatal(err)
	}
	c, _ := a.Config(ctx)
	c.ExecutionMode, c.PRReviewSkillID = "single-codex", "managed:dk-pr-iter"
	if err := a.SaveConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticReviewPreservesProfileSkills(t *testing.T) {
	for _, tc := range []struct {
		name     string
		workflow bool
		skills   []string
	}{
		{name: "native-nil"},
		{name: "native-empty", skills: []string{}},
		{name: "native-other", skills: []string{"managed:review-check"}},
		{name: "native-selected", skills: []string{"managed:dk-pr-iter"}},
		{name: "workflow-empty", workflow: true, skills: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var a *App
			var pr domain.Item
			if tc.workflow {
				a, pr, _, _ = retainedFixture(t)
			} else {
				a, pr = reviewFixture(t)
				configureSingleCodex(t, a, a.Runs.ctx)
			}
			ctx := a.Runs.ctx
			if err := a.Runs.SaveSkill(ctx, domain.Skill{Name: "review-check", Content: "---\nname: review-check\ndescription: Review evidence.\n---\nCheck correctness.\n"}); err != nil {
				t.Fatal(err)
			}
			var profile domain.Profile
			if err := a.Store.Get(ctx, "profile", "review", &profile); err != nil {
				t.Fatal(err)
			}
			profile.Skills = tc.skills
			if err := a.SaveProfile(ctx, profile); err != nil {
				t.Fatal(err)
			}
			c, _ := a.Config(ctx)
			c.AutoReview = true
			if err := a.SaveConfig(ctx, c); err != nil {
				t.Fatal(err)
			}
			if err := a.ProcessReviewRequests(ctx); err != nil {
				t.Fatal(err)
			}
			claims, err := store.List[reviewClaim](ctx, a.Store, "review-claim")
			if err != nil || len(claims) != 1 || claims[0].Status != "started" {
				t.Fatalf("automatic launch failed: claims=%+v error=%v", claims, err)
			}
			var run domain.Run
			waitFor(t, func() bool {
				_ = a.Store.Get(ctx, "run", claims[0].RunID, &run)
				return run.Status == "idle" || run.Status == "failed"
			})
			if run.Status != "idle" || !run.AutomaticReview || run.ItemID != pr.ID || string(raw(run.Profile)) != string(raw(profile)) {
				t.Fatal("automatic review lost its selected profile or failed", run.Status, run.Error, run.Profile)
			}
			if tc.workflow {
				if run.ReviewWorkflow != prreview.Contract || !run.ReviewComplete || run.ThreadID != "" || len(run.Skills) != 0 {
					t.Fatal("empty profile did not complete through the review workflow", run)
				}
			} else {
				want := slices.Clone(tc.skills)
				if !slices.Contains(want, c.PRReviewSkillID) {
					want = append(want, c.PRReviewSkillID)
				}
				var got []string
				for _, skill := range run.Skills {
					got = append(got, skill.ID)
				}
				if run.ReviewWorkflow != "" || run.ThreadID == "" || !slices.Equal(got, want) {
					t.Fatal("native review lost or duplicated retained skills", got, want)
				}
			}
			var stored domain.Profile
			if err := a.Store.Get(ctx, "profile", profile.ID, &stored); err != nil || string(raw(stored)) != string(raw(profile)) {
				t.Fatal("preparation changed the configured profile", stored, err)
			}
			// Even a change already present in effective bundles remains a profile change.
			stored.Skills = append(slices.Clone(stored.Skills), "managed:dk-pr-iter")
			if err := a.SaveProfile(ctx, stored); err != nil {
				t.Fatal(err)
			}
			err = a.Store.Write(ctx, func(tx *store.Tx) error { return validateAutomaticStart(tx, run) })
			if err == nil || !strings.Contains(err.Error(), "automatic review policy or target changed") {
				t.Fatal("changed profile bypassed automatic policy validation", err)
			}
		})
	}
}

func TestSingleCodexExplicitSkillsRemainSelectedAndUnmodified(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	configureSingleCodex(t, a, ctx)
	for _, selected := range [][]string{{}, {"managed:dk-pr-iter"}} {
		backing := []string{"sentinel", "sentinel"}
		copy(backing, selected)
		input := backing[:len(selected)]
		before := slices.Clone(backing)
		run, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: pr.ID, ProfileID: "review", SkillIDs: input})
		if err != nil {
			t.Fatal(err)
		}
		if string(raw(run.Profile.Skills)) != string(raw(selected)) || len(run.Skills) != 1 || run.Skills[0].ID != "managed:dk-pr-iter" || !slices.Equal(backing, before) {
			t.Fatal("derived skill changed explicit selection or its backing slice", run.Profile.Skills, backing)
		}
	}
}

func TestSingleCodexReviewRetainsSkillModeAndNativeSession(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	configureSingleCodex(t, a, ctx)
	record := filepath.Join(t.TempDir(), "rpc.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.ExecutionMode != "single-codex" || run.ReviewWorkflow != "" || len(run.Skills) != 1 || run.Skills[0].Name != "dk-pr-iter" || len(run.Skills[0].Files) == 0 || !strings.Contains(run.Prompt, "$dk-pr-iter") {
		t.Fatalf("missing retained single-session contract: mode=%s workflow=%s skills=%d", run.ExecutionMode, run.ReviewWorkflow, len(run.Skills))
	}
	c, _ := a.Config(ctx)
	c.ExecutionMode = "workflow"
	if err = a.SaveConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	workflow, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil || workflow.ReviewWorkflow == "" {
		t.Fatal("switching back did not prepare engine review", err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &run)
		return run.Status == "idle" || run.Status == "failed"
	})
	if run.Status != "idle" || run.ThreadID == "" {
		t.Fatalf("native launch failed: %s %s", run.Status, run.Error)
	}
	thread := run.ThreadID
	waitForResolvedCompletion(t, a, ctx, run.ID)
	delivery, err := a.Runs.QueueMessage(ctx, run.ID, "Continue reviewing the same target", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		var saved MessageDelivery
		_ = a.Store.Get(ctx, "message", delivery.ID, &saved)
		_ = a.Store.Get(ctx, "run", run.ID, &run)
		return saved.State == "delivered" && run.Status == "idle"
	})
	if run.ThreadID != thread {
		t.Fatal("follow-up replaced the session")
	}
	rpc, err := os.ReadFile(record)
	if err != nil || !strings.Contains(string(rpc), "read and follow "+filepath.Join(a.DataDir, "snapshots", run.ID, "0", "SKILL.md")) {
		t.Fatal("native prompt omitted the retained skill entrypoint", err)
	}
	files, _ := filepath.Glob(filepath.Join(a.DataDir, "ai-workflows/runs/*.json"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "workflows/review.yml") {
			t.Fatal("single Codex review entered the YAML executor")
		}
	}
}

func TestSingleCodexConfigurationRejectsMissingDisabledOrWrongSkill(t *testing.T) {
	a, _ := reviewFixture(t)
	ctx := a.Runs.ctx
	c, _ := a.Config(ctx)
	c.ExecutionMode = "single-codex"
	if a.SaveConfig(ctx, c) == nil {
		t.Fatal("missing iteration skill accepted")
	}
	configureSingleCodex(t, a, ctx)
	c, _ = a.Config(ctx)
	var skill domain.Skill
	_ = a.Store.Get(ctx, "skill", c.PRReviewSkillID, &skill)
	skill.Disabled = true
	_ = a.Store.Put(ctx, "skill", skill.ID, skill, "")
	if a.SaveConfig(ctx, c) == nil {
		t.Fatal("disabled skill accepted")
	}
	fallback := c
	fallback.ExecutionMode = "workflow"
	if err := a.SaveConfig(ctx, fallback); err != nil {
		t.Fatal("unavailable iteration skill prevented switching back to workflows", err)
	}
	skill.Disabled = false
	skill.Name = "unrelated"
	_ = a.Store.Put(ctx, "skill", skill.ID, skill, "")
	if a.SaveConfig(ctx, c) == nil {
		t.Fatal("unrelated skill accepted")
	}
	c.ExecutionMode = "unknown"
	if a.SaveConfig(ctx, c) == nil {
		t.Fatal("unknown execution mode accepted")
	}
}

func TestSingleCodexStartupRetainsNativeSessionAfterSettingsSwitch(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := context.WithoutCancel(a.Runs.ctx)
	configureSingleCodex(t, a, ctx)
	t.Setenv("CF_FAKE_MODE", "hold")
	record := filepath.Join(t.TempDir(), "rpc.jsonl")
	t.Setenv("CF_FAKE_RECORD", record)
	run, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &run)
		return run.Status == "running" && run.TurnID != ""
	})
	before := run
	if before.ExecutionMode != "single-codex" || before.ReviewWorkflow != "" || before.ThreadID == "" || len(before.Skills) != 1 {
		t.Fatal("native review was not retained", before)
	}
	checkpoint := filepath.Join(run.Worktree, "review-progress.json")
	progress := `{"reviewer":"completed","findings":[]}`
	if err := os.WriteFile(checkpoint, []byte(progress), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := a.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.ExecutionMode = "workflow"
	if err := a.SaveConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	a.Runs.Close()
	a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &run)
		return run.Status == "running" && run.TurnID != ""
	})
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if run.ID != before.ID || run.ExecutionMode != before.ExecutionMode || run.ReviewWorkflow != "" || run.ThreadID != before.ThreadID || run.Worktree != before.Worktree || run.Prompt != before.Prompt || string(raw(run.Profile)) != string(raw(before.Profile)) || string(raw(run.Skills)) != string(raw(before.Skills)) {
		t.Fatal("startup replaced the retained native session or authority", run)
	}
	if data, err := os.ReadFile(checkpoint); err != nil || string(data) != progress {
		t.Fatal("startup lost retained review progress", err)
	}
	starts, resumes, turns := 0, 0, 0
	for _, call := range recordedCalls(t, record) {
		switch call.Method {
		case "thread/start":
			starts++
		case "thread/resume":
			resumes++
			if string(call.Params["threadId"]) != string(raw(before.ThreadID)) || string(call.Params["cwd"]) != string(raw(before.Worktree)) {
				t.Fatal("startup resumed another thread or workspace", call)
			}
		case "turn/start":
			turns++
			input := string(call.Params["input"])
			if !strings.Contains(input, "read and follow "+before.Skills[0].Path) {
				t.Fatal("native turn omitted the retained skill entrypoint", input)
			}
			if turns == 2 && (strings.Contains(input, before.Prompt) || !strings.Contains(input, "last unfinished step") || !strings.Contains(input, "receipts")) {
				t.Fatal("startup replayed the original review instead of continuing", input)
			}
		}
	}
	if starts != 1 || resumes != 1 || turns != 2 {
		t.Fatalf("startup launched duplicate or fresh work: starts=%d resumes=%d turns=%d", starts, resumes, turns)
	}
	files, err := filepath.Glob(filepath.Join(a.DataDir, "ai-workflows/runs/*.json"))
	if err != nil || len(files) != 0 {
		t.Fatal("native startup entered the YAML executor", files, err)
	}
}
