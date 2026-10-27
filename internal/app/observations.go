package app

import (
	"context"
	"errors"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
)

// Observation describes saved external evidence independently of a host read.
type Observation struct {
	ObservedAt  time.Time `json:"observedAt"`
	Refresh     string    `json:"refresh"`
	LastAttempt time.Time `json:"lastAttempt,omitempty"`
	Error       string    `json:"error,omitempty"`
	RetryAt     time.Time `json:"retryAt,omitempty"`
}

func (a *App) observation(ctx context.Context, item domain.Item, kind string, observed time.Time, ttl time.Duration) (Observation, error) {
	var out Observation
	if err := a.Store.Get(ctx, "observation", kind+":"+item.ID, &out); err != nil && !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	pending := out.Refresh == "pending" && time.Since(out.LastAttempt) < 90*time.Second
	out.ObservedAt = observed
	if kind == "reviews" {
		rows, err := store.Select[domain.PRReviews](ctx, a.Store, "pr-reviews", "id", item.ID, "$.reviews")
		r := domain.PRReviews{}
		if len(rows) > 0 {
			r = rows[0]
		}
		if err != nil {
			return out, err
		}
		if r.LastAttempt.After(out.LastAttempt) {
			out.LastAttempt = r.LastAttempt
		}
		out.Error = r.Error
		out.RetryAt = r.RetryAt
	}
	out.Refresh = "stale"
	if observed.IsZero() {
		out.Refresh = "missing"
	} else if time.Since(observed) < ttl {
		out.Refresh = "fresh"
	}
	var repo domain.Repository
	if err := a.Store.Get(ctx, "repository", item.Repository, &repo); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			out.Refresh = "unavailable"
			return out, nil
		}
		return out, err
	}
	if repo.Paused {
		out.Refresh = "paused"
		return out, nil
	}
	if repo.RetryAt.After(out.RetryAt) {
		out.RetryAt = repo.RetryAt
	}
	if a.SavedOnly {
		if pending {
			out.Refresh = "pending"
		} else if out.Refresh != "fresh" && out.RetryAt.After(time.Now()) {
			out.Refresh = "cooldown"
		}
		return out, nil
	}
	if a.GitHub == nil || !a.GitHub.Managed() || a.Runs == nil {
		out.Refresh = "unavailable"
		return out, nil
	}
	usage, err := a.GitHub.UsageStatus(ctx)
	if err != nil {
		return out, err
	}
	if usage.NextRequest.After(out.RetryAt) {
		out.RetryAt = usage.NextRequest
	}
	key := kind + ":" + item.ID
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if a.refreshing[key] {
		out.Refresh = "pending"
		return out, nil
	}
	if out.Refresh == "fresh" {
		return out, nil
	}
	if out.RetryAt.After(time.Now()) {
		out.Refresh = "cooldown"
		return out, nil
	}
	if !out.LastAttempt.IsZero() && time.Since(out.LastAttempt) < time.Minute {
		out.Refresh = "cooldown"
		out.RetryAt = out.LastAttempt.Add(time.Minute)
		return out, nil
	}
	if a.scheduleObservationLocked(item, kind, false) {
		out.Refresh = "pending"
	} else {
		out.Refresh = "unavailable"
	}
	return out, nil
}

// All readers and post-turn hints share a single worker per observation. A
// publication that completes during a read requests one follow-up pass, rather
// than racing the older observation or losing the completion signal.
func (a *App) scheduleObservationLocked(item domain.Item, kind string, afterTurn bool) bool {
	key := kind + ":" + item.ID
	if a.refreshing == nil {
		a.refreshing = map[string]bool{}
		a.refreshAgain = map[string]bool{}
	}
	if a.refreshing[key] {
		if afterTurn {
			a.refreshAgain[key] = true
		}
		return true
	}
	a.refreshing[key] = true
	scheduled := a.Runs.background(func() {
		force := afterTurn
		for {
			ctx, cancel := context.WithTimeout(a.Runs.ctx, 90*time.Second)
			var attempt Observation
			_ = a.Store.Get(ctx, "observation", key, &attempt)
			attempt.LastAttempt = time.Now().UTC()
			attempt.Refresh = "pending"
			if err := a.Store.Put(ctx, "observation", key, attempt, ""); err != nil {
				cancel()
				a.refreshMu.Lock()
				delete(a.refreshing, key)
				delete(a.refreshAgain, key)
				a.refreshMu.Unlock()
				return
			}
			if kind == "reviews" {
				result, err := a.refreshPRReviews(ctx, item.ID, force)
				attempt.Error = result.Error
				attempt.RetryAt = result.RetryAt
				if err != nil {
					attempt.Error = err.Error()
				}
				attempt.Refresh = "complete"
				_ = a.Store.Put(ctx, "observation", key, attempt, "")
			} else {
				err := a.RefreshItemDetail(ctx, item.ID)
				if err != nil {
					attempt.Error = err.Error()
					attempt.RetryAt = time.Now().UTC().Add(time.Minute)
					var rate *providers.RateLimitError
					if errors.As(err, &rate) {
						attempt.RetryAt = rate.Until
					}
				} else {
					attempt.Error = ""
					attempt.RetryAt = time.Time{}
				}
				attempt.Refresh = "complete"
				_ = a.Store.Put(ctx, "observation", key, attempt, "item.observation")
			}
			cancel()
			a.refreshMu.Lock()
			if a.refreshAgain[key] && a.Runs.ctx.Err() == nil {
				delete(a.refreshAgain, key)
				force = true
				a.refreshMu.Unlock()
				continue
			}
			delete(a.refreshing, key)
			delete(a.refreshAgain, key)
			a.refreshMu.Unlock()
			return
		}
	})
	if !scheduled {
		delete(a.refreshing, key)
	}
	return scheduled
}
func (a *App) queueReviewsAfterTurn(id string) {
	rows, err := store.Select[domain.Item](a.Runs.ctx, a.Store, "item", "id", id, "$.activity", "$.body", "$.checks", "$.pr.checks")
	if err != nil || len(rows) == 0 || rows[0].Kind != "pr" {
		return
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	a.scheduleObservationLocked(rows[0], "reviews", true)
}

// Focus is a local monitoring hint, not a prerequisite for a saved read. Queue
// it separately so even an ongoing import transaction cannot hold up tracking.
func (a *App) queueItemFocus(item domain.Item) {
	if a.GitHub == nil || !a.GitHub.Managed() || a.Runs == nil {
		return
	}
	key := "focus:" + item.ID
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if a.refreshing == nil {
		a.refreshing = map[string]bool{}
		a.refreshAgain = map[string]bool{}
	}
	if a.refreshing[key] {
		return
	}
	a.refreshing[key] = true
	if !a.Runs.background(func() {
		defer func() { a.refreshMu.Lock(); delete(a.refreshing, key); a.refreshMu.Unlock() }()
		_ = a.Store.Put(a.Runs.ctx, "github-detail-focus", item.ID, detailFocus{ID: item.ID, SeenAt: time.Now().UTC()}, "")
	}) {
		delete(a.refreshing, key)
	}
}
func (a *App) ItemObservation(ctx context.Context, item domain.Item) (Observation, error) {
	if !a.SavedOnly {
		a.queueItemFocus(item)
	}
	return a.observation(ctx, item, "detail", item.DetailSyncedAt, providers.GitHubDetailTTL)
}

// External observations advance independently of which views happen to be open.
func (a *App) RefreshSavedObservations(ctx context.Context) {
	if a.Runs == nil {
		return
	}
	items, err := store.ListWithout[domain.Item](ctx, a.Store, "item", "$.body", "$.activity", "$.checks", "$.pr.checks")
	if err != nil {
		return
	}
	if len(items) == 0 {
		return
	}
	a.refreshMu.Lock()
	start := 0
	for index, item := range items {
		if item.ID > a.observationAfter {
			start = index
			break
		}
	}
	count := min(16, len(items))
	a.observationAfter = items[(start+count-1)%len(items)].ID
	a.refreshMu.Unlock()
	for n := 0; n < count; n++ {
		item := items[(start+n)%len(items)]
		if ctx.Err() != nil {
			return
		}
		_, _ = a.observation(ctx, item, "detail", item.DetailSyncedAt, providers.GitHubDetailTTL)
		if item.Kind == "pr" {
			_, _ = a.readPRReviews(ctx, item.ID, true)
		}
	}
}
