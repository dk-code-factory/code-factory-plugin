package providers

import (
	"bytes"
	"codefactory/internal/domain"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type githubHeadContext struct{}

// graph only accepts application-owned queries, never mutations or source text.
func (g *GitHub) graph(ctx context.Context, query string, variables any, out any) error {
	token, err := g.Token(ctx)
	if err != nil {
		return err
	}
	b, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", g.BaseURL+"/graphql", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if g.Managed() {
		g.requestMu.Lock()
		defer g.requestMu.Unlock()
	}
	key := fmt.Sprintf("graphql:%x:%x", sha256.Sum256([]byte(token)), sha256.Sum256(append(b, []byte(fmt.Sprint(ctx.Value(githubHeadContext{})))...)))
	cached, ok, err := g.cached(ctx, key)
	if err != nil {
		return fmt.Errorf("GitHub cache unavailable")
	}
	if g.Managed() && ok && cached.Until.After(g.now()) && ctx.Value(requireFreshRead{}) != true {
		noteRead(ctx, cached.CheckedAt)
		return json.Unmarshal(cached.Body, out)
	}
	// Retain an in-memory hint for clients without attached durable accounting.
	g.mu.Lock()
	retry := g.graphRetryAt
	g.mu.Unlock()
	if g.now().Before(retry) {
		return &RateLimitError{Until: retry}
	}
	if err = g.beginRequest(ctx, token, "graphql", "/graphql"); err != nil {
		return err
	}
	res, err := g.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("GitHub metadata connection failed")
	}
	defer res.Body.Close()
	if err = g.observe(ctx, token, "graphql", "/graphql", res.Header); err != nil {
		return fmt.Errorf("GitHub request budget unavailable")
	}
	rateLimit := func() error {
		rate := g.rateLimited(ctx, token, "graphql", res.Header)
		var limited *RateLimitError
		if errors.As(rate, &limited) {
			g.mu.Lock()
			g.graphRetryAt = limited.Until
			g.mu.Unlock()
		}
		return rate
	}
	if res.StatusCode == 429 || res.StatusCode == 403 && (res.Header.Get("Retry-After") != "" || res.Header.Get("X-RateLimit-Remaining") == "0") {
		return rateLimit()
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
	if err != nil || len(body) > 16<<20 {
		return fmt.Errorf("GitHub metadata unavailable")
	}
	if res.StatusCode == 403 && rateBody(body) {
		return rateLimit()
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub metadata unavailable (HTTP %d)", res.StatusCode)
	}
	var result struct {
		Data   json.RawMessage
		Errors []struct {
			Type       string
			Message    string
			Extensions struct{ Code string }
		}
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("GitHub metadata unavailable")
	}
	for _, e := range result.Errors {
		if e.Type == "RATE_LIMITED" || e.Extensions.Code == "RATE_LIMITED" || strings.Contains(strings.ToLower(e.Message), "rate limit") || res.Header.Get("X-RateLimit-Remaining") == "0" {
			return rateLimit()
		}
	}
	if len(result.Errors) > 0 || len(result.Data) == 0 || string(result.Data) == "null" {
		return fmt.Errorf("GitHub metadata unavailable")
	}
	if err = json.Unmarshal(result.Data, out); err != nil {
		return fmt.Errorf("GitHub metadata unavailable")
	}
	noteRead(ctx, g.now())
	if err = g.requestSucceeded(ctx); err != nil {
		return fmt.Errorf("GitHub request budget unavailable")
	}
	if g.Managed() {
		if err = g.remember(ctx, key, cacheEntry{Body: result.Data, Header: http.Header{}, CheckedAt: g.now(), Until: g.now().Add(GitHubDetailTTL)}); err != nil {
			return fmt.Errorf("GitHub cache unavailable")
		}
	}
	return nil
}

// EnrichPullRequest reads authoritative review state and development links. A
// partial failure remains visible independently of the REST item checkpoint.
func (g *GitHub) EnrichPullRequest(ctx context.Context, item *domain.Item) {
	ctx, observed := observeReads(ctx, g.now())
	ctx = context.WithValue(ctx, githubHeadContext{}, item.Head)
	p := domain.PullRequest{ID: item.ID, Title: item.Title, URL: item.URL, State: item.State, Draft: item.Draft, Head: item.Head, ReviewDecision: "unknown", CI: "unknown", Checks: checksFromREST(item.Checks)}
	parts := strings.SplitN(item.Repository, "/", 2)
	if len(parts) != 2 {
		p.Error = "Invalid PR repository"
		item.PR = &p
		return
	}
	var response struct {
		Repository *struct {
			PullRequest *struct {
				HeadRefOid               string
				ReviewDecision           *string
				StatusCheckRollup        *struct{ State string }
				LatestOpinionatedReviews struct {
					Nodes    []struct{ State string }
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
				}
				ClosingIssuesReferences struct {
					Nodes []struct {
						Number     int
						Repository struct{ NameWithOwner string }
					}
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
				}
			}
		}
	}
	query := `query($owner:String!,$name:String!,$number:Int!,$after:String,$reviewAfter:String){repository(owner:$owner,name:$name){pullRequest(number:$number){headRefOid reviewDecision statusCheckRollup{state} latestOpinionatedReviews(first:100,after:$reviewAfter){nodes{state} pageInfo{hasNextPage endCursor}} closingIssuesReferences(first:100,after:$after){nodes{number repository{nameWithOwner}} pageInfo{hasNextPage endCursor}}}}}`
	var after, reviewAfter any
	closingDone, reviewsDone, approved, changesRequested := false, false, false, false
	closings := []string{}
	for page := 0; page < 10000; page++ {
		response.Repository = nil
		err := g.graph(ctx, query, map[string]any{"owner": parts[0], "name": parts[1], "number": item.Number, "after": after, "reviewAfter": reviewAfter}, &response)
		if err != nil || response.Repository == nil || response.Repository.PullRequest == nil {
			var rate *RateLimitError
			if errors.As(err, &rate) {
				p.RetryAt = rate.Until
			}
			p.Error = "PR review and development links unavailable"
			item.PR = &p
			return
		}
		pr := response.Repository.PullRequest
		// Never combine checks or approval state from a different head snapshot.
		if pr.HeadRefOid != item.Head {
			p.Error = "PR head changed during synchronization"
			item.PR = &p
			return
		}
		p.ReviewDecision = "none"
		if pr.ReviewDecision != nil {
			p.ReviewDecision = strings.ToLower(*pr.ReviewDecision)
		}
		p.CI = "none"
		if pr.StatusCheckRollup != nil {
			p.CI = strings.ToLower(pr.StatusCheckRollup.State)
		}
		if !closingDone {
			for _, issue := range pr.ClosingIssuesReferences.Nodes {
				closings = append(closings, fmt.Sprintf("%s#%d", issue.Repository.NameWithOwner, issue.Number))
			}
		}
		if !reviewsDone {
			for _, review := range pr.LatestOpinionatedReviews.Nodes {
				approved = approved || review.State == "APPROVED"
				changesRequested = changesRequested || review.State == "CHANGES_REQUESTED"
			}
		}
		info := pr.ClosingIssuesReferences.PageInfo
		reviewInfo := pr.LatestOpinionatedReviews.PageInfo
		closingDone = closingDone || !info.HasNextPage
		reviewsDone = reviewsDone || !reviewInfo.HasNextPage
		if closingDone && reviewsDone {
			if pr.ReviewDecision == nil {
				if changesRequested {
					p.ReviewDecision = "changes_requested"
				} else if approved {
					p.ReviewDecision = "approved"
				}
			}
			item.ClosingIssues = closings
			p.SyncedAt = *observed
			item.PR = &p
			return
		}
		if !closingDone {
			if info.EndCursor == "" || info.EndCursor == after {
				break
			}
			after = info.EndCursor
		}
		if !reviewsDone {
			if reviewInfo.EndCursor == "" || reviewInfo.EndCursor == reviewAfter {
				break
			}
			reviewAfter = reviewInfo.EndCursor
		}
	}
	p.Error = "Incomplete PR development links"
	item.PR = &p
}

func checksFromREST(raw json.RawMessage) []domain.Check {
	var data struct {
		Checks []struct {
			Name, Status, Conclusion string
			URL                      string `json:"html_url"`
			Started                  string `json:"started_at"`
			ID                       int64
		}
		Statuses []struct {
			Context, State string
			URL            string `json:"target_url"`
			Created        string `json:"created_at"`
			ID             int64
		}
	}
	_ = json.Unmarshal(raw, &data)
	out := []domain.Check{}
	// GitHub returns statuses newest first. Older failures must not override a rerun.
	sort.SliceStable(data.Statuses, func(i, j int) bool {
		return data.Statuses[i].Created > data.Statuses[j].Created || data.Statuses[i].Created == data.Statuses[j].Created && data.Statuses[i].ID > data.Statuses[j].ID
	})
	seen := map[string]bool{}
	for _, s := range data.Statuses {
		if !seen[s.Context] {
			out = append(out, domain.Check{Name: s.Context, Status: s.State, Conclusion: s.State, URL: s.URL})
			seen[s.Context] = true
		}
	}
	for _, c := range data.Checks {
		out = append(out, domain.Check{Name: c.Name, Status: c.Status, Conclusion: c.Conclusion, URL: c.URL})
	}
	return out
}

// PullRequest reads only this PR; it never imports or configures its repository.
func (g *GitHub) PullRequest(ctx context.Context, repo string, number int) (domain.Item, error) {
	ctx, observed := observeReads(ctx, g.now())
	var pr struct {
		ghIssue
		Draft, Merged      bool
		Head               struct{ SHA, Ref string }
		RequestedReviewers []domain.Actor          `json:"requested_reviewers"`
		RequestedTeams     []struct{ Slug string } `json:"requested_teams"`
	}
	path := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
	if _, err := g.get(ctx, path, &pr); err != nil {
		return domain.Item{}, err
	}
	if pr.Number != number || pr.Head.SHA == "" || pr.State == "" {
		return domain.Item{}, fmt.Errorf("Incomplete GitHub PR response")
	}
	item := domain.Item{ID: fmt.Sprintf("%s#%d", repo, number), Repository: repo, Number: number, Kind: "pr", Title: pr.Title, Body: pr.Body, State: pr.State, URL: pr.HTMLURL, Author: pr.User.Login, Updated: pr.Updated, Draft: pr.Draft, Head: pr.Head.SHA, Branch: pr.Head.Ref, Activity: []domain.Activity{}, Labels: []string{}, Assignees: []string{}}
	if pr.Merged {
		item.State = "merged"
	}
	for _, a := range pr.Assignees {
		item.Assignees = append(item.Assignees, a.Login)
	}
	for _, l := range pr.Labels {
		item.Labels = append(item.Labels, l.Name)
	}
	for _, r := range pr.RequestedReviewers {
		item.ReviewRequests = append(item.ReviewRequests, r.Login)
	}
	for _, t := range pr.RequestedTeams {
		item.ReviewTeams = append(item.ReviewTeams, strings.SplitN(repo, "/", 2)[0]+"/"+t.Slug)
	}
	// This targeted fetch is used for references and lifecycle reconciliation;
	// complete activity is retained by the ordinary configured-repository sync.
	var checks struct {
		Total int               `json:"total_count"`
		Runs  []json.RawMessage `json:"check_runs"`
	}
	for page := 1; page < 10000; page++ {
		var part struct {
			Total int               `json:"total_count"`
			Runs  []json.RawMessage `json:"check_runs"`
		}
		if _, err := g.get(ctx, fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100&page=%d", repo, item.Head, page), &part); err != nil {
			return domain.Item{}, err
		}
		checks.Total = part.Total
		checks.Runs = append(checks.Runs, part.Runs...)
		if len(checks.Runs) >= checks.Total {
			break
		}
		if len(part.Runs) == 0 {
			return domain.Item{}, fmt.Errorf("Incomplete PR checks")
		}
	}
	statuses, err := g.pages(ctx, fmt.Sprintf("/repos/%s/commits/%s/statuses?per_page=100", repo, item.Head))
	if err != nil {
		return domain.Item{}, err
	}
	item.Checks, _ = json.Marshal(map[string]any{"checks": checks.Runs, "statuses": statuses})
	g.EnrichPullRequest(ctx, &item)
	item.ReviewObservedAt = *observed
	item.Revision = item.ContentRevision()
	return item, nil
}
