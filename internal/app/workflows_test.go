package app

import (
	"codefactory/internal/domain"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestAnsweredContinuationPreservesStateOnStorageFailure(t *testing.T) {
	for _, phase := range []string{"admission", "acknowledged"} {
		t.Run(phase, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "hold")
			record := filepath.Join(t.TempDir(), "provider.jsonl")
			t.Setenv("CF_FAKE_RECORD", record)
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool {
				_ = a.Store.Get(ctx, "run", run.ID, &run)
				return run.Status == "running" && run.TurnID != ""
			})
			threadID := run.ThreadID
			s, err := a.Runs.get(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			waitLaunchRetry(t, func() { <-s.started })
			if err := a.Runs.Interrupt(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool {
				_ = a.Store.Get(ctx, "run", run.ID, &run)
				return run.Status == "interrupted" && run.TurnID == ""
			})
			recovery := &domain.QuestionRecovery{ID: "storage-failure-answer", RunID: run.ID, Stage: "answered", Confirmation: "native"}
			if err := func() error {
				s.mu.Lock()
				defer s.mu.Unlock()
				s.recovery = recovery
				s.run.Status = "idle"
				if err := a.Store.Put(ctx, "question-recovery", recovery.ID, recovery, ""); err != nil {
					return err
				}
				return a.Runs.persist(s, "run.state")
			}(); err != nil {
				t.Fatal(err)
			}
			dbURL := url.URL{Scheme: "file", Path: filepath.Join(a.DataDir, "state.db"), RawQuery: "_pragma=busy_timeout(5000)"}
			db, err := sql.Open("sqlite", dbURL.String())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			predicate := `json_extract(NEW.data, '$.status') = 'starting'`
			if phase == "acknowledged" {
				// Fail the post-ack write, but permit the diagnostic fallback write.
				predicate = `json_extract(NEW.data, '$.status') = 'running' AND json_extract(NEW.data, '$.error') = ''`
			}
			if _, err := db.Exec(`CREATE TRIGGER fail_continuation BEFORE UPDATE ON entities WHEN NEW.kind = 'run' AND ` + predicate + ` BEGIN SELECT RAISE(ABORT, 'fixture continuation persistence failure'); END`); err != nil {
				t.Fatal(err)
			}
			beforeCalls, beforeEvents := len(recordedCalls(t, record)), a.Store.Latest(ctx)
			a.Runs.continueAfterRepair(s, recovery)
			var saved domain.QuestionRecovery
			if err := a.Store.Get(ctx, "question-recovery", recovery.ID, &saved); err != nil {
				t.Fatal(err)
			}
			if err := a.Store.Get(ctx, "run", run.ID, &run); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			live := s.run
			s.mu.Unlock()
			if phase == "admission" {
				if live.Status != "idle" || run.Status != "idle" || live.TurnID != "" || saved.Stage != "answered" || recovery.Stage != "answered" {
					t.Fatal("unsent continuation lost recoverable idle state or claimed its answer", live.Status, run.Status, live.TurnID, saved.Stage)
				}
				if len(recordedCalls(t, record)) != beforeCalls {
					t.Fatal("failed admission dispatched a provider continuation")
				}
			} else {
				events, err := a.Store.Events(ctx, beforeEvents, 100)
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range events {
					var change struct{ Status string }
					_ = json.Unmarshal(event.Data, &change)
					// Native turn/started can run after continueAfterRepair unlocks.
					// The journal must never advertise its acknowledged turn as idle,
					// even if that notification later repairs the live session.
					if event.Kind == "run.state" && event.Entity == run.ID && change.Status == "idle" {
						t.Fatal("acknowledged continuation was persisted as idle")
					}
				}
				if live.Status != "running" || live.TurnID == "" || live.ThreadID != threadID || live.Error == "" || run.Status != "running" || run.TurnID != live.TurnID || run.ThreadID != threadID || run.Error == "" || saved.Stage != "resolved" {
					t.Fatal("post-ack persistence failure lost active identity, diagnostic or resolved claim", live.Status, live.TurnID, live.Error, saved.Stage)
				}
			}
			if _, err := db.Exec(`DROP TRIGGER fail_continuation`); err != nil {
				t.Fatal(err)
			}
			// Only an unsent admission may proceed after storage recovers. A
			// resolved answer must not dispatch its acknowledged continuation again.
			a.Runs.continueAfterRepair(s, recovery)
			a.Runs.continueAfterRepair(s, recovery)
			calls := recordedCalls(t, record)
			if len(calls) != beforeCalls+1 || calls[beforeCalls].Method != "turn/start" {
				t.Fatal("continuation was lost or replayed after storage recovered", calls)
			}
			if err := a.Store.Get(ctx, "question-recovery", recovery.ID, &saved); err != nil || saved.Stage != "resolved" {
				t.Fatal("sent continuation did not retain its resolved answer claim", saved.Stage, err)
			}
		})
	}
}

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
