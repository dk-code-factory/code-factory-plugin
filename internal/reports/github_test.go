package reports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codefactory/internal/providers"
	"codefactory/internal/store"
)

func adapter(t *testing.T, handler http.HandlerFunc) *GitHubAdapter {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := providers.NewGitHub()
	client.BaseURL = server.URL
	client.Client = server.Client()
	client.Token = func(context.Context) (string, error) { return "fixture-token", nil }
	s, _ := fixture(t)
	return &GitHubAdapter{Client: client, Store: s.Store}
}
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func TestRepositoryRoutingLabelsDuplicateAndPublicationBoundary(t *testing.T) {
	var posts int
	g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing auth")
		}
		switch {
		case r.Method == "POST":
			posts++
			var input map[string]any
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input["title"] != "Bug" || !strings.Contains(input["body"].(string), marker("local-report")) {
				t.Error("draft or marker changed", input)
			}
			reply(w, map[string]any{"number": 7, "html_url": "https://github.com/" + DefaultRepository + "/issues/7", "state": "open"})
		case r.URL.Path == "/repos/"+DefaultRepository+"/issues/7":
			reply(w, map[string]any{"number": 7, "html_url": "https://github.com/" + DefaultRepository + "/issues/7", "body": marker("known-report"), "state": "open"})
		case strings.Contains(r.URL.Path, "/contents/"):
			reply(w, map[string]any{"type": "file", "sha": strings.Repeat("a", 40)})
		case strings.HasSuffix(r.URL.Path, "/labels"):
			reply(w, []any{map[string]string{"name": "bug"}})
		case strings.HasSuffix(r.URL.Path, "/issues"):
			reply(w, []any{map[string]any{"number": 7, "html_url": "https://github.com/" + DefaultRepository + "/issues/7", "body": marker("known-report"), "state": "open"}})
		default:
			repo := strings.TrimPrefix(r.URL.Path, "/repos/")
			reply(w, map[string]any{"full_name": repo, "has_issues": true, "archived": false})
		}
	})
	ctx := context.Background()
	f := findings()
	f.Title = "Bug"
	f.SourceCommit = strings.Repeat("a", 40)
	f.SourcePath = "internal/app/app.go"
	f.OwnershipReason = "Implementation is in this source path"
	f.Labels = []string{"bug"}
	if e := g.Validate(ctx, f); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Findings){func(f *Findings) { f.Repository = "external/repo" }, func(f *Findings) { f.Component = "unknown" }, func(f *Findings) { f.Repository = "dk-code-factory/agent-workflow" }, func(f *Findings) { f.SourceCommit = "main" }, func(f *Findings) { f.SourcePath = "../private" }, func(f *Findings) { f.Labels = []string{"invented"} }} {
		bad := f
		change(&bad)
		if e := g.Validate(ctx, bad); e == nil {
			t.Fatal("bad routing or provenance accepted", bad)
		}
	}
	dep := f
	dep.Component = "agent-workflow"
	dep.Repository = "dk-code-factory/agent-workflow"
	if e := g.Validate(ctx, dep); e != nil {
		t.Fatal("dependency routing failed", e)
	}
	found, e := g.FindMarker(ctx, DefaultRepository, marker("known-report"))
	if e != nil || found == nil || found.Number != 7 {
		t.Fatal(found, e)
	}
	issue, e := g.Create(ctx, f, marker("local-report"))
	if e != nil || issue.Number != 7 || posts != 1 {
		t.Fatal(issue, e, posts)
	}
}
func TestTrackingRequiresPublishedMatchingProvenance(t *testing.T) {
	sha := strings.Repeat("a", 40)
	fix := strings.Repeat("b", 40)
	component := "code-factory"
	owner := DefaultRepository
	var releases []githubRelease
	timeline := []any{}
	assets := map[int64][]byte{}
	comparison := "ahead"
	tagSHA := sha
	failurePath := ""
	closingCommit := map[string]any{"sha": fix, "html_url": "https://github.com/" + owner + "/commit/" + fix, "commit": map[string]string{"message": "Fixes: #17"}}
	g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == failurePath {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch {
		case strings.HasSuffix(p, "/timeline"):
			reply(w, timeline)
		case strings.Contains(p, "/pulls/"):
			reply(w, map[string]any{"merged": true, "merge_commit_sha": fix, "body": "Resolves: " + owner + "#17", "html_url": "https://github.com/" + owner + "/pull/21"})
		case strings.Contains(p, "/compare/"):
			reply(w, map[string]string{"status": comparison})
		case strings.HasSuffix(p, "/commits/"+fix):
			reply(w, closingCommit)
		case strings.Contains(p, "/commits/"):
			reply(w, map[string]string{"sha": tagSHA})
		case strings.Contains(p, "/releases/assets/"):
			var id int64
			fmt.Sscanf(p[strings.LastIndex(p, "/")+1:], "%d", &id)
			_, _ = w.Write(assets[id])
		case strings.HasSuffix(p, "/releases"):
			reply(w, releases)
		default:
			t.Errorf("unexpected request %s", p)
			http.NotFound(w, r)
		}
	})
	issue := Issue{Repository: owner, Number: 17, State: "closed", ClosureReason: "completed"}
	ctx := context.Background()
	fixes, available, e := g.Track(ctx, issue, component)
	if e != nil || len(fixes) != 0 || len(available) != 0 {
		t.Fatal("closure inferred a fix", fixes, available, e)
	}
	closingEvent := map[string]any{"event": "closed", "commit_id": fix, "commit_url": "https://api.github.com/repos/" + owner + "/commits/" + fix}
	timeline = []any{closingEvent}
	fixes, available, e = g.Track(ctx, issue, component)
	if e != nil || len(fixes) != 1 || fixes[0].Commit != fix || fixes[0].PR != "" || len(available) != 0 {
		t.Fatal("verified closing commit without a release was lost", fixes, available, e)
	}
	for _, message := range []string{"CLOSED: " + owner + "#17", "resolved: https://github.com/" + owner + "/issues/17"} {
		closingCommit["commit"] = map[string]string{"message": message}
		fixes, available, e = g.Track(ctx, issue, component)
		if e != nil || len(fixes) != 1 || fixes[0].Commit != fix || len(available) != 0 {
			t.Fatal("supported qualified closing reference was lost", message, fixes, available, e)
		}
	}
	for _, invalid := range []struct {
		name, event, eventURL, commitSHA, commitURL, message string
	}{
		{"manual closure", "closed", "", fix, "https://github.com/" + owner + "/commit/" + fix, "Fixes #17"},
		{"reference only", "referenced", closingEvent["commit_url"].(string), fix, "https://github.com/" + owner + "/commit/" + fix, "Fixes #17"},
		{"foreign event", "closed", "https://api.github.com/repos/other/repo/commits/" + fix, fix, "https://github.com/" + owner + "/commit/" + fix, "Fixes #17"},
		{"wrong identity", "closed", closingEvent["commit_url"].(string), sha, "https://github.com/" + owner + "/commit/" + fix, "Fixes #17"},
		{"foreign commit", "closed", closingEvent["commit_url"].(string), fix, "https://github.com/other/repo/commit/" + fix, "Fixes #17"},
		{"unrelated issue", "closed", closingEvent["commit_url"].(string), fix, "https://github.com/" + owner + "/commit/" + fix, "Fixes: #170"},
		{"foreign issue", "closed", closingEvent["commit_url"].(string), fix, "https://github.com/" + owner + "/commit/" + fix, "Fixes: other/repo#17"},
		{"ordinary mention", "closed", closingEvent["commit_url"].(string), fix, "https://github.com/" + owner + "/commit/" + fix, "See #17"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			timeline = []any{map[string]any{"event": invalid.event, "commit_id": fix, "commit_url": invalid.eventURL}}
			closingCommit = map[string]any{"sha": invalid.commitSHA, "html_url": invalid.commitURL, "commit": map[string]string{"message": invalid.message}}
			fixes, available, e := g.Track(ctx, issue, component)
			if e != nil || len(fixes) != 0 || len(available) != 0 {
				t.Fatal("unverified closing commit accepted", fixes, available, e)
			}
		})
	}
	closingCommit = map[string]any{"sha": fix, "html_url": "https://github.com/" + owner + "/commit/" + fix, "commit": map[string]string{"message": "Fixes #17"}}
	timeline = []any{closingEvent, map[string]any{"event": "cross-referenced", "source": map[string]any{"issue": map[string]any{"number": 21, "html_url": "https://github.com/" + owner + "/pull/21", "pull_request": map[string]any{"url": "api"}}}}}
	fixes, available, e = g.Track(ctx, issue, component)
	if e != nil || len(fixes) != 1 || fixes[0].PR != "https://github.com/"+owner+"/pull/21" || len(available) != 0 {
		t.Fatal("merged-but-unreleased state incorrect", fixes, available, e)
	}
	p := Provenance{Version: 1, Commit: sha, Components: map[string]ComponentProvenance{component: {Repository: owner, Version: "1.2.0", Commit: sha}}, Fixes: []FixedIssue{{Issue: owner + "#17", Component: component, Commit: fix}}, Files: []Artifact{}}
	for i, name := range []string{"code-factory-linux-amd64.tar.gz", "code-factory-plugin.zip"} {
		b := []byte(name)
		h := sha256.Sum256(b)
		assets[int64(i+1)] = b
		p.Files = append(p.Files, Artifact{Name: name, SHA256: hex.EncodeToString(h[:]), Size: int64(len(b))})
	}
	b, _ := json.Marshal(p)
	assets[3] = b
	h := sha256.Sum256(b)
	sums := ""
	for _, file := range p.Files {
		sums += file.SHA256 + "  " + file.Name + "\n"
	}
	sums += hex.EncodeToString(h[:]) + "  CI-MANIFEST.json\n"
	assets[4] = []byte(sums)
	published := time.Now().UTC()
	release := githubRelease{Tag: "v1.2.0", URL: "https://github.com/" + owner + "/releases/tag/v1.2.0", PublishedAt: &published}
	for i, name := range []string{"code-factory-linux-amd64.tar.gz", "code-factory-plugin.zip", "CI-MANIFEST.json", "SHA256SUMS"} {
		h := sha256.Sum256(assets[int64(i+1)])
		release.Assets = append(release.Assets, struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			State  string `json:"state"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		}{int64(i + 1), name, "uploaded", int64(len(assets[int64(i+1)])), "sha256:" + hex.EncodeToString(h[:])})
	}
	releases = []githubRelease{release}
	originalManifestDigest := release.Assets[2].Digest
	fixes, available, e = g.Track(ctx, issue, component)
	if e != nil || len(available) != 1 || available[0].ComponentVersion != "1.2.0" || available[0].Prerelease || len(fixes) != 1 || fixes[0].Version != "1.2.0" || fixes[0].PR != "https://github.com/"+owner+"/pull/21" {
		t.Fatal("verified release lost", fixes, available, e)
	}
	for _, endpoint := range []string{"/repos/" + owner + "/releases", "/repos/" + owner + "/releases/assets/3"} {
		failurePath = endpoint
		// A new immutable identity forces the asset request; unchanged verified bytes
		// are intentionally reusable when only connectivity changes.
		if strings.Contains(endpoint, "/assets/") {
			releases[0].Assets[2].Digest = ""
		}
		fixes, available, e = g.Track(ctx, issue, component)
		if e == nil || len(fixes) != 1 || fixes[0].Commit != fix || fixes[0].PR == "" || fixes[0].Version != "" || len(available) != 0 {
			t.Fatal("release read failure discarded the fix or inferred delivery", endpoint, fixes, available, e)
		}
	}
	releases[0].Assets[2].Digest = originalManifestDigest
	broken := release
	broken.Tag = "v1.3.0"
	broken.Assets = append(broken.Assets[:0:0], release.Assets...)
	broken.Assets[2].ID = 99
	releases = []githubRelease{release, broken}
	failurePath = "/repos/" + owner + "/releases/assets/99"
	fixes, available, e = g.Track(ctx, issue, component)
	if e == nil || len(fixes) != 1 || fixes[0].Version != "1.2.0" || fixes[0].PR == "" || len(available) != 1 || available[0].Version != release.Tag {
		t.Fatal("later release failure discarded earlier verified delivery", fixes, available, e)
	}
	failurePath = ""
	releases = []githubRelease{release}
	releases[0].Prerelease = true
	_, available, e = g.Track(ctx, issue, component)
	if e != nil || len(available) != 1 || !available[0].Prerelease {
		t.Fatal("prerelease treated as stable", available, e)
	}
	releases[0].Draft = true
	_, available, e = g.Track(ctx, issue, component)
	if e != nil || len(available) != 0 {
		t.Fatal("draft treated as published", available, e)
	}
	releases[0].Draft = false
	releases[0].PublishedAt = nil
	_, available, e = g.Track(ctx, issue, component)
	if e != nil || len(available) != 0 {
		t.Fatal("unpublished treated as release", available, e)
	}
	releases[0].PublishedAt = &published
	tagSHA = fix
	_, available, e = g.Track(ctx, issue, component)
	if e != nil || len(available) != 0 {
		t.Fatal("mismatched tag accepted", available, e)
	}
	tagSHA = sha
	releases[0].Assets[0].Digest = "sha256:" + strings.Repeat("c", 64)
	_, available, e = g.Track(ctx, issue, component)
	if e != nil || len(available) != 0 {
		t.Fatal("mismatched artifact accepted", available, e)
	}
	releases[0].Assets[0].Digest = "sha256:" + p.Files[0].SHA256
	comparison = "behind"
	_, available, e = g.Track(ctx, issue, component)
	if e != nil || len(available) != 0 {
		t.Fatal("fix outside release ancestry accepted", available, e)
	}
	comparison = "ahead"
	// Code Factory attribution requires its explicit dependency relationship.
	owner = "dk-code-factory/agent-workflow"
	component = "agent-workflow"
	issue.Repository = owner
	timeline = []any{}
	p.Components = map[string]ComponentProvenance{component: {Repository: owner, Version: "2.0.0", Commit: sha}}
	p.Fixes = []FixedIssue{{Issue: owner + "#17", Component: component, Commit: fix}}
	b, _ = json.Marshal(p)
	assets[3] = b
	h = sha256.Sum256(b)
	sums = ""
	for _, file := range p.Files {
		sums += file.SHA256 + "  " + file.Name + "\n"
	}
	sums += hex.EncodeToString(h[:]) + "  CI-MANIFEST.json\n"
	assets[4] = []byte(sums)
	releases[0].URL = "https://github.com/" + DefaultRepository + "/releases/tag/v1.2.0"
	for i := range releases[0].Assets {
		a := &releases[0].Assets[i]
		h := sha256.Sum256(assets[a.ID])
		a.Size = int64(len(assets[a.ID]))
		a.Digest = "sha256:" + hex.EncodeToString(h[:])
	}
	// The dependency does not publish this Code Factory manifest format.
	oldHandler := g.Client.Client.Transport
	g.Client.Client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/repos/"+owner+"/releases") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("[]")), Header: http.Header{}}, nil
		}
		return oldHandler.RoundTrip(r)
	})
	_, available, e = g.Track(ctx, issue, component)
	if e != nil || len(available) != 1 || available[0].Repository != DefaultRepository || available[0].ComponentVersion != "2.0.0" {
		t.Fatal("dependency delivery misattributed", available, e)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPPublicationRejectsKnownFailuresAndRetainsServerUncertainty(t *testing.T) {
	for _, status := range []int{400, 403, 422, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
				if status == 429 {
					w.Header().Set("Retry-After", "120")
				}
				w.WriteHeader(status)
			})
			_, err := g.Create(context.Background(), findings(), marker("report"))
			var rejected *publicationRejected
			if errors.As(err, &rejected) != (status < 500) {
				t.Fatal("incorrect mutation uncertainty classification", status, err)
			}
			var retry retryError
			if errors.As(err, &retry) != (status == 429) || status == 429 && !retry.RetryTime().After(time.Now()) {
				t.Fatal("rate-limited rejection lost its retry deadline", status, err)
			}
		})
	}
}

func TestHTTPPublicationFailuresBeforeDispatchAreDefinite(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*GitHubAdapter) any
	}{
		{"encoding", func(g *GitHubAdapter) any { return make(chan int) }},
		{"request configuration", func(g *GitHubAdapter) any { g.Client.BaseURL = "http://[private-fixture-value"; return nil }},
		{"authentication", func(g *GitHubAdapter) any {
			g.Client.Token = func(context.Context) (string, error) { return "", errors.New("private-fixture-value") }
			return nil
		}},
		{"missing cooldown store", func(g *GitHubAdapter) any { g.Store = nil; return nil }},
		{"unreadable cooldown store", func(g *GitHubAdapter) any { _ = g.Store.Close(); return nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int64
			g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			})
			_, err := g.request(context.Background(), http.MethodPost, "/repos/"+DefaultRepository+"/issues", test.change(g), "")
			var rejected *publicationRejected
			if !errors.As(err, &rejected) || requests.Load() != 0 || strings.Contains(err.Error(), "private-fixture-value") || !strings.Contains(err.Error(), "resume") {
				t.Fatal("pre-dispatch failure lost safe actionable recovery", err, requests.Load())
			}
		})
	}
}

func TestHTTPPublicationRecoveryDistinguishesTokenFailureFromLostResponse(t *testing.T) {
	for _, lostResponse := range []bool{false, true} {
		t.Run(fmt.Sprint(lostResponse), func(t *testing.T) {
			s, ctx := fixture(t)
			engine, _ := setupEngine(s)
			engine.findings.SourceCommit = strings.Repeat("a", 40)
			engine.findings.SourcePath = "internal/app/app.go"
			engine.findings.OwnershipReason = "Implementation is in this source path"
			var posts, tokens, markerReads atomic.Int64
			g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost:
					posts.Add(1)
					if lostResponse {
						c, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Error(e)
							return
						}
						_ = c.Close()
						return
					}
					reply(w, map[string]any{"number": 17, "html_url": "https://github.com/" + DefaultRepository + "/issues/17", "state": "open"})
				case strings.Contains(r.URL.Path, "/contents/"):
					reply(w, map[string]string{"type": "file"})
				case strings.HasPrefix(r.URL.Path, "/repos/") && strings.HasSuffix(r.URL.Path, "/issues"):
					markerReads.Add(1)
					reply(w, []any{})
				default:
					reply(w, map[string]any{"full_name": DefaultRepository, "has_issues": true})
				}
			})
			s.GitHub = g
			failToken := !lostResponse
			g.Client.Token = func(context.Context) (string, error) {
				tokens.Add(1)
				if failToken {
					return "", errors.New("private-fixture-value")
				}
				return "fixture-token", nil
			}
			r := submit(t, s, ctx)
			step(t, s, ctx)
			step(t, s, ctx)
			current := get(t, s, ctx, r.ID)
			rt, e := s.runtime(ctx, r.ID)
			if e != nil || current.Issue != nil || tokens.Load() != 2 {
				t.Fatal("unexpected operation outcome", current.Processing, rt, e, tokens.Load())
			}
			if lostResponse {
				if current.Processing != "needs_attention" || !rt.CreateAttempted || posts.Load() != 1 {
					t.Fatal("lost response became replayable", current.Processing, rt, posts.Load())
				}
			} else if current.Processing != "blocked" || rt.CreateAttempted || posts.Load() != 0 || markerReads.Load() != 0 || !strings.Contains(current.Message, "authentication") || strings.Contains(current.Message, "private-fixture-value") {
				t.Fatal("token failure dispatched publication", current.Processing, rt, posts.Load())
			}
			failToken = false
			if _, e = s.ResumeReport(ctx, ResumeInput{ID: r.ID}); e != nil {
				t.Fatal(e)
			}
			if lostResponse {
				if get(t, s, ctx, r.ID).Processing != "needs_attention" || posts.Load() != 1 || markerReads.Load() != 2 {
					t.Fatal("uncertain creation replayed", posts.Load(), markerReads.Load())
				}
				return
			}
			step(t, s, ctx)
			step(t, s, ctx)
			if get(t, s, ctx, r.ID).Processing != "complete" || posts.Load() != 1 || markerReads.Load() != 1 || tokens.Load() != 4 {
				t.Fatal("fresh authenticated operation failed", posts.Load(), markerReads.Load(), tokens.Load())
			}
		})
	}
}

func TestGitHubReportingCooldownSurvivesRestartAndSeparatesCredentials(t *testing.T) {
	s, ctx := fixture(t)
	reset := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	var requests atomic.Int64
	g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "120")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(reset.Unix()))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		reply(w, map[string]any{"number": 17, "state": "open", "html_url": "https://github.com/" + DefaultRepository + "/issues/17"})
	})
	g.Store = s.Store
	assertLimited := func(err error) {
		t.Helper()
		var retry retryError
		if !errors.As(err, &retry) || !retry.RetryTime().Equal(reset) || requests.Load() != 1 {
			t.Fatal("credential cooldown did not retain its deadline or suppress HTTP", err, requests.Load())
		}
	}
	_, err := g.ReadIssue(ctx, DefaultRepository, 17)
	assertLimited(err)
	_, err = g.ReadIssue(ctx, "dk-code-factory/agent-workflow", 18)
	assertLimited(err)
	_, _, err = g.Track(ctx, Issue{Repository: DefaultRepository, Number: 17}, "code-factory")
	assertLimited(err)
	_, err = g.Create(ctx, findings(), marker("blocked-publication"))
	assertLimited(err)
	var rejected *publicationRejected
	if !errors.As(err, &rejected) {
		t.Fatal("locally deferred publication became an uncertain mutation", err)
	}
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(s.Dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	g = &GitHubAdapter{Client: g.Client, Store: reopened}
	_, err = g.ReadIssue(ctx, DefaultRepository, 17)
	assertLimited(err)
	g.Client.Token = func(context.Context) (string, error) { return "different-fixture-token", nil }
	if _, err := g.ReadIssue(ctx, DefaultRepository, 17); err != nil || requests.Load() != 2 {
		t.Fatal("independent credential inherited another credential's cooldown", err, requests.Load())
	}
	g.Client.Token = func(context.Context) (string, error) { return "fixture-token", nil }
	g.Client.Now = func() time.Time { return reset.Add(time.Second) }
	if _, err := g.ReadIssue(ctx, DefaultRepository, 17); err != nil || requests.Load() != 3 {
		t.Fatal("expired cooldown prevented reporting refresh", err, requests.Load())
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ReadIssue(ctx, DefaultRepository, 17); err == nil || requests.Load() != 3 {
		t.Fatal("unreadable cooldown state allowed an HTTP request", err, requests.Load())
	}
}

func TestTrackingNativeWorkflowRelease(t *testing.T) {
	sha, fix, fingerprint := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 64)
	var repo, comparison, tagSHA string
	var manifest, engine, evidence, evidenceEngine map[string]any
	var payloads map[string][]byte
	var downloads map[int64][]byte
	var names map[int64]string
	var release githubRelease
	var hasFix bool
	reset := func() {
		repo, comparison, tagSHA, hasFix = "dk-code-factory/agent-workflow", "ahead", sha, true
		manifest = map[string]any{"repository": repo, "tag": "v2.0.0", "commit": sha, "source_fingerprint": fingerprint, "registry": map[string]any{"image": "ghcr.io/" + repo + "-worker", "digest": "sha256:" + fingerprint, "platforms": map[string]string{"amd64": "sha256:" + fingerprint, "arm64": "sha256:" + fingerprint}}}
		// The native dependency inventory uses a boolean dirty value; conformance
		// engine records use the CLI's string value and source fingerprint.
		engine = map[string]any{"version": "2.0.0", "commit": sha, "dirty": false}
		evidenceEngine = map[string]any{"version": "2.0.0", "commit": sha, "dirty": "false", "source_fingerprint": fingerprint}
		evidence = map[string]any{"candidate": sha, "source_fingerprint": fingerprint, "reviewed": true, "records": []any{map[string]any{"scenario": "fixture", "engine": evidenceEngine}}}
		payloads = map[string][]byte{}
		for _, platform := range []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"} {
			name := "agent-workflow-2.0.0-" + platform + ".tar.gz"
			payloads[name] = []byte("opaque CLI package fixture: " + name)
		}
		// Auxiliary native artifacts are inventoried, rather than executed or
		// interpreted as independent signature, registry or archive verification.
		for _, name := range []string{"registry.json", "sbom.cdx.json", "vulnerability-summary.json", "worker-linux-amd64.json", "worker-linux-arm64.json", "worker-linux-amd64.tar.gz", "worker-linux-arm64.tar.gz", "conformance-evidence.json.asc", "archive-provenance.jsonl", "image-index-provenance.jsonl", "image-amd64-provenance.jsonl", "image-arm64-provenance.jsonl"} {
			payloads[name] = []byte("opaque auxiliary fixture: " + name)
		}
		published := time.Now().UTC()
		release = githubRelease{Tag: "v2.0.0", URL: "https://github.com/" + repo + "/releases/tag/v2.0.0", PublishedAt: &published}
	}
	seal := func() {
		payloads["dependencies.json"], _ = json.Marshal(map[string]any{"engine": engine, "go_modules": []any{}, "npm_packages": map[string]any{}})
		payloads["conformance-evidence.json"], _ = json.Marshal(evidence)
		inventory := map[string]string{}
		for name, b := range payloads {
			h := sha256.Sum256(b)
			inventory[name] = hex.EncodeToString(h[:])
		}
		manifest["assets"] = inventory
		payloads["release-manifest.json"], _ = json.Marshal(manifest)
		// The native manifest excludes itself, sums, and this later bundle;
		// the final sorted checksums cover all files except SHA256SUMS itself.
		payloads["manifest-provenance.jsonl"] = []byte("opaque manifest bundle fixture")
		ordered := []string{}
		for name := range payloads {
			ordered = append(ordered, name)
		}
		sort.Strings(ordered)
		sums := ""
		for _, name := range ordered {
			h := sha256.Sum256(payloads[name])
			sums += hex.EncodeToString(h[:]) + "  " + name + "\n"
		}
		payloads["SHA256SUMS"] = []byte(sums)
		ordered = append(ordered, "SHA256SUMS")
		downloads, names = map[int64][]byte{}, map[int64]string{}
		for i, name := range ordered {
			b, id := payloads[name], int64(i+1)
			h := sha256.Sum256(b)
			release.Assets = append(release.Assets, githubAsset{ID: id, Name: name, State: "uploaded", Size: int64(len(b)), Digest: "sha256:" + hex.EncodeToString(h[:])})
			downloads[id], names[id] = b, name
		}
	}
	replace := func(name string, b []byte) {
		for i := range release.Assets {
			a := &release.Assets[i]
			if a.Name == name {
				h := sha256.Sum256(b)
				a.Size, a.Digest, downloads[a.ID] = int64(len(b)), "sha256:"+hex.EncodeToString(h[:]), b
			}
		}
	}
	for _, test := range []struct {
		name        string
		change      func()
		afterSeal   bool
		wantFix     bool
		wantRelease bool
	}{
		{"native stable", func() {}, false, true, true},
		{"native prerelease", func() { release.Prerelease = true }, false, true, true},
		{"no closing fix", func() { hasFix = false }, false, false, false},
		{"ancestry behind", func() { comparison = "behind" }, false, true, false},
		{"ancestry diverged", func() { comparison = "diverged" }, false, true, false},
		{"foreign repository", func() { manifest["repository"] = "other/repo" }, false, true, false},
		{"foreign release URL", func() { release.URL = "https://github.com/other/repo/releases/tag/v2.0.0" }, false, true, false},
		{"wrong tag", func() { manifest["tag"] = "v2.1.0" }, false, true, false},
		{"wrong tag commit", func() { tagSHA = fix }, false, true, false},
		{"short source commit", func() { manifest["commit"] = sha[:7] }, false, true, false},
		{"invalid fingerprint", func() { manifest["source_fingerprint"] = sha }, false, true, false},
		{"evidence candidate mismatch", func() { evidence["candidate"] = fix }, false, true, false},
		{"evidence fingerprint mismatch", func() { evidence["source_fingerprint"] = strings.Repeat("d", 64) }, false, true, false},
		{"engine fingerprint mismatch", func() { evidenceEngine["source_fingerprint"] = strings.Repeat("d", 64) }, false, true, false},
		{"engine version mismatch", func() { engine["version"] = "2.1.0" }, false, true, false},
		{"engine commit mismatch", func() { engine["commit"] = fix }, false, true, false},
		{"dirty dependency", func() { engine["dirty"] = true }, false, true, false},
		{"missing dirty declaration", func() { delete(engine, "dirty") }, false, true, false},
		{"wrong native dirty type", func() { engine["dirty"] = "false" }, false, true, false},
		{"dirty conformance engine", func() { evidenceEngine["dirty"] = "true" }, false, true, false},
		{"unreviewed evidence", func() { evidence["reviewed"] = false }, false, true, false},
		{"missing engine evidence", func() { evidence["records"] = []any{} }, false, true, false},
		{"missing CLI platform", func() { delete(payloads, "agent-workflow-2.0.0-darwin-arm64.tar.gz") }, false, true, false},
		{"extra CLI version", func() { payloads["agent-workflow-1.9.0-linux-amd64.tar.gz"] = []byte("different version") }, false, true, false},
		{"draft", func() { release.Draft = true }, false, true, false},
		{"unpublished", func() { release.PublishedAt = nil }, false, true, false},
		{"duplicate asset", func() { release.Assets = append(release.Assets, release.Assets[0]) }, true, true, false},
		{"duplicate asset ID", func() { release.Assets[1].ID = release.Assets[0].ID }, true, true, false},
		{"unexpected uploaded asset", func() {
			name, b := "unexpected.txt", []byte("otherwise valid unexpected upload")
			id, digest := int64(len(release.Assets)+1), sha256.Sum256(b)
			release.Assets = append(release.Assets, githubAsset{ID: id, Name: name, State: "uploaded", Size: int64(len(b)), Digest: "sha256:" + hex.EncodeToString(digest[:])})
			payloads[name], downloads[id], names[id] = b, b, name
			// Keep checksums complete so only the native manifest's omission of
			// this unique, valid upload can reject its inventory.
			ordered := []string{}
			for name := range payloads {
				if name != "SHA256SUMS" {
					ordered = append(ordered, name)
				}
			}
			sort.Strings(ordered)
			sums := ""
			for _, name := range ordered {
				h := sha256.Sum256(payloads[name])
				sums += hex.EncodeToString(h[:]) + "  " + name + "\n"
			}
			replace("SHA256SUMS", []byte(sums))
		}, true, true, false},
		{"unsafe asset", func() { release.Assets[0].Name = "../unsafe" }, true, true, false},
		{"missing asset digest", func() { release.Assets[0].Digest = "" }, true, true, false},
		{"mismatched package digest", func() { release.Assets[0].Digest = "sha256:" + strings.Repeat("e", 64) }, true, true, false},
		{"upload incomplete", func() { release.Assets[0].State = "new" }, true, true, false},
		{"bad checksum inventory", func() { replace("SHA256SUMS", []byte("incomplete\n")) }, true, true, false},
		{"missing provenance bundle", func() {
			for i, a := range release.Assets {
				if a.Name == "manifest-provenance.jsonl" {
					release.Assets = append(release.Assets[:i], release.Assets[i+1:]...)
					break
				}
			}
		}, true, true, false},
		{"source-only dk-workflows", func() { repo = "dk-code-factory/dk-workflows"; release.Assets = nil }, true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			reset()
			if !test.afterSeal {
				test.change()
			}
			seal()
			if test.afterSeal {
				test.change()
			}
			manifestReads := 0
			g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
				p := r.URL.Path
				switch {
				case strings.HasPrefix(p, "/repos/"+DefaultRepository+"/"):
					reply(w, []any{})
				case strings.HasSuffix(p, "/timeline"):
					timeline := []any{}
					if hasFix {
						timeline = append(timeline, map[string]any{"event": "cross-referenced", "source": map[string]any{"issue": map[string]any{"number": 21, "html_url": "https://github.com/" + repo + "/pull/21", "pull_request": map[string]any{"url": "api"}}}})
					}
					reply(w, timeline)
				case strings.Contains(p, "/pulls/"):
					reply(w, map[string]any{"merged": true, "merge_commit_sha": fix, "body": "Fixes: #17", "html_url": "https://github.com/" + repo + "/pull/21"})
				case strings.Contains(p, "/compare/"):
					reply(w, map[string]string{"status": comparison})
				case strings.Contains(p, "/commits/"):
					reply(w, map[string]string{"sha": tagSHA})
				case strings.HasSuffix(p, "/releases"):
					reply(w, []githubRelease{release})
				case strings.Contains(p, "/releases/assets/"):
					var id int64
					fmt.Sscanf(p[strings.LastIndex(p, "/")+1:], "%d", &id)
					if names[id] == "release-manifest.json" {
						manifestReads++
					}
					if name := names[id]; name != "release-manifest.json" && name != "SHA256SUMS" && name != "dependencies.json" && name != "conformance-evidence.json" {
						t.Errorf("unexpected archive or auxiliary artifact download: %s", name)
					}
					_, _ = w.Write(downloads[id])
				default:
					t.Errorf("unexpected request %s", p)
					http.NotFound(w, r)
				}
			})
			fixes, releases, err := g.Track(context.Background(), Issue{Repository: repo, Number: 17, State: "closed"}, strings.TrimPrefix(repo, "dk-code-factory/"))
			if test.name == "unexpected uploaded asset" && manifestReads != 1 {
				t.Fatal("unexpected upload did not reach native manifest inventory validation", manifestReads, err)
			}
			if (len(fixes) == 1) != test.wantFix || (len(releases) == 1) != test.wantRelease {
				t.Fatal("incorrect independent native fix/delivery state", fixes, releases, err)
			}
			if test.wantRelease {
				if err != nil || fixes[0].Version != "2.0.0" || fixes[0].PR != "https://github.com/"+repo+"/pull/21" || releases[0].Repository != repo || releases[0].Version != "v2.0.0" || releases[0].ComponentVersion != "2.0.0" || releases[0].Prerelease != release.Prerelease || releases[0].Commit != fix {
					t.Fatal("native release identity, version or PR association lost", fixes, releases, err)
				}
			} else if test.wantFix && fixes[0].Version != "" {
				t.Fatal("unverified native package supplied a fix version", fixes, releases, err)
			}
		})
	}
}
