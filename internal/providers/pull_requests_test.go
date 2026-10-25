package providers

import (
	"codefactory/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPullRequestMetadataPaginationCurrentHeadAndRevision(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "POST" || r.URL.Path != "/graphql" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("unexpected metadata request")
		}
		var query struct {
			Query     string
			Variables struct{ After *string }
		}
		_ = json.NewDecoder(r.Body).Decode(&query)
		if !strings.HasPrefix(query.Query, "query(") {
			t.Error("metadata request was not read-only")
		}
		page := `"hasNextPage":true,"endCursor":"next"`
		number := 7
		if query.Variables.After != nil {
			page = `"hasNextPage":false,"endCursor":null`
			number = 8
		}
		fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"headRefOid":"head","reviewDecision":"APPROVED","statusCheckRollup":{"state":"SUCCESS"},"closingIssuesReferences":{"nodes":[{"number":%d,"repository":{"nameWithOwner":"org/repo"}}],"pageInfo":{%s}}}}}}`, number, page)
	}))
	defer server.Close()
	g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	i := domain.Item{ID: "org/repo#1", Repository: "org/repo", Number: 1, Kind: "pr", State: "open", Head: "head", Checks: json.RawMessage(`{"checks":[{"name":"build","status":"completed","conclusion":"success"}],"statuses":[{"context":"deploy","state":"success","id":2},{"context":"deploy","state":"failure","id":1}]}`)}
	g.EnrichPullRequest(context.Background(), &i)
	if i.PR.Error != "" || i.PR.ReviewDecision != "approved" || i.PR.CI != "success" || len(i.ClosingIssues) != 2 || len(i.PR.Checks) != 2 || i.PR.Checks[0].Conclusion != "success" {
		t.Fatal("incorrect PR summary")
	}
	revision := i.ContentRevision()
	g.EnrichPullRequest(context.Background(), &i)
	if revision != i.ContentRevision() || requests != 4 {
		t.Fatal("freshness changed content revision or pagination failed")
	}
	i.Head = "new-head"
	g.EnrichPullRequest(context.Background(), &i)
	if i.PR.CI != "unknown" || i.PR.Error == "" {
		t.Fatal("mixed metadata from two heads")
	}
}

func TestPullRequestMetadataRateLimitBackoff(t *testing.T) {
	for _, status := range []int{429, 200} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(status)
				if status == 200 {
					fmt.Fprint(w, `{"errors":[{"type":"RATE_LIMITED"}]}`)
				}
			}))
			defer server.Close()
			g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
			i := domain.Item{ID: "org/repo#1", Repository: "org/repo", Number: 1, Kind: "pr", State: "open", Head: "head"}
			g.EnrichPullRequest(context.Background(), &i)
			g.EnrichPullRequest(context.Background(), &i)
			if calls != 1 || i.PR.Error == "" || i.PR.RetryAt.IsZero() || i.PR.CI != "unknown" {
				t.Fatal("rate limit was retried or presented as passing CI")
			}

		})
	}
}

func TestUnprotectedPRReviewFallbackFollowsEveryOpinionPage(t *testing.T) {
	for _, second := range []string{"APPROVED", "CHANGES_REQUESTED", "DISMISSED"} {
		t.Run(second, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var query struct{ Variables struct{ ReviewAfter *string } }
				_ = json.NewDecoder(r.Body).Decode(&query)
				state, page := "APPROVED", `"hasNextPage":true,"endCursor":"reviews-next"`
				if query.Variables.ReviewAfter != nil {
					state = second
					page = `"hasNextPage":false`
				}
				fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"headRefOid":"head","reviewDecision":null,"latestOpinionatedReviews":{"nodes":[{"state":%q}],"pageInfo":{%s}},"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`, state, page)
			}))
			defer server.Close()
			g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
			i := domain.Item{ID: "org/repo#1", Repository: "org/repo", Number: 1, Kind: "pr", Head: "head"}
			g.EnrichPullRequest(context.Background(), &i)
			expected := "approved"
			if second == "CHANGES_REQUESTED" {
				expected = "changes_requested"
			}
			if calls != 2 || i.PR.Error != "" || i.PR.ReviewDecision != expected {
				t.Fatal("unprotected PR review state lost opinion pages")
			}
		})
	}
}
