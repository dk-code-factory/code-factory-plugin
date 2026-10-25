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
	"strings"
	"testing"
	"time"

	"codefactory/internal/providers"
)

func adapter(t *testing.T, handler http.HandlerFunc) *GitHubAdapter {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := providers.NewGitHub()
	client.BaseURL = server.URL
	client.Client = server.Client()
	client.Token = func(context.Context) (string, error) { return "fixture-token", nil }
	return &GitHubAdapter{Client: client}
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
	g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/timeline"):
			reply(w, timeline)
		case strings.Contains(p, "/pulls/"):
			reply(w, map[string]any{"merged": true, "merge_commit_sha": fix, "body": "Fixes #17", "html_url": "https://github.com/" + owner + "/pull/21"})
		case strings.Contains(p, "/compare/"):
			reply(w, map[string]string{"status": comparison})
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
	timeline = []any{map[string]any{"event": "cross-referenced", "source": map[string]any{"issue": map[string]any{"number": 21, "html_url": "https://github.com/" + owner + "/pull/21", "pull_request": map[string]any{"url": "api"}}}}}
	fixes, available, e = g.Track(ctx, issue, component)
	if e != nil || len(fixes) != 1 || len(available) != 0 {
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
	fixes, available, e = g.Track(ctx, issue, component)
	if e != nil || len(available) != 1 || available[0].ComponentVersion != "1.2.0" || available[0].Prerelease {
		t.Fatal("verified release lost", fixes, available, e)
	}
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
	// Dependency availability belongs to the dependency's own verified release.
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
	releases[0].URL = "https://github.com/" + owner + "/releases/tag/v1.2.0"
	for i := range releases[0].Assets {
		a := &releases[0].Assets[i]
		h := sha256.Sum256(assets[a.ID])
		a.Size = int64(len(assets[a.ID]))
		a.Digest = "sha256:" + hex.EncodeToString(h[:])
	}
	// The Code Factory repository does not publish the dependency manifest.
	oldHandler := g.Client.Client.Transport
	g.Client.Client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/repos/"+DefaultRepository+"/releases") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("[]")), Header: http.Header{}}, nil
		}
		return oldHandler.RoundTrip(r)
	})
	_, available, e = g.Track(ctx, issue, component)
	if e != nil || len(available) != 1 || available[0].Repository != owner || available[0].ComponentVersion != "2.0.0" {
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
		})
	}
}
