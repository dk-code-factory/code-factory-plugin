package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"context"
	"fmt"
	"strings"
	"time"
)

func validatePRReviewAuthor(get func(string, string, any) error, item domain.Item) error {
	var viewer domain.GitHubViewer
	if err := get("identity", "github", &viewer); err != nil {
		return fmt.Errorf("cannot verify PR review eligibility: GitHub identity is unavailable: %w", err)
	}
	if strings.TrimSpace(viewer.Login) == "" || strings.TrimSpace(item.Author) == "" {
		return fmt.Errorf("cannot verify PR review eligibility: refresh GitHub identity and PR author before reviewing")
	}
	if strings.EqualFold(item.Author, viewer.Login) {
		return fmt.Errorf("you cannot review your own pull request (%s)", item.ID)
	}
	return nil
}

// New runs retain their review intent even if the configured default changes.
// Legacy snapshots use the same profile selection as ordinary preparation.
func validatePRReviewRun(get func(string, string, any) error, run domain.Run, item domain.Item) error {
	review, err := isPRReview(get, run, item)
	if err != nil {
		return err
	}
	if review {
		// Base is the selected checkout reference, which may be a branch such
		// as main. New preparations retain the accepted PR head separately.
		head := run.Base
		if run.SourceSnapshot != nil && run.SourceSnapshot.Head != "" {
			head = run.SourceSnapshot.Head
		}
		if run.ThreadID == "" && head != "" && item.Head != head {
			return fmt.Errorf("PR head changed from the pinned review target; prepare a new review")
		}
		return validatePRReviewAuthor(get, item)
	}
	return nil
}

func isPRReview(get func(string, string, any) error, run domain.Run, item domain.Item) (bool, error) {
	if item.Kind != "pr" {
		return false, nil
	}
	review := run.AutomaticReview || run.Profile.ID == "review"
	if run.PRReview != nil {
		review = run.AutomaticReview || *run.PRReview
	}
	if !review && run.PRReview == nil {
		var config domain.Config
		var repo domain.Repository
		if err := get("config", "settings", &config); err != nil {
			return false, err
		}
		if err := get("repository", item.Repository, &repo); err != nil {
			return false, err
		}
		review = run.Profile.ID == reviewProfileID(config, repo)
	}
	return review, nil
}

// Network identity validation belongs to execution preflight, never a store
// transaction, preparation, or saved-message restoration.
func (m *Manager) activeReviewPrincipal(ctx context.Context, run domain.Run, item domain.Item) (*domain.GitHubViewer, string, error) {
	review, err := isPRReview(func(kind, id string, out any) error { return m.app.Store.Get(ctx, kind, id, out) }, run, item)
	if err != nil || !review || m.app.GitHub == nil || !m.app.GitHub.Managed() {
		return nil, "", err
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	scope, err := m.app.GitHub.CacheScope(bounded)
	if err != nil || scope == "" {
		return nil, "", fmt.Errorf("cannot verify active GitHub review identity")
	}
	login, err := m.app.GitHub.User(providers.WithGitHubRead(bounded, providers.RequireLive))
	if err != nil {
		return nil, "", fmt.Errorf("cannot verify active GitHub review identity: %w", err)
	}
	if strings.TrimSpace(login) == "" {
		return nil, "", fmt.Errorf("cannot verify active GitHub review identity")
	}
	current, err := m.app.GitHub.CacheScope(bounded)
	if err != nil || current != scope {
		return nil, "", fmt.Errorf("GitHub identity changed during review validation; refresh before starting")
	}
	return &domain.GitHubViewer{Login: login}, scope, nil
}

func withReviewPrincipal(get func(string, string, any) error, principal *domain.GitHubViewer) func(string, string, any) error {
	return func(kind, id string, out any) error {
		if kind == "identity" && id == "github" {
			if principal == nil {
				return fmt.Errorf("active review identity was not verified; refresh before starting")
			}
			*out.(*domain.GitHubViewer) = *principal
			return nil
		}
		return get(kind, id, out)
	}
}
