package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestManualPRReviewRejectsOwnPullRequests(t *testing.T) {
	for _, route := range []string{"dedicated", "generic", "configured", "unknown-viewer", "unknown-author"} {
		t.Run(route, func(t *testing.T) {
			a, pr := reviewFixture(t)
			ctx := a.Runs.ctx
			pr.Author = "ME"
			want := "cannot review your own pull request"
			if route == "unknown-viewer" {
				if err := a.Store.Put(ctx, "identity", "github", domain.GitHubViewer{}, ""); err != nil {
					t.Fatal(err)
				}
				want = "cannot verify PR review eligibility"
			}
			if route == "unknown-author" {
				pr.Author = ""
				want = "cannot verify PR review eligibility"
			}
			if err := a.Store.Put(ctx, "item", pr.ID, pr, ""); err != nil {
				t.Fatal(err)
			}
			profile := "review"
			if route == "configured" {
				var p domain.Profile
				_ = a.Store.Get(ctx, "profile", profile, &p)
				p.ID = "custom-review"
				if err := a.SaveProfile(ctx, p); err != nil {
					t.Fatal(err)
				}
				c, _ := a.Config(ctx)
				c.ReviewProfileID = p.ID
				if err := a.SaveConfig(ctx, c); err != nil {
					t.Fatal(err)
				}
				profile = p.ID
			}
			before := a.Store.Latest(ctx)
			var err error
			if route == "generic" || route == "configured" {
				_, err = a.Runs.Prepare(ctx, LaunchInput{ItemID: pr.ID, ProfileID: profile})
			} else {
				_, err = a.Runs.PreparePRReview(ctx, pr.ID)
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("review should be rejected with %q: %v", want, err)
			}
			if a.Store.Latest(ctx) != before {
				t.Fatal("rejected review wrote an event")
			}
			runs, err := store.List[domain.Run](ctx, a.Store, "run")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range runs {
				if r.ItemID == pr.ID {
					t.Fatal("rejected review persisted a run")
				}
			}
			// Other work on the owner's PR remains available.
			if _, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: pr.ID, ProfileID: "investigate"}); err != nil {
				t.Fatal("investigation of own PR was blocked", err)
			}
		})
	}
}

func TestManualPRReviewRetainsEligibilityAfterDefaultChanges(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	var p domain.Profile
	_ = a.Store.Get(ctx, "profile", "review", &p)
	p.ID = "old-review"
	if err := a.SaveProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	c, _ := a.Config(ctx)
	c.ReviewProfileID = p.ID
	if err := a.SaveConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	r, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.ReviewProfileID = "review"
	if err := a.SaveConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	pr.Author = "me"
	if err := a.Store.Put(ctx, "item", pr.ID, pr, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Runs.Start(ctx, r.ID, false); err == nil || !strings.Contains(err.Error(), "cannot review your own pull request") {
		t.Fatal("changed default bypassed retained review eligibility", err)
	}
}

func TestManualPRReviewChecksFreshAuthorBeforeProvider(t *testing.T) {
	a, pr := reviewFixture(t)
	ctx := a.Runs.ctx
	r, err := a.Runs.PreparePRReview(ctx, pr.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentItemProvider(t, a, fmt.Sprintf(`{"number":2,"title":"PR","state":"open","user":{"login":"me"},"head":{"sha":%q}}`, pr.Head), 200)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_ = a.Store.Get(ctx, "run", r.ID, &r)
		return r.Status == "failed"
	})
	if !strings.Contains(r.Error, "cannot review your own pull request") || r.ThreadID != "" || r.ResumePending {
		t.Fatal("fresh author check did not stop the review", r.Error)
	}
	if _, err := os.Stat(r.Worktree); !os.IsNotExist(err) {
		t.Fatal("fresh self review created a workspace", err)
	}
}

func TestManualPRReviewRechecksAuthorBeforeStart(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepared", true: "legacy"}[legacy], func(t *testing.T) {
			a, pr := reviewFixture(t)
			ctx := a.Runs.ctx
			r, err := a.Runs.PreparePRReview(ctx, pr.ID)
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				// Construct an older snapshot without any new eligibility metadata.
				r = domain.Run{ID: r.ID, ItemID: r.ItemID, Profile: r.Profile, Status: "prepared", Worktree: r.Worktree}
				if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Store.Put(ctx, "identity", "github", domain.GitHubViewer{Login: "OTHER"}, ""); err != nil {
				t.Fatal(err)
			}
			before := a.Store.Latest(ctx)
			if _, err := a.Runs.Start(ctx, r.ID, false); err == nil || !strings.Contains(err.Error(), "cannot review your own pull request") {
				t.Fatal("prepared self review was accepted", err)
			}
			if a.Store.Latest(ctx) != before {
				t.Fatal("rejected start changed run state")
			}
			if _, err := os.Stat(r.Worktree); !os.IsNotExist(err) {
				t.Fatal("rejected start created a workspace", err)
			}
		})
	}
}
