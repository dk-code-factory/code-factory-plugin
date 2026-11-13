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

func TestPullRequestMetadataOmitsCompletedConnections(t *testing.T) {
	for _, pages := range []struct {
		name                      string
		closing, reviews, threads int
	}{
		{"threads-last", 1, 2, 3},
		{"closing-last", 3, 1, 2},
		{"reviews-last", 2, 3, 1},
	} {
		t.Run(pages.name, func(t *testing.T) {
			calls := 0
			reads := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query     string
					Variables map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					http.Error(w, "invalid query", http.StatusBadRequest)
					return
				}
				page := calls
				calls++
				pr := map[string]any{"headRefOid": "head", "reviewDecision": nil, "statusCheckRollup": map[string]string{"state": "SUCCESS"}}
				for _, scalar := range []string{"headRefOid", "reviewDecision", "statusCheckRollup"} {
					if !strings.Contains(request.Query, scalar) {
						t.Errorf("page %d omitted current PR state %s", page, scalar)
					}
				}
				for _, connection := range []struct {
					field, variable string
					pages           int
				}{
					{"closingIssuesReferences", "after", pages.closing},
					{"latestOpinionatedReviews", "reviewAfter", pages.reviews},
					{"reviewThreads", "threadAfter", pages.threads},
				} {
					requested := strings.Contains(request.Query, connection.field+"(")
					want := page < connection.pages
					if requested != want {
						t.Errorf("page %d requested %s = %v, want %v", page, connection.field, requested, want)
					}
					cursor, hasVariable := request.Variables[connection.variable]
					declared := strings.Contains(request.Query, "$"+connection.variable+":String")
					if hasVariable != want || declared != want {
						t.Errorf("page %d cursor variable %s present=%v declared=%v, want %v", page, connection.variable, hasVariable, declared, want)
					}
					if !requested {
						continue
					}
					reads[connection.field]++
					var wantCursor any
					if page > 0 {
						wantCursor = fmt.Sprintf("%s-%d", connection.field, page)
					}
					if cursor != wantCursor {
						t.Errorf("page %d %s cursor = %v, want %v", page, connection.field, cursor, wantCursor)
					}
					var node any
					switch connection.field {
					case "closingIssuesReferences":
						node = map[string]any{"number": page + 1, "repository": map[string]string{"nameWithOwner": "org/repo"}}
					case "latestOpinionatedReviews":
						state := "APPROVED"
						if page > 0 {
							state = "CHANGES_REQUESTED"
						}
						node = map[string]string{"state": state}
					case "reviewThreads":
						node = map[string]any{"id": fmt.Sprintf("thread-%d", page), "isResolved": page%2 == 0}
					}
					pr[connection.field] = map[string]any{"nodes": []any{node}, "pageInfo": map[string]any{"hasNextPage": page+1 < connection.pages, "endCursor": fmt.Sprintf("%s-%d", connection.field, page+1)}}
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": pr}}}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
			i := domain.Item{ID: "org/repo#7", Repository: "org/repo", Number: 7, Kind: "pr", Head: "head"}
			g.EnrichPullRequest(context.Background(), &i)
			if calls != 3 || reads["closingIssuesReferences"] != pages.closing || reads["latestOpinionatedReviews"] != pages.reviews || reads["reviewThreads"] != pages.threads {
				t.Fatalf("connections were resolved after completion: calls=%d reads=%v", calls, reads)
			}
			decision := "approved"
			if pages.reviews > 1 {
				decision = "changes_requested"
			}
			if i.PR == nil || i.PR.Error != "" || i.PR.ReviewDecision != decision || i.PR.CI != "success" || len(i.ClosingIssues) != pages.closing || len(i.ReviewThreads) != pages.threads {
				t.Fatalf("incomplete independent pages or lost review fallback: %+v", i)
			}
			for page, issue := range i.ClosingIssues {
				if issue != fmt.Sprintf("org/repo#%d", page+1) {
					t.Errorf("closing issue %d = %s", page, issue)
				}
			}
			for page, thread := range i.ReviewThreads {
				if thread.ID != fmt.Sprintf("thread-%d", page) || thread.Resolved != (page%2 == 0) {
					t.Errorf("thread %d = %+v", page, thread)
				}
			}
		})
	}
}

func TestPullRequestMetadataRejectsHeadChangeAfterConnectionCompletes(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		head := "head"
		if calls > 1 {
			head = "new-head"
		}
		fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"headRefOid":%q,"reviewDecision":null,"latestOpinionatedReviews":{"nodes":[{"state":"APPROVED"}],"pageInfo":{"hasNextPage":false}},"closingIssuesReferences":{"nodes":[{"number":8,"repository":{"nameWithOwner":"org/repo"}}],"pageInfo":{"hasNextPage":false}},"reviewThreads":{"nodes":[{"id":"new-thread"}],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}}`, head)
	}))
	defer server.Close()
	g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	i := domain.Item{ID: "org/repo#7", Repository: "org/repo", Number: 7, Kind: "pr", Head: "head", ClosingIssues: []string{"org/repo#1"}, ReviewThreads: []domain.ReviewThread{{ID: "saved-thread"}}}
	g.EnrichPullRequest(context.Background(), &i)
	if calls != 2 || i.PR == nil || i.PR.Error != "PR head changed during synchronization" || !i.PR.SyncedAt.IsZero() {
		t.Fatalf("mixed PR heads were accepted: calls=%d pr=%+v", calls, i.PR)
	}
	if len(i.ClosingIssues) != 1 || i.ClosingIssues[0] != "org/repo#1" || len(i.ReviewThreads) != 1 || i.ReviewThreads[0].ID != "saved-thread" {
		t.Fatalf("partial metadata replaced saved links or threads: %+v", i)
	}
}
