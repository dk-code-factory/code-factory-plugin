package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This is deliberately opt-in: it uses existing local authentication and model
// calls, operates only in a temporary Git repository, and publishes nothing.
func TestRealCodexInteractiveLifecycle(t *testing.T) {
	for _, mode := range []string{"workflow", "single-codex"} {
		t.Run(mode, func(t *testing.T) { realCodexInteractiveLifecycle(t, mode) })
	}
}

func realCodexInteractiveLifecycle(t *testing.T, mode string) {
	if os.Getenv("CODE_FACTORY_REAL_CODEX") != "1" {
		t.Skip("set CODE_FACTORY_REAL_CODEX=1 for real model lifecycle acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	// The native sandbox masks /tmp. Match installed data placement so retained
	// skill snapshots and worktrees remain visible through their scoped grants.
	cache, e := os.UserCacheDir()
	if e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(cache, "code-factory-real-lifecycle-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	s, e := store.Open(filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v, e := store.NewVault(s, dir)
	if e != nil {
		t.Fatal(e)
	}
	a := &App{Store: s, Vault: v, DataDir: dir, GitHub: providers.NewGitHub()}
	a.Runs = NewManager(ctx, a, "codex")
	defer a.Runs.Close()
	if e = a.Init(ctx); e != nil {
		t.Fatal(e)
	}
	checkout := filepath.Join(dir, "source")
	if e = os.MkdirAll(checkout, 0700); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Code Factory Test", "-c", "user.email=pilot@example.invalid", "commit", "--allow-empty", "-m", "pilot fixture"}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("%s: %v", b, e)
		}
	}
	repo := domain.Repository{ID: "pilot/local", Checkout: checkout, Base: "HEAD"}
	if e = a.SaveRepository(ctx, repo); e != nil {
		t.Fatal(e)
	}
	item := domain.Item{ID: "pilot/local#1", Repository: repo.ID, Number: 1, Kind: "issue", State: "open", Title: "Read-only pilot question", Body: "Investigate a fictional reconnect issue; do not edit or publish.", Labels: []string{}, Activity: []domain.Activity{}, Updated: time.Now()}
	item.Revision = item.ContentRevision()
	if e = a.Import(ctx, repo, []domain.Item{item}, "", time.Now(), true); e != nil {
		t.Fatal(e)
	}
	p := domain.Profile{ID: "pilot", Name: "Interactive pilot", Mode: "plan", Prompt: "Use request_user_input to ask the user which scope to investigate, with Focused and Broad options. Ask before completing. After the answer, report the selected scope and the marker from the pilot-skill skill. Do not edit files or access external systems.", Skills: []string{"managed:pilot-skill"}, Sandbox: "read-only", Approval: "never", Publishing: "ask"}
	if e = a.SaveProfile(ctx, p); e != nil {
		t.Fatal(e)
	}
	if e = a.Runs.SaveSkill(ctx, domain.Skill{Name: "pilot-skill", Content: "---\nname: pilot-skill\ndescription: Read-only pilot marker.\n---\n\nInclude PILOT_SKILL_LOADED in your final response.\n"}); e != nil {
		t.Fatal(e)
	}
	if mode == "single-codex" {
		if e = a.Runs.SaveSkill(ctx, domain.Skill{Name: "dk-pr-iter", Content: "---\nname: dk-pr-iter\ndescription: Unused PR iteration fixture for local issue lifecycle.\n---\nNever publish."}); e != nil {
			t.Fatal(e)
		}
		config, _ := a.Config(ctx)
		config.ExecutionMode, config.PRReviewSkillID = mode, "managed:dk-pr-iter"
		if e = a.SaveConfig(ctx, config); e != nil {
			t.Fatal(e)
		}
	}
	r, e := a.Runs.Prepare(ctx, LaunchInput{ItemID: item.ID, ProfileID: p.ID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Runs.Start(ctx, r.ID, false); e != nil {
		t.Fatal(e)
	}
	wait := func(predicate func() bool) {
		t.Helper()
		for !predicate() {
			select {
			case <-ctx.Done():
				var run domain.Run
				_ = s.Get(context.Background(), "run", r.ID, &run)
				t.Fatalf("real Codex checkpoint timed out: status %s error %s", run.Status, run.Error)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	var attention domain.Attention
	wait(func() bool {
		atts, _ := store.List[domain.Attention](ctx, s, "attention")
		for _, a := range atts {
			if a.Status == "pending" && a.Method == "item/tool/requestUserInput" {
				attention = a
				return true
			}
		}
		return false
	})
	var params struct {
		Questions []struct {
			ID      string
			Options []struct{ Label string }
		}
	}
	if e = json.Unmarshal(attention.Params, &params); e != nil || len(params.Questions) == 0 {
		t.Fatal("real question missing", e)
	}
	answers := map[string]any{}
	for _, q := range params.Questions {
		if len(q.Options) == 0 {
			t.Fatal("real question has no options")
		}
		answers[q.ID] = map[string]any{"answers": []string{q.Options[0].Label}}
	}
	answer := raw(map[string]any{"answers": answers})
	if e = a.Runs.Answer(ctx, attention.ID, answer); e != nil {
		t.Fatal(e)
	}
	if e = a.Runs.Answer(ctx, attention.ID, answer); e == nil {
		t.Fatal("duplicate answer accepted")
	}
	idle := func() bool { _ = s.Get(ctx, "run", r.ID, &r); return r.Status == "idle" }
	wait(idle)
	foundSkill := false
	var cursor int64
	for {
		history, err := s.RunEvents(ctx, r.ID, cursor, 200)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(history)
		foundSkill = foundSkill || contains(string(b), "PILOT_SKILL_LOADED")
		next, _ := history["cursor"].(int64)
		if next <= cursor {
			break
		}
		cursor = next
	}
	if !foundSkill {
		final, _ := s.TurnFinalText(ctx, r.ID, attention.TurnID)
		if live, err := a.Runs.get(r.ID); err == nil {
			live.mu.Lock()
			final += " live=" + live.finalText
			live.mu.Unlock()
		}
		t.Fatalf("skill was not observed in complete real output; fixture final: %s", final)
	}
	if e = a.Runs.Message(ctx, r.ID, "Reply with PILOT_FOLLOWUP_OK. Do not ask another question.", ""); e != nil {
		t.Fatal(e)
	}
	wait(idle)
	if e = a.Runs.Message(ctx, r.ID, "Use request_user_input to ask a new Focused/Broad scope question and wait for my answer.", ""); e != nil {
		t.Fatal(e)
	}
	wait(func() bool {
		atts, _ := store.List[domain.Attention](ctx, s, "attention")
		for _, a := range atts {
			if a.Status == "pending" {
				attention = a
				return true
			}
		}
		return false
	})
	if e = a.Runs.Interrupt(ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	wait(func() bool { _ = s.Get(ctx, "run", r.ID, &r); return r.Status == "interrupted" })
	if e = a.Runs.Answer(ctx, attention.ID, answer); e == nil {
		t.Fatal("stale answer accepted after interrupt")
	}
	if _, e = a.Runs.Start(ctx, r.ID, true); e != nil {
		t.Fatal(e)
	}
	wait(func() bool { _ = s.Get(ctx, "run", r.ID, &r); return r.Status == "idle" || r.Status == "waiting" })
	// Inference is a separate read-only model call with external tools disabled.
	a.Runs.Close()
	a.Runs = NewManager(ctx, a, "codex")
	if e = a.AssessNext(ctx); e != nil {
		t.Fatal(e)
	}
	var assessment domain.Assessment
	if e = s.Get(ctx, "assessment", item.ID, &assessment); e != nil || assessment.Status != "complete" {
		t.Fatalf("real inference: %s (%s): %v", assessment.Status, assessment.Error, e)
	}
	t.Log("PASS: real question/answer, duplicate and stale rejection, skill loading, follow-up, interrupt, resume and priority inference")
}
func contains(text, part string) bool {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
