package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"errors"
	"fmt"
	"time"
)

// SavedPRReviews never treats a completed run or a transcript URL as proof of
// publication, and does not attribute other reviews on the PR to this run.
func (a *App) SavedPRReviews(ctx context.Context, id string) (domain.PRReviews, error) {
	out := domain.PRReviews{ItemID: id, Reviews: []domain.PublishedReview{}}
	err := a.Store.Get(ctx, "pr-reviews", id, &out)
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	return out, err
}

func (a *App) ReadPRReviews(ctx context.Context, id string) (domain.PRReviews, error) {
	return a.refreshPRReviews(ctx, id, false)
}

func (a *App) refreshPRReviews(ctx context.Context, id string, afterTurn bool) (domain.PRReviews, error) {
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	var item domain.Item
	if err := a.Store.Get(ctx, "item", id, &item); err != nil {
		return domain.PRReviews{}, err
	}
	if item.Kind != "pr" {
		return domain.PRReviews{}, fmt.Errorf("Posted reviews require a synchronized PR itemId")
	}
	out, err := a.SavedPRReviews(ctx, id)
	if err != nil {
		return out, err
	}
	if !afterTurn && time.Since(out.LastAttempt) < time.Minute {
		return out, nil
	}
	out.LastAttempt = time.Now().UTC()
	var repo domain.Repository
	if err := a.Store.Get(ctx, "repository", item.Repository, &repo); err != nil {
		return out, err
	}
	switch {
	case repo.Paused:
		out.Error = "Repository monitoring is paused; showing saved reviews"
	case a.GitHub == nil:
		out.Error = "GitHub is unavailable; showing saved reviews"
	default:
		reviews, observed, err := a.GitHub.PublishedReviews(ctx, item)
		if err != nil {
			out.Error = "Could not refresh posted reviews; showing saved information. " + err.Error()
		} else {
			out.Reviews, out.SyncedAt, out.Error = reviews, observed.UTC(), ""
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return out, a.Store.Put(ctx, "pr-reviews", id, out, "reviews.changed")
}
