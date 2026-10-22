package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestInboxDetectsPullRequestsInBothDirectionsAndExternalEvidence(t *testing.T) {
	a, ctx := fixture(t)
	issue := item()
	issue.Body = ""
	issue.Revision = issue.ContentRevision()
	importOne(t, a, ctx, issue)
	pr := issue
	pr.ID = "owner/repo#2"
	pr.Number = 2
	pr.Kind = "pr"
	pr.Revision = pr.ContentRevision()
	importOne(t, a, ctx, pr)
	for _, link := range []domain.Relationship{
		{ID: "forward", Source: issue.ID, Target: pr.ID, Manual: true},
		{ID: "reverse", Source: pr.ID, Target: issue.ID, Manual: true},
		{ID: "external", Source: issue.ID, Target: "external/repo#8", Evidence: "https://github.com/external/repo/pull/8", Manual: true},
	} {
		if err := a.Link(ctx, link, false); err != nil {
			t.Fatal(err)
		}
		rows, err := a.Inbox(ctx, "all")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.Item.ID == issue.ID {
				found = row.HasPullRequest
			}
		}
		if !found {
			t.Fatalf("link %s did not mark issue", link.ID)
		}
		if err := a.Link(ctx, link, true); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := a.Inbox(ctx, "all")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Item.ID == issue.ID && row.HasPullRequest {
			t.Fatal("removed links left stale issue membership")
		}
	}
}

func TestSyncPublishesAuthenticatedViewerAndTeamFailures(t *testing.T) {
	a, ctx := fixture(t)
	var denyTeams atomic.Bool
	var changedAccount atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			if changedAccount.Load() {
				fmt.Fprint(w, `{"login":"other"}`)
			} else {
				fmt.Fprint(w, `{"login":"me"}`)
			}
		case "/user/teams":
			if denyTeams.Load() {
				w.WriteHeader(403)
			} else {
				fmt.Fprint(w, `[{"slug":"core","organization":{"login":"org"}}]`)
			}
		case "/repos/org/repo/pulls":
			fmt.Fprint(w, `[{"number":1,"title":"Review","state":"open","updated_at":"2026-10-21T00:00:00Z","user":{"login":"author"}}]`)
		case "/repos/org/repo/pulls/1":
			fmt.Fprint(w, `{"head":{"sha":"abc","ref":"feature"},"requested_teams":[{"slug":"core"}]}`)
		case "/repos/org/repo/commits/abc/check-runs":
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	if err := a.SaveRepository(ctx, domain.Repository{ID: "org/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var viewer domain.GitHubViewer
	if err := a.Store.Get(ctx, "identity", "github", &viewer); err != nil {
		t.Fatal(err)
	}
	if viewer.Login != "me" || len(viewer.Teams) != 1 || viewer.Teams[0] != "org/core" || viewer.TeamError != "" {
		t.Fatal(viewer)
	}
	denyTeams.Store(true)
	if err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "identity", "github", &viewer); err != nil {
		t.Fatal(err)
	}
	if len(viewer.Teams) != 0 || viewer.TeamError == "" {
		t.Fatal("stale team membership or missing permission feedback", viewer)
	}
	changedAccount.Store(true)
	if err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "identity", "github", &viewer); err != nil {
		t.Fatal(err)
	}
	if viewer.Login != "other" || len(viewer.Teams) != 0 {
		t.Fatal("identity reused previous account", viewer)
	}
}
