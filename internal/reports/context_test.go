package reports

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestRepositoryContextPinsDocumentsBoundsSearchAndUsesConditionalCache(t *testing.T) {
	revision := strings.Repeat("a", 40)
	nextRevision := strings.Repeat("b", 40)
	searches := map[string]int{}
	conditional := 0
	contentReads := 0
	g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("context is not an authenticated read", r.Method)
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		repo := ""
		if len(parts) >= 3 {
			repo = parts[1] + "/" + parts[2]
		}
		switch {
		case r.URL.Path == "/search/issues":
			q := r.URL.Query().Get("q")
			if !strings.Contains(q, "answer") || r.URL.Query().Get("per_page") != "10" || r.URL.Query().Get("page") != "" {
				t.Error("unbounded or unrelated search", r.URL)
			}
			target := strings.TrimPrefix(strings.Fields(q)[0], "repo:")
			searches[target]++
			items := []any{}
			for n := 1; n <= 12; n++ {
				items = append(items, map[string]any{"number": n, "html_url": fmt.Sprintf("https://github.com/%s/issues/%d", target, n), "title": "Answer failure", "body": "Authorization: Bearer private-token\nrelated evidence"})
			}
			reply(w, map[string]any{"items": items})
		case len(parts) == 3:
			reply(w, map[string]any{"full_name": repo, "has_issues": true, "default_branch": "main"})
		case strings.Contains(r.URL.Path, "/commits/"):
			reply(w, map[string]string{"sha": revision})
		case strings.Contains(r.URL.Path, "/git/trees/"):
			if !strings.Contains(r.URL.Path, revision) {
				t.Error("unpinned tree", r.URL)
			}
			reply(w, map[string]any{"tree": []any{map[string]string{"path": ".github/ISSUE_TEMPLATE/bug.md", "type": "blob"}, map[string]string{"path": "SECURITY.md", "type": "blob"}, map[string]string{"path": "private.txt", "type": "blob"}}})
		case strings.Contains(r.URL.Path, "/contents/"):
			contentReads++
			if r.URL.Query().Get("ref") != revision {
				t.Error("document not pinned", r.URL)
			}
			w.Header().Set("ETag", `"fixed"`)
			if r.Header.Get("If-None-Match") == `"fixed"` {
				conditional++
				w.WriteHeader(http.StatusNotModified)
				return
			}
			text := "## What happened\nDescribe the observation.\n"
			if strings.HasSuffix(r.URL.Path, "SECURITY.md") {
				text = "Please report privately.\nAuthorization: Bearer private-token\n"
			}
			reply(w, map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(text))})
		case strings.HasSuffix(r.URL.Path, "/labels"):
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("Link", fmt.Sprintf("<http://%s%s?per_page=100&page=2>; rel=\"next\"", r.Host, r.URL.Path))
				reply(w, []any{map[string]string{"name": "bug"}})
			} else {
				reply(w, []any{map[string]string{"name": "regression"}})
			}
		default:
			t.Error("unexpected metadata request", r.URL)
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	first, err := g.PrepareContext(ctx, observation())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Repositories) != 3 || contentReads != 6 {
		t.Fatal(first, contentReads)
	}
	for _, r := range first.Repositories {
		if !r.Available || r.Revision != revision || len(r.Labels) != 2 || len(r.Documents) != 2 || len(r.Templates) != 1 || len(r.Candidates) != 10 || len(r.Omissions) != 0 || searches[r.Repository] != 1 {
			t.Fatal(r, searches)
		}
		for _, d := range r.Documents {
			if d.SHA256 != hashContent(d.Content) || strings.Contains(d.Content, "private-token") {
				t.Fatal("unsafe document", d)
			}
		}
	}
	if strings.Contains(string(marshalContext(&first)), "private-token") {
		t.Fatal("unsafe duplicate evidence")
	}
	if _, err = g.PrepareContext(ctx, observation()); err != nil || conditional != 6 {
		t.Fatal("conditional cache not reused", err, conditional)
	}
	revision = nextRevision
	latest, err := g.PrepareContext(ctx, observation())
	if err != nil || latest.Repositories[0].Revision != nextRevision || first.Repositories[0].Revision == nextRevision {
		t.Fatal("fresh generation did not resolve fresh revision", latest, err)
	}
}

func TestRepositoryContextMetadataOmissionsAndSharedCooldown(t *testing.T) {
	for _, rateLimited := range []bool{false, true} {
		t.Run(fmt.Sprint(rateLimited), func(t *testing.T) {
			requests := 0
			g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if rateLimited {
					w.Header().Set("Retry-After", "120")
					w.WriteHeader(429)
				} else {
					w.WriteHeader(404)
				}
			})
			first, err := g.PrepareContext(context.Background(), observation())
			if err != nil || len(first.Repositories) != 3 {
				t.Fatal(first, err)
			}
			for _, r := range first.Repositories {
				if len(r.Omissions) == 0 || r.Available || len(r.Documents) != 0 {
					t.Fatal("metadata failure not explicit", r)
				}
			}
			if rateLimited {
				if requests != 1 {
					t.Fatal("cooldown ignored during preparation", requests)
				}
				restarted := &GitHubAdapter{Client: g.Client, Store: g.Store}
				_, _ = restarted.PrepareContext(context.Background(), observation())
				if requests != 1 {
					t.Fatal("cooldown lost after adapter restart", requests)
				}
				if err := restarted.Validate(context.Background(), findings()); err == nil {
					t.Fatal("publication ignored cooldown")
				}
			}
		})
	}
}

func TestInvestigationSnapshotPrivateImmutableAndFreshOnRecovery(t *testing.T) {
	s, ctx := fixture(t)
	engine, g := setupEngine(s)
	engine.status = "running"
	prepared := draftContext()
	prepared.Repositories[0].Documents = []contextDocument{{Path: "CONTRIBUTING.md", Content: "Private captured guidance", SHA256: "not refreshed"}}
	g.preparedContext = prepared
	r := submit(t, s, ctx)
	step(t, s, ctx)
	rt, err := s.runtime(ctx, r.ID)
	if err != nil || rt.OutputContract != structuredContract || rt.Context.Repositories[0].Documents[0].SHA256 != hashContent("Private captured guidance") {
		t.Fatal(rt, err)
	}
	original := hash(rt.Context)
	prepared.Repositories[0].Documents[0].Content = "new upstream guidance"
	restarted := &Service{Dir: s.Dir, Store: s.Store, Engine: engine, GitHub: g, Vault: s.Vault}
	step(t, restarted, ctx)
	after, _ := s.runtime(ctx, r.ID)
	if g.preparations != 1 || hash(after.Context) != original {
		t.Fatal("polling refreshed the immutable context", g.preparations, after.Context)
	}
	public, _ := json.Marshal(get(t, s, ctx, r.ID))
	if strings.Contains(string(public), "Private captured guidance") || strings.Contains(string(public), "outputContract") {
		t.Fatal("private context leaked to MCP", string(public))
	}
	engine.status = "succeeded"
	engine.rawResult = json.RawMessage(`{"version":2,"repository":"invented"}`)
	step(t, restarted, ctx)
	if get(t, s, ctx, r.ID).Processing != "needs_attention" || engine.completions != 1 {
		t.Fatal("malformed draft did not finalize")
	}
	if _, err := restarted.ResumeReport(ctx, ResumeInput{ID: r.ID}); err != nil {
		t.Fatal(err)
	}
	engine.status = "running"
	engine.rawResult = nil
	step(t, restarted, ctx)
	fresh, _ := s.runtime(ctx, r.ID)
	if g.preparations != 2 || fresh.Generation != 2 || fresh.Context.Repositories[0].Documents[0].Content != "new upstream guidance" {
		t.Fatal("recovery did not prepare fresh context", fresh, g.preparations)
	}
}
