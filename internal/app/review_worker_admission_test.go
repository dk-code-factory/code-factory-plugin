package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/prreview"
	"codefactory/internal/store"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func reviewFaultDB(t *testing.T, a *App) *sql.DB {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(a.DataDir, "state.db"), RawQuery: "_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func assertReviewBarrierRejects(t *testing.T, a *App, id string) {
	t.Helper()
	ctx := context.Background()
	var run domain.Run
	var saved retainedReview
	if err := a.Store.Get(ctx, "run", id, &run); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "review-workflow", id, &saved); err != nil || saved.PendingWorker == nil {
		t.Fatal("missing durable worker admission", saved, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := a.Runs.Start(ctx, id, true); !errors.Is(err, prreview.ErrWorkerCleanup) {
			t.Fatal("Resume replaced a worker without shutdown proof", err)
		}
		for _, text := range []string{"continue", "resume"} {
			if _, err := a.Runs.QueueMessage(ctx, id, text, ""); !errors.Is(err, prreview.ErrWorkerCleanup) {
				t.Fatal("message bypassed the retained admission", err)
			}
		}
		if _, err := a.Runs.startReviewWorkflowIntent(ctx, run, true, "accepted-answer"); !errors.Is(err, prreview.ErrWorkerCleanup) {
			t.Fatal("automatic answer continuation bypassed the retained admission", err)
		}
	}
	var current domain.Run
	var retained retainedReview
	if err := a.Store.Get(ctx, "run", id, &current); err != nil || !reflect.DeepEqual(current, run) {
		t.Fatal("rejected replacement changed execution, stop intent or error", current, err)
	}
	if err := a.Store.Get(ctx, "review-workflow", id, &retained); err != nil || !reflect.DeepEqual(retained, saved) {
		t.Fatal("rejected replacement erased the admission or checkpoints", retained, err)
	}
}

type closedStoreCleanupWorkers struct {
	prreview.Workers
	a *App
}

func (w closedStoreCleanupWorkers) Review(ctx context.Context, b prreview.Binding, h prreview.Handoff, correction string) ([]byte, error) {
	data, err := w.Workers.Review(ctx, b, h, correction)
	if errors.Is(err, prreview.ErrWorkerCleanup) {
		// Interrupt has already committed intent before it cancels this worker.
		// Closing here makes the finalizer fail in BeginTx, before its callback.
		err = errors.Join(err, w.a.Store.Close())
	}
	return data, err
}

func reopenReviewStore(t *testing.T, a *App) {
	t.Helper()
	_ = a.Store.Close()
	disk, err := store.Open(filepath.Join(a.DataDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disk.Close() })
	a.Store = disk
	a.Vault, err = store.NewVault(disk, a.DataDir)
	if err != nil {
		t.Fatal(err)
	}
}

func TestReviewCleanupFinalizationFailureBlocksRestart(t *testing.T) {
	for _, boundary := range []string{"retained-write", "event", "ui-changes", "commit", "before-callback"} {
		t.Run(boundary, func(t *testing.T) {
			a, item, workers, _ := retainedFixture(t)
			ctx := context.Background()
			db := reviewFaultDB(t, a)
			trigger := `CREATE TRIGGER fail_cleanup_retention BEFORE UPDATE ON entities WHEN NEW.kind = 'review-workflow' AND json_extract(NEW.data, '$.cleanupError') IS NOT NULL BEGIN SELECT RAISE(ABORT, 'fixture failed cleanup retention'); END`
			retainedError := `EXISTS (SELECT 1 FROM entities WHERE kind = 'review-workflow' AND json_extract(data, '$.cleanupError') IS NOT NULL)`
			switch boundary {
			case "event":
				trigger = `CREATE TRIGGER fail_cleanup_retention BEFORE INSERT ON events WHEN ` + retainedError + ` BEGIN SELECT RAISE(ABORT, 'fixture failed finalization event'); END`
			case "ui-changes":
				trigger = `CREATE TRIGGER fail_cleanup_retention BEFORE INSERT ON ui_changes WHEN ` + retainedError + ` BEGIN SELECT RAISE(ABORT, 'fixture failed finalization UI changes'); END`
			case "commit":
				if _, err := db.Exec(`CREATE TABLE fixture_parent (id INTEGER PRIMARY KEY); CREATE TABLE fixture_child (parent INTEGER REFERENCES fixture_parent(id) DEFERRABLE INITIALLY DEFERRED)`); err != nil {
					t.Fatal(err)
				}
				trigger = `CREATE TRIGGER fail_cleanup_retention AFTER UPDATE ON entities WHEN NEW.kind = 'run' AND ` + retainedError + ` BEGIN INSERT INTO fixture_child VALUES(42); END`
			}
			if boundary != "before-callback" {
				if _, err := db.Exec(trigger); err != nil {
					t.Fatal(err)
				}
			}
			started := make(chan struct{})
			uncertain := &cleanupFailureWorkers{Workers: workers, started: started}
			a.ReviewWorkers = uncertain
			if boundary == "before-callback" {
				a.ReviewWorkers = closedStoreCleanupWorkers{Workers: uncertain, a: a}
			}
			run, err := a.Runs.PreparePRReview(ctx, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			waitRetainedReviewValue(t, started, "worker before failed cleanup retention")
			if err = a.Runs.Interrupt(ctx, run.ID); !errors.Is(err, prreview.ErrWorkerCleanup) || !strings.Contains(err.Error(), "final state could not be retained") {
				t.Fatal("Interrupt acknowledged cleanup after its persistence failed", err)
			}
			reopenReviewStore(t, a)
			if boundary != "before-callback" {
				if _, err = db.Exec(`DROP TRIGGER fail_cleanup_retention`); err != nil {
					t.Fatal(err)
				}
			}
			var saved retainedReview
			if err = a.Store.Get(ctx, "review-workflow", run.ID, &saved); err != nil || saved.PendingWorker == nil || saved.CleanupError != "" {
				t.Fatal("late rollback lost the earlier admission or partially committed cleanup", saved, err)
			}
			if boundary == "commit" {
				var count int
				if err = db.QueryRow(`SELECT count(*) FROM fixture_child`).Scan(&count); err != nil || count != 0 {
					t.Fatal("failed commit left partial writes", count, err)
				}
			}
			assertReviewBarrierRejects(t, a, run.ID)
			a.Runs.Close()
			a.Runs = NewManager(ctx, a, "")
			if err = a.Runs.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			assertReviewBarrierRejects(t, a, run.ID)
			if workers.prepareCalls != 1 || workers.reviewCalls != 1 || workers.completionCalls != 0 || uncertain.calls.Load() != 1 {
				t.Fatal("failed finalization admitted replacement work", workers, uncertain.calls.Load())
			}
		})
	}
}

type admissionProbeWorkers struct {
	prreview.Workers
	hook func(string) error
}

func (w admissionProbeWorkers) Prepare(context.Context, prreview.PreparationInput, string) ([]byte, error) {
	return nil, w.hook("prepare")
}
func (w admissionProbeWorkers) MaterialChoice(context.Context, prreview.PreparationInput, prreview.Analysis) (prreview.MaterialCheck, error) {
	return prreview.MaterialCheck{}, w.hook("material-choice")
}
func (w admissionProbeWorkers) Review(_ context.Context, b prreview.Binding, _ prreview.Handoff, _ string) ([]byte, error) {
	return nil, w.hook("review:" + b.Reviewer.ID)
}
func (w admissionProbeWorkers) Completion(context.Context, prreview.Preparation, prreview.Result) (prreview.CompletionAssessment, error) {
	return prreview.CompletionAssessment{}, w.hook("completion")
}

func workerAdmissionFixture(t *testing.T) (*App, domain.Run, *reviewAdmissionWorkers) {
	t.Helper()
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	run.Status, run.ReviewExecution = "running", ID()
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "review-workflow", run.ID, retainedReview{Session: prreview.Session{ID: run.ID}}, ""); err != nil {
		t.Fatal(err)
	}
	owner, unlock, err := a.Runs.lockRun(ctx, run.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	return a, run, &reviewAdmissionWorkers{m: a.Runs, owner: owner, run: run}
}

var admissionCalls = []struct {
	stage string
	call  func(context.Context, *reviewAdmissionWorkers) error
}{
	{"prepare", func(ctx context.Context, w *reviewAdmissionWorkers) error {
		_, err := w.Prepare(ctx, prreview.PreparationInput{}, "")
		return err
	}},
	{"material-choice", func(ctx context.Context, w *reviewAdmissionWorkers) error {
		_, err := w.MaterialChoice(ctx, prreview.PreparationInput{}, prreview.Analysis{})
		return err
	}},
	{"review:one", func(ctx context.Context, w *reviewAdmissionWorkers) error {
		_, err := w.Review(ctx, prreview.Binding{Reviewer: prreview.Reviewer{ID: "one"}}, prreview.Handoff{}, "")
		return err
	}},
	{"completion", func(ctx context.Context, w *reviewAdmissionWorkers) error {
		_, err := w.Completion(ctx, prreview.Preparation{}, prreview.Result{})
		return err
	}},
}

func TestReviewWorkerAdmissionAndVerifiedReturn(t *testing.T) {
	for _, operation := range admissionCalls {
		for _, outcome := range []string{"success", "provider-failure", "verified-cancellation", "uncertain"} {
			t.Run(operation.stage+"/"+outcome, func(t *testing.T) {
				a, run, worker := workerAdmissionFixture(t)
				ctx := context.Background()
				var returned error
				switch outcome {
				case "provider-failure":
					returned = errors.New("fixture provider failed after joining")
				case "verified-cancellation":
					returned = context.Canceled
				case "uncertain":
					returned = fmt.Errorf("%w: fixture admission response lost", prreview.ErrWorkerCleanup)
				}
				worker.Workers = admissionProbeWorkers{hook: func(stage string) error {
					var saved retainedReview
					if err := a.Store.Get(ctx, "review-workflow", run.ID, &saved); err != nil || saved.PendingWorker == nil || saved.PendingWorker.Execution != run.ReviewExecution || saved.PendingWorker.ID == "" || saved.PendingWorker.Stage != stage {
						t.Fatal("worker entered without committed admission", saved, err)
					}
					return returned
				}}
				if err := operation.call(ctx, worker); !errors.Is(err, returned) {
					t.Fatal("worker result changed", err, returned)
				}
				var saved retainedReview
				if err := a.Store.Get(ctx, "review-workflow", run.ID, &saved); err != nil || (saved.PendingWorker != nil) != (outcome == "uncertain") {
					t.Fatal("worker return did not persist the matching proof", saved, err)
				}
			})
		}
	}
}

func TestReviewWorkerAdmissionWriteFailureStartsNoWorkers(t *testing.T) {
	a, run, worker := workerAdmissionFixture(t)
	db := reviewFaultDB(t, a)
	if _, err := db.Exec(`CREATE TRIGGER fail_worker_admission BEFORE UPDATE ON entities WHEN NEW.kind = 'review-workflow' AND json_extract(NEW.data, '$.pendingWorker') IS NOT NULL BEGIN SELECT RAISE(ABORT, 'fixture failed worker admission'); END`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	worker.Workers = admissionProbeWorkers{hook: func(string) error { calls++; return nil }}
	for _, operation := range admissionCalls {
		if err := operation.call(context.Background(), worker); err == nil || !strings.Contains(err.Error(), "fixture failed worker admission") {
			t.Fatal("failed write permitted worker admission", operation.stage, err)
		}
	}
	var saved retainedReview
	if err := a.Store.Get(context.Background(), "review-workflow", run.ID, &saved); err != nil || saved.PendingWorker != nil || calls != 0 {
		t.Fatal("pre-admission failure invoked a worker or invented an admission", saved, calls, err)
	}
}

func TestReviewWorkerProofWriteFailureStopsLaterDispatch(t *testing.T) {
	for _, operation := range admissionCalls {
		t.Run(operation.stage, func(t *testing.T) {
			a, run, worker := workerAdmissionFixture(t)
			db := reviewFaultDB(t, a)
			calls := 0
			worker.Workers = admissionProbeWorkers{hook: func(string) error {
				calls++
				_, err := db.Exec(`CREATE TRIGGER fail_worker_proof BEFORE UPDATE ON entities WHEN NEW.kind = 'review-workflow' AND json_extract(OLD.data, '$.pendingWorker') IS NOT NULL AND json_extract(NEW.data, '$.pendingWorker') IS NULL BEGIN SELECT RAISE(ABORT, 'fixture failed worker proof'); END`)
				return err
			}}
			if err := operation.call(context.Background(), worker); !errors.Is(err, prreview.ErrWorkerCleanup) || !strings.Contains(err.Error(), "fixture failed worker proof") {
				t.Fatal("failed proof was acknowledged", err)
			}
			if _, err := db.Exec(`DROP TRIGGER fail_worker_proof`); err != nil {
				t.Fatal(err)
			}
			for _, later := range admissionCalls {
				if err := later.call(context.Background(), worker); !errors.Is(err, prreview.ErrWorkerCleanup) {
					t.Fatal("failed proof allowed later dispatch", later.stage, err)
				}
			}
			var saved retainedReview
			if err := a.Store.Get(context.Background(), "review-workflow", run.ID, &saved); err != nil || saved.PendingWorker == nil || saved.PendingWorker.Stage != operation.stage || calls != 1 {
				t.Fatal("failed proof lost admission or invoked later workers", saved, calls, err)
			}
		})
	}
}

func TestReviewWorkerProofCannotClearAnotherAdmission(t *testing.T) {
	a, run, worker := workerAdmissionFixture(t)
	var replacement reviewWorkerAdmission
	worker.Workers = admissionProbeWorkers{hook: func(string) error {
		var saved retainedReview
		if err := a.Store.Get(context.Background(), "review-workflow", run.ID, &saved); err != nil {
			return err
		}
		replacement = *saved.PendingWorker
		replacement.ID = ID()
		saved.PendingWorker = &replacement
		return a.Store.Put(context.Background(), "review-workflow", run.ID, saved, "")
	}}
	if _, err := worker.Prepare(context.Background(), prreview.PreparationInput{}, ""); !errors.Is(err, prreview.ErrWorkerCleanup) {
		t.Fatal("stale proof was accepted", err)
	}
	var saved retainedReview
	if err := a.Store.Get(context.Background(), "review-workflow", run.ID, &saved); err != nil || saved.PendingWorker == nil || *saved.PendingWorker != replacement {
		t.Fatal("stale proof erased another admission", saved, err)
	}
}

type admissionSaveAdapter struct {
	prreview.FixtureAdapter
	execute func(context.Context, *prreview.Workflow, *prreview.Session) (prreview.Result, error)
}

func (a admissionSaveAdapter) Execute(ctx context.Context, w *prreview.Workflow, s *prreview.Session) (prreview.Result, error) {
	return a.execute(ctx, w, s)
}

func TestReviewSessionSavePreservesWorkerAdmission(t *testing.T) {
	a, item, _, _ := retainedFixture(t)
	ctx := context.Background()
	var save func(context.Context, *prreview.Session) error
	var state *prreview.Session
	observed := make(chan error, 1)
	a.ReviewEngine = admissionSaveAdapter{execute: func(ctx context.Context, w *prreview.Workflow, s *prreview.Session) (prreview.Result, error) {
		save, state = w.Save, s
		_, err := w.Workers.Prepare(ctx, prreview.PreparationInput{}, "")
		return prreview.Result{}, err
	}}
	a.ReviewWorkers = admissionProbeWorkers{hook: func(string) error {
		var current retainedReview
		if err := a.Store.Get(ctx, "review-workflow", state.ID, &current); err != nil {
			return err
		}
		current.CleanupError = "fixture retained uncertainty"
		if err := a.Store.Put(ctx, "review-workflow", state.ID, current, ""); err != nil {
			return err
		}
		state.Stage = "admitted-checkpoint"
		if err := save(ctx, state); err != nil {
			return err
		}
		var saved retainedReview
		err := a.Store.Get(ctx, "review-workflow", state.ID, &saved)
		if err == nil && (saved.PendingWorker == nil || saved.CleanupError != current.CleanupError || saved.Session.Stage != state.Stage) {
			err = fmt.Errorf("Session save erased current admission/diagnostic or lost its checkpoint: %+v", saved)
		}
		observed <- err
		return fmt.Errorf("%w: fixture retained uncertainty", prreview.ErrWorkerCleanup)
	}}
	run, err := a.Runs.PreparePRReview(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitGuardReviewDone(t, a, run.ID)
	if err = waitRetainedReviewValue(t, observed, "Session save while worker admitted"); err != nil {
		t.Fatal(err)
	}
	var saved retainedReview
	if err = a.Store.Get(ctx, "review-workflow", run.ID, &saved); err != nil || saved.PendingWorker == nil || saved.Session.Stage != "admitted-checkpoint" {
		t.Fatal("Session/finalizer save dropped unmatched admission", saved, err)
	}
}

func TestReviewOrchestratorExitCannotAcknowledgeAdmittedWorker(t *testing.T) {
	a, item, _, _ := retainedFixture(t)
	ctx := context.Background()
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	a.ReviewWorkers = admissionProbeWorkers{hook: func(string) error {
		close(entered)
		<-release
		return nil
	}}
	a.ReviewEngine = admissionSaveAdapter{execute: func(ctx context.Context, w *prreview.Workflow, _ *prreview.Session) (prreview.Result, error) {
		go func() { _, err := w.Workers.Prepare(ctx, prreview.PreparationInput{}, ""); returned <- err }()
		waitRetainedReviewValue(t, entered, "admitted worker before orchestrator exit")
		return prreview.Result{}, nil // Deliberately supplies no child-return proof.
	}}
	t.Cleanup(func() {
		close(release)
		waitRetainedReviewValue(t, returned, "late verified worker return")
	})
	run, err := a.Runs.PreparePRReview(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitGuardReviewDone(t, a, run.ID)
	if err = a.Runs.Interrupt(ctx, run.ID); !errors.Is(err, prreview.ErrWorkerCleanup) {
		t.Fatal("orchestrator exit acknowledged an admitted worker", err)
	}
	assertReviewBarrierRejects(t, a, run.ID)
}

type proofFailurePreparationWorkers struct {
	prreview.Workers
	db            *sql.DB
	materialCalls int
}

func (w *proofFailurePreparationWorkers) Prepare(ctx context.Context, in prreview.PreparationInput, correction string) ([]byte, error) {
	data, err := w.Workers.Prepare(ctx, in, correction)
	if err == nil {
		// Fail only the same-Session proof write. A later changed Session save
		// remains possible and must merge the admission rather than erase it.
		_, err = w.db.Exec(`CREATE TRIGGER fail_verified_return BEFORE UPDATE ON entities WHEN NEW.kind = 'review-workflow' AND json_extract(OLD.data, '$.pendingWorker') IS NOT NULL AND json_extract(NEW.data, '$.pendingWorker') IS NULL AND json_extract(NEW.data, '$.session.stage') = json_extract(OLD.data, '$.session.stage') BEGIN SELECT RAISE(ABORT, 'fixture lost verified return'); END`)
	}
	return data, err
}

func (w *proofFailurePreparationWorkers) MaterialChoice(ctx context.Context, in prreview.PreparationInput, a prreview.Analysis) (prreview.MaterialCheck, error) {
	w.materialCalls++
	return w.Workers.MaterialChoice(ctx, in, a)
}

func TestReviewVerifiedReturnWriteFailureStopsPreparation(t *testing.T) {
	a, item, workers, _ := retainedFixture(t)
	ctx := context.Background()
	probe := &proofFailurePreparationWorkers{Workers: workers, db: reviewFaultDB(t, a)}
	a.ReviewWorkers = probe
	run, err := a.Runs.PreparePRReview(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitGuardReviewDone(t, a, run.ID)
	var saved retainedReview
	if err = a.Store.Get(ctx, "review-workflow", run.ID, &saved); err != nil || saved.PendingWorker == nil || saved.PendingWorker.Stage != "prepare" || saved.Session.Preparation != nil || saved.Session.Status != "incomplete" {
		t.Fatal("incomplete Session save lost the unretained-return barrier", saved, err)
	}
	if workers.prepareCalls != 1 || workers.reviewCalls != 0 || workers.completionCalls != 0 || probe.materialCalls != 0 {
		t.Fatal("proof persistence failure dispatched later workers", workers, probe.materialCalls)
	}
	if err = a.Runs.Interrupt(ctx, run.ID); !errors.Is(err, prreview.ErrWorkerCleanup) || !strings.Contains(err.Error(), "fixture lost verified return") {
		t.Fatal("unretained shutdown proof was acknowledged", err)
	}
	assertReviewBarrierRejects(t, a, run.ID)
}

func TestReviewCommandDelegationRetainsWorkerAdmission(t *testing.T) {
	a, run, worker := workerAdmissionFixture(t)
	calls := 0
	worker.Workers = prreview.CommandWorkers{Root: t.TempDir(), Execute: func(context.Context, string, []string, []string, string) ([]byte, error) {
		calls++
		var saved retainedReview
		if err := a.Store.Get(context.Background(), "review-workflow", run.ID, &saved); err != nil || saved.PendingWorker == nil || saved.PendingWorker.Stage != "review:command" {
			t.Fatal("command delegate lacked its outer worker admission", saved, err)
		}
		return []byte(`{"findings":[]}`), nil
	}}
	binding := prreview.PinBinding(prreview.Binding{Reviewer: prreview.Reviewer{ID: "command", Kind: "command", Argv: []string{"{package}/review", "{handoff}"}}, Entrypoint: "review", Resources: map[string]string{"review": base64.StdEncoding.EncodeToString([]byte("fixture command"))}, Modes: map[string]uint32{"review": 0700}})
	if _, err := worker.Review(context.Background(), binding, prreview.Handoff{Checkout: run.Worktree}, ""); err != nil || calls != 1 {
		t.Fatal("command delegation failed", calls, err)
	}
}

func TestReviewAbruptWorkerLossRetainsAdmission(t *testing.T) {
	if dir := os.Getenv("CF_REVIEW_ADMISSION_HELPER_DIR"); dir != "" {
		disk, err := store.Open(filepath.Join(dir, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		a := &App{Store: disk, DataDir: dir}
		a.Runs = NewManager(context.Background(), a, "")
		var run domain.Run
		if err = disk.Get(context.Background(), "run", os.Getenv("CF_REVIEW_ADMISSION_HELPER_RUN"), &run); err != nil {
			t.Fatal(err)
		}
		owner, unlock, err := a.Runs.lockRun(context.Background(), run.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		unlock()
		worker := &reviewAdmissionWorkers{m: a.Runs, owner: owner, run: run, Workers: admissionProbeWorkers{hook: func(string) error {
			if err := os.WriteFile(filepath.Join(dir, "worker-entered"), []byte("admitted"), 0600); err != nil {
				return err
			}
			select {} // The parent kills this process; no worker-return proof runs.
		}}}
		_, err = worker.Prepare(context.Background(), prreview.PreparationInput{}, "")
		t.Fatal("helper worker returned unexpectedly", err)
	}
	a, item, workers, _ := retainedFixture(t)
	ctx := context.Background()
	run, err := a.Runs.PreparePRReview(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	config, _ := a.Config(ctx)
	var repo domain.Repository
	if err = a.Store.Get(ctx, "repository", item.Repository, &repo); err != nil {
		t.Fatal(err)
	}
	reviewers, rules := reviewSettings(config, repo)
	run.Status, run.ReviewExecution, run.ResumePending = "running", ID(), true
	retained := retainedReview{Session: prreview.Session{ID: run.ID, Target: item.URL, Gate: &prreview.Gate{ID: "accepted-gate", AnswerID: "accepted-answer", Answer: json.RawMessage(`{"scope":"narrow"}`)}}, Configuration: prreview.Digest([]any{reviewers, rules, run.Profile, run.Skills})}
	if err = a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Put(ctx, "review-workflow", run.ID, retained, ""); err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(os.Args[0], "-test.run=^TestReviewAbruptWorkerLossRetainsAdmission$", "-test.timeout=30s")
	helper.Env = append(os.Environ(), "CF_REVIEW_ADMISSION_HELPER_DIR="+a.DataDir, "CF_REVIEW_ADMISSION_HELPER_RUN="+run.ID)
	if err = helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = helper.Process.Kill(); _ = helper.Wait() })
	waitFor(t, func() bool { _, err := os.Stat(filepath.Join(a.DataDir, "worker-entered")); return err == nil })
	if err = a.Store.Get(ctx, "review-workflow", run.ID, &retained); err != nil || retained.PendingWorker == nil {
		t.Fatal("helper did not commit admission before worker entry", retained, err)
	}
	admission := *retained.PendingWorker
	if err = helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = helper.Wait(); err == nil {
		t.Fatal("abrupt helper loss was not exercised")
	}
	a.Runs.Close() // This manager never owned the helper's admitted worker.
	reopenReviewStore(t, a)
	a.Runs = NewManager(ctx, a, "")
	if err = a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Get(ctx, "review-workflow", run.ID, &retained); err != nil || retained.PendingWorker == nil || *retained.PendingWorker != admission {
		t.Fatal("restart cleared unmatched crashed admission", retained, err)
	}
	assertReviewBarrierRejects(t, a, run.ID)
	if workers.prepareCalls != 0 || workers.reviewCalls != 0 || workers.completionCalls != 0 {
		t.Fatal("abrupt restart dispatched replacement work", workers)
	}
}

func TestReviewHistoricalWorkerIdentityRequiresFreshPreparation(t *testing.T) {
	a, item, workers, _ := retainedFixture(t)
	ctx := context.Background()
	workers.analysis.Questions = []prreview.Question{{ID: "scope", Status: "open", Material: true, Statements: "Choose scope", AffectedWork: "Review boundary", Recommendation: "Narrow", ProposedChanges: "Choose boundary", Options: []prreview.Option{{Choice: "Narrow", Benefits: "Bounded", Costs: "Deferred"}, {Choice: "Wide", Benefits: "Coverage", Costs: "Complexity"}}, Evidence: []string{"pr/body"}}}
	run, err := a.Runs.PreparePRReview(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitGuardReviewDone(t, a, run.ID)
	var historical retainedReview
	if err = a.Store.Get(ctx, "review-workflow", run.ID, &historical); err != nil || historical.Session.Gate == nil || historical.PendingWorker != nil {
		t.Fatal("waiting review lacks its no-child checkpoint", historical, err)
	}
	// Old unwrapped worker identities cannot certify the new admission policy.
	// Preserve the original checkpoint and gate rather than upgrading them.
	historical.Session.WorkerImplementation = prreview.WorkerIdentity(a.ReviewWorkers)
	if err = a.Store.Put(ctx, "review-workflow", run.ID, historical, ""); err != nil {
		t.Fatal(err)
	}
	if err = a.Runs.Answer(ctx, historical.Session.Gate.ID, json.RawMessage(`{"scope":"Narrow"}`)); err == nil || !strings.Contains(err.Error(), "prepare a fresh review") {
		t.Fatal("old worker identity accepted an answer", err)
	}
	if err = a.Runs.Interrupt(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, true); err != nil {
		t.Fatal(err)
	}
	waitGuardReviewDone(t, a, run.ID)
	var saved retainedReview
	if err = a.Store.Get(ctx, "review-workflow", run.ID, &saved); err != nil || !reflect.DeepEqual(saved, historical) {
		t.Fatal("incompatible Resume rewrote historical gate or artifacts", saved, err)
	}
	if err = a.Store.Get(ctx, "run", run.ID, &run); err != nil || run.Status != "failed" || !strings.Contains(run.Error, "start a fresh review") || workers.prepareCalls != 1 || workers.reviewCalls != 0 || workers.completionCalls != 0 {
		t.Fatal("incompatible Resume dispatched workers or hid fresh preparation", run, err)
	}
}
