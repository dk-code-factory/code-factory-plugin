package providers

import (
	"codefactory/internal/domain"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestReviewRequestsRefreshWithoutIssueTimestampChange(t *testing.T) {
	var requested atomic.Bool
	requested.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/org/repo/issues":
			fmt.Fprint(w, `[]`)
		case "/repos/org/repo/pulls":
			fmt.Fprint(w, `[{"number":1,"title":"Review me","state":"open","user":{"login":"author"},"updated_at":"2026-10-21T00:00:00Z"}]`)
		case "/repos/org/repo/pulls/1":
			if requested.Load() {
				fmt.Fprint(w, `{"head":{"sha":"abc","ref":"feature"},"requested_reviewers":[{"login":"reviewer"}],"requested_teams":[{"slug":"core"}]}`)
			} else {
				fmt.Fprint(w, `{"head":{"sha":"abc","ref":"feature"},"requested_reviewers":[],"requested_teams":[]}`)
			}
		case "/repos/org/repo/commits/abc/check-runs":
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	first, err := g.Sync(context.Background(), domain.Repository{ID: "org/repo"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || !reflect.DeepEqual(first[0].ReviewRequests, []string{"reviewer"}) || !reflect.DeepEqual(first[0].ReviewTeams, []string{"org/core"}) {
		t.Fatal(first)
	}
	requested.Store(false)
	second, err := g.Sync(context.Background(), domain.Repository{ID: "org/repo"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || len(second[0].ReviewRequests) != 0 || len(second[0].ReviewTeams) != 0 || second[0].Revision == first[0].Revision {
		t.Fatal("review removal was not synchronized", second)
	}
}

func TestGitHubTeamMembershipPaginationAndFailure(t *testing.T) {
	var server *httptest.Server
	var denied atomic.Bool
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if denied.Load() {
			w.WriteHeader(403)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `[{"slug":"second","organization":{"login":"org"}}]`)
			return
		}
		w.Header().Set("Link", "<"+server.URL+"/user/teams?page=2>; rel=\"next\"")
		fmt.Fprint(w, `[{"slug":"first","organization":{"login":"org"}}]`)
	}))
	defer server.Close()
	g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	teams, err := g.Teams(context.Background())
	if err != nil || !reflect.DeepEqual(teams, []string{"org/first", "org/second"}) {
		t.Fatal(teams, err)
	}
	denied.Store(true)
	if _, err = g.Teams(context.Background()); err == nil {
		t.Fatal("membership permission failure hidden")
	}
}
