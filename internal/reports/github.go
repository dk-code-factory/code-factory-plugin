package reports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"codefactory/internal/providers"
)

type GitHubAdapter struct{ Client *providers.GitHub }
type rateLimit struct{ until time.Time }
type publicationRejected struct{ Reason string }

func (e *publicationRejected) Error() string { return e.Reason }

func (e *rateLimit) Error() string {
	return "GitHub rate limit; retained results will refresh after the retry time"
}
func (e *rateLimit) RetryTime() time.Time { return e.until }
func (g *GitHubAdapter) request(ctx context.Context, method, endpoint string, input any, accept string) ([]byte, error) {
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return nil, e
		}
		body = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, method, g.Client.BaseURL+endpoint, body)
	if e != nil {
		return nil, e
	}
	token, e := g.Client.Token(ctx)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := g.Client.Client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("GitHub connection failed")
	}
	defer res.Body.Close()
	if res.StatusCode == 429 || res.StatusCode == 403 && (res.Header.Get("X-RateLimit-Remaining") == "0" || res.Header.Get("Retry-After") != "") {
		until := time.Now().Add(time.Minute)
		if sec, e := strconv.ParseInt(res.Header.Get("X-RateLimit-Reset"), 10, 64); e == nil {
			until = time.Unix(sec, 0)
		}
		if sec, e := strconv.Atoi(res.Header.Get("Retry-After")); e == nil {
			until = time.Now().Add(time.Duration(sec) * time.Second)
		}
		if method == http.MethodPost {
			return nil, &publicationRejected{Reason: "GitHub rejected issue creation due to a rate limit; retry after " + until.UTC().Format(time.RFC3339)}
		}
		return nil, &rateLimit{until}
	}
	if method == http.MethodGet && accept == "application/octet-stream" && res.StatusCode == 302 {
		target, e := url.Parse(res.Header.Get("Location"))
		if e != nil || target.Scheme != "https" || (target.Hostname() != "release-assets.githubusercontent.com" && target.Hostname() != "objects.githubusercontent.com") {
			return nil, fmt.Errorf("Untrusted release asset redirect")
		}
		// Signed CDN URLs receive no owner/GitHub authorization headers.
		req, e = http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if e != nil {
			return nil, e
		}
		download := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, e = download.Do(req)
		if e != nil {
			return nil, fmt.Errorf("Release asset unavailable")
		}
		defer res.Body.Close()
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if method == http.MethodPost && res.StatusCode >= 400 && res.StatusCode < 500 && res.StatusCode != 408 {
			return nil, &publicationRejected{Reason: fmt.Sprintf("GitHub rejected issue creation (HTTP %d); restore permissions or correct the draft and resume", res.StatusCode)}
		}
		return nil, fmt.Errorf("GitHub operation failed (HTTP %d)", res.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
	if e != nil {
		return nil, e
	}
	if len(b) > 16<<20 {
		return nil, fmt.Errorf("GitHub response exceeds limit")
	}
	return b, nil
}
func (g *GitHubAdapter) get(ctx context.Context, endpoint string, out any) error {
	b, e := g.request(ctx, "GET", endpoint, nil, "")
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}
func listPages[T any](ctx context.Context, g *GitHubAdapter, endpoint string) ([]T, error) {
	out := []T{}
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	for page := 1; page <= 100; page++ {
		var rows []T
		if e := g.get(ctx, endpoint+sep+"per_page=100&page="+strconv.Itoa(page), &rows); e != nil {
			return nil, e
		}
		out = append(out, rows...)
		if len(rows) < 100 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("GitHub pagination limit reached; results are incomplete")
}

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var routes = map[string]string{"code-factory": DefaultRepository, "plugin": DefaultRepository, "agent-workflow": "dk-code-factory/agent-workflow", "dk-workflows": "dk-code-factory/dk-workflows"}

func allowedRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	return len(parts) == 2 && parts[0] == "dk-code-factory" && regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*$`).MatchString(parts[1]) && !strings.Contains(parts[1], "..")
}
func (g *GitHubAdapter) Validate(ctx context.Context, f Findings) error {
	if !allowedRepo(f.Repository) || routes[f.Component] != f.Repository {
		return fmt.Errorf("Ownership routing is uncertain or outside dk-code-factory; supply supported dependency provenance")
	}
	var repo struct {
		FullName  string `json:"full_name"`
		HasIssues bool   `json:"has_issues"`
		Archived  bool   `json:"archived"`
	}
	if e := g.get(ctx, "/repos/"+f.Repository, &repo); e != nil {
		return e
	}
	if !strings.EqualFold(repo.FullName, f.Repository) || !repo.HasIssues || repo.Archived {
		return fmt.Errorf("Destination does not accept issues")
	}
	if !commitPattern.MatchString(f.SourceCommit) || !validText(f.OwnershipReason) || f.SourcePath == "" || path.Clean(f.SourcePath) != f.SourcePath || strings.HasPrefix(f.SourcePath, "/") || strings.HasPrefix(f.SourcePath, "..") {
		return fmt.Errorf("Full source commit, component path and ownership evidence required")
	}
	var source json.RawMessage
	if e := g.get(ctx, "/repos/"+f.Repository+"/contents/"+escapePath(f.SourcePath)+"?ref="+f.SourceCommit, &source); e != nil {
		return fmt.Errorf("Destination component provenance could not be verified")
	}
	if len(f.Labels) > 0 {
		labels, e := listPages[struct {
			Name string `json:"name"`
		}](ctx, g, "/repos/"+f.Repository+"/labels")
		if e != nil {
			return e
		}
		supported := map[string]bool{}
		for _, l := range labels {
			supported[l.Name] = true
		}
		for _, l := range f.Labels {
			if !supported[l] {
				return fmt.Errorf("Draft uses an unsupported repository label")
			}
		}
	}
	return nil
}
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

type githubIssue struct {
	Number int             `json:"number"`
	URL    string          `json:"html_url"`
	Body   string          `json:"body"`
	State  string          `json:"state"`
	Reason string          `json:"state_reason"`
	PR     json.RawMessage `json:"pull_request"`
}

func (i githubIssue) issue(repo string) (Issue, error) {
	expected := fmt.Sprintf("https://github.com/%s/issues/%d", repo, i.Number)
	if i.Number < 1 || i.URL != expected || len(i.PR) > 0 && string(i.PR) != "null" {
		return Issue{}, fmt.Errorf("Invalid GitHub issue identity")
	}
	return Issue{Repository: repo, Number: i.Number, URL: i.URL, State: i.State, ClosureReason: i.Reason}, nil
}
func (g *GitHubAdapter) ReadIssue(ctx context.Context, repo string, number int) (Issue, error) {
	if !allowedRepo(repo) || number < 1 {
		return Issue{}, fmt.Errorf("Invalid issue target")
	}
	var i githubIssue
	if e := g.get(ctx, fmt.Sprintf("/repos/%s/issues/%d", repo, number), &i); e != nil {
		return Issue{}, e
	}
	if i.Number != number {
		return Issue{}, fmt.Errorf("GitHub returned a different issue")
	}
	return i.issue(repo)
}
func (g *GitHubAdapter) FindMarker(ctx context.Context, repo, marker string) (*Issue, error) {
	if !allowedRepo(repo) {
		return nil, fmt.Errorf("Invalid marker repository")
	}
	rows, e := listPages[githubIssue](ctx, g, "/repos/"+repo+"/issues?state=all&sort=created&direction=desc")
	if e != nil {
		return nil, e
	}
	var found *Issue
	for _, row := range rows {
		if strings.Contains(row.Body, marker) {
			i, e := row.issue(repo)
			if e != nil {
				return nil, e
			}
			if found != nil {
				return nil, fmt.Errorf("Multiple issues contain the report marker; reconcile manually")
			}
			found = &i
		}
	}
	return found, nil
}
func (g *GitHubAdapter) Create(ctx context.Context, f Findings, marker string) (Issue, error) {
	if !allowedRepo(f.Repository) {
		return Issue{}, fmt.Errorf("Invalid publication repository")
	}
	b, e := g.request(ctx, "POST", "/repos/"+f.Repository+"/issues", map[string]any{"title": f.Title, "body": f.Body + "\n\n" + marker, "labels": f.Labels}, "")
	if e != nil {
		return Issue{}, e
	}
	var i githubIssue
	if e = json.Unmarshal(b, &i); e != nil {
		return Issue{}, e
	}
	return i.issue(f.Repository)
}

type ComponentProvenance struct {
	Repository string `json:"repository"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
}
type FixedIssue struct {
	Issue     string `json:"issue"`
	Component string `json:"component"`
	Commit    string `json:"commit"`
}
type Artifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Provenance struct {
	Version    int                            `json:"version"`
	Commit     string                         `json:"commit"`
	Components map[string]ComponentProvenance `json:"components"`
	Fixes      []FixedIssue                   `json:"fixes"`
	Files      []Artifact                     `json:"files"`
}
type githubRelease struct {
	Tag         string     `json:"tag_name"`
	URL         string     `json:"html_url"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	PublishedAt *time.Time `json:"published_at"`
	Assets      []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		State  string `json:"state"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

func (g *GitHubAdapter) Track(ctx context.Context, issue Issue, component string) ([]Fix, []Release, error) {
	if !allowedRepo(issue.Repository) {
		return nil, nil, fmt.Errorf("Invalid tracking repository")
	}
	fixes := []Fix{}
	releases := []Release{}
	// Closure alone never proves a fix. Only a merged closing PR with a concrete
	// merge commit, or checked fixed-issue provenance, supplies a fix identity.
	timeline, e := listPages[struct {
		Event  string `json:"event"`
		Source struct {
			Issue struct {
				Number int             `json:"number"`
				URL    string          `json:"html_url"`
				PR     json.RawMessage `json:"pull_request"`
			} `json:"issue"`
		} `json:"source"`
	}](ctx, g, fmt.Sprintf("/repos/%s/issues/%d/timeline", issue.Repository, issue.Number))
	if e != nil {
		return nil, nil, e
	}
	seen := map[string]bool{}
	for _, event := range timeline {
		if event.Event != "cross-referenced" || len(event.Source.Issue.PR) == 0 {
			continue
		}
		number := event.Source.Issue.Number
		if event.Source.Issue.URL != fmt.Sprintf("https://github.com/%s/pull/%d", issue.Repository, number) {
			continue
		}
		var pr struct {
			Merged bool   `json:"merged"`
			Commit string `json:"merge_commit_sha"`
			Body   string `json:"body"`
			URL    string `json:"html_url"`
		}
		if e = g.get(ctx, fmt.Sprintf("/repos/%s/pulls/%d", issue.Repository, number), &pr); e != nil {
			return nil, nil, e
		}
		ref := fmt.Sprintf(`(?i)\b(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?)\s+(?:#%d\b|%s#%d\b|https://github\.com/%s/issues/%d\b)`, issue.Number, regexp.QuoteMeta(issue.Repository), issue.Number, regexp.QuoteMeta(issue.Repository), issue.Number)
		if pr.Merged && commitPattern.MatchString(pr.Commit) && regexp.MustCompile(ref).MatchString(pr.Body) && !seen[pr.Commit] {
			fixes = append(fixes, Fix{Repository: issue.Repository, PR: pr.URL, Commit: pr.Commit, Component: component})
			seen[pr.Commit] = true
		}
	}
	repos := []string{issue.Repository}
	if issue.Repository != DefaultRepository {
		repos = append(repos, DefaultRepository)
	}
	for _, repo := range repos {
		rows, e := listPages[githubRelease](ctx, g, "/repos/"+repo+"/releases")
		if e != nil {
			return nil, nil, e
		}
		for _, release := range rows {
			if release.Draft || release.PublishedAt == nil {
				continue
			}
			p, ok, e := g.releaseProvenance(ctx, repo, release)
			if e != nil {
				return nil, nil, e
			}
			if !ok {
				continue
			}
			for _, fixed := range p.Fixes {
				if fixed.Issue != fmt.Sprintf("%s#%d", issue.Repository, issue.Number) || fixed.Component != component || !commitPattern.MatchString(fixed.Commit) {
					continue
				}
				c, ok := p.Components[component]
				if !ok || c.Repository != issue.Repository || c.Version == "" || !commitPattern.MatchString(c.Commit) {
					continue
				}
				var comparison struct {
					Status string `json:"status"`
				}
				if e = g.get(ctx, "/repos/"+issue.Repository+"/compare/"+fixed.Commit+"..."+c.Commit, &comparison); e != nil {
					return nil, nil, e
				}
				if comparison.Status != "ahead" && comparison.Status != "identical" {
					continue
				}
				if !seen[fixed.Commit] {
					fixes = append(fixes, Fix{Repository: issue.Repository, Commit: fixed.Commit, Component: component, Version: c.Version})
					seen[fixed.Commit] = true
				}
				releases = append(releases, Release{Repository: repo, Version: release.Tag, ComponentVersion: c.Version, PublishedAt: *release.PublishedAt, URL: release.URL, Prerelease: release.Prerelease, Commit: fixed.Commit})
			}
		}
	}
	return fixes, releases, nil
}

// Published metadata and uploaded assets are separate GitHub states. The tag,
// complete package set, checksums and component commits must all agree.
func (g *GitHubAdapter) releaseProvenance(ctx context.Context, repo string, r githubRelease) (Provenance, bool, error) {
	var p Provenance
	assets := map[string]int{}
	for i, a := range r.Assets {
		if _, dup := assets[a.Name]; dup {
			return p, false, nil
		}
		assets[a.Name] = i
	}
	manifest, ok := assets["CI-MANIFEST.json"]
	if !ok {
		return p, false, nil
	}
	sums, ok := assets["SHA256SUMS"]
	if !ok {
		return p, false, nil
	}
	download := func(i int) ([]byte, error) {
		a := r.Assets[i]
		if a.State != "uploaded" || a.ID < 1 || a.Size > 16<<20 {
			return nil, fmt.Errorf("Release evidence asset is unavailable")
		}
		b, e := g.request(ctx, "GET", fmt.Sprintf("/repos/%s/releases/assets/%d", repo, a.ID), nil, "application/octet-stream")
		if e != nil {
			return nil, e
		}
		if int64(len(b)) != a.Size {
			return nil, fmt.Errorf("Release evidence asset size mismatch")
		}
		h := sha256.Sum256(b)
		if a.Digest != "" && a.Digest != "sha256:"+hex.EncodeToString(h[:]) {
			return nil, fmt.Errorf("Release asset digest mismatch")
		}
		return b, nil
	}
	b, e := download(manifest)
	if e != nil {
		return p, false, e
	}
	if e = json.Unmarshal(b, &p); e != nil {
		return p, false, fmt.Errorf("Malformed release provenance")
	}
	if p.Version != 1 || !commitPattern.MatchString(p.Commit) || len(p.Files) == 0 {
		return p, false, nil
	}
	var tag struct {
		SHA string `json:"sha"`
	}
	if e = g.get(ctx, "/repos/"+repo+"/commits/"+url.PathEscape(r.Tag), &tag); e != nil {
		return p, false, e
	}
	if tag.SHA != p.Commit {
		return p, false, nil
	}
	checksum, e := download(sums)
	if e != nil {
		return p, false, e
	}
	expected := ""
	seen := map[string]bool{}
	hasZip, hasTar := false, false
	for _, file := range p.Files {
		i, ok := assets[file.Name]
		if !ok || seen[file.Name] || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(file.SHA256) {
			return p, false, nil
		}
		seen[file.Name] = true
		a := r.Assets[i]
		if a.State != "uploaded" || a.Size != file.Size || a.Digest != "sha256:"+file.SHA256 {
			return p, false, nil
		}
		hasZip = hasZip || strings.HasSuffix(file.Name, ".zip")
		hasTar = hasTar || strings.HasSuffix(file.Name, ".tar.gz")
		expected += file.SHA256 + "  " + file.Name + "\n"
	}
	h := sha256.Sum256(b)
	expected += hex.EncodeToString(h[:]) + "  CI-MANIFEST.json\n"
	if string(checksum) != expected || !hasZip || !hasTar || len(assets) != len(p.Files)+2 {
		return p, false, nil
	}
	// Components belonging to this source repository must identify the release commit.
	for _, c := range p.Components {
		if c.Repository == repo && c.Commit != p.Commit {
			return p, false, nil
		}
	}
	return p, true, nil
}
