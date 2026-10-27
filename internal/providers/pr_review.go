package providers

import (
	"codefactory/internal/prreview"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type ReviewGitHub struct {
	submissionCtx context.Context
	Client        *GitHub
	Pinned        *prreview.Evidence
}

var reviewTarget = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/pull/([1-9][0-9]*)$`)

func reviewEndpoint(target string) (string, error) {
	m := reviewTarget.FindStringSubmatch(target)
	if m == nil {
		return "", errors.New("review target must be a canonical GitHub PR URL")
	}
	return "repos/" + m[1] + "/" + m[2] + "/pulls/" + m[3], nil
}
func (g ReviewGitHub) metadata(ctx context.Context, target string) (prreview.Metadata, error) {
	endpoint, err := reviewEndpoint(target)
	if err != nil {
		return prreview.Metadata{}, err
	}
	if g.Client == nil {
		return prreview.Metadata{}, errors.New("GitHub review transport unavailable")
	}
	var data struct {
		URL          string `json:"html_url"`
		NodeID       string `json:"node_id"`
		Body         string
		User         struct{ Login string }
		Base         struct{ SHA string }
		Head         struct{ SHA string }
		ChangedFiles int `json:"changed_files"`
	}
	if _, err = g.Client.get(WithGitHubRead(ctx, RequireLive), endpoint, &data); err != nil {
		return prreview.Metadata{}, err
	}
	return prreview.Metadata{URL: data.URL, NodeID: data.NodeID, Base: data.Base.SHA, Head: data.Head.SHA, Body: data.Body, Author: data.User.Login, ChangedFiles: data.ChangedFiles}, nil
}
func (g ReviewGitHub) ReadEvidence(ctx context.Context, target string) (prreview.Evidence, error) {
	if g.Client == nil {
		return prreview.Evidence{}, errors.New("GitHub review transport unavailable")
	}
	ctx = WithGitHubRead(g.Client.Operation(ctx, nil, false), RequireLive)
	e := prreview.Evidence{Scope: map[string]json.RawMessage{}}
	m, err := g.metadata(ctx, target)
	if err != nil {
		return e, err
	}
	e.Metadata = m
	viewer, err := g.Client.User(ctx)
	if err != nil || viewer == "" {
		return e, errors.New("cannot verify current GitHub review identity")
	}
	if strings.EqualFold(viewer, m.Author) {
		return e, errors.New("cannot review your own PR")
	}

	endpoint, _ := reviewEndpoint(target)
	rows, err := g.Client.pages(ctx, endpoint+"/files?per_page=100")
	if err != nil {
		return e, err
	}
	e.Files = []prreview.File{}
	for _, row := range rows {
		var file struct {
			Filename             string
			Additions, Deletions int
			Status               string
		}
		if err = json.Unmarshal(row, &file); err != nil {
			return e, err
		}
		e.Files = append(e.Files, prreview.File{Path: file.Filename, Additions: file.Additions, Deletions: file.Deletions, Status: file.Status})
	}
	if len(e.Files) != m.ChangedFiles {
		return e, errors.New("GitHub changed-file inventory is incomplete or truncated")
	}
	// Retain complete discussions as scope evidence, independently of their meaning.
	issueEndpoint := strings.Replace(endpoint, "/pulls/", "/issues/", 1)
	for _, suffix := range []string{"/comments?per_page=100"} {
		comments, err := g.Client.pages(ctx, issueEndpoint+suffix)
		if err != nil {
			return e, err
		}
		b, _ := json.Marshal(comments)
		e.Scope["pr-comments"] = b
	}
	reviews, err := g.reviewSnapshot(ctx, target)
	if err != nil {
		return e, err
	}
	b, _ := json.Marshal(reviews)
	e.Scope["pr-reviews"] = b
	refs := regexp.MustCompile(`https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/issues/([1-9][0-9]*)|(?:^|[\s(])#([1-9][0-9]*)`)
	repo := strings.Split(strings.TrimPrefix(target, "https://github.com/"), "/pull/")[0]
	issues := map[string]bool{}
	for _, match := range refs.FindAllStringSubmatch(m.Body, -1) {
		r, n := match[1], match[2]
		if n == "" {
			r, n = repo, match[3]
		}
		issues[r+"/"+n] = true
	}
	// GraphQL closing references include links absent from the description.
	parts := strings.Split(repo, "/")
	number, _ := strconv.Atoi(strings.TrimPrefix(target, "https://github.com/"+repo+"/pull/"))
	cursor := ""
	seen := map[string]bool{}
	expected := -1
	count := 0
	for {
		query := `query($owner:String!,$repo:String!,$number:Int!,$cursor:String){repository(owner:$owner,name:$repo){pullRequest(number:$number){closingIssuesReferences(first:100,after:$cursor){totalCount nodes{number repository{nameWithOwner}} pageInfo{hasNextPage endCursor}}}}}`
		var cur any
		if cursor != "" {
			cur = cursor
		}
		response, _, err := g.Client.Request(ctx, http.MethodPost, "graphql", map[string]any{"query": query, "variables": map[string]any{"owner": parts[0], "repo": parts[1], "number": number, "cursor": cur}}, "")
		if err != nil {
			return e, err
		}
		var result struct {
			Errors []json.RawMessage
			Data   struct {
				Repository *struct {
					PullRequest *struct {
						ClosingIssuesReferences *struct {
							TotalCount int
							Nodes      []struct {
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
		}
		if err = json.Unmarshal(response, &result); err != nil || len(result.Errors) > 0 || result.Data.Repository == nil || result.Data.Repository.PullRequest == nil || result.Data.Repository.PullRequest.ClosingIssuesReferences == nil {
			return e, errors.New("incomplete linked issue scope")
		}
		conn := result.Data.Repository.PullRequest.ClosingIssuesReferences
		if conn.Nodes == nil {
			return e, errors.New("missing linked issue inventory")
		}
		if expected < 0 {
			expected = conn.TotalCount
		} else if expected != conn.TotalCount {
			return e, errors.New("linked issue inventory changed")
		}
		for _, node := range conn.Nodes {
			if node.Number < 1 || node.Repository.NameWithOwner == "" {
				return e, errors.New("invalid linked issue identity")
			}
			issues[node.Repository.NameWithOwner+"/"+strconv.Itoa(node.Number)] = true
			count++
		}
		if !conn.PageInfo.HasNextPage {
			if count != expected {
				return e, errors.New("incomplete linked issue pagination")
			}
			break
		}
		cursor = conn.PageInfo.EndCursor
		if cursor == "" || seen[cursor] {
			return e, errors.New("cyclic linked issue pagination")
		}
		seen[cursor] = true
	}
	for key := range issues {
		parts := strings.Split(key, "/")
		if len(parts) != 3 {
			return e, errors.New("invalid linked issue repository")
		}
		var issue json.RawMessage
		if _, err = g.Client.get(ctx, "repos/"+parts[0]+"/"+parts[1]+"/issues/"+parts[2], &issue); err != nil {
			return e, err
		}
		e.Scope[key] = issue
	}
	e.Decisions, err = prreview.PublishedDecisions(m)
	if err != nil {
		return e, err
	}
	latest, err := g.metadata(ctx, target)
	if err != nil {
		return e, err
	}
	if prreview.Digest(m) != prreview.Digest(latest) {
		return e, errors.New("PR evidence changed during capture")
	}
	return e, nil
}
func (g ReviewGitHub) reviewSnapshot(ctx context.Context, target string) ([]prreview.RemoteReview, error) {
	endpoint, err := reviewEndpoint(target)
	if err != nil {
		return nil, err
	}
	rows, err := g.Client.pages(ctx, endpoint+"/reviews?per_page=100")
	if err != nil {
		return nil, err
	}
	out := []prreview.RemoteReview{}
	for _, row := range rows {
		var r struct {
			ID          json.Number
			URL         string `json:"html_url"`
			User        struct{ Login string }
			Head        string `json:"commit_id"`
			State, Body string
		}
		if err = json.Unmarshal(row, &r); err != nil {
			return nil, err
		}
		if r.ID.String() == "" {
			return nil, errors.New("incomplete review snapshot")
		}
		comments, err := g.Client.pages(ctx, endpoint+"/reviews/"+r.ID.String()+"/comments?per_page=100")
		if err != nil {
			return nil, err
		}
		threads := []prreview.Thread{}
		for _, comment := range comments {
			var t struct {
				Path       string
				Line       int
				Side, Body string
				StartLine  int    `json:"start_line"`
				StartSide  string `json:"start_side"`
			}
			if err = json.Unmarshal(comment, &t); err != nil {
				return nil, err
			}
			threads = append(threads, prreview.Thread{Path: t.Path, Line: t.Line, Side: t.Side, Body: t.Body, StartLine: t.StartLine, StartSide: t.StartSide})
		}
		out = append(out, prreview.RemoteReview{ID: r.ID.String(), URL: r.URL, Author: r.User.Login, Head: r.Head, State: r.State, Body: r.Body, Threads: threads})
	}
	return out, nil
}
func (g *ReviewGitHub) Snapshot(ctx context.Context, target string) (prreview.Snapshot, error) {
	if g.Client == nil {
		return prreview.Snapshot{}, errors.New("GitHub publication transport unavailable")
	}
	if g.Pinned == nil {
		return prreview.Snapshot{}, errors.New("publication needs pinned review evidence")
	}
	e := *g.Pinned
	ctx = WithGitHubRead(g.Client.Operation(ctx, nil, false), RequireLive)
	live, err := g.ReadEvidence(ctx, target)
	if err != nil {
		return prreview.Snapshot{}, err
	}
	live.Metadata.Tree = e.Metadata.Tree
	scope := map[string]json.RawMessage{}
	for k, v := range e.Scope {
		if k != "pr-reviews" {
			scope[k] = v
		}
	}
	delete(live.Scope, "pr-reviews")
	if prreview.Digest(live.Metadata) != prreview.Digest(e.Metadata) || prreview.Digest(live.Files) != prreview.Digest(e.Files) || prreview.Digest(live.Decisions) != prreview.Digest(e.Decisions) || prreview.Digest(live.Scope) != prreview.Digest(scope) {
		return prreview.Snapshot{}, errors.New("review engineering evidence changed")
	}

	if err := e.ValidateCheckout(ctx); err != nil {
		return prreview.Snapshot{}, err
	}
	author, err := g.Client.User(ctx)
	if err != nil {
		return prreview.Snapshot{}, err
	}
	reviews, err := g.reviewSnapshot(ctx, target)
	if err != nil {
		return prreview.Snapshot{}, err
	}
	latest, err := g.metadata(ctx, target)
	if err != nil {
		return prreview.Snapshot{}, err
	}
	latest.Tree = e.Metadata.Tree
	g.submissionCtx = ctx
	return prreview.Snapshot{Metadata: latest, Patch: e.Patch, Author: author, Reviews: reviews}, nil
}
func (g *ReviewGitHub) Submit(ctx context.Context, target string, payload prreview.Payload) (string, error) {
	if _, err := reviewEndpoint(target); err != nil {
		return "", err
	}
	if payload.Query != prreview.ReviewMutation {
		return "", errors.New("unowned review mutation")
	}
	if g.Client == nil || g.submissionCtx == nil {
		return "", errors.New("publication requires a fresh authenticated pre-submit snapshot")
	}
	ctx = context.WithValue(ctx, operationKey{}, g.submissionCtx.Value(operationKey{}))
	b, _, err := g.Client.Request(ctx, http.MethodPost, "graphql", payload, "")
	if err != nil {
		return "", err
	}
	var result struct {
		Errors []json.RawMessage
		Data   struct {
			AddPullRequestReview struct {
				PullRequestReview struct {
					ID         string
					DatabaseID json.Number `json:"fullDatabaseId"`
					URL, State string
				}
			}
		}
	}
	if err = json.Unmarshal(b, &result); err != nil || len(result.Errors) > 0 {
		return "", errors.New("review mutation response is uncertain")
	}
	r := result.Data.AddPullRequestReview.PullRequestReview
	if r.ID == "" || r.DatabaseID.String() == "" || r.URL == "" || r.State == "PENDING" {
		return "", fmt.Errorf("review mutation has no verified submitted receipt")
	}
	if _, err = url.ParseRequestURI(r.URL); err != nil {
		return "", err
	}
	return r.DatabaseID.String(), nil
}
