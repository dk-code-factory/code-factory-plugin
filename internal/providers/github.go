package providers

import (
	"codefactory/internal/domain"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	ETag   string
	Body   []byte
	Header http.Header
}

type RateLimitError struct{ Until time.Time }

func (e *RateLimitError) Error() string { return "GitHub rate limit; synchronization will retry" }

type GitHub struct {
	cacheBytes int
	mu         sync.Mutex
	cache      map[string]cacheEntry
	BaseURL    string
	Client     *http.Client
	Token      func(context.Context) (string, error)
}

func NewGitHub() *GitHub {
	return &GitHub{BaseURL: "https://api.github.com", Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Token: func(ctx context.Context) (string, error) {
		b, e := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com").Output()
		if e != nil {
			return "", fmt.Errorf("GitHub authentication unavailable; reconnect with GitHub CLI")
		}
		return strings.TrimSpace(string(b)), nil
	}}
}
func (g *GitHub) get(ctx context.Context, path string, out any) (http.Header, error) {
	u := g.BaseURL + path
	if strings.HasPrefix(path, "http") {
		u = path
	}
	parsed, e := url.Parse(u)
	if e != nil {
		return nil, e
	}
	base, _ := url.Parse(g.BaseURL)
	if parsed.Host != base.Host || parsed.Scheme != base.Scheme {
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
	g.mu.Lock()
	cached, hasCache := g.cache[cacheKey]
	g.mu.Unlock()
	if hasCache {
		req.Header.Set("If-None-Match", cached.ETag)
	}
	res, e := g.Client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("GitHub connection failed")
	}
	defer res.Body.Close()
	if res.StatusCode == 304 && hasCache {
		return cached.Header.Clone(), json.Unmarshal(cached.Body, out)
	}
	if res.StatusCode == 429 || (res.StatusCode == 403 && (res.Header.Get("X-RateLimit-Remaining") == "0" || res.Header.Get("Retry-After") != "")) {
		until := time.Now().Add(time.Minute)
		if n, e := strconv.ParseInt(res.Header.Get("X-RateLimit-Reset"), 10, 64); e == nil {
			until = time.Unix(n, 0)
		}
		if n, e := strconv.Atoi(res.Header.Get("Retry-After")); e == nil {
			until = time.Now().Add(time.Duration(n) * time.Second)
		}
		return nil, &RateLimitError{Until: until}
	}
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, fmt.Errorf("GitHub access revoked or insufficient repository permissions")
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub request failed (HTTP %d)", res.StatusCode)
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
	if e != nil {
		return nil, e
	}
	if len(body) > 16<<20 {
		return nil, fmt.Errorf("GitHub response too large")
	}
	if e = json.Unmarshal(body, out); e != nil {
		return nil, e
	}
	if etag := res.Header.Get("ETag"); etag != "" {
		g.mu.Lock()
		if g.cache == nil {
			g.cache = map[string]cacheEntry{}
		}
		if g.cacheBytes+len(body) > 64<<20 {
			g.cache = map[string]cacheEntry{}
			g.cacheBytes = 0
		}
		if old, ok := g.cache[cacheKey]; ok {
			g.cacheBytes -= len(old.Body)
		}
		g.cacheBytes += len(body)
		g.cache[cacheKey] = cacheEntry{ETag: etag, Body: body, Header: res.Header.Clone()}
		g.mu.Unlock()
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

type ghIssue struct {
	Number             int
	Title, Body, State string
	HTMLURL            string `json:"html_url"`
	User               domain.Actor
	Assignees          []domain.Actor
	Labels             []struct{ Name string }
	Updated            time.Time       `json:"updated_at"`
	Pull               json.RawMessage `json:"pull_request"`
}

func (g *GitHub) Sync(ctx context.Context, repo domain.Repository, full bool) ([]domain.Item, error) {
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
	items := []domain.Item{}
	for _, i := range byID {
		v := domain.Item{ID: fmt.Sprintf("%s#%d", repo.ID, i.Number), Repository: repo.ID, Number: i.Number, Title: i.Title, Body: i.Body, URL: i.HTMLURL, State: i.State, Author: i.User.Login, Updated: i.Updated, Kind: "issue", Labels: []string{}, Assignees: []string{}, Activity: []domain.Activity{}}
		for _, l := range i.Labels {
			v.Labels = append(v.Labels, l.Name)
		}
		for _, a := range i.Assignees {
			v.Assignees = append(v.Assignees, a.Login)
		}
		if len(i.Pull) > 0 && string(i.Pull) != "null" {
			v.Kind = "pr"
			var pr struct {
				Draft  bool
				Merged bool
				Head   struct{ SHA, Ref string }
			}
			if _, e = g.get(ctx, fmt.Sprintf("%s/pulls/%d", path, i.Number), &pr); e != nil {
				return nil, e
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
					return nil, e
				}
				v.Activity = append(v.Activity, a...)
			}
			var checks json.RawMessage
			if _, e = g.get(ctx, path+"/commits/"+v.Head+"/check-runs?per_page=100", &checks); e != nil {
				return nil, e
			}
			var checksObj struct {
				Total int               `json:"total_count"`
				Runs  []json.RawMessage `json:"check_runs"`
			}
			if e = json.Unmarshal(checks, &checksObj); e != nil {
				return nil, e
			}
			for page := 2; len(checksObj.Runs) < checksObj.Total; page++ {
				var more struct {
					Runs []json.RawMessage `json:"check_runs"`
				}
				if _, e = g.get(ctx, fmt.Sprintf("%s/commits/%s/check-runs?per_page=100&page=%d", path, v.Head, page), &more); e != nil {
					return nil, e
				}
				if len(more.Runs) == 0 {
					return nil, fmt.Errorf("incomplete GitHub checks pagination")
				}
				checksObj.Runs = append(checksObj.Runs, more.Runs...)
			}
			statuses, e := g.pages(ctx, path+"/commits/"+v.Head+"/statuses?per_page=100")
			if e != nil {
				return nil, e
			}
			v.Checks, _ = json.Marshal(map[string]any{"checks": checksObj.Runs, "statuses": statuses})
		}
		comments, e := g.activities(ctx, fmt.Sprintf("%s/issues/%d/comments?per_page=100", path, i.Number), "comment")
		if e != nil {
			return nil, e
		}
		v.Activity = append(v.Activity, comments...)
		timeline, e := g.pages(ctx, fmt.Sprintf("%s/issues/%d/timeline?per_page=100", path, i.Number))
		if e != nil {
			return nil, e
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
		items = append(items, v)
	}
	return items, nil
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
