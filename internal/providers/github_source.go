package providers

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

// GitHubSource is the application's GitHub boundary. Both the live adapter and
// cache expose these reads; callers express freshness through the context.
type GitHubSource interface {
	CacheScope(context.Context) (string, error)
	Managed() bool
	ConfigureUsage(*store.Store, func(context.Context) (GitHubPolicy, error))
	UsageStatus(context.Context) (GitHubUsage, error)
	SeedCooldown(context.Context, time.Time) error
	User(context.Context) (string, error)
	Teams(context.Context) ([]string, error)
	Sync(context.Context, domain.Repository, bool) ([]domain.Item, error)
	SyncIndex(context.Context, domain.Repository, bool) ([]domain.Item, time.Time, error)
	ItemDetail(context.Context, domain.Item) (domain.Item, error)
	CurrentWorkItem(context.Context, domain.Item) (domain.Item, error)
	PullRequest(context.Context, string, int) (domain.Item, error)
	ReviewRequest(context.Context, string, int) (domain.Item, error)
	PublishedReviews(context.Context, domain.Item) ([]domain.PublishedReview, time.Time, error)
	ReviewNotifications(context.Context, time.Time) ([]ReviewNotification, time.Time, error)
}

type GitHubReadMode int

const (
	CacheAllowed GitHubReadMode = iota
	RequireLive
)

// RequireLive bypasses every TTL cache. Conditional 304 validation is live;
// errors never silently fall back to an older response.
func WithGitHubRead(ctx context.Context, mode GitHubReadMode) context.Context {
	return context.WithValue(ctx, requireFreshRead{}, mode == RequireLive)
}

func (g *GitHub) CacheScope(ctx context.Context) (string, error) {
	token, err := g.Token(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(g.BaseURL+"\x00"+token))), nil
}

var _ GitHubSource = (*GitHub)(nil)
var _ GitHubSource = (*CachedGitHub)(nil)
