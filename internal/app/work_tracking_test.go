package app

import (
	"codefactory/internal/domain"
	"testing"
	"time"
)

func TestWorkingTracksSessionsUntilGitHubCompletion(t *testing.T) {
	a, ctx := fixture(t)
	issue := item()
	issue.Body = ""
	importOne(t, a, ctx, issue)
	pr := issue
	pr.ID = "owner/repo#2"
	pr.Number = 2
	pr.Kind = "pr"
	pr.State = "merged"
	pr.Author = "me"
	pr.PR = &domain.PullRequest{ID: pr.ID, State: pr.State, ReviewDecision: "approved", CI: "success"}
	importOne(t, a, ctx, pr)
	_ = a.Store.Put(ctx, "identity", "github", domain.GitHubViewer{Login: "me"}, "")
	for _, r := range []domain.Run{{ID: "older-blocked", ItemID: issue.ID, Status: "waiting"}, {ID: "newer-idle", ItemID: issue.ID, Status: "idle"}, {ID: "pr", ItemID: pr.ID, Status: "idle"}} {
		if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
			t.Fatal(err)
		}
	}
	_ = a.Store.Put(ctx, "attention", "question", domain.Attention{ID: "question", RunID: "older-blocked", Status: "pending", Blocking: true}, "")
	read := func(id string) domain.InboxEntry {
		entries, err := a.Inbox(ctx, "all")
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Item.ID == id {
				return e
			}
		}
		t.Fatal("missing item")
		return domain.InboxEntry{}
	}
	if e := read(issue.ID); !ContainsQueue(e, "working") || e.Work.Status != "Waiting for you" || e.Work.AttentionCount != 1 || len(e.Work.RunIDs) != 2 {
		t.Fatal("older blocked session was hidden", e.Work)
	}
	// An incidental reference must not complete the issue.
	_ = a.Store.Put(ctx, "relationship", "reference", domain.Relationship{ID: "reference", Source: pr.ID, Target: issue.ID, Kind: "github"}, "")
	if e := read(issue.ID); !ContainsQueue(e, "working") || len(e.LinkedPullRequests) != 1 || e.LinkedPullRequests[0].Implementation {
		t.Fatal("reference completed implementation work")
	}
	if err := a.Link(ctx, domain.Relationship{Source: issue.ID, Target: pr.ID}, false); err != nil {
		t.Fatal(err)
	}
	if e := read(issue.ID); ContainsQueue(e, "working") || !e.Work.Complete || !e.LinkedPullRequests[0].Implementation {
		t.Fatal("merged linked implementation did not complete the issue")
	}
	pr.State = "open"
	pr.PR.State = "open"
	importOne(t, a, ctx, pr)
	if e := read(issue.ID); !ContainsQueue(e, "working") {
		t.Fatal("reopened work was not restored")
	}
	if e := read(pr.ID); !ContainsQueue(e, "mine") || !ContainsQueue(e, "working") {
		t.Fatal("authored PR queue mismatch")
	}
	pr.State = "closed"
	pr.PR.State = "closed"
	importOne(t, a, ctx, pr)
	if e := read(issue.ID); !ContainsQueue(e, "working") {
		t.Fatal("unmerged closed PR completed issue")
	}
	issue.State = "closed"
	importOne(t, a, ctx, issue)
	if e := read(issue.ID); ContainsQueue(e, "working") {
		t.Fatal("closed issue still working")
	}
}

func TestPRMetadataFreshnessDoesNotConsumeUnreadOrLoseLinks(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	i.Kind = "pr"
	i.Body = ""
	i.Head = "one"
	i.ClosingIssues = []string{"owner/repo#2"}
	i.PR = &domain.PullRequest{ID: i.ID, Head: "one", CI: "success", ReviewDecision: "approved", SyncedAt: time.Now()}
	importOne(t, a, ctx, i)
	var old domain.Item
	_ = a.Store.Get(ctx, "item", i.ID, &old)
	_ = a.UserAction(ctx, i.ID, "read", old.Revision, nil)
	i.PR.SyncedAt = time.Now().Add(time.Minute)
	importOne(t, a, ctx, i)
	entries, _ := a.Inbox(ctx, "all")
	if entries[0].Unread {
		t.Fatal("poll timestamp created unread content")
	}
	i.PR.Error = "metadata unavailable"
	i.PR.CI = "unknown"
	i.ClosingIssues = nil
	importOne(t, a, ctx, i)
	_ = a.Store.Get(ctx, "item", i.ID, &old)
	if old.PR.CI != "success" || old.PR.Error == "" || len(old.ClosingIssues) != 1 {
		t.Fatal("failed refresh discarded prior evidence")
	}
	i.Head = "two"
	i.PR.Head = "two"
	importOne(t, a, ctx, i)
	_ = a.Store.Get(ctx, "item", i.ID, &old)
	if old.PR.CI != "unknown" || len(old.ClosingIssues) != 1 {
		t.Fatal("new head reused old CI or lost unresolved development links")
	}
}
