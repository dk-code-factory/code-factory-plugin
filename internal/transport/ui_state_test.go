package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func uiFixture(t *testing.T) (*Server, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := &app.App{Store: db, DataDir: dir}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return New(a, events.New(db, nil)), ctx
}
func waitProjection(t *testing.T, s *Server, ctx context.Context, cursor int64) *uiProjection {
	t.Helper()
	for {
		p, err := s.publishedUI(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if p.cursor >= cursor {
			return p
		}
		s.ui.mu.Lock()
		err = s.ui.err
		s.ui.mu.Unlock()
		if err != nil {
			t.Fatal("projection rebuild:", err)
		}
		if _, err := s.watchUIChanges(ctx, WatchUIInput{After: p.cursor, WaitMs: 100}); err != nil {
			t.Fatal(err)
		}
	}
}
func TestUIProjectionPaginatesAndSearchesCompleteRetainedStateWithoutProviders(t *testing.T) {
	s, ctx := uiFixture(t)
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		time.Sleep(100 * time.Millisecond)
		http.Error(w, "failed provider", 503)
	}))
	defer provider.Close()
	gh := providers.NewGitHub()
	gh.BaseURL = provider.URL
	s.App.GitHub = gh
	if err := s.App.Store.Write(ctx, func(tx *store.Tx) error {
		for n := 0; n < 1000; n++ {
			id := fmt.Sprintf("pilot/repo#%04d", n)
			item := domain.Item{ID: id, Title: fmt.Sprintf("Item %04d", n), Body: "searchable body " + id, Kind: "issue", State: "open", Activity: []domain.Activity{{Body: strings.Repeat("history ", 4000)}}}
			if err := tx.Put("item", id, item); err != nil {
				return err
			}
			if err := tx.Put("user", id, domain.UserState{ID: id}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cursor, _ := s.App.Store.UILatest(ctx)
	waitProjection(t, s, ctx, cursor)
	ids := map[string]bool{}
	for offset := 0; offset < 1000; offset += 50 {
		page, err := s.readUIState(ctx, UIStateInput{Views: []string{"inbox"}, Queue: "issues", Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		rows := page["inbox"].([]domain.InboxEntry)
		if len(rows) != 50 || page["page"].(map[string]any)["total"] != 1000 {
			t.Fatal("missing saved rows", page["page"])
		}
		for _, row := range rows {
			ids[row.Item.ID] = true
			if len(row.Item.Activity) > 0 {
				t.Fatal("history in list payload")
			}
		}
	}
	if len(ids) != 1000 {
		t.Fatal("pagination lost items", len(ids))
	}
	page, err := s.readUIState(ctx, UIStateInput{Search: "body pilot/repo#0999", Queue: "issues"})
	if err != nil || len(page["inbox"].([]domain.InboxEntry)) != 1 {
		t.Fatal("search omitted later page", page, err)
	}
	selected, err := s.readUIState(ctx, UIStateInput{Views: []string{"shell"}, ID: "pilot/repo#0999"})
	if err != nil || selected["selectedEntry"].(domain.InboxEntry).Item.ID != "pilot/repo#0999" {
		t.Fatal("selection not independently reachable", err)
	}
	if requests.Load() != 0 {
		t.Fatal("UI read contacted a provider", requests.Load())
	}
}

func TestUIWatchPublishesCommittedStateAndReconcilesOtherConnections(t *testing.T) {
	s, ctx := uiFixture(t)
	initial, err := s.publishedUI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fail := errors.New("rollback")
	if err := s.App.Store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.Put("item", "rolled-back", domain.Item{}); err != nil {
			return err
		}
		return fail
	}); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	quiet, err := s.watchUIChanges(ctx, WatchUIInput{After: initial.cursor, WaitMs: 30})
	if err != nil || quiet["cursor"] != initial.cursor || len(quiet["changes"].(map[string][]string)) != 0 {
		t.Fatal("rollback notified", quiet, err)
	}
	external, err := store.Open(filepath.Join(s.App.DataDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	result := make(chan map[string]any, 1)
	go func() {
		change, _ := s.watchUIChanges(ctx, WatchUIInput{After: initial.cursor, WaitMs: 2000})
		result <- change
	}()
	if err := external.Write(ctx, func(tx *store.Tx) error {
		if err := tx.Put("item", "external", domain.Item{ID: "external", Kind: "issue", State: "open", Title: "Committed before notification"}); err != nil {
			return err
		}
		return tx.Put("user", "external", domain.UserState{ID: "external"})
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case change := <-result:
		if change["cursor"].(int64) <= initial.cursor {
			t.Fatal("external commit not reconciled", change)
		}
		state, err := s.readUIState(ctx, UIStateInput{})
		if err != nil || state["cursor"] != change["cursor"] || state["inbox"].([]domain.InboxEntry)[0].Item.Title != "Committed before notification" {
			t.Fatal("notified before publication", state, err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestUIWatchCoalescesBurstsConcurrentWritersReconnectAndResync(t *testing.T) {
	s, ctx := uiFixture(t)
	initial, _ := s.publishedUI(ctx)
	external, err := store.Open(filepath.Join(s.App.DataDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	var wg sync.WaitGroup
	for writer, db := range []*store.Store{s.App.Store, external} {
		wg.Add(1)
		go func(writer int, db *store.Store) {
			defer wg.Done()
			for n := 0; n < 12; n++ {
				id := fmt.Sprintf("writer-%d-%d", writer, n)
				if err := db.Write(ctx, func(tx *store.Tx) error {
					if err := tx.Put("item", id, domain.Item{ID: id, Kind: "issue", State: "open"}); err != nil {
						return err
					}
					return tx.Put("user", id, domain.UserState{ID: id})
				}); err != nil {
					t.Error(err)
					return
				}
			}
		}(writer, db)
	}
	wg.Wait()
	cursor, _ := s.App.Store.UILatest(ctx)
	waitProjection(t, s, ctx, cursor)
	change, err := s.watchUIChanges(ctx, WatchUIInput{After: initial.cursor, WaitMs: 1})
	if err != nil || change["resync"] != false || len(change["changes"].(map[string][]string)["item"]) != 24 {
		t.Fatal("burst/reconnect lost invalidations", change, err)
	}
	data, _ := json.Marshal(change)
	if strings.Contains(string(data), "title") || strings.Contains(string(data), "body") {
		t.Fatal("watch disclosed payloads", string(data))
	}
	// A client ahead of this database, or older than retained publications, resyncs.
	future, err := s.watchUIChanges(ctx, WatchUIInput{After: cursor + 1, WaitMs: 1})
	if err != nil || future["resync"] != true {
		t.Fatal("unknown cursor did not resync", future, err)
	}
	s.ui.mu.Lock()
	s.ui.history = s.ui.history[len(s.ui.history)-1:]
	s.ui.mu.Unlock()
	old, err := s.watchUIChanges(ctx, WatchUIInput{After: initial.cursor, WaitMs: 1})
	if err != nil || old["resync"] != true {
		t.Fatal("unavailable cursor did not resync", old, err)
	}
}

func TestUIWatchCancellationAndSavedItemDoNotStartRefresh(t *testing.T) {
	s, ctx := uiFixture(t)
	if err := s.App.SaveRepository(ctx, domain.Repository{ID: "pilot/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := s.App.Store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.Put("item", "pilot/repo#1", domain.Item{ID: "pilot/repo#1", Repository: "pilot/repo", Kind: "issue", State: "open"}); err != nil {
			return err
		}
		return tx.Put("user", "pilot/repo#1", domain.UserState{ID: "pilot/repo#1"})
	}); err != nil {
		t.Fatal(err)
	}
	cursor, _ := s.App.Store.UILatest(ctx)
	waitProjection(t, s, ctx, cursor)
	before := s.App.Store.Latest(ctx)
	state, err := s.readUIState(ctx, UIStateInput{Views: []string{"item"}, ID: "pilot/repo#1"})
	if err != nil || state["detail"] == nil || s.App.Store.Latest(ctx) != before {
		t.Fatal("saved item read scheduled external work", state, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.watchUIChanges(canceled, WatchUIInput{After: cursor}); !errors.Is(err, context.Canceled) {
		t.Fatal("watch ignored disconnect", err)
	}
}

func TestUIProjectionExcludesPrivateSourceSkillFilesAndCredentials(t *testing.T) {
	s, ctx := uiFixture(t)
	run := domain.Run{ID: "saved-run", Status: "idle", SourceSnapshot: &domain.Item{Body: "private-source-marker"}, Skills: []domain.Skill{{Name: "fixture", Content: "private-skill-content", Files: map[string]string{"private": "private-skill-file"}}}}
	if err := s.App.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.App.Store.Put(ctx, "secret", "owner", "private-owner-credential", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.App.Store.Put(ctx, "skill", "fixture", domain.Skill{Name: "fixture", Content: "private-skill-content", Files: map[string]string{"private": "private-skill-file"}}, ""); err != nil {
		t.Fatal(err)
	}
	cursor, _ := s.App.Store.UILatest(ctx)
	waitProjection(t, s, ctx, cursor)
	state, err := s.readUIState(ctx, UIStateInput{Views: []string{"messages", "settings"}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(state)
	for _, marker := range []string{"private-source-marker", "private-skill-content", "private-skill-file", "private-owner-credential"} {
		if strings.Contains(string(data), marker) {
			t.Fatal("private payload escaped public projection", marker)
		}
	}
}
