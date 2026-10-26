package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
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
	return a.readPRReviews(ctx, id, false)
}

func (a *App) readPRReviews(ctx context.Context, id string, compact bool) (domain.PRReviews, error) {
	rows, err := store.Select[domain.Item](ctx, a.Store, "item", "id", id, "$.body", "$.activity", "$.checks", "$.pr.checks")
	if err != nil {
		return domain.PRReviews{}, err
	}
	if len(rows) == 0 {
		return domain.PRReviews{}, store.ErrNotFound
	}
	item := rows[0]
	if item.Kind != "pr" {
		return domain.PRReviews{}, fmt.Errorf("Posted reviews require a synchronized PR itemId")
	}
	var out domain.PRReviews
	if compact {
		out, err = a.Store.ReviewSummaries(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			out = domain.PRReviews{ItemID: id, Reviews: []domain.PublishedReview{}}
			err = nil
		}
	} else {
		out, err = a.SavedPRReviews(ctx, id)
	}
	if err != nil {
		return out, err
	}
	status, err := a.observation(ctx, item, "reviews", out.SyncedAt, time.Minute)
	out.Refresh = status.Refresh
	out.RetryAt = status.RetryAt
	return out, err
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
	if !afterTurn && (time.Since(out.LastAttempt) < time.Minute || out.RetryAt.After(time.Now())) {
		return out, nil
	}
	out.LastAttempt = time.Now().UTC()
	out.RetryAt = out.LastAttempt.Add(time.Minute)
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
		if afterTurn {
			ctx = providers.WithGitHubRead(ctx, providers.RequireLive)
		}
		reviews, observed, err := a.GitHub.PublishedReviews(ctx, item)
		if err != nil {
			out.Error = "Could not refresh posted reviews; showing saved information. " + err.Error()
			var rate *providers.RateLimitError
			if errors.As(err, &rate) {
				out.RetryAt = rate.Until
			}
		} else {
			out.Reviews, out.SyncedAt, out.Error = reviews, observed.UTC(), ""
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return out, a.Store.Put(ctx, "pr-reviews", id, out, "reviews.changed")
}
