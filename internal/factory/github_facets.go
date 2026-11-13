package factory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
)

const githubHistoryInterval = time.Hour

var errFacetHistoryRequired = errors.New("current rules require complete history")

type detailFacetCheckpoint struct {
	Revision string    `json:"revision"`
	StateAt  time.Time `json:"stateAt"`
	RetryAt  time.Time `json:"retryAt,omitempty"`
}

func (s *Service) stateFacetsTriggerRule(ctx context.Context, settings Settings, fresh domain.Item) (bool, error) {
	for _, e := range observations(fresh) {
		for _, rule := range settings.Rules {
			if match(rule, e) {
				// Routing has its own durable baseline: an item import may
				// commit before the routing transaction fails or is canceled.
				var routed struct{ Value string }
				err := s.Store.Get(ctx, "factory-observation", e.ItemID+":"+e.Facet, &routed)
				if err != nil && err != store.ErrNotFound {
					return false, err
				}
				if routed.Value != e.Value {
					return true, nil
				}
				break
			}
		}
	}
	return false, nil
}

// History remains on the ordinary detail cadence when discussion rules, retained
// work or a recent viewer need it. Other PRs periodically reconcile full history.
func (s *Service) detailFacetsDue(ctx context.Context, settings Settings, item domain.Item) (history, due bool, err error) {
	now := s.Now()
	var checkpoint detailFacetCheckpoint
	if e := s.Store.Get(ctx, "factory-detail-facets", item.ID, &checkpoint); e != nil && e != store.ErrNotFound {
		return false, false, e
	}
	if checkpoint.Revision == item.Revision && now.Before(checkpoint.RetryAt) || item.PR != nil && now.Before(item.PR.RetryAt) {
		return false, false, nil
	}
	history = item.Kind != "pr" || item.DetailSyncedAt.IsZero() || item.Updated.After(item.DetailSyncedAt) || now.Sub(item.DetailSyncedAt) >= githubHistoryInterval
	for _, rule := range settings.Rules {
		if rule.Disabled || (!slices.Contains(rule.Repositories, "*") && !slices.Contains(rule.Repositories, item.Repository)) {
			continue
		}
		for _, kind := range []string{"issue_comment", "pull_request_review", "pull_request_review_comment"} {
			// Author, labels, draft and actions may change remotely, so a rule's
			// repository/type applicability conservatively requires full history.
			if slices.Contains(rule.Types, kind) {
				history = true
			}
		}
	}
	var session struct{ Execution string }
	if e := s.Store.Get(ctx, "factory-session-runtime", item.ID, &session); e == nil {
		if session.Execution != "prepared" && session.Execution != "cleaned" && session.Execution != "terminated" {
			history = true
		}
	} else if e != store.ErrNotFound {
		return false, false, e
	}
	var focus struct{ SeenAt time.Time }
	if e := s.Store.Get(ctx, "github-detail-focus", item.ID, &focus); e == nil {
		if now.Sub(focus.SeenAt) < providers.GitHubDetailTTL {
			history = true
		}
	} else if e != store.ErrNotFound {
		return false, false, e
	}
	if history {
		return true, now.Sub(item.DetailSyncedAt) >= providers.GitHubDetailTTL, nil
	}
	observed := item.DetailSyncedAt
	if checkpoint.Revision == item.Revision && checkpoint.StateAt.After(observed) {
		observed = checkpoint.StateAt
	}
	return false, now.Sub(observed) >= providers.GitHubDetailTTL, nil
}

// Failure backoff is separate from successful state/history observations. New
// source revisions remain eligible and explicit detail requests bypass it.
func (s *Service) deferDetailFacets(ctx context.Context, before domain.Item, retryAt time.Time, failed ...domain.Item) error {
	return s.Store.Write(ctx, func(tx *store.Tx) error {
		var current domain.Item
		if err := tx.Get("item", before.ID, &current); err != nil {
			return err
		}
		if current.Revision != before.Revision || !current.ReviewObservedAt.Equal(before.ReviewObservedAt) {
			return nil
		}
		if len(failed) > 0 && failed[0].PR != nil && failed[0].PR.Error != "" {
			// Publish the diagnostic without replacing successful observations or
			// routing incomplete provider state. Error/retry fields are operational.
			pr := domain.PullRequest{ID: current.ID, Head: current.Head}
			if current.PR != nil {
				pr = *current.PR
			}
			pr.Error, pr.RetryAt = failed[0].PR.Error, failed[0].PR.RetryAt
			current.PR = &pr
			current.Revision = current.ContentRevision()
			if err := tx.Put("item", current.ID, current); err != nil {
				return err
			}
		}
		var checkpoint detailFacetCheckpoint
		if err := tx.Get("factory-detail-facets", before.ID, &checkpoint); err != nil && err != store.ErrNotFound {
			return err
		}
		if checkpoint.Revision != current.Revision {
			checkpoint.StateAt = time.Time{}
		}
		checkpoint.Revision = current.Revision
		checkpoint.RetryAt = s.Now().Add(providers.GitHubDetailTTL)
		if retryAt.After(checkpoint.RetryAt) {
			checkpoint.RetryAt = retryAt
		}
		return tx.Put("factory-detail-facets", before.ID, checkpoint)
	})
}

// A partial refresh is merged with the latest complete activity under the same
// lock as webhook imports. It never turns omitted activity into deletion or
// claims that old activity was revalidated at the current-state observation time.
func (s *Service) importDetailFacets(ctx context.Context, repo Repository, before, fresh domain.Item, started time.Time, partial bool) error {
	release, ok := s.tryLockSession(fresh.ID)
	if !ok {
		return nil
	}
	defer release()
	var current domain.Item
	if err := s.Store.Get(ctx, "item", fresh.ID, &current); err != nil {
		return err
	}
	if current.Revision != before.Revision || !current.ReviewObservedAt.Equal(before.ReviewObservedAt) {
		return nil
	}
	if fresh.PR != nil && fresh.PR.Error != "" {
		return fmt.Errorf("PR state is incomplete: %s", fresh.PR.Error)
	}
	if partial {
		settings, err := s.Settings(ctx)
		if err != nil {
			return err
		}
		promote, err := s.stateFacetsTriggerRule(ctx, settings, fresh)
		if err != nil {
			return err
		}
		if promote {
			return errFacetHistoryRequired
		}
		if fresh.Head != current.Head || fresh.Updated.After(current.Updated) {
			// New source content needs a complete observation before preserving
			// the old history, including commits from a previous head.
			return nil
		}
		fresh.Activity = current.Activity
		fresh.DetailSyncedAt = current.DetailSyncedAt
	}
	if err := s.importDetailLocked(ctx, repo, fresh, started); err != nil {
		return err
	}
	if fresh.PR != nil && fresh.PR.Error != "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Store.Write(ctx, func(tx *store.Tx) error {
		var saved domain.Item
		if err := tx.Get("item", fresh.ID, &saved); err != nil {
			return err
		}
		if !saved.ReviewObservedAt.Equal(fresh.ReviewObservedAt) || saved.Head != fresh.Head {
			return nil
		}
		return tx.Put("factory-detail-facets", saved.ID, detailFacetCheckpoint{Revision: saved.Revision, StateAt: fresh.ReviewObservedAt})
	})
}
