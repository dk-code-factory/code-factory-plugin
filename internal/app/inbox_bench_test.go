package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"codefactory/internal/domain"
	"codefactory/internal/store"
)

// Match the pilot's record counts and large retained histories without copying
// user data, credentials, provider configuration, or private skill contents.
func BenchmarkInboxRetainedHistory(b *testing.B) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(b.TempDir(), "state.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	a := &App{Store: db}
	if err := a.Init(ctx); err != nil {
		b.Fatal(err)
	}
	c, err := a.Config(ctx)
	if err != nil {
		b.Fatal(err)
	}
	if err := db.Write(ctx, func(tx *store.Tx) error {
		if err := tx.Put("identity", "github", domain.GitHubViewer{Login: "pilot"}); err != nil {
			return err
		}
		for n := 0; n < 452; n++ {
			id := fmt.Sprintf("pilot/repo#%d", n+1)
			i := domain.Item{ID: id, Kind: "pr", Repository: "pilot/repo", State: "open", Revision: "version", Activity: []domain.Activity{{Body: strings.Repeat("history ", 4000)}}}
			if n < 11 {
				i.ReviewRequests = []string{"pilot"}
			}
			if err := tx.Put("item", id, i); err != nil {
				return err
			}
			if err := tx.Put("user", id, domain.UserState{ID: id}); err != nil {
				return err
			}
			if err := tx.Put("assessment", id, domain.Assessment{ID: id, Status: "complete", Revision: i.Revision, Policy: c.PolicyVersion}); err != nil {
				return err
			}
		}
		for n := 0; n < 1372; n++ {
			l := domain.Relationship{ID: fmt.Sprintf("link:%04d", n), Source: fmt.Sprintf("pilot/repo#%d", n%452+1), Target: fmt.Sprintf("pilot/repo#%d", (n+1)%452+1)}
			if err := tx.Put("relationship", l.ID, l); err != nil {
				return err
			}
		}
		for n := 0; n < 265; n++ {
			i := domain.Item{ID: fmt.Sprintf("external/repo#%d", n), Kind: "pr", Activity: []domain.Activity{{Body: strings.Repeat("history ", 7000)}}}
			if err := tx.Put("linked-pr", i.ID, i); err != nil {
				return err
			}
		}
		for n := 0; n < 27; n++ {
			r := domain.Run{ID: fmt.Sprintf("run:%d", n), ItemID: fmt.Sprintf("pilot/repo#%d", n+1), Status: "idle", Skills: []domain.Skill{{Files: map[string]string{"fixture": strings.Repeat("resource ", 66000)}}}}
			if err := tx.Put("run", r.ID, r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for _, mode := range []string{"full", "summaries"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				var rows []domain.InboxEntry
				var err error
				if mode == "summaries" {
					rows, err = a.InboxSummaries(ctx, "open")
				} else {
					rows, err = a.Inbox(ctx, "open")
				}
				if err != nil || len(rows) != 452 {
					b.Fatal("inbox", len(rows), err)
				}
			}
		})
	}
	b.Run("dashboard-attention", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			page, err := a.ListAttention(ctx, AttentionInput{Limit: 1})
			if err != nil || len(page.Requests) != 0 {
				b.Fatal("unexpected attention from unrelated retained histories", page, err)
			}
		}
	})
}
