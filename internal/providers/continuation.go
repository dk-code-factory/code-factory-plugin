package providers

import (
	"codefactory/internal/domain"
	"context"
	"fmt"
	"strings"
)

// CurrentWorkItem revalidates the source before work starts or continues. It
// bypasses the local TTL, retaining conditional requests and the shared budget.
// It deliberately does not fetch unrelated histories, checks or repositories.
func (g *GitHub) CurrentWorkItem(ctx context.Context, item domain.Item) (domain.Item, error) {
	ctx = context.WithValue(ctx, requireFreshRead{}, true)
	part := "issues"
	if item.Kind == "pr" {
		part = "pulls"
	} else if item.Kind != "issue" {
		return domain.Item{}, fmt.Errorf("unsupported work item kind")
	}
	var source ghIssue
	if _, err := g.get(ctx, fmt.Sprintf("/repos/%s/%s/%d", item.Repository, part, item.Number), &source); err != nil {
		return domain.Item{}, err
	}
	if source.Number != item.Number || (source.State != "open" && source.State != "closed") || item.Kind == "pr" && source.Head.SHA == "" {
		return domain.Item{}, fmt.Errorf("incomplete current work item response")
	}
	fresh := baseItem(domain.Repository{ID: item.Repository}, source)
	fresh.Kind = item.Kind
	fresh.Draft = source.Draft
	fresh.Head, fresh.Branch = source.Head.SHA, source.Head.Ref
	if source.Merged {
		fresh.State = "merged"
	}
	for _, actor := range source.RequestedReviewers {
		fresh.ReviewRequests = append(fresh.ReviewRequests, actor.Login)
	}
	for _, team := range source.RequestedTeams {
		fresh.ReviewTeams = append(fresh.ReviewTeams, strings.SplitN(item.Repository, "/", 2)[0]+"/"+team.Slug)
	}
	return fresh, nil
}
