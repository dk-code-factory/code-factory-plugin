package providers

import (
	"codefactory/internal/prreview"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewEvidenceCompleteInventoryDecisionsAndPagination(t *testing.T) {
	for _, mode := range []string{"complete", "truncated", "moved", "graph-error"} {
		t.Run(mode, func(t *testing.T) {
			metadataCalls := 0
			filesCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/user":
					fmt.Fprint(w, `{"login":"reviewer"}`)
				case "/repos/o/r/pulls/2":
					metadataCalls++
					head := strings.Repeat("a", 40)
					if mode == "moved" && metadataCalls > 1 {
						head = strings.Repeat("c", 40)
					}
					fmt.Fprintf(w, `{"html_url":"https://github.com/o/r/pull/2","node_id":"PR_test","body":"## Decisions\n\n- Preserve ordering.\n\nSee #7","user":{"login":"owner"},"base":{"sha":%q},"head":{"sha":%q},"changed_files":2}`, strings.Repeat("b", 40), head)
				case "/repos/o/r/pulls/2/files":
					filesCalls++
					if r.URL.Query().Get("page") == "2" {
						fmt.Fprint(w, `[{"filename":"b.go","additions":1,"deletions":0,"status":"added"}]`)
					} else {
						if mode != "truncated" {
							w.Header().Set("Link", fmt.Sprintf(`<http://%s/repos/o/r/pulls/2/files?per_page=100&page=2>; rel="next"`, r.Host))
						}
						fmt.Fprint(w, `[{"filename":"a.go","additions":1,"deletions":0,"status":"modified"}]`)
					}
				case "/repos/o/r/issues/2/comments", "/repos/o/r/pulls/2/reviews":
					fmt.Fprint(w, `[]`)
				case "/repos/o/r/issues/7":
					fmt.Fprint(w, `{"number":7,"body":"Linked scope"}`)
				case "/graphql":
					if mode == "graph-error" {
						fmt.Fprint(w, `{"errors":[{"message":"incomplete"}]}`)
					} else {
						fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"totalCount":0,"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`)
					}
				default:
					t.Errorf("unexpected %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
			e, err := (ReviewGitHub{Client: g}).ReadEvidence(context.Background(), "https://github.com/o/r/pull/2")
			if mode != "complete" {
				if err == nil {
					t.Fatal("incomplete or changing evidence accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(e.Files) != 2 || filesCalls != 2 || !e.Decisions.Data.Verified || len(e.Decisions.Data.Decisions) != 2 || e.Scope["o/r/7"] == nil {
				t.Fatalf("capture missing evidence: %+v", e)
			}
		})
	}
}
func TestReviewSubmissionOwnsMutationAndDoesNotRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload prreview.Payload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Query != prreview.ReviewMutation {
			t.Error("unowned mutation")
		}
		fmt.Fprint(w, `{"errors":[{"message":"uncertain"}]}`)
	}))
	defer server.Close()
	g := ReviewGitHub{Client: &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}}
	g.submissionCtx = g.Client.StartOperation(context.Background(), nil, false)
	if _, err := g.Submit(context.Background(), "https://github.com/o/r/pull/2", prreview.Payload{Query: "forged"}); err == nil || calls != 0 {
		t.Fatal("forged query sent")
	}
	if _, err := g.Submit(context.Background(), "https://github.com/o/r/pull/2", prreview.Payload{Query: prreview.ReviewMutation}); err == nil || calls != 1 {
		t.Fatal("uncertain submission retried or accepted")
	}
}
