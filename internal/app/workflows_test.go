package app

import (
	"codefactory/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkflowAdmissionPreservesStartingStateAndRecoveryClaim(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	gate := filepath.Join(t.TempDir(), "turn-gate")
	t.Setenv("CF_FAKE_GATE_METHOD", "turn/start")
	t.Setenv("CF_FAKE_GATE_FILE", gate)
	a, ctx := fixture(t)
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0600) })
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(gate + ".waiting"); return err == nil })
	if err := a.Store.Get(ctx, "run", run.ID, &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "starting" || run.ThreadID == "" || run.TurnID != "" {
		t.Fatal("workflow admission appeared active before provider turn", run.Status, run.ThreadID, run.TurnID)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &run)
		return run.Status == "running" && run.TurnID != ""
	})
	if err := a.Runs.Interrupt(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", run.ID, &run); return run.Status == "interrupted" })
	for _, path := range []string{gate, gate + ".waiting"} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	s, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	recovery := &domain.QuestionRecovery{ID: "workflow-answer", RunID: run.ID, Stage: "answered", Confirmation: "native"}
	s.recovery = recovery
	s.run.Status = "idle"
	if err := a.Store.Put(ctx, "question-recovery", recovery.ID, recovery, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.persist(s, "run.state"); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { defer close(done); a.Runs.continueAfterRepair(s, recovery) }()
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0600); waitLaunchRetry(t, func() { <-done }) })
	waitFor(t, func() bool { _, err := os.Stat(gate + ".waiting"); return err == nil })
	var saved domain.QuestionRecovery
	_ = a.Store.Get(ctx, "run", run.ID, &run)
	_ = a.Store.Get(ctx, "question-recovery", recovery.ID, &saved)
	if run.Status != "starting" || saved.Stage != "resolved" {
		t.Fatal("continuation claim appeared completed during admission", run.Status, saved.Stage)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitLaunchRetry(t, func() { <-done })
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", run.ID, &run)
		return run.Status == "running" && run.TurnID != ""
	})
}
