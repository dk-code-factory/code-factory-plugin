package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"errors"
	"time"
)

// Only the managed source preflight can defer a launch. Provider execution and
// publication failures retain their ordinary, non-replayed failure semantics.
func (m *Manager) deferLaunch(s *session, cause error) bool {
	var rate *providers.RateLimitError
	if m.app.GitHub == nil || !m.app.GitHub.Managed() || !errors.As(cause, &rate) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retired || s.run.InterruptRequested || s.run.ThreadID != "" || s.run.TurnID != "" {
		return false
	}
	s.run.Status, s.run.Error, s.run.ResumePending = "deferred", cause.Error(), true
	s.run.LaunchRetryAt = rate.Until.UTC()
	if !s.run.LaunchRetryAt.After(time.Now()) {
		s.run.LaunchRetryAt = time.Now().UTC().Add(SyncInterval)
	}
	if err := m.persist(s, "run.state"); err != nil {
		s.run.Status, s.run.Error, s.run.ResumePending = "failed", err.Error(), false
		s.run.LaunchRetryAt = time.Time{}
		_ = m.persist(s, "run.state")
	}
	return true
}

func legacyLaunchBudgetFailure(r domain.Run) bool {
	if r.Status != "failed" || r.ThreadID != "" || r.TurnID != "" || r.InterruptRequested || !(r.PRReview || r.AutomaticReview || r.Profile.ID == "review") {
		return false
	}
	const prefix = "cannot verify current work item; work remains stopped: "
	for _, reason := range []string{
		"Code Factory GitHub budget reached; synchronization will retry",
		"GitHub request reserve reached; synchronization will retry",
		"GitHub rate limit; synchronization will retry",
	} {
		if r.Error == prefix+reason {
			return true
		}
	}
	return false
}

func (m *Manager) recoverDeferredLaunch(ctx context.Context, r domain.Run) error {
	if !legacyLaunchBudgetFailure(r) || m.app.GitHub == nil || !m.app.GitHub.Managed() {
		return nil
	}
	var item domain.Item
	if err := m.app.Store.Get(ctx, "item", r.ItemID, &item); err != nil {
		return err
	}
	if item.Kind != "pr" {
		return nil
	}
	usage, err := m.app.GitHub.UsageStatus(ctx)
	if err != nil {
		return err
	}
	return m.app.Store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.Get("run", r.ID, &r); err != nil {
			return err
		}
		if !legacyLaunchBudgetFailure(r) {
			return nil
		}
		r.Status, r.ResumePending, r.LaunchRetryAt = "deferred", true, usage.NextRequest.UTC()
		if err := tx.Put("run", r.ID, r); err != nil {
			return err
		}
		return tx.Event("run.state", r.ID, map[string]string{"id": r.ID, "status": r.Status})
	})
}

// RetryDeferredLaunches runs on the daemon's normal cadence even when repository
// synchronization fails or is not due. startRun serializes and persists the claim
// before launching, so concurrent polls and startup cannot replay the same run.
func (m *Manager) RetryDeferredLaunches(ctx context.Context) error {
	m.app.autoReviewMu.Lock()
	defer m.app.autoReviewMu.Unlock()
	// Share the fresh automatic acceptance/start critical section. A retry cannot
	// take the idle slot between ProcessReviewRequests busy check and Start.
	m.app.autoReviewMu.Lock()
	defer m.app.autoReviewMu.Unlock()
	if !m.adminGate.TryRLock() {
		return nil
	}
	defer m.adminGate.RUnlock()
	if m.app.GitHub == nil || !m.app.GitHub.Managed() {
		return nil
	}
	runs, err := store.ListWithout[domain.Run](ctx, m.app.Store, "run", "$.skills", "$.profile", "$.prompt", "$.reviewPromptSnapshot", "$.sourceSnapshot")
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.Status != "deferred" || !r.ResumePending || r.InterruptRequested || r.ThreadID != "" || r.TurnID != "" || time.Now().Before(r.LaunchRetryAt) {
			continue
		}
		// Disabling or pausing the policy holds accepted work without fetching
		// source or creating a new automatic review claim.
		if r.AutomaticReview {
			if m.busy() {
				continue
			}
			var item domain.Item
			var repo domain.Repository
			if err := m.app.Store.Get(ctx, "item", r.ItemID, &item); err != nil {
				return err
			}
			if err := m.app.Store.Get(ctx, "repository", item.Repository, &repo); err != nil {
				return err
			}
			config, err := m.app.Config(ctx)
			if err != nil {
				return err
			}
			if repo.Paused || !autoReviewEnabled(config, repo) {
				continue
			}
		}
		_, _, err := m.startRun(ctx, r.ID, false, "", true, nil)
		if err != nil && (ctx.Err() != nil || m.ctx.Err() != nil) {
			return err
		}
	}
	return nil
}

func (m *Manager) validateLaunchPolicy(ctx context.Context, r domain.Run, source domain.Item) error {
	if r.ThreadID != "" || source.Kind != "pr" {
		return nil
	}
	return m.app.Store.Write(ctx, func(tx *store.Tx) error {
		if err := validatePRReviewRun(tx.Get, r, source); err != nil {
			return err
		}
		if r.AutomaticReview {
			return validateAutomaticPolicy(tx, r, source, false)
		}
		return nil
	})
}
