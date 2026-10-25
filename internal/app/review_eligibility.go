package app

import (
	"codefactory/internal/domain"
	"fmt"
	"strings"
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
	if item.Kind != "pr" {
		return nil
	}
	review := run.PRReview || run.AutomaticReview || run.Profile.ID == "review"
	if !review {
		var config domain.Config
		var repo domain.Repository
		if err := get("config", "settings", &config); err != nil {
			return err
		}
		if err := get("repository", item.Repository, &repo); err != nil {
			return err
		}
		review = run.Profile.ID == reviewProfileID(config, repo)
	}
	if review {
		return validatePRReviewAuthor(get, item)
	}
	return nil
}
