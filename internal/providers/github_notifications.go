package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"codefactory/internal/domain"
)

type ReviewNotification struct {
	ID, Repository string
	Number         int
	Updated        time.Time
}

// GET with all=true includes read threads. No read/done/subscription mutation is
// used. Notifications are hints; repository reconciliation remains authoritative.
func (g *GitHub) ReviewNotifications(ctx context.Context, since time.Time) ([]ReviewNotification, time.Time, error) {
	raw, err := g.pages(ctx, "/notifications?all=true&participating=false&per_page=100&since="+url.QueryEscape(since.Add(-2*time.Minute).UTC().Format(time.RFC3339)))
	if err != nil {
		return nil, since, err
	}
	result := []ReviewNotification{}
	latest := since
	base, _ := url.Parse(g.BaseURL)
	for _, b := range raw {
		var n struct {
			Reason     string
			Updated    time.Time `json:"updated_at"`
			Repository struct {
				FullName string `json:"full_name"`
			}
			Subject struct{ Type, URL string }
		}
		if err = json.Unmarshal(b, &n); err != nil {
			return nil, since, fmt.Errorf("GitHub notification response unavailable")
		}
		if n.Updated.After(latest) {
			latest = n.Updated
		}
		if n.Reason != "review_requested" || n.Subject.Type != "PullRequest" {
			continue
		}
		u, e := url.Parse(n.Subject.URL)
		if e != nil || u.Host != base.Host || u.Scheme != base.Scheme || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, since, fmt.Errorf("Invalid GitHub review notification target")
		}
		prefix := "/repos/" + n.Repository.FullName + "/pulls/"
		if !strings.HasPrefix(u.Path, prefix) {
			return nil, since, fmt.Errorf("Invalid GitHub review notification target")
		}
		number, e := strconv.Atoi(strings.TrimPrefix(u.Path, prefix))
		if e != nil || number < 1 {
			return nil, since, fmt.Errorf("Invalid GitHub review notification target")
		}
		result = append(result, ReviewNotification{ID: fmt.Sprintf("%s#%d", n.Repository.FullName, number), Repository: n.Repository.FullName, Number: number, Updated: n.Updated})
	}
	return result, latest, nil
}
func (g *GitHub) ReviewRequest(ctx context.Context, repo string, number int) (domain.Item, error) {
	ctx, observed := observeReads(ctx, g.now())
	var i ghIssue
	if _, err := g.get(ctx, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), &i); err != nil {
		return domain.Item{}, err
	}
	if i.Number != number || i.Head.SHA == "" || i.State == "" {
		return domain.Item{}, fmt.Errorf("Incomplete GitHub PR response")
	}
	v := baseItem(domain.Repository{ID: repo}, i)
	v.Kind = "pr"
	v.Head = i.Head.SHA
	v.Branch = i.Head.Ref
	v.Draft = i.Draft
	v.SummaryOnly = true
	v.ReviewObservedAt = *observed
	if i.Merged {
		v.State = "merged"
	}
	for _, r := range i.RequestedReviewers {
		v.ReviewRequests = append(v.ReviewRequests, r.Login)
	}
	for _, t := range i.RequestedTeams {
		v.ReviewTeams = append(v.ReviewTeams, strings.SplitN(repo, "/", 2)[0]+"/"+t.Slug)
	}
	return v, nil
}
