package reports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"codefactory/internal/providers"
	"codefactory/internal/store"
)

type GitHubAdapter struct {
	Client *providers.GitHub
	Store  *store.Store
}
type rateLimit struct{ until time.Time }
type publicationRejected struct {
	Reason string
	cause  error
}

func (e *publicationRejected) Error() string { return e.Reason }
func (e *publicationRejected) Unwrap() error { return e.cause }

func (e *rateLimit) Error() string {
	return "GitHub rate limit; retained results will refresh after the retry time"
}
func (e *rateLimit) RetryTime() time.Time { return e.until }

const githubCooldownKind = providers.CooldownKind

func githubCooldownKey(baseURL, token string) string { return providers.CooldownKey(baseURL, token) }

func reportingRateLimit(method string, until time.Time) error {
	retry := &rateLimit{until}
	if method == http.MethodPost {
		return &publicationRejected{Reason: "GitHub rejected issue creation due to a rate limit; retry after " + until.UTC().Format(time.RFC3339), cause: retry}
	}
	return retry
}

// BeginOperation is also used by Service to share reads across a refresh or publication.
func (g *GitHubAdapter) BeginOperation(ctx context.Context) context.Context {
	ctx = g.Client.StartOperation(ctx, g.Store, true)
	return context.WithValue(ctx, trackingKey{}, &trackingBatch{client: g.Client, id: batchSequence.Add(1)})
}
func (g *GitHubAdapter) operation(ctx context.Context) context.Context {
	if batch, _ := ctx.Value(trackingKey{}).(*trackingBatch); batch != nil && batch.client == g.Client {
		return ctx
	}
	return g.BeginOperation(ctx)
}

func (g *GitHubAdapter) request(ctx context.Context, method, endpoint string, input any, accept string) ([]byte, error) {
	b, _, e := g.requestHeaders(ctx, method, endpoint, input, accept)
	return b, e
}
func (g *GitHubAdapter) requestHeaders(ctx context.Context, method, endpoint string, input any, accept string) ([]byte, http.Header, error) {
	ctx = g.operation(ctx)
	b, h, e := g.Client.Request(ctx, method, endpoint, input, accept)
	if e != nil && method == http.MethodPost {
		var rate *providers.RateLimitError
		if errors.As(e, &rate) {
			return nil, nil, reportingRateLimit(method, rate.Until)
		}
		var outcome *providers.RequestError
		if errors.As(e, &outcome) && (outcome.Phase == providers.BeforeDispatch || outcome.Phase == providers.HTTPRejected && outcome.Status >= 400 && outcome.Status < 500 && outcome.Status != 408) {
			return nil, nil, &publicationRejected{Reason: e.Error(), cause: e}
		}
	}
	return b, h, e
}
func (g *GitHubAdapter) get(ctx context.Context, endpoint string, out any) error {
	b, e := g.request(ctx, "GET", endpoint, nil, "")
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}
func listPages[T any](ctx context.Context, g *GitHubAdapter, endpoint string) ([]T, error) {
	ctx = g.operation(ctx)
	u, e := url.Parse(endpoint)
	if e != nil {
		return nil, e
	}
	q := u.Query()
	q.Set("per_page", "100")
	q.Set("page", "1")
	u.RawQuery = q.Encode()
	next, e := g.Client.RequestURL(u.String())
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	out := []T{}
	for page := 0; next != ""; page++ {
		if page >= 100 || seen[next] {
			return nil, fmt.Errorf("GitHub pagination limit or cycle; results are incomplete")
		}
		seen[next] = true
		b, h, e := g.requestHeaders(ctx, http.MethodGet, next, nil, "")
		if e != nil {
			return nil, e
		}
		var rows []T
		if e = json.Unmarshal(b, &rows); e != nil {
			return nil, e
		}
		out = append(out, rows...)
		next, e = g.Client.NextPage(h)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var routes = map[string]string{"code-factory": DefaultRepository, "plugin": DefaultRepository, "agent-workflow": "dk-code-factory/agent-workflow", "dk-workflows": "dk-code-factory/dk-workflows"}

func allowedRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	return len(parts) == 2 && parts[0] == "dk-code-factory" && regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*$`).MatchString(parts[1]) && !strings.Contains(parts[1], "..")
}
func (g *GitHubAdapter) Validate(ctx context.Context, f Findings) error {
	ctx = g.operation(ctx)
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
	ctx = g.operation(ctx)
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
func (g *GitHubAdapter) Create(ctx context.Context, f Findings, marker string) (Issue, error) {
	ctx = g.operation(ctx)
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
	issue, e := i.issue(f.Repository)
	if e != nil {
		return Issue{}, e
	}
	// Failure to update this optimization cannot erase a successful issue identity.
	// The durable publication claim still prevents replay; future lookups refresh history.
	_ = g.recordCreation(ctx, f.Repository, marker, i)
	return issue, nil
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
	ID          int64         `json:"id"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Target      string        `json:"target_commitish"`
	Tag         string        `json:"tag_name"`
	URL         string        `json:"html_url"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt *time.Time    `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}
type githubAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

// Track returns verified observations alongside later request errors. Callers
// must merge those observations with retained evidence when the refresh fails.
func (g *GitHubAdapter) Track(ctx context.Context, issue Issue, component string) ([]Fix, []Release, error) {
	ctx = g.operation(ctx)
	if !allowedRepo(issue.Repository) {
		return nil, nil, fmt.Errorf("Invalid tracking repository")
	}
	fixes := []Fix{}
	releases := []Release{}
	// Closure alone never proves a fix. Verify a closing commit, merged closing
	// PR, or checked fixed-issue provenance before retaining its identity.
	timeline, e := listPages[struct {
		Event     string `json:"event"`
		Commit    string `json:"commit_id"`
		CommitURL string `json:"commit_url"`
		Source    struct {
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
	seen := map[string]int{}
	addFix := func(f Fix) {
		if i, ok := seen[f.Commit]; ok {
			if f.PR != "" {
				fixes[i].PR = f.PR
			}
			if fixes[i].Version == "" {
				fixes[i].Version = f.Version
			}
			return
		}
		seen[f.Commit] = len(fixes)
		fixes = append(fixes, f)
	}
	ref := regexp.MustCompile(fmt.Sprintf(`(?i)\b(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?):?\s+(?:#%d\b|%s#%d\b|https://github\.com/%s/issues/%d\b)`, issue.Number, regexp.QuoteMeta(issue.Repository), issue.Number, regexp.QuoteMeta(issue.Repository), issue.Number))
	for _, event := range timeline {
		if event.Event == "closed" && commitPattern.MatchString(event.Commit) && event.CommitURL == fmt.Sprintf("https://api.github.com/repos/%s/commits/%s", issue.Repository, event.Commit) {
			var commit struct {
				SHA    string `json:"sha"`
				URL    string `json:"html_url"`
				Commit struct {
					Message string `json:"message"`
				} `json:"commit"`
			}
			if e = g.get(ctx, "/repos/"+issue.Repository+"/commits/"+event.Commit, &commit); e != nil {
				return fixes, releases, e
			}
			if commit.SHA == event.Commit && commit.URL == fmt.Sprintf("https://github.com/%s/commit/%s", issue.Repository, event.Commit) && ref.MatchString(commit.Commit.Message) {
				addFix(Fix{Repository: issue.Repository, Commit: event.Commit, Component: component})
			}
		}
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
			return fixes, releases, e
		}
		if pr.Merged && commitPattern.MatchString(pr.Commit) && ref.MatchString(pr.Body) {
			addFix(Fix{Repository: issue.Repository, PR: pr.URL, Commit: pr.Commit, Component: component})
		}
	}
	repos := []string{issue.Repository}
	if issue.Repository != DefaultRepository {
		repos = append(repos, DefaultRepository)
	}
	for _, repo := range repos {
		rows, e := listPages[githubRelease](ctx, g, "/repos/"+repo+"/releases")
		if e != nil {
			return fixes, releases, e
		}
		for _, release := range rows {
			if release.Draft || release.PublishedAt == nil {
				continue
			}
			if repo == "dk-code-factory/agent-workflow" {
				c, ok, e := g.workflowReleaseProvenance(ctx, repo, release)
				if e != nil {
					return fixes, releases, e
				}
				if !ok {
					continue
				}
				for _, fix := range fixes {
					if fix.Repository != repo || fix.Component != component {
						continue
					}
					var comparison struct {
						Status string `json:"status"`
					}
					if e = g.get(ctx, "/repos/"+repo+"/compare/"+fix.Commit+"..."+c.Commit, &comparison); e != nil {
						return fixes, releases, e
					}
					if comparison.Status == "ahead" || comparison.Status == "identical" {
						addFix(Fix{Repository: repo, Commit: fix.Commit, Component: component, Version: c.Version})
						releases = append(releases, Release{Repository: repo, Version: release.Tag, ComponentVersion: c.Version, PublishedAt: *release.PublishedAt, URL: release.URL, Prerelease: release.Prerelease, Commit: fix.Commit})
					}
				}
				continue
			}
			if repo != DefaultRepository {
				continue // Source-only dependency releases do not prove checked packages.
			}
			p, ok, e := g.releaseProvenance(ctx, repo, release)
			if e != nil {
				return fixes, releases, e
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
					return fixes, releases, e
				}
				if comparison.Status != "ahead" && comparison.Status != "identical" {
					continue
				}
				addFix(Fix{Repository: issue.Repository, Commit: fixed.Commit, Component: component, Version: c.Version})
				releases = append(releases, Release{Repository: repo, Version: release.Tag, ComponentVersion: c.Version, PublishedAt: *release.PublishedAt, URL: release.URL, Prerelease: release.Prerelease, Commit: fixed.Commit})
			}
		}
	}
	return fixes, releases, nil
}

func (g *GitHubAdapter) releaseAsset(ctx context.Context, repo string, a githubAsset) ([]byte, error) {
	ctx = g.operation(ctx)
	scope, e := g.Client.Scope(ctx)
	if e != nil {
		return nil, e
	}
	key := fmt.Sprintf("asset:%s:%s:%d:%d:%s", scope, repo, a.ID, a.Size, a.Digest)
	if a.Digest != "" && a.State == "uploaded" {
		if b, ok := g.Client.CachedBlob(key); ok {
			return b, nil
		}
	}
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
	if a.Digest != "" {
		g.Client.CacheBlob(key, b, 0)
	}
	return b, nil
}

// Native workflow releases declare their package inventory and build identity in
// small metadata files. Their fix association comes from independent closing
// evidence in Track, not Code Factory's fixed-issue manifest fields.
func (g *GitHubAdapter) verifyWorkflowReleaseProvenance(ctx context.Context, repo string, r githubRelease) (ComponentProvenance, bool, error) {
	c := ComponentProvenance{Repository: repo}
	if r.Tag == "" || r.URL != "https://github.com/"+repo+"/releases/tag/"+r.Tag {
		return c, false, nil
	}
	assets := map[string]githubAsset{}
	assetIDs := map[int64]bool{}
	names := []string{}
	cliPackages := 0
	for _, a := range r.Assets {
		if _, duplicate := assets[a.Name]; duplicate || assetIDs[a.ID] || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`).MatchString(a.Name) || a.ID < 1 || a.State != "uploaded" || a.Size <= 0 || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(a.Digest) {
			return c, false, nil
		}
		assets[a.Name] = a
		assetIDs[a.ID] = true
		names = append(names, a.Name)
		if strings.HasPrefix(a.Name, "agent-workflow-") && strings.HasSuffix(a.Name, ".tar.gz") {
			cliPackages++
		}
	}
	if cliPackages != 4 {
		return c, false, nil
	}
	for _, name := range []string{"release-manifest.json", "SHA256SUMS", "manifest-provenance.jsonl", "dependencies.json", "conformance-evidence.json"} {
		if _, ok := assets[name]; !ok {
			return c, false, nil
		}
	}
	var manifest struct {
		Repository        string            `json:"repository"`
		Tag               string            `json:"tag"`
		Commit            string            `json:"commit"`
		SourceFingerprint string            `json:"source_fingerprint"`
		Assets            map[string]string `json:"assets"`
	}
	b, e := g.releaseAsset(ctx, repo, assets["release-manifest.json"])
	if e != nil {
		return c, false, e
	}
	if json.Unmarshal(b, &manifest) != nil || manifest.Repository != repo || manifest.Tag != r.Tag || !commitPattern.MatchString(manifest.Commit) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(manifest.SourceFingerprint) || len(manifest.Assets) != len(assets)-3 {
		return c, false, nil
	}
	for name, digest := range manifest.Assets {
		a, ok := assets[name]
		if !ok || name == "release-manifest.json" || name == "SHA256SUMS" || name == "manifest-provenance.jsonl" || a.Digest != "sha256:"+digest {
			return c, false, nil
		}
	}
	checksum, e := g.releaseAsset(ctx, repo, assets["SHA256SUMS"])
	if e != nil {
		return c, false, e
	}
	sort.Strings(names)
	expected := ""
	for _, name := range names {
		if name != "SHA256SUMS" {
			expected += strings.TrimPrefix(assets[name].Digest, "sha256:") + "  " + name + "\n"
		}
	}
	if string(checksum) != expected {
		return c, false, nil
	}
	var dependencies struct {
		Engine struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			Dirty   *bool  `json:"dirty"`
		} `json:"engine"`
	}
	b, e = g.releaseAsset(ctx, repo, assets["dependencies.json"])
	if e != nil {
		return c, false, e
	}
	if json.Unmarshal(b, &dependencies) != nil || dependencies.Engine.Version == "" || "v"+dependencies.Engine.Version != r.Tag || dependencies.Engine.Commit != manifest.Commit || dependencies.Engine.Dirty == nil || *dependencies.Engine.Dirty {
		return c, false, nil
	}
	var evidence struct {
		Candidate         string `json:"candidate"`
		SourceFingerprint string `json:"source_fingerprint"`
		Reviewed          bool   `json:"reviewed"`
		Records           []struct {
			Engine struct {
				Version           string `json:"version"`
				Commit            string `json:"commit"`
				Dirty             string `json:"dirty"`
				SourceFingerprint string `json:"source_fingerprint"`
			} `json:"engine"`
		} `json:"records"`
	}
	b, e = g.releaseAsset(ctx, repo, assets["conformance-evidence.json"])
	if e != nil {
		return c, false, e
	}
	if json.Unmarshal(b, &evidence) != nil || evidence.Candidate != manifest.Commit || evidence.SourceFingerprint != manifest.SourceFingerprint || !evidence.Reviewed || len(evidence.Records) == 0 {
		return c, false, nil
	}
	for _, record := range evidence.Records {
		if record.Engine.Version != dependencies.Engine.Version || record.Engine.Commit != manifest.Commit || record.Engine.Dirty != "false" || record.Engine.SourceFingerprint != manifest.SourceFingerprint {
			return c, false, nil
		}
	}
	for _, platform := range []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"} {
		if _, ok := assets["agent-workflow-"+dependencies.Engine.Version+"-"+platform+".tar.gz"]; !ok {
			return c, false, nil
		}
	}
	var tag struct {
		SHA string `json:"sha"`
	}
	if e = g.get(ctx, "/repos/"+repo+"/commits/"+url.PathEscape(r.Tag), &tag); e != nil {
		return c, false, e
	}
	if tag.SHA != manifest.Commit {
		return c, false, nil
	}
	c.Version, c.Commit = dependencies.Engine.Version, manifest.Commit
	return c, true, nil
}

// Published metadata and uploaded assets are separate GitHub states. The tag,
// complete package set, checksums and component commits must all agree.
func (g *GitHubAdapter) verifyReleaseProvenance(ctx context.Context, repo string, r githubRelease) (Provenance, bool, error) {
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
	b, e := g.releaseAsset(ctx, repo, r.Assets[manifest])
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
	checksum, e := g.releaseAsset(ctx, repo, r.Assets[sums])
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
