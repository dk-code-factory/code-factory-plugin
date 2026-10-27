package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/prreview"
	"codefactory/internal/store"
	"context"
	"errors"
	"fmt"
)

type PostRetainedReviewInput struct {
	RunID      string               `json:"runId"`
	Artifact   prreview.Reference   `json:"artifact"`
	Target     string               `json:"target,omitempty"`
	Event      string               `json:"event,omitempty"`
	Summary    *string              `json:"summary,omitempty"`
	Placements []prreview.Placement `json:"placements,omitempty"`
}
type reviewOperationStore struct {
	a     *App
	runID string
}

func (s reviewOperationStore) LoadOperation(ctx context.Context, id string) (prreview.Operation, bool, error) {
	var op prreview.Operation
	err := s.a.Store.Get(ctx, "review-publication", id, &op)
	if errors.Is(err, store.ErrNotFound) {
		return op, false, nil
	}
	return op, err == nil, err
}
func (s reviewOperationStore) SaveOperation(ctx context.Context, op prreview.Operation) error {
	return s.a.Store.Write(ctx, func(t *store.Tx) error {
		var old prreview.Operation
		if err := t.Get("review-publication", op.ID, &old); err == nil {
			if old.Target != op.Target || old.Artifact != op.Artifact {
				return errors.New("review publication target changed")
			}
			if old.PayloadHash != op.PayloadHash || prreview.Digest(old.Expected) != prreview.Digest(op.Expected) || prreview.Digest(old.Approved) != prreview.Digest(op.Approved) {
				if old.Status != "prepared" && old.Status != "skipped" {
					return errors.New("immutable submitted publication intent changed")
				}
				if len(op.PriorAttempts) != len(old.PriorAttempts)+1 {
					return errors.New("unsubmitted intent must be archived before refresh")
				}
				var archived prreview.Operation
				if err := s.a.reviewArtifacts().Load(op.PriorAttempts[len(op.PriorAttempts)-1], &archived); err != nil {
					return err
				}
				if prreview.Digest(archived) != prreview.Digest(old) {
					return errors.New("publication intent archive changed")
				}
			}
			if old.Status == "verified" && prreview.Digest(old) != prreview.Digest(op) {
				return errors.New("terminal publication receipt cannot change")
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if s.runID != "" {
			var run domain.Run
			if err := t.Get("run", s.runID, &run); err != nil {
				return err
			}
			run.PostingOperation = op.ID
			run.PostingState = op.Status
			run.PostingReceipt = op.Receipt
			run.ReviewOmissions = op.Omissions
			if err := t.Put("run", run.ID, run); err != nil {
				return err
			}
			if err := t.Event("run.state", run.ID, map[string]string{"id": run.ID, "postingState": op.Status}); err != nil {
				return err
			}
		}
		if err := t.Put("review-publication", op.ID, op); err != nil {
			return err
		}
		return t.Event("review.publication", op.ID, map[string]string{"id": op.ID, "status": op.Status})
	})
}

var errPostingDisabled = errors.New("automatic review posting disabled by current policy")

func (a *App) PostRetainedReview(ctx context.Context, in PostRetainedReviewInput) (prreview.Operation, error) {
	return a.postRetainedReview(ctx, in, false)
}
func (a *App) postRetainedReview(ctx context.Context, in PostRetainedReviewInput, automatic bool) (op prreview.Operation, err error) {
	var run domain.Run
	if err = a.Store.Get(ctx, "run", in.RunID, &run); err != nil {
		return
	}
	if run.ReviewWorkflow != prreview.Contract || run.ReviewArtifact == nil || *run.ReviewArtifact != in.Artifact || !run.ReviewComplete {
		err = errors.New("posting requires this run's complete retained review artifact")
		return
	}
	var item domain.Item
	if err = a.Store.Get(ctx, "item", run.ItemID, &item); err != nil {
		return
	}
	target := item.URL
	if in.Target != "" && in.Target != target {
		err = errors.New("review posting target mismatch")
		return
	}
	if automatic && (in.Event != "" || in.Summary != nil || len(in.Placements) > 0) {
		err = errors.New("automatic posting requires COMMENT, inline findings and an empty body")
		return
	}
	authorize := func(ctx context.Context, e prreview.Evidence) error {
		var current domain.Run
		if err := a.Store.Get(ctx, "run", run.ID, &current); err != nil {
			return err
		}
		if current.InterruptRequested || current.Status == "terminated" || current.Status == "cleaned" || current.ReviewArtifact == nil || *current.ReviewArtifact != in.Artifact {
			return errors.New("review run or approved artifact changed")
		}
		var repo domain.Repository
		if err := a.Store.Get(ctx, "repository", item.Repository, &repo); err != nil {
			return err
		}
		config, err := a.Config(ctx)
		if err != nil {
			return err
		}
		reviewers, rules := reviewSettings(config, repo)
		var retained retainedReview
		if err = a.Store.Get(ctx, "review-workflow", run.ID, &retained); err != nil {
			return err
		}
		if retained.Session.Implementation != prreview.ImplementationIdentity() {
			return errors.New("review action implementation changed; prepare a fresh review")
		}
		if retained.Configuration != prreview.Digest([]any{reviewers, rules, run.Profile, run.Skills}) || rules != e.Rules {
			return errors.New("effective review configuration changed; review again")
		}
		if automatic {
			policy, err := a.ReviewPostingPolicy(ctx, item.ID, run.AutoPostReview)
			if err != nil {
				return err
			}
			if !policy.Enabled {
				return errPostingDisabled
			}
		}
		return nil
	}
	remote := a.ReviewRemote
	if remote == nil {
		var artifact prreview.Artifact
		var evidence prreview.Evidence
		if err = a.reviewArtifacts().Load(in.Artifact, &artifact); err != nil {
			return
		}
		if err = a.reviewArtifacts().Load(artifact.Evidence, &evidence); err != nil {
			return
		}
		remote = &providers.ReviewGitHub{Client: providers.LiveGitHub(a.GitHub), Pinned: &evidence}
	}
	publisher := prreview.Publisher{Artifacts: a.reviewArtifacts(), Remote: remote, Store: reviewOperationStore{a: a, runID: run.ID}}
	op, err = publisher.Post(ctx, in.Artifact, target, prreview.PostRequest{Event: in.Event, Summary: in.Summary, Placements: in.Placements}, authorize)
	if errors.Is(err, errPostingDisabled) {
		err = nil
		op.Status = "disabled"
	}
	if op.Status != "" {
		var saveErr error
		op, saveErr = a.projectReviewPublication(context.WithoutCancel(ctx), run.ID, "", op)
		if err == nil {
			err = saveErr
		}
	}
	return
}
func (a *App) ReconcileRetainedReview(ctx context.Context, id string) (prreview.Operation, error) {
	remote := a.ReviewRemote
	if remote == nil {
		saved, ok, err := (reviewOperationStore{a: a}).LoadOperation(ctx, id)
		if err != nil {
			return saved, err
		}
		if !ok {
			return saved, errors.New("unknown review publication")
		}
		var artifact prreview.Artifact
		var evidence prreview.Evidence
		if err = a.reviewArtifacts().Load(saved.Artifact, &artifact); err != nil {
			return saved, err
		}
		if err = a.reviewArtifacts().Load(artifact.Evidence, &evidence); err != nil {
			return saved, err
		}
		remote = &providers.ReviewGitHub{Client: providers.LiveGitHub(a.GitHub), Pinned: &evidence}
	}
	publisher := prreview.Publisher{Artifacts: a.reviewArtifacts(), Remote: remote, Store: reviewOperationStore{a: a}}
	op, err := publisher.Reconcile(ctx, id)
	if err != nil {
		return op, err
	}
	runs, err := store.List[domain.Run](ctx, a.Store, "run")
	if err != nil {
		return op, err
	}
	for _, run := range runs {
		if run.PostingOperation == id {
			op, err = a.projectReviewPublication(ctx, run.ID, id, op)
			if err != nil {
				return op, err
			}
		}
	}
	return op, nil
}

// projectReviewPublication reads both records under the writer lock: Publisher has
// already released its operation lock, so its returned operation may be stale.
func (a *App) projectReviewPublication(ctx context.Context, runID, expectedOperation string, op prreview.Operation) (prreview.Operation, error) {
	err := a.Store.Write(ctx, func(t *store.Tx) error {
		var current domain.Run
		if err := t.Get("run", runID, &current); err != nil {
			return err
		}
		operationID := op.ID
		if operationID == "" && op.Status == "disabled" {
			operationID = current.PostingOperation
		}
		if operationID != "" {
			var durable prreview.Operation
			if err := t.Get("review-publication", operationID, &durable); err == nil {
				// Disabled is an application policy result for unsubmitted intent.
				if op.Status == "disabled" && (durable.Status == "prepared" || durable.Status == "skipped") {
					durable.Status = "disabled"
				}
				op = durable
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		if expectedOperation != "" && current.PostingOperation != expectedOperation {
			return nil
		}
		current.PostingState = op.Status
		current.PostingOperation = op.ID
		current.PostingReceipt = op.Receipt
		current.ReviewOmissions = op.Omissions
		if err := t.Put("run", runID, current); err != nil {
			return err
		}
		return t.Event("run.state", runID, map[string]string{"id": runID, "postingState": op.Status})
	})
	return op, err
}

func (a *App) ReviewPublication(ctx context.Context, id string) (prreview.Operation, error) {
	op, exists, err := (reviewOperationStore{a: a}).LoadOperation(ctx, id)
	if err == nil && !exists {
		err = fmt.Errorf("review publication operation not found")
	}
	return op, err
}
