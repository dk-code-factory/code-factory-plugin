package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type reviewRequestState struct {
	ID         string
	Active     bool
	Head       string
	Generation int
}
type reviewClaim struct {
	ID     string `json:"id"`
	ItemID string `json:"itemId"`
	RunID  string `json:"runId"`
	Head   string `json:"head"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func reviewClaimID(itemID, head string, generation int) string {
	hash := sha256.Sum256(raw([]any{itemID, head, generation}))
	return hex.EncodeToString(hash[:])
}

func reviewProfileID(c domain.Config, r domain.Repository) string {
	if r.ReviewProfileID != "" {
		return r.ReviewProfileID
	}
	if c.ReviewProfileID != "" {
		return c.ReviewProfileID
	}
	return "review"
}
func autoReviewEnabled(c domain.Config, r domain.Repository) bool {
	if r.AutoReview != nil {
		return *r.AutoReview
	}
	return c.AutoReview
}
func requestedReview(i domain.Item, v domain.GitHubViewer) bool {
	if i.Kind != "pr" || i.State != "open" || i.Draft || v.Login == "" {
		return false
	}
	for _, name := range i.ReviewRequests {
		if strings.EqualFold(name, v.Login) {
			return true
		}
	}
	for _, team := range i.ReviewTeams {
		for _, mine := range v.Teams {
			if strings.EqualFold(team, mine) {
				return true
			}
		}
	}
	return false
}
func (a *App) reviewError(ctx context.Context, id, message string) {
	_ = a.Store.Write(ctx, func(t *store.Tx) error {
		var r domain.Repository
		if err := t.Get("repository", id, &r); err != nil {
			return err
		}
		if r.ReviewError == message {
			return nil
		}
		r.ReviewError = message
		if err := t.Put("repository", id, r); err != nil {
			return err
		}
		return t.Event("repository.review", id, map[string]string{"error": message})
	})
}

// ProcessReviewRequests uses explicit system/repository policy. Claims survive restarts,
// and a disappeared/reintroduced request creates a new generation even on the same head.
func (a *App) ProcessReviewRequests(ctx context.Context) error {
	a.autoReviewMu.Lock()
	defer a.autoReviewMu.Unlock()
	if !a.Runs.adminGate.TryRLock() {
		return nil
	}
	defer a.Runs.adminGate.RUnlock()
	config, err := a.Config(ctx)
	if err != nil {
		return err
	}
	repos, err := store.List[domain.Repository](ctx, a.Store, "repository")
	if err != nil {
		return err
	}
	items, err := store.List[domain.Item](ctx, a.Store, "item")
	if err != nil {
		return err
	}
	var viewer domain.GitHubViewer
	if err = a.Store.Get(ctx, "identity", "github", &viewer); errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	byRepo := map[string]domain.Repository{}
	for _, r := range repos {
		byRepo[r.ID] = r
	}
	for _, item := range items {
		if item.Kind != "pr" {
			continue
		}
		repo, ok := byRepo[item.Repository]
		if !ok || repo.Paused || repo.Error != "" || !reviewFresh(config, repo, item) {
			continue
		}
		active := requestedReview(item, viewer)
		var state reviewRequestState
		err = a.Store.Get(ctx, "review-request", item.ID, &state)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if state.ID == "" {
			state.ID = item.ID
		}
		if active && (!state.Active || state.Head != item.Head) {
			state.Generation++
		}
		changed := state.Active != active || state.Head != item.Head
		state.Active = active
		state.Head = item.Head
		if changed {
			if err = a.Store.Put(ctx, "review-request", item.ID, state, ""); err != nil {
				return err
			}
		}
		if !active || item.Head == "" || !autoReviewEnabled(config, repo) {
			continue
		}
		key := reviewClaimID(item.ID, item.Head, state.Generation)
		var old reviewClaim
		if err = a.Store.Get(ctx, "review-claim", key, &old); err == nil {
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		runs, err := store.Select[struct{ ItemID, Status string }](ctx, a.Store, "run", "itemId", item.ID, "$.skills", "$.profile", "$.prompt", "$.reviewPromptSnapshot", "$.sourceSnapshot")
		if err != nil {
			return err
		}
		busy := false
		for _, run := range runs {
			if run.ItemID == item.ID && (run.Status == "queued" || run.Status == "starting" || run.Status == "running" || run.Status == "waiting") {
				busy = true
			}
		}
		if busy || a.Runs.busy() {
			continue
		}
		var profile domain.Profile
		if err = a.Store.Get(ctx, "profile", reviewProfileID(config, repo), &profile); err != nil || !reviewProfileReady(profile) {
			a.reviewError(ctx, repo.ID, "Automatic review needs an available read-only/ask pipeline or an explicitly authorized full-access pipeline.")
			continue
		}
		run, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: item.ID, ProfileID: profile.ID})
		if err != nil {
			a.reviewError(ctx, repo.ID, "Could not prepare the review pipeline; check its selected skills and configuration.")
			continue
		}
		claim := reviewClaim{ID: key, ItemID: item.ID, RunID: run.ID, Head: item.Head, Status: "claimed"}
		err = a.Store.Write(ctx, func(t *store.Tx) error {
			if err := t.Get("review-claim", key, &old); err == nil {
				return fmt.Errorf("already claimed")
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			// Re-read policy and target in the claim transaction so a disabled repository,
			// changed head or removed review request cannot launch from a stale poll.
			var current domain.Repository
			var settings domain.Config
			var pr domain.Item
			var identity domain.GitHubViewer
			var currentProfile domain.Profile
			if err := t.Get("repository", repo.ID, &current); err != nil {
				return err
			}
			if err := t.Get("config", "settings", &settings); err != nil {
				return err
			}
			if err := t.Get("item", item.ID, &pr); err != nil {
				return err
			}
			if err := t.Get("identity", "github", &identity); err != nil {
				return err
			}
			if current.Paused || !autoReviewEnabled(settings, current) || !requestedReview(pr, identity) || pr.Head != item.Head || current.ReviewPrompt != repo.ReviewPrompt || reviewProfileID(settings, current) != profile.ID {
				return fmt.Errorf("review policy or target changed")
			}
			if err := t.Get("profile", profile.ID, &currentProfile); err != nil {
				return err
			}
			if string(raw(currentProfile)) != string(raw(profile)) {
				return fmt.Errorf("review pipeline changed")
			}
			run.AutomaticReview = true
			if err := t.Put("run", run.ID, run); err != nil {
				return err
			}
			if err := t.Put("review-claim", key, claim); err != nil {
				return err
			}
			return t.Event("review.claimed", item.ID, claim)
		})
		if err != nil {
			continue
		}
		_, err = a.Runs.Start(ctx, run.ID, false)
		if err != nil {
			claim.Status = "uncertain"
			claim.Error = "Automatic start was not confirmed. Inspect the retained run; the request will not be replayed."
			if run.ReviewWorkflow != "" {
				claim.Status = "blocked"
				claim.Error = err.Error()
			}

		} else {
			claim.Status = "started"
		}
		if err = a.Store.Put(ctx, "review-claim", key, claim, "review.state"); err != nil {
			return err
		}
		a.reviewError(ctx, repo.ID, claim.Error)
	}
	return nil
}

func validateAutomaticStart(t *store.Tx, run domain.Run) error {
	var item domain.Item
	if err := t.Get("item", run.ItemID, &item); err != nil {
		return err
	}
	return validateAutomaticPolicy(t, run, item, true)
}

func validateAutomaticPolicy(t *store.Tx, run domain.Run, item domain.Item, requireSync bool) error {
	// The current request episode must still own this accepted run. A new
	// request on the same head has a different claim and cannot renew old work.
	var state reviewRequestState
	if err := t.Get("review-request", item.ID, &state); err != nil {
		return err
	}
	var claim reviewClaim
	if err := t.Get("review-claim", reviewClaimID(item.ID, state.Head, state.Generation), &claim); err != nil {
		return err
	}
	if !state.Active || state.Head != item.Head || claim.RunID != run.ID || claim.ItemID != item.ID || claim.Head != item.Head {
		return fmt.Errorf("automatic review request episode changed; inspect the retained launch")
	}
	var repo domain.Repository
	var config domain.Config
	var viewer domain.GitHubViewer
	var profile domain.Profile
	if err := t.Get("repository", item.Repository, &repo); err != nil {
		return err
	}
	if err := t.Get("config", "settings", &config); err != nil {
		return err
	}
	if err := t.Get("identity", "github", &viewer); err != nil {
		return err
	}
	if err := t.Get("profile", run.Profile.ID, &profile); err != nil {
		return err
	}
	if repo.Paused || requireSync && (repo.Error != "" || !reviewFresh(config, repo, item)) || !autoReviewEnabled(config, repo) || !requestedReview(item, viewer) || item.Head != run.Base || repo.ReviewPrompt != run.ReviewPromptSnapshot || reviewProfileID(config, repo) != run.Profile.ID || string(raw(profile)) != string(raw(run.Profile)) || !reviewProfileReady(profile) {
		return fmt.Errorf("automatic review policy or target changed; inspect the retained launch")
	}
	return nil
}

func reviewProfileReady(p domain.Profile) bool {
	return p.Sandbox == "read-only" && p.Publishing == "ask" ||
		p.Sandbox == "danger-full-access" && p.Approval == "never" && p.Publishing == "authorized"
}

func reviewFresh(_ domain.Config, repo domain.Repository, item domain.Item) bool {
	observed := item.ReviewObservedAt
	if observed.IsZero() {
		observed = repo.LastSync
	}
	return !observed.IsZero() && time.Since(observed) <= 2*SyncInterval
}
