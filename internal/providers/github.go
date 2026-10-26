package providers

import (
	"codefactory/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	ETag    string
	Body    []byte
	Header  http.Header
	Expires time.Time
	Used    uint64
	Size    int
}

type RateLimitError struct{ Until time.Time }

func (e *RateLimitError) Error() string { return "GitHub rate limit; synchronization will retry" }

type GitHub struct {
	dispatch   sync.Mutex
	tick       uint64
	cooldowns  map[string]time.Time
	Now        func() time.Time
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
	b, h, e := g.Request(ctx, http.MethodGet, path, nil, "")
	if e != nil {
		return nil, e
	}
	return h, json.Unmarshal(b, out)
}
func (g *GitHub) pages(ctx context.Context, path string) ([]json.RawMessage, error) {
	ctx = g.Operation(ctx, nil, false)
	out := []json.RawMessage{}
	seen := map[string]bool{}
	for page := 0; path != ""; page++ {
		canonical, e := g.endpoint(path)
		if e != nil {
			return nil, e
		}
		if page >= 10000 || seen[canonical] {
			return nil, fmt.Errorf("GitHub pagination exceeded limit or cycle")
		}
		seen[canonical] = true
		var data []json.RawMessage
		h, e := g.get(ctx, path, &data)
		if e != nil {
			return nil, e
		}
		out = append(out, data...)
		path, e = g.NextPage(h)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (g *GitHub) User(ctx context.Context) (string, error) {
	ctx = g.Operation(ctx, nil, false)
	scope, e := g.Scope(ctx)
	if e != nil {
		return "", e
	}
	if b, ok := g.CachedBlob("identity:" + scope); ok {
		return string(b), nil
	}
	var u struct{ Login string }
	_, e = g.get(ctx, "/user", &u)
	if e == nil && u.Login != "" {
		g.CacheBlob("identity:"+scope, []byte(u.Login), 5*time.Minute)
	}
	return u.Login, e
}

type ghBranch struct {
	SHA, Ref string
	Repo     struct {
		FullName string `json:"full_name"`
	}
}
type ghPR struct {
	Draft, Merged bool
	Head, Base    ghBranch
}

func (p ghPR) complete() bool {
	return p.Head.SHA != "" && p.Head.Ref != "" && p.Head.Repo.FullName != "" && p.Base.SHA != "" && p.Base.Ref != "" && p.Base.Repo.FullName != ""
}

type ghIssue struct {
	ghPR
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
	ctx = g.Operation(ctx, nil, false)
	scope, e := g.Scope(ctx)
	if e != nil {
		return nil, e
	}
	path := "/repos/" + repo.ID
	query := "?state=all&sort=updated&direction=asc&per_page=100"
	since := ""
	if repo.Imported && !repo.LastSync.IsZero() {
		since = "&since=" + url.QueryEscape(repo.LastSync.Add(-2*time.Minute).Truncate(2*time.Minute).Format(time.RFC3339))
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
	openPRs := map[int]ghPR{}
	for _, b := range prs {
		var i ghIssue
		if e = json.Unmarshal(b, &i); e != nil {
			return nil, e
		}
		openPRs[i.Number] = i.ghPR
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
			pr, listed := openPRs[i.Number]
			if !listed || !pr.complete() {
				if _, e = g.get(ctx, fmt.Sprintf("%s/pulls/%d", path, i.Number), &pr); e != nil {
					return nil, e
				}
			}
			v.Draft = pr.Draft
			v.Head = pr.Head.SHA
			v.Branch = pr.Head.Ref
			if pr.Merged {
				v.State = "merged"
			}
			for _, part := range []struct{ path, kind string }{{fmt.Sprintf("/pulls/%d/reviews", i.Number), "review"}, {fmt.Sprintf("/pulls/%d/comments", i.Number), "review-comment"}} {
				a, e := g.activities(ctx, path+part.path+"?per_page=100", part.kind)
				if e != nil {
					return nil, e
				}
				v.Activity = append(v.Activity, a...)
			}
			signature, _ := json.Marshal(struct{ Head, Base ghBranch }{pr.Head, pr.Base})
			commitKey := fmt.Sprintf("commits:%s:%s:%d:%s", scope, repo.ID, i.Number, signature)
			var commits []domain.Activity
			cached, ok := g.CachedBlob(commitKey)
			if full || !pr.complete() || !ok || json.Unmarshal(cached, &commits) != nil {
				commits, e = g.activities(ctx, fmt.Sprintf("%s/pulls/%d/commits?per_page=100", path, i.Number), "commit")
				if e != nil {
					return nil, e
				}
				if pr.complete() {
					b, _ := json.Marshal(commits)
					g.CacheBlob(commitKey, b, 0)
				}
			}
			v.Activity = append(v.Activity, commits...)
			checksObj := struct {
				Total int               `json:"total_count"`
				Runs  []json.RawMessage `json:"check_runs"`
			}{}
			next := path + "/commits/" + v.Head + "/check-runs?per_page=100"
			seen := map[string]bool{}
			for page := 0; next != ""; page++ {
				canonical, err := g.endpoint(next)
				if err != nil {
					return nil, err
				}
				if page >= 10000 || seen[canonical] {
					return nil, fmt.Errorf("GitHub checks pagination limit or cycle")
				}
				seen[canonical] = true
				var more struct {
					Total int               `json:"total_count"`
					Runs  []json.RawMessage `json:"check_runs"`
				}
				h, err := g.get(ctx, next, &more)
				if err != nil {
					return nil, err
				}
				if page == 0 {
					checksObj.Total = more.Total
				} else if more.Total != checksObj.Total || len(more.Runs) == 0 {
					return nil, fmt.Errorf("incomplete GitHub checks pagination")
				}
				checksObj.Runs = append(checksObj.Runs, more.Runs...)
				next, err = g.NextPage(h)
				if err != nil {
					return nil, err
				}
			}
			if len(checksObj.Runs) != checksObj.Total {
				return nil, fmt.Errorf("incomplete GitHub checks pagination")
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
