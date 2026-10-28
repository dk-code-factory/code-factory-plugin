package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/prreview"
	"codefactory/internal/store"
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func publicationProjectionFixture(t *testing.T) (*App, context.Context, domain.Run, prreview.Operation) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "publication.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	a := &App{Store: s}
	run := domain.Run{ID: "run", Status: "running", ReviewExecution: "execution", Worktree: "/retained-worktree"}
	if err := s.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	op := prreview.Operation{Version: 1, ID: "operation", Status: "uncertain", Target: "https://example.test/pr/1", Omissions: []prreview.Omission{{Finding: 2, Reason: "outside patch"}}}
	return a, ctx, run, op
}

// Hold the application projection after it has captured Publisher's result and
// listed runs, exactly the boundary at which another writer may commit first.
func delayedPublicationProjection(t *testing.T, a *App, ctx context.Context, op prreview.Operation, reconcile bool) (func(), func() prreview.Operation) {
	t.Helper()
	captured, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var result prreview.Operation
	var projectionErr error
	go func() {
		defer close(done)
		runs, err := store.List[domain.Run](ctx, a.Store, "run")
		if err != nil || len(runs) != 1 {
			projectionErr = err
			return
		}
		close(captured)
		select {
		case <-release:
		case <-ctx.Done():
			projectionErr = ctx.Err()
			return
		}
		expected := ""
		if reconcile {
			expected = op.ID
		}
		result, projectionErr = a.projectReviewPublication(ctx, runs[0].ID, expected, op)
	}()
	join := func() {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("publication projection did not stop")
		}
	}
	t.Cleanup(func() { unblock(); join() })
	select {
	case <-captured:
	case <-done:
		t.Fatalf("publication snapshot failed: %v", projectionErr)
	case <-time.After(5 * time.Second):
		t.Fatal("publication snapshot was not captured")
	}
	return unblock, func() prreview.Operation {
		t.Helper()
		join()
		if projectionErr != nil {
			t.Fatal(projectionErr)
		}
		return result
	}
}

func TestReviewPublicationProjectionPreservesNewerVerifiedReceipt(t *testing.T) {
	for _, tc := range []struct{ name, staleStatus, durableStatus string }{
		{"stale-result", "uncertain", "verified"},
		{"disabled-verified", "disabled", "verified"},
		{"disabled-uncertain", "disabled", "uncertain"},
		{"disabled-submitting", "disabled", "submitting"},
		{"disabled-prepared", "disabled", "prepared"},
		{"disabled-skipped", "disabled", "skipped"},
		{"disabled-no-operation", "disabled", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, ctx, run, op := publicationProjectionFixture(t)
			stale := op
			if tc.staleStatus == "disabled" {
				stale = prreview.Operation{Status: "disabled"}
			} else if err := (reviewOperationStore{a: a, runID: run.ID}).SaveOperation(ctx, stale); err != nil {
				t.Fatal(err)
			}
			release, result := delayedPublicationProjection(t, a, ctx, stale, false)
			concurrent := op
			concurrent.Status = tc.durableStatus
			if tc.durableStatus == "verified" {
				concurrent.Receipt = &prreview.Receipt{ID: "receipt", URL: "https://example.test/receipt", Head: "head", Comments: 1}
			}
			want := stale
			if tc.durableStatus != "" {
				if err := (reviewOperationStore{a: a, runID: run.ID}).SaveOperation(ctx, concurrent); err != nil {
					t.Fatal(err)
				}
				want = concurrent
				if stale.Status == "disabled" && (concurrent.Status == "prepared" || concurrent.Status == "skipped") {
					want.Status = "disabled"
				}
			}
			release()
			if got := result(); !reflect.DeepEqual(got, want) {
				t.Fatalf("stale publisher result replaced durable publication: got %+v, want %+v", got, want)
			}
			var saved domain.Run
			if err := a.Store.Get(ctx, "run", run.ID, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.PostingOperation != want.ID || saved.PostingState != want.Status || !reflect.DeepEqual(saved.PostingReceipt, want.Receipt) || !reflect.DeepEqual(saved.ReviewOmissions, want.Omissions) {
				t.Fatalf("run publication regressed: %+v", saved)
			}
			if tc.durableStatus != "" {
				if durable, err := a.ReviewPublication(ctx, op.ID); err != nil || !reflect.DeepEqual(durable, concurrent) {
					t.Fatalf("durable publication changed: %+v, %v", durable, err)
				}
			}
		})
	}
}

func TestReviewPublicationProjectionPreservesLatestLifecycle(t *testing.T) {
	for _, status := range []string{"terminated", "cleaned", "retargeted"} {
		t.Run(status, func(t *testing.T) {
			a, ctx, run, op := publicationProjectionFixture(t)
			if err := (reviewOperationStore{a: a, runID: run.ID}).SaveOperation(ctx, op); err != nil {
				t.Fatal(err)
			}
			if err := a.Store.Get(ctx, "run", run.ID, &run); err != nil {
				t.Fatal(err)
			}
			op.Status = "verified"
			op.Receipt = &prreview.Receipt{ID: "receipt"}
			if err := (reviewOperationStore{a: a}).SaveOperation(ctx, op); err != nil {
				t.Fatal(err)
			}
			release, result := delayedPublicationProjection(t, a, ctx, op, true)
			latest := run
			latest.Status = status
			latest.InterruptRequested = true
			latest.ResumePending = true
			latest.ReviewExecution = "newer-execution"
			latest.Error = "interrupted during publication reconciliation"
			latest.Worktree = ""
			if status == "retargeted" {
				latest.PostingOperation = "new-operation"
				latest.PostingState = "verified"
				latest.PostingReceipt = &prreview.Receipt{ID: "new-receipt"}
			}
			if err := a.Store.Put(ctx, "run", run.ID, latest, "run.state"); err != nil {
				t.Fatal(err)
			}
			release()
			result()
			if status != "retargeted" {
				latest.PostingState = op.Status
				latest.PostingReceipt = op.Receipt
			}
			var saved domain.Run
			if err := a.Store.Get(ctx, "run", run.ID, &saved); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(saved, latest) {
				t.Fatalf("publication projection lost newer lifecycle or operation: got %+v, want %+v", saved, latest)
			}
		})
	}
}
