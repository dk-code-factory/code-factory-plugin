package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"errors"
	"fmt"
	"strings"
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

// Historical custom reviews did not save PRReview. Recognize the complete saved
// default-template writer format, never markers embedded in untrusted source or
// today's profile defaults. Unmatched owner overrides remain stopped.
func legacyCustomReviewIntent(r domain.Run) bool {
	p, item := r.Profile, r.SourceSnapshot
	if r.PRReview != nil || p.ID == "" || p.Prompt == "" || item == nil || item.ID != r.ItemID || item.Kind != "pr" || item.Repository == "" || item.Number <= 0 || strings.TrimSpace(item.Head) == "" || strings.TrimSpace(item.URL) == "" {
		return false
	}
	// Generic historical preparation also accepted workspace-write reviews.
	// Keep exactly the saved, supported authority; do not upgrade to a pipeline.
	if p.Sandbox != "read-only" && p.Sandbox != "workspace-write" && p.Sandbox != "danger-full-access" ||
		p.Approval != "on-request" && p.Approval != "untrusted" && p.Approval != "never" ||
		p.Publishing != "ask" && p.Publishing != "authorized" {
		return false
	}
	prompt := strings.NewReplacer("{{title}}", item.Title, "{{body}}", item.Body, "{{url}}", item.URL).Replace(p.Prompt)
	return r.Prompt == appendLaunchAuthority(appendPRReviewPrompt(prompt, *item, r.ReviewPromptSnapshot), p)
}

func legacyLaunchBudgetFailure(r domain.Run) bool {
	if r.Status != "failed" || r.ThreadID != "" || r.TurnID != "" || r.InterruptRequested || !(r.AutomaticReview || r.PRReview != nil && *r.PRReview || r.PRReview == nil && r.Profile.ID == "review" || legacyCustomReviewIntent(r)) {
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
		var item domain.Item
		if err := tx.Get("item", r.ItemID, &item); err != nil {
			return err
		}
		if item.Kind != "pr" {
			return nil
		}
		if r.PRReview == nil && !r.AutomaticReview && r.Profile.ID != "review" {
			saved := r.SourceSnapshot // The predicate proved the complete retained writer format.
			if item.ID != saved.ID || item.Repository != saved.Repository || item.Number != saved.Number || item.URL != saved.URL {
				return nil
			}
			intent := true
			r.PRReview = &intent
		}
		r.Status, r.ResumePending, r.LaunchRetryAt = "deferred", true, usage.NextRequest.UTC()
		if r.LaunchRetryAt.IsZero() {
			r.LaunchRetryAt = time.Now().UTC()
		}
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
		_, _, err := m.startDeferredLaunch(ctx, r)
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

func (m *Manager) startDeferredLaunch(ctx context.Context, candidate domain.Run) (domain.Run, *session, error) {
	// The caller holds autoReviewMu through the busy check and accepted start,
	// sharing fresh automatic acceptance without holding manager membership locks.
	// Reject before rereading the retained immutable bundles.
	if candidate.AutomaticReview && m.busy() {
		return candidate, nil, fmt.Errorf("automatic review is waiting for active work")
	}
	return m.startRun(ctx, candidate.ID, false, "", true, nil)
}
