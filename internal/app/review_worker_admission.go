package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/prreview"
	"codefactory/internal/store"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"
)

//go:embed review_worker_admission.go
var reviewAdmissionSource string

type reviewWorkerAdmission struct {
	Execution string `json:"execution"`
	ID        string `json:"id"`
	Stage     string `json:"stage"`
}

// One reservation covers the actual Workers call, including CommandWorkers'
// native command delegation. Capture, waits and publication admit no child here.
type reviewAdmissionWorkers struct {
	prreview.Workers
	m       *Manager
	owner   *session
	run     domain.Run
	failure error // Protected by owner.mu; prevents later dispatch after a write failure.
}

func (m *Manager) reviewWorkflowWorkers(run domain.Run, owner *session) *reviewAdmissionWorkers {
	workers := m.app.ReviewWorkers
	if workers == nil {
		workers = m.productionReviewWorkers(run)
	}
	return &reviewAdmissionWorkers{Workers: workers, m: m, owner: owner, run: run}
}

func (w *reviewAdmissionWorkers) ExecutionIdentity() string {
	return prreview.Digest([]string{prreview.Hash([]byte(reviewAdmissionSource)), prreview.WorkerIdentity(w.Workers)})
}

func reviewCleanupError(saved retainedReview) error {
	if saved.CleanupError != "" {
		return fmt.Errorf("%w: %s", prreview.ErrWorkerCleanup, saved.CleanupError)
	}
	if saved.PendingWorker != nil {
		return fmt.Errorf("%w: %s worker admission %s has no retained shutdown proof", prreview.ErrWorkerCleanup, saved.PendingWorker.Stage, saved.PendingWorker.ID)
	}
	return nil
}

func admittedReviewCall[T any](ctx context.Context, w *reviewAdmissionWorkers, stage string, call func() (T, error)) (result T, err error) {
	admission := reviewWorkerAdmission{Execution: w.run.ReviewExecution, ID: ID(), Stage: stage}
	err = func() error {
		w.owner.mu.Lock()
		defer w.owner.mu.Unlock()
		if w.failure != nil {
			return w.failure
		}
		err := w.m.app.Store.Write(ctx, func(t *store.Tx) error {
			var run domain.Run
			var saved retainedReview
			if err := t.Get("run", w.run.ID, &run); err != nil {
				return err
			}
			if run.ReviewExecution != admission.Execution || run.InterruptRequested || run.Status == "terminated" || run.Status == "cleaned" {
				return context.Canceled
			}
			if err := t.Get("review-workflow", w.run.ID, &saved); err != nil {
				return err
			}
			if err := reviewCleanupError(saved); err != nil {
				return err
			}
			saved.PendingWorker = &admission
			return t.Put("review-workflow", w.run.ID, saved)
		})
		if err != nil {
			w.failure = fmt.Errorf("review worker admission could not be retained: %w", err)
		}
		return w.failure
	}()
	if err != nil {
		return result, err
	}
	result, err = call()
	w.owner.mu.Lock()
	defer w.owner.mu.Unlock()
	if errors.Is(err, prreview.ErrWorkerCleanup) {
		w.failure = err
		return result, err
	}
	// Workers return only after their child is joined or no child was admitted;
	// uncertain native admission/termination must return ErrWorkerCleanup. Record
	// that proof even when Interrupt or Close has cancelled/retired the owner.
	proof, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	proofErr := w.m.app.Store.Write(proof, func(t *store.Tx) error {
		var run domain.Run
		var saved retainedReview
		if err := t.Get("run", w.run.ID, &run); err != nil {
			return err
		}
		if err := t.Get("review-workflow", w.run.ID, &saved); err != nil {
			return err
		}
		if run.ReviewExecution != admission.Execution || saved.PendingWorker == nil || *saved.PendingWorker != admission {
			return errors.New("review worker shutdown proof does not match its retained admission")
		}
		saved.PendingWorker = nil
		return t.Put("review-workflow", w.run.ID, saved)
	})
	if proofErr != nil {
		w.failure = errors.Join(err, fmt.Errorf("%w: verified worker return could not be retained: %w", prreview.ErrWorkerCleanup, proofErr))
		return result, w.failure
	}
	return result, err
}

func (w *reviewAdmissionWorkers) Prepare(ctx context.Context, in prreview.PreparationInput, correction string) ([]byte, error) {
	return admittedReviewCall(ctx, w, "prepare", func() ([]byte, error) { return w.Workers.Prepare(ctx, in, correction) })
}
func (w *reviewAdmissionWorkers) MaterialChoice(ctx context.Context, in prreview.PreparationInput, a prreview.Analysis) (prreview.MaterialCheck, error) {
	return admittedReviewCall(ctx, w, "material-choice", func() (prreview.MaterialCheck, error) { return w.Workers.MaterialChoice(ctx, in, a) })
}
func (w *reviewAdmissionWorkers) Review(ctx context.Context, b prreview.Binding, h prreview.Handoff, correction string) ([]byte, error) {
	return admittedReviewCall(ctx, w, "review:"+b.Reviewer.ID, func() ([]byte, error) { return w.Workers.Review(ctx, b, h, correction) })
}
func (w *reviewAdmissionWorkers) Completion(ctx context.Context, p prreview.Preparation, r prreview.Result) (prreview.CompletionAssessment, error) {
	return admittedReviewCall(ctx, w, "completion", func() (prreview.CompletionAssessment, error) { return w.Workers.Completion(ctx, p, r) })
}
