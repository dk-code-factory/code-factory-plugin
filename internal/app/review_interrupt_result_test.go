package app

import (
	"codefactory/internal/domain"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// Used only by the shared interrupt wait routine, after store/cancellation work.
// Blocking Done evaluation pauses result consumption without a scheduler race
// or a production test hook, even though the captured execution already joined.
type pausedReviewWaitContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
}

func (c pausedReviewWaitContext) Done() <-chan struct{} {
	close(c.entered)
	<-c.release
	return nil
}

func TestReviewInterruptRetainsJoinedExecutionOutcome(t *testing.T) {
	a, item, workers, _ := retainedFixture(t)
	ctx := context.Background()
	workers.second, workers.release = make(chan struct{}), make(chan struct{})
	run, err := a.Runs.PreparePRReview(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitRetainedReviewValue(t, workers.second, "reviewer before verified interruption")
	db := reviewFaultDB(t, a)
	// The initial Interrupt intent has a pending worker. Fail only finalization
	// after its verified return clears admission; Resume then remains safe.
	if _, err = db.Exec(`CREATE TRIGGER fail_joined_finalization BEFORE UPDATE ON entities WHEN NEW.kind = 'run' AND json_extract(NEW.data, '$.status') = 'interrupted' AND EXISTS (SELECT 1 FROM entities WHERE kind = 'review-workflow' AND id = NEW.id AND json_extract(data, '$.pendingWorker') IS NULL) BEGIN SELECT RAISE(ABORT, 'fixture lost joined execution finalization'); END`); err != nil {
		t.Fatal(err)
	}
	priorErr := a.Runs.Interrupt(ctx, run.ID)
	var persistenceFailure interface{ Code() int }
	if priorErr == nil || !strings.Contains(priorErr.Error(), "fixture lost joined execution finalization") || !errors.As(priorErr, &persistenceFailure) {
		t.Fatal("verified Interrupt lost its typed finalization failure", priorErr)
	}
	owner, unlock, err := a.Runs.lockRun(ctx, run.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	execution := owner.reviewResult
	stopReason := owner.run.Error
	firstExecution := owner.run.ReviewExecution
	unlock()
	paused := pausedReviewWaitContext{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}
	releaseWaiter := sync.OnceFunc(func() { close(paused.release) })
	t.Cleanup(releaseWaiter)
	oldWaiter := make(chan error, 1)
	go func() { oldWaiter <- waitReviewInterrupt(paused, execution, stopReason) }()
	waitRetainedReviewValue(t, paused.entered, "old interrupt before consuming its result")
	if _, err = db.Exec(`DROP TRIGGER fail_joined_finalization`); err != nil {
		t.Fatal(err)
	}
	close(workers.release)
	if _, err = a.Runs.Start(ctx, run.ID, true); err != nil {
		t.Fatal("verified return did not permit replacement", err)
	}
	waitGuardReviewDone(t, a, run.ID)
	var replacement domain.Run
	if err = a.Store.Get(ctx, "run", run.ID, &replacement); err != nil || replacement.ReviewExecution == firstExecution || replacement.Status != "idle" || !replacement.ReviewComplete || replacement.Error != "" || replacement.InterruptRequested {
		t.Fatal("replacement did not complete independently", replacement, err)
	}
	releaseWaiter()
	if err = waitRetainedReviewValue(t, oldWaiter, "old interrupt joined-execution result"); !errors.Is(err, priorErr) || !errors.As(err, &persistenceFailure) {
		t.Fatal("replacement erased the old interrupt's typed failure", err, priorErr)
	}
	var after domain.Run
	if err = a.Store.Get(ctx, "run", run.ID, &after); err != nil || string(raw(after)) != string(raw(replacement)) || workers.prepareCalls != 1 || workers.reviewCalls != 3 || workers.completionCalls != 1 {
		t.Fatal("old result consumption changed replacement state or replayed work", after, err)
	}
}
