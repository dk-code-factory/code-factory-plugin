package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
)

func githubPollSeconds(c domain.Config) int {
	if c.GitHubPollSeconds > 0 {
		return c.GitHubPollSeconds
	}
	return 120
}
func (a *App) EnableGitHubUsage(ctx context.Context) error {
	a.GitHub.ConfigureUsage(a.Store, func(ctx context.Context) (providers.GitHubPolicy, error) {
		c, err := a.Config(ctx)
		if err != nil {
			return providers.GitHubPolicy{}, err
		}
		budget := c.GitHubRequestsPerHour
		if budget == 0 {
			budget = 600
		}
		reserve := 100
		if c.GitHubReserve != nil {
			reserve = *c.GitHubReserve
		}
		return providers.GitHubPolicy{RequestsPerHour: budget, Reserve: reserve, MinInterval: time.Second}, nil
	})
	var existing map[string]any
	if err := a.Store.Get(ctx, "github-usage", "application", &existing); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	repos, err := store.List[domain.Repository](ctx, a.Store, "repository")
	if err != nil {
		return err
	}
	for _, r := range repos {
		if r.RetryAt.After(time.Now()) && strings.Contains(r.Error, "GitHub rate limit") {
			if err := a.GitHub.SeedCooldown(ctx, r.RetryAt); err != nil {
				return err
			}
		}
	}
	return nil
}
func (a *App) saveSyncError(ctx context.Context, id string, cause error) error {
	config, err := a.Config(ctx)
	if err != nil {
		return err
	}
	return a.Store.Write(ctx, func(t *store.Tx) error {
		var current domain.Repository
		if err := t.Get("repository", id, &current); err != nil {
			return err
		}
		current.Error = cause.Error()
		current.SyncFailures = min(current.SyncFailures+1, 5)
		delay := min(time.Duration(githubPollSeconds(config))*time.Second*time.Duration(1<<uint(current.SyncFailures-1)), 15*time.Minute)
		current.RetryAt = time.Now().UTC().Add(delay)
		var rate *providers.RateLimitError
		if errors.As(cause, &rate) && rate.Until.After(current.RetryAt) {
			current.RetryAt = rate.Until
		}
		if err := t.Put("repository", id, current); err != nil {
			return err
		}
		return t.Event("sync.error", id, map[string]string{"error": current.Error})
	})
}
func (a *App) RefreshItemDetail(ctx context.Context, id string) error {
	if a.GitHub == nil || !a.GitHub.Managed() {
		return nil
	}
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	var item domain.Item
	if err := a.Store.Get(ctx, "item", id, &item); err != nil {
		return err
	}
	if err := a.Store.Put(ctx, "github-detail-focus", id, detailFocus{ID: id, SeenAt: time.Now().UTC()}, ""); err != nil {
		return err
	}
	if !item.DetailSyncedAt.IsZero() && time.Since(item.DetailSyncedAt) < providers.GitHubDetailTTL {
		return nil
	}
	var repo domain.Repository
	if err := a.Store.Get(ctx, "repository", item.Repository, &repo); err != nil {
		return err
	}
	if repo.Paused {
		return fmt.Errorf("Repository monitoring is paused; showing saved details")
	}
	fresh, err := a.GitHub.ItemDetail(ctx, item)
	if err != nil {
		return err
	}
	if item.Kind == "pr" && fresh.ReviewObservedAt.Before(item.ReviewObservedAt) {
		return fmt.Errorf("Newer PR data is already saved; retry after the cached response expires")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var viewer domain.GitHubViewer
	if err = a.Store.Get(ctx, "identity", "github", &viewer); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return a.importItems(ctx, repo, []domain.Item{fresh}, viewer.Login, nil, false)
}

type notificationSync struct {
	Login       string
	LastAttempt time.Time
	Since       time.Time
	Error       string
}

func (a *App) SyncNotifications(ctx context.Context) error {
	if a.GitHub == nil || !a.GitHub.Managed() {
		return nil
	}
	c, err := a.Config(ctx)
	if err != nil {
		return err
	}
	var checkpoint notificationSync
	if err = a.Store.Get(ctx, "github-notifications", "cursor", &checkpoint); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if time.Since(checkpoint.LastAttempt) < time.Duration(githubPollSeconds(c))*time.Second {
		return nil
	}
	checkpoint.LastAttempt = time.Now().UTC()
	if checkpoint.Since.IsZero() {
		checkpoint.Since = checkpoint.LastAttempt.Add(-5 * time.Minute)
	}
	// Save the attempt before reading. Failures keep the successful cursor, so an
	// interrupted multi-page batch is retried without dropping its notifications.
	if err = a.Store.Put(ctx, "github-notifications", "cursor", checkpoint, ""); err != nil {
		return err
	}
	repos, e := store.List[domain.Repository](ctx, a.Store, "repository")
	if e != nil {
		return e
	}
	active := false
	for _, r := range repos {
		if !r.Paused {
			active = true
		}
	}
	if !active {
		return nil
	}
	login, e := a.GitHub.User(ctx)
	if e != nil {
		checkpoint.Error = e.Error()
		if saveErr := a.Store.Put(ctx, "github-notifications", "cursor", checkpoint, ""); saveErr != nil {
			return saveErr
		}
		return e
	}
	if checkpoint.Login != "" && checkpoint.Login != login {
		checkpoint.Since = checkpoint.LastAttempt.Add(-5 * time.Minute)
	}
	checkpoint.Login = login
	var identity domain.GitHubViewer
	if e = a.Store.Get(ctx, "identity", "github", &identity); e != nil && !errors.Is(e, store.ErrNotFound) {
		return e
	}
	if identity.Login != login {
		identity = domain.GitHubViewer{Login: login, Teams: []string{}}
		if e = a.Store.Put(ctx, "identity", "github", identity, "identity.changed"); e != nil {
			return e
		}
	}
	notices, next, err := a.GitHub.ReviewNotifications(ctx, checkpoint.Since)
	if err == nil {
		repos, e := store.List[domain.Repository](ctx, a.Store, "repository")
		if e != nil {
			return e
		}
		allowed := map[string]domain.Repository{}
		for _, r := range repos {
			if !r.Paused {
				allowed[r.ID] = r
			}
		}
		var viewer domain.GitHubViewer
		if e = a.Store.Get(ctx, "identity", "github", &viewer); e != nil && !errors.Is(e, store.ErrNotFound) {
			return e
		}
		seen := map[string]bool{}
		for _, n := range notices {
			repo, ok := allowed[n.Repository]
			if !ok || seen[n.ID] {
				continue
			}
			seen[n.ID] = true
			processedKey := viewer.Login + ":" + n.ID
			var processed struct{ Updated time.Time }
			if e = a.Store.Get(ctx, "github-notification-seen", processedKey, &processed); e == nil && !processed.Updated.Before(n.Updated) {
				continue
			} else if e != nil && !errors.Is(e, store.ErrNotFound) {
				return e
			}
			item, e := a.GitHub.ReviewRequest(providers.WithGitHubRead(ctx, providers.RequireLive), n.Repository, n.Number)
			if e != nil {
				err = e
				break
			}
			if e = a.importItems(ctx, repo, []domain.Item{item}, viewer.Login, nil, false); e != nil {
				return e
			}
			processed.Updated = n.Updated
			if e = a.Store.Put(ctx, "github-notification-seen", processedKey, processed, ""); e != nil {
				return e
			}
		}
	}
	checkpoint.Error = ""
	if err != nil {
		checkpoint.Error = err.Error()
	} else if next.After(checkpoint.Since) {
		checkpoint.Since = next
	}
	if e := a.Store.Put(ctx, "github-notifications", "cursor", checkpoint, ""); e != nil {
		return e
	}
	if err == nil && a.Runs != nil {
		return a.ProcessReviewRequests(ctx)
	}
	return err
}

type GitHubStatus struct {
	providers.GitHubUsage
	NotificationError string `json:"notificationError,omitempty"`
}

func (a *App) GitHubStatus(ctx context.Context) (GitHubStatus, error) {
	out := GitHubStatus{}
	if a.GitHub != nil {
		usage, err := a.GitHub.UsageStatus(ctx)
		if err != nil {
			return out, err
		}
		out.GitHubUsage = usage
	}
	var checkpoint notificationSync
	if err := a.Store.Get(ctx, "github-notifications", "cursor", &checkpoint); err != nil && !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	out.NotificationError = checkpoint.Error
	return out, nil
}

type detailFocus struct {
	ID     string
	SeenAt time.Time
}
