package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"testing"
)

// Legacy fixtures represent runs retained before the workflow migration. They
// keep exercising native transport, deferred admission and original recovery;
// fresh workflow behavior has its own fixture suite.
func legacyReviewSnapshot(t *testing.T, a *App, ctx context.Context, run domain.Run) (domain.Run, error) {
	t.Helper()
	run.ReviewWorkflow = ""
	run.ReviewStage = ""
	return run, a.Store.Put(ctx, "run", run.ID, run, "")
}
func prepareLegacyPRReview(t *testing.T, a *App, ctx context.Context, id string) (domain.Run, error) {
	t.Helper()
	run, err := a.Runs.PreparePRReview(ctx, id)
	if err != nil {
		return run, err
	}
	return legacyReviewSnapshot(t, a, ctx, run)
}
func prepareLegacyReviewLaunch(t *testing.T, a *App, ctx context.Context, in LaunchInput) (domain.Run, error) {
	t.Helper()
	run, err := a.Runs.Prepare(ctx, in)
	if err != nil {
		return run, err
	}
	if run.ReviewWorkflow == "" {
		return run, nil
	}
	return legacyReviewSnapshot(t, a, ctx, run)
}
func processLegacyReviewRequests(t *testing.T, a *App, ctx context.Context) error {
	t.Helper()
	if err := a.ProcessReviewRequests(ctx); err != nil {
		return err
	}
	claims, err := store.List[reviewClaim](ctx, a.Store, "review-claim")
	if err != nil {
		return err
	}
	for _, claim := range claims {
		var run domain.Run
		if err = a.Store.Get(ctx, "run", claim.RunID, &run); err != nil {
			return err
		}
		if run.ReviewWorkflow == "" || run.Status != "prepared" {
			continue
		}
		if run, err = legacyReviewSnapshot(t, a, ctx, run); err != nil {
			return err
		}
		_, startErr := a.Runs.Start(ctx, run.ID, false)
		claim.Status = "started"
		claim.Error = ""
		if startErr != nil {
			claim.Status = "uncertain"
			claim.Error = startErr.Error()
		}
		if err = a.Store.Put(ctx, "review-claim", claim.ID, claim, ""); err != nil {
			return err
		}
		a.reviewError(ctx, run.SourceSnapshot.Repository, claim.Error)
	}
	return nil
}
