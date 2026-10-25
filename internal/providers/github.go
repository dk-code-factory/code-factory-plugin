package providers

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	Redirect  string
	ID        string
	CheckedAt time.Time
	Until     time.Time

	ETag   string
	Body   []byte
	Header http.Header
}

type RateLimitError struct {
	Until  time.Time
	Reason string
}

func (e *RateLimitError) Error() string {
	if e.Reason != "" {
		return e.Reason
	}
	return "GitHub rate limit; synchronization will retry"
}

type GitHub struct {
	requestMu   sync.Mutex
	usageStore  *store.Store
	usageMemory *githubUsage
	policy      func(context.Context) (GitHubPolicy, error)
	clock       func() time.Time

	cacheBytes   int
	mu           sync.Mutex
	cache        map[string]cacheEntry
	graphRetryAt time.Time
	BaseURL      string
	Client       *http.Client
	Token        func(context.Context) (string, error)
}

func NewGitHub() *GitHub {
	return &GitHub{BaseURL: "https://api.github.com", Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Token: func(ctx context.Context) (string, error) {
		b, e := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com").Output()
		if e != nil {
			return "", fmt.Errorf("GitHub authentication unavailable; reconnect with GitHub CLI")
		}
		token := strings.TrimSpace(string(b))
		if token == "" {
			return "", fmt.Errorf("GitHub authentication unavailable; reconnect with GitHub CLI")
		}
		return token, nil
	}}
}

type githubRedirect struct{ location string }
type requireFreshRead struct{}

func (r *githubRedirect) Error() string { return "GitHub repository redirected" }
func (g *GitHub) get(ctx context.Context, path string, out any) (http.Header, error) {
	for hop := 0; hop < 4; hop++ {
		h, err := g.getOne(ctx, path, out)
		var redirect *githubRedirect
		if !errors.As(err, &redirect) {
			return h, err
		}
		path = redirect.location
	}
	return nil, fmt.Errorf("GitHub redirect limit exceeded")
}
func (g *GitHub) getOne(ctx context.Context, path string, out any) (http.Header, error) {
	u := g.BaseURL + path
	if strings.HasPrefix(path, "http") {
		u = path
	}
	parsed, e := url.Parse(u)
	if e != nil {
		return nil, fmt.Errorf("invalid GitHub pagination URL")
	}
	base, _ := url.Parse(g.BaseURL)
	if parsed.Host != base.Host || parsed.Scheme != base.Scheme || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid GitHub pagination URL")
	}
	token, e := g.Token(ctx)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", u, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	tokenHash := sha256.Sum256([]byte(token))
	cacheKey := fmt.Sprintf("%s#%x", u, tokenHash)
	if g.Managed() {
		g.requestMu.Lock()
		defer g.requestMu.Unlock()
	}
	cached, hasCache, err := g.cached(ctx, cacheKey)
	if err != nil {
		return nil, fmt.Errorf("GitHub cache unavailable")
	}
	if g.Managed() && hasCache && cached.Until.After(g.now()) && ctx.Value(requireFreshRead{}) != true {
		if cached.Redirect != "" {
			return nil, &githubRedirect{location: cached.Redirect}
		}
		noteRead(ctx, cached.CheckedAt)
		return cached.Header.Clone(), json.Unmarshal(cached.Body, out)
	}
	if hasCache {
		if cached.ETag != "" {
			req.Header.Set("If-None-Match", cached.ETag)
		} else if modified := cached.Header.Get("Last-Modified"); modified != "" {
			req.Header.Set("If-Modified-Since", modified)
		}
	}
	if err = g.beginRequest(ctx, token, "core", parsed.Path); err != nil {
		return nil, err
	}
	res, e := g.httpClient().Do(req)
	if e != nil {
		return nil, fmt.Errorf("GitHub connection failed")
	}
	defer res.Body.Close()
	if err = g.observe(ctx, token, "core", parsed.Path, res.Header); err != nil {
		return nil, fmt.Errorf("GitHub request budget unavailable")
	}
	if res.StatusCode == 429 || res.StatusCode == 403 && (res.Header.Get("X-RateLimit-Remaining") == "0" || res.Header.Get("Retry-After") != "") || res.StatusCode >= 500 && res.Header.Get("Retry-After") != "" {
		return nil, g.rateLimited(ctx, token, "core", res.Header)
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
	if e != nil {
		return nil, fmt.Errorf("GitHub response could not be read")
	}
	if len(body) > 16<<20 {
		return nil, fmt.Errorf("GitHub response too large")
	}
	if res.StatusCode >= 500 && res.Header.Get("Retry-After") != "" {
		return nil, g.rateLimited(ctx, token, "core", res.Header)
	}
	if res.StatusCode == 429 || (res.StatusCode == 403 && (res.Header.Get("X-RateLimit-Remaining") == "0" || res.Header.Get("Retry-After") != "" || rateBody(body))) {
		return nil, g.rateLimited(ctx, token, "core", res.Header)
	}
	if res.StatusCode == 301 || res.StatusCode == 302 || res.StatusCode == 307 || res.StatusCode == 308 {
		next, e := url.Parse(res.Header.Get("Location"))
		if e != nil || res.Header.Get("Location") == "" {
			return nil, fmt.Errorf("Invalid GitHub redirect")
		}
		next = parsed.ResolveReference(next)
		if next.Host != base.Host || next.Scheme != base.Scheme || next.User != nil || next.Fragment != "" {
			return nil, fmt.Errorf("Invalid GitHub redirect")
		}
		if res.StatusCode == 301 {
			if err = g.remember(ctx, cacheKey, cacheEntry{Redirect: next.String(), Header: http.Header{}, CheckedAt: g.now(), Until: g.now().Add(24 * time.Hour)}); err != nil {
				return nil, fmt.Errorf("GitHub cache unavailable")
			}
		}
		return nil, &githubRedirect{location: next.String()}
	}
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, fmt.Errorf("GitHub access revoked or insufficient repository permissions")
	}
	if res.StatusCode == 304 && hasCache {
		if err = g.requestSucceeded(ctx); err != nil {
			return nil, fmt.Errorf("GitHub request budget unavailable")
		}
		noteRead(ctx, g.now())
		cached.CheckedAt = g.now()
		cached.Until = cached.CheckedAt.Add(restTTL(parsed.Path, res.Header))
		for k, v := range safeCacheHeader(res.Header) {
			cached.Header[k] = v
		}
		if e = g.remember(ctx, cacheKey, cached); e != nil {
			return nil, fmt.Errorf("GitHub cache unavailable")
		}
		return cached.Header.Clone(), json.Unmarshal(cached.Body, out)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub request failed (HTTP %d)", res.StatusCode)
	}
	if e = json.Unmarshal(body, out); e != nil {
		return nil, fmt.Errorf("GitHub response unavailable")
	}
	noteRead(ctx, g.now())
	if err = g.requestSucceeded(ctx); err != nil {
		return nil, fmt.Errorf("GitHub request budget unavailable")
	}
	ttl := restTTL(parsed.Path, res.Header)
	if res.Header.Get("ETag") != "" || res.Header.Get("Last-Modified") != "" || ttl > 0 {
		entry := cacheEntry{ETag: res.Header.Get("ETag"), Body: body, Header: safeCacheHeader(res.Header), CheckedAt: g.now(), Until: g.now().Add(ttl)}
		if e = g.remember(ctx, cacheKey, entry); e != nil {
			return nil, fmt.Errorf("GitHub cache unavailable")
		}
	}
	return res.Header, nil
}
func (g *GitHub) pages(ctx context.Context, path string) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	for page := 0; path != ""; page++ {
		if page > 10000 {
			return nil, fmt.Errorf("GitHub pagination exceeded limit")
		}
		var data []json.RawMessage
		h, e := g.get(ctx, path, &data)
		if e != nil {
			return nil, e
		}
		out = append(out, data...)
		path = ""
		for _, link := range strings.Split(h.Get("Link"), ",") {
			parts := strings.Split(strings.TrimSpace(link), ";")
			if len(parts) > 1 && strings.Contains(parts[1], `rel="next"`) {
				path = strings.Trim(parts[0], "<>")
			}
		}
	}
	return out, nil
}
func (g *GitHub) User(ctx context.Context) (string, error) {
	var u struct{ Login string }
	_, e := g.get(ctx, "/user", &u)
	return u.Login, e
}

// Teams uses the current GitHub authentication and follows every membership page.
func (g *GitHub) Teams(ctx context.Context) ([]string, error) {
	raw, err := g.pages(ctx, "/user/teams?per_page=100")
	if err != nil {
		return nil, err
	}
	teams := []string{}
	for _, b := range raw {
		var t struct {
			Slug         string
			Organization domain.Actor
		}
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, err
		}
		if t.Organization.Login != "" && t.Slug != "" {
			teams = append(teams, t.Organization.Login+"/"+t.Slug)
		}
	}
	return teams, nil
}

type ghIssue struct {
	Draft              bool
	Merged             bool
	Head               struct{ SHA, Ref string }
	RequestedReviewers []domain.Actor          `json:"requested_reviewers"`
	RequestedTeams     []struct{ Slug string } `json:"requested_teams"`

	Number             int
	Title, Body, State string
	HTMLURL            string `json:"html_url"`
	User               domain.Actor
	Assignees          []domain.Actor
	Labels             []struct{ Name string }
	Updated            time.Time       `json:"updated_at"`
	Pull               json.RawMessage `json:"pull_request"`
}

func (g *GitHub) listItems(ctx context.Context, repo domain.Repository, full bool) ([]ghIssue, error) {
	path := "/repos/" + repo.ID
	query := "?state=all&sort=updated&direction=asc&per_page=100"
	since := ""
	if repo.Imported && !repo.LastSync.IsZero() {
		since = "&since=" + url.QueryEscape(repo.LastSync.Add(-2*time.Minute).Format(time.RFC3339))
	}
	if full {
		query = "?state=open&sort=updated&direction=asc&per_page=100"
	} else {
		query += since
	}
	raw, e := g.pages(ctx, path+"/issues"+query)
	if e != nil {
		return nil, e
	}
	if full && repo.Imported {
		incremental, e := g.pages(ctx, path+"/issues?state=all&sort=updated&direction=asc&per_page=100"+since)
		if e != nil {
			return nil, e
		}
		raw = append(raw, incremental...)
	}
	byID := map[int]ghIssue{}
	for _, b := range raw {
		var i ghIssue
		if e = json.Unmarshal(b, &i); e != nil {
			return nil, e
		}
		byID[i.Number] = i
	}
	// Reviews, checks and commits need not advance the issue updated_at timestamp.
	prs, e := g.pages(ctx, path+"/pulls?state=open&per_page=100")
	if e != nil {
		return nil, e
	}
	for _, b := range prs {
		var i ghIssue
		if e = json.Unmarshal(b, &i); e != nil {
			return nil, e
		}
		i.Pull = json.RawMessage(`{}`)
		byID[i.Number] = i
	}

	issues := make([]ghIssue, 0, len(byID))
	for _, i := range byID {
		issues = append(issues, i)
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	return issues, nil
}
func (g *GitHub) Sync(ctx context.Context, repo domain.Repository, full bool) ([]domain.Item, error) {
	issues, e := g.listItems(ctx, repo, full)
	if e != nil {
		return nil, e
	}
	// Bound enrichment work while allowing independent items to make progress.
	// The repository checkpoint advances only after every worker succeeds.
	items := make([]domain.Item, len(issues))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var workers sync.WaitGroup
	var firstErr error
	var failed sync.Once
	for worker := 0; worker < min(4, len(issues)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				item, err := g.syncItem(ctx, repo, issues[index])
				if err != nil {
					failed.Do(func() { firstErr = err; cancel() })
					return
				}
				items[index] = item
			}
		}()
	}
dispatch:
	for index := range issues {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func baseItem(repo domain.Repository, i ghIssue) domain.Item {
	v := domain.Item{ID: fmt.Sprintf("%s#%d", repo.ID, i.Number), Repository: repo.ID, Number: i.Number, Title: i.Title, Body: i.Body, URL: i.HTMLURL, State: i.State, Author: i.User.Login, Updated: i.Updated, Kind: "issue", Labels: []string{}, Assignees: []string{}, Activity: []domain.Activity{}}
	for _, l := range i.Labels {
		v.Labels = append(v.Labels, l.Name)
	}
	for _, a := range i.Assignees {
		v.Assignees = append(v.Assignees, a.Login)
	}

	return v
}

// SyncIndex uses list payloads; rich per-item history is never part of polling.
func (g *GitHub) SyncIndex(ctx context.Context, repo domain.Repository, full bool) ([]domain.Item, time.Time, error) {
	ctx, observed := observeReads(ctx, g.now())
	raw, err := g.listItems(ctx, repo, full)
	if err != nil {
		return nil, time.Time{}, err
	}
	items := make([]domain.Item, 0, len(raw))
	for _, i := range raw {
		v := baseItem(repo, i)
		v.SummaryOnly = true
		if len(i.Pull) > 0 && string(i.Pull) != "null" {
			if i.Head.SHA == "" { // Older/provider fixtures can omit list metadata.
				var metadata ghIssue
				if _, err = g.get(ctx, fmt.Sprintf("/repos/%s/pulls/%d", repo.ID, i.Number), &metadata); err != nil {
					return nil, time.Time{}, err
				}
				i.Head = metadata.Head
				i.Draft = metadata.Draft
				i.RequestedReviewers = metadata.RequestedReviewers
				i.RequestedTeams = metadata.RequestedTeams
				i.Merged = metadata.Merged
			}
			v.Kind = "pr"
			v.Draft = i.Draft
			v.Head = i.Head.SHA
			v.Branch = i.Head.Ref
			if i.Merged {
				v.State = "merged"
			}
			for _, r := range i.RequestedReviewers {
				v.ReviewRequests = append(v.ReviewRequests, r.Login)
			}
			for _, t := range i.RequestedTeams {
				v.ReviewTeams = append(v.ReviewTeams, strings.SplitN(repo.ID, "/", 2)[0]+"/"+t.Slug)
			}
			v.ReviewObservedAt = *observed
		}
		items = append(items, v)
	}
	return items, *observed, nil
}
func (g *GitHub) ItemDetail(ctx context.Context, item domain.Item) (domain.Item, error) {
	ctx, observed := observeReads(ctx, g.now())
	var i ghIssue
	if _, err := g.get(ctx, fmt.Sprintf("/repos/%s/issues/%d", item.Repository, item.Number), &i); err != nil {
		return domain.Item{}, err
	}
	if i.Number != item.Number || i.State == "" {
		return domain.Item{}, fmt.Errorf("Incomplete GitHub item response")
	}
	if item.Kind == "pr" {
		i.Pull = json.RawMessage(`{}`)
	}
	v, err := g.syncItem(ctx, domain.Repository{ID: item.Repository}, i)
	v.DetailSyncedAt = *observed
	if v.Kind == "pr" {
		v.ReviewObservedAt = *observed
	}
	return v, err
}

func (g *GitHub) syncItem(ctx context.Context, repo domain.Repository, i ghIssue) (domain.Item, error) {
	path := "/repos/" + repo.ID
	var e error
	v := baseItem(repo, i)
	if len(i.Pull) > 0 && string(i.Pull) != "null" {
		v.Kind = "pr"
		var pr struct {
			Draft              bool
			Merged             bool
			Head               struct{ SHA, Ref string }
			RequestedReviewers []domain.Actor          `json:"requested_reviewers"`
			RequestedTeams     []struct{ Slug string } `json:"requested_teams"`
		}
		if _, e = g.get(ctx, fmt.Sprintf("%s/pulls/%d", path, i.Number), &pr); e != nil {
			return domain.Item{}, e
		}
		for _, reviewer := range pr.RequestedReviewers {
			v.ReviewRequests = append(v.ReviewRequests, reviewer.Login)
		}
		org := strings.SplitN(repo.ID, "/", 2)[0]
		for _, team := range pr.RequestedTeams {
			v.ReviewTeams = append(v.ReviewTeams, org+"/"+team.Slug)
		}
		v.Draft = pr.Draft
		v.Head = pr.Head.SHA
		v.Branch = pr.Head.Ref
		if pr.Merged {
			v.State = "merged"
		}
		for _, part := range []struct{ path, kind string }{{fmt.Sprintf("/pulls/%d/reviews", i.Number), "review"}, {fmt.Sprintf("/pulls/%d/comments", i.Number), "review-comment"}, {fmt.Sprintf("/pulls/%d/commits", i.Number), "commit"}} {
			a, e := g.activities(ctx, path+part.path+"?per_page=100", part.kind)
			if e != nil {
				return domain.Item{}, e
			}
			v.Activity = append(v.Activity, a...)
		}
		var checks json.RawMessage
		if _, e = g.get(ctx, path+"/commits/"+v.Head+"/check-runs?per_page=100", &checks); e != nil {
			return domain.Item{}, e
		}
		var checksObj struct {
			Total int               `json:"total_count"`
			Runs  []json.RawMessage `json:"check_runs"`
		}
		if e = json.Unmarshal(checks, &checksObj); e != nil {
			return domain.Item{}, e
		}
		for page := 2; len(checksObj.Runs) < checksObj.Total; page++ {
			var more struct {
				Runs []json.RawMessage `json:"check_runs"`
			}
			if _, e = g.get(ctx, fmt.Sprintf("%s/commits/%s/check-runs?per_page=100&page=%d", path, v.Head, page), &more); e != nil {
				return domain.Item{}, e
			}
			if len(more.Runs) == 0 {
				return domain.Item{}, fmt.Errorf("incomplete GitHub checks pagination")
			}
			checksObj.Runs = append(checksObj.Runs, more.Runs...)
		}
		statuses, e := g.pages(ctx, path+"/commits/"+v.Head+"/statuses?per_page=100")
		if e != nil {
			return domain.Item{}, e
		}
		v.Checks, _ = json.Marshal(map[string]any{"checks": checksObj.Runs, "statuses": statuses})
		g.EnrichPullRequest(ctx, &v)
	}
	comments, e := g.activities(ctx, fmt.Sprintf("%s/issues/%d/comments?per_page=100", path, i.Number), "comment")
	if e != nil {
		return domain.Item{}, e
	}
	v.Activity = append(v.Activity, comments...)
	timeline, e := g.pages(ctx, fmt.Sprintf("%s/issues/%d/timeline?per_page=100", path, i.Number))
	if e != nil {
		return domain.Item{}, e
	}
	for _, b := range timeline {
		var ev struct {
			Event  string
			Source struct {
				Issue struct {
					HTMLURL string `json:"html_url"`
				}
			}
		}
		if json.Unmarshal(b, &ev) == nil && ev.Event == "cross-referenced" && ev.Source.Issue.HTMLURL != "" {
			v.Activity = append(v.Activity, domain.Activity{Kind: "reference", URL: ev.Source.Issue.HTMLURL})
		}
	}
	v.Revision = v.ContentRevision()
	return v, nil
}

func (g *GitHub) activities(ctx context.Context, path, kind string) ([]domain.Activity, error) {
	raw, e := g.pages(ctx, path)
	if e != nil {
		return nil, e
	}
	out := []domain.Activity{}
	for _, b := range raw {
		var v struct {
			ID        json.Number
			SHA       string
			User      domain.Actor
			Author    domain.Actor
			Body      string
			State     string
			HTMLURL   string `json:"html_url"`
			Updated   string `json:"updated_at"`
			Submitted string `json:"submitted_at"`
			Commit    struct{ Message string }
		}
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		id := v.ID.String()
		author := v.User.Login
		body := v.Body
		if kind == "commit" {
			id = v.SHA
			author = v.Author.Login
			body = v.Commit.Message
		}
		if v.Updated == "" {
			v.Updated = v.Submitted
		}
		out = append(out, domain.Activity{ID: kind + ":" + id, Kind: kind, Author: author, Body: body + v.State, Updated: v.Updated, URL: v.HTMLURL})
	}
	return out, nil
}

func (g *GitHub) httpClient() *http.Client {
	client := http.Client{Timeout: 30 * time.Second}
	if g.Client != nil {
		client = *g.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}
