package providers

import (
	"context"

	"codefactory/internal/domain"
)

// GitHubItemState distinguishes deliberately omitted history from a source's
// complete-detail fallback, including older sources without freshness metadata.
type GitHubItemState struct {
	domain.Item
	Partial bool `json:"partial"`
}

// RefreshItemState reads lifecycle, reviews, development links and CI without
// downloading activity history. Sources without this optional capability retain
// their complete-detail behavior. Callers must merge a partial observation into
// the current saved item rather than treating missing activity as deletion.
func RefreshItemState(ctx context.Context, source GitHubSource, item domain.Item) (GitHubItemState, error) {
	if partial, ok := source.(interface {
		ItemState(context.Context, domain.Item) (GitHubItemState, error)
	}); ok {
		return partial.ItemState(ctx, item)
	}
	fresh, err := source.ItemDetail(ctx, item)
	return GitHubItemState{Item: fresh}, err
}

func (g *GitHub) ItemState(ctx context.Context, item domain.Item) (GitHubItemState, error) {
	if item.Kind != "pr" {
		fresh, err := g.ItemDetail(ctx, item)
		return GitHubItemState{Item: fresh}, err
	}
	// State refresh is meaningful only when its mutable components are validated.
	ctx = WithGitHubRead(ctx, RequireLive)
	fresh, err := g.itemDetail(ctx, item, false)
	if err != nil {
		return GitHubItemState{Item: fresh, Partial: true}, err
	}
	if fresh.Head != item.Head || fresh.Updated.After(item.Updated) {
		// A state read can discover source changes before the index does. Fetch
		// their history now instead of repeatedly returning an unmergeable row.
		fresh, err = g.ItemDetail(ctx, item)
		return GitHubItemState{Item: fresh}, err
	}
	return GitHubItemState{Item: fresh, Partial: true}, nil
}

func (g *CachedGitHub) ItemState(ctx context.Context, item domain.Item) (GitHubItemState, error) {
	var result GitHubItemState
	err := g.query(ctx, "state", item.ID, []any{item.ID, item.Repository, item.Number, item.Kind, item.Head, item.Updated}, &result, GitHubDetailTTL, func(ctx context.Context) (err error) {
		result, err = RefreshItemState(ctx, g.GitHubSource, item)
		return
	})
	return result, err
}
