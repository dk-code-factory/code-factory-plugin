package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRefreshPullRequestsTerminatesOnlyClosedSourceSessions(t *testing.T) {
	for _, merged := range []bool{false, true} {
		t.Run(fmt.Sprint(merged), func(t *testing.T) {
			a, pr := reviewFixture(t)
			t.Setenv("CF_FAKE_MODE", "question")
			ctx := a.Runs.ctx
			start := func(id string) domain.Run {
				run, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: id, ProfileID: "investigate"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
					t.Fatal(err)
				}
				waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", run.ID, &run); return run.Status == "waiting" })
				return run
			}
			run := start(pr.ID)
			original := run
			at := pendingQuestion(t, a, "rpc")
			issueRun := start("owner/repo#1")
			if err := a.Link(ctx, domain.Relationship{Source: issueRun.ItemID, Target: pr.ID}, false); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(run.Worktree, "unsaved.txt")
			if err := os.WriteFile(file, []byte("retained work"), 0600); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/owner/repo/pulls/2":
					fmt.Fprintf(w, `{"number":2,"title":"PR","state":"closed","merged":%t,"user":{"login":"other"},"head":{"sha":%q}}`, merged, pr.Head)
				case "/repos/owner/repo/commits/" + pr.Head + "/check-runs":
					fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
				default:
					fmt.Fprint(w, `[]`)
				}
			}))
			defer server.Close()
			a.GitHub = &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
			if err := a.RefreshPullRequests(ctx); err != nil {
				t.Fatal(err)
			}
			var saved domain.Item
			if err := a.Store.Get(ctx, "item", pr.ID, &saved); err != nil || !closedWork(saved) {
				t.Fatal("targeted closure was not accepted", saved.State, err)
			}
			_ = a.Store.Get(ctx, "run", run.ID, &run)
			if run.Status != "terminated" || run.ResumePending {
				t.Fatal("targeted closure retained active source work", run.Status)
			}
			if run.ThreadID != original.ThreadID || run.Worktree != original.Worktree || run.Prompt != original.Prompt {
				t.Fatal("targeted closure discarded retained session history")
			}
			events, err := a.Store.Events(ctx, 0, 200)
			if err != nil {
				t.Fatal(err)
			}
			retainedOutput := false
			for _, event := range events {
				retainedOutput = retainedOutput || event.Entity == run.ID && event.Kind == "run.output"
			}
			if !retainedOutput {
				t.Fatal("targeted closure discarded conversation history")
			}
			live, err := a.Runs.get(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			live.mu.Lock()
			retired := live.retired
			live.mu.Unlock()
			if !retired {
				t.Fatal("terminated source still owns its provider")
			}
			page, err := a.ListAttention(ctx, AttentionInput{RunID: run.ID})
			if err != nil || len(page.Requests) != 0 {
				t.Fatal("targeted closure retained actionable questions", err)
			}
			if err := a.Runs.Answer(ctx, at.ID, json.RawMessage(`{"answers":{"scope":{"answers":["Focused"]}}}`)); err == nil {
				t.Fatal("terminated question accepted an answer")
			}
			_ = a.Store.Get(ctx, "run", issueRun.ID, &issueRun)
			if issueRun.Status != "waiting" {
				t.Fatal("linked PR closure stopped the independent issue session", issueRun.Status)
			}
			page, err = a.ListAttention(ctx, AttentionInput{RunID: issueRun.ID})
			if err != nil || len(page.Requests) != 1 {
				t.Fatal("linked issue lost its own question", err)
			}
			if err := a.Runs.Cleanup(ctx, run.ID); err == nil {
				t.Fatal("dirty terminated workspace was removed")
			}
			if data, err := os.ReadFile(file); err != nil || string(data) != "retained work" {
				t.Fatal("targeted closure lost unsaved work", err)
			}
			saved.State, saved.PR.State = "open", "open"
			importOne(t, a, ctx, saved)
			if _, err := a.Runs.Start(ctx, run.ID, true); err == nil {
				t.Fatal("reopening source revived terminated work")
			}
		})
	}
}

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
