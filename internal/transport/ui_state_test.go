package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"database/sql"
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

func TestUIManagedSkillEditorReadsOnlySelectedContent(t *testing.T) {
	s, ctx := uiFixture(t)
	selected := domain.Skill{ID: "managed:selected", Name: "selected", Managed: true, Content: "---\nname: selected\n---\nSaved instructions", Files: map[string]string{"private": "private-file-marker"}}
	other := domain.Skill{ID: "managed:other", Name: "other", Managed: true, Content: "other-content-marker"}
	external := domain.Skill{ID: "external", Name: "external", Content: "external-content-marker"}
	for _, skill := range []domain.Skill{selected, other, external} {
		if err := s.App.Store.Put(ctx, "skill", skill.ID, skill, ""); err != nil {
			t.Fatal(err)
		}
	}
	cursor, _ := s.App.Store.UILatest(ctx)
	waitProjection(t, s, ctx, cursor)
	summary, err := s.readUIState(ctx, UIStateInput{Views: []string{"settings", "launch"}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(summary)
	for _, marker := range []string{"Saved instructions", "private-file-marker", "other-content-marker", "external-content-marker"} {
		if strings.Contains(string(data), marker) {
			t.Fatal("summary disclosed authoring content", marker)
		}
	}
	editor, err := s.readUIState(ctx, UIStateInput{Views: []string{"skill"}, ID: selected.ID})
	if err != nil {
		t.Fatal(err)
	}
	skill := editor["skill"].(domain.Skill)
	if skill.Content != selected.Content || len(skill.Files) != 0 || editor["skillCursor"] != cursor {
		t.Fatal("selected editor state incomplete or unsafe", editor)
	}
	data, _ = json.Marshal(editor)
	if strings.Contains(string(data), "other-content-marker") || strings.Contains(string(data), "private-file-marker") {
		t.Fatal("editor read disclosed unrelated content")
	}
	if _, err := s.readUIState(ctx, UIStateInput{Views: []string{"skill"}, ID: external.ID}); err == nil {
		t.Fatal("non-managed skill exposed to managed editor")
	}
}

func TestUIInitialPublicationNeedsNoLifetimeInvalidationHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &app.App{Store: db}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// Old invalidations are not authoritative state and need not be decoded for
	// a new full snapshot, even if a retired record can no longer be interpreted.
	if _, err := raw.ExecContext(ctx, "INSERT INTO ui_changes(data) VALUES(?)", []byte("retired invalidation format")); err != nil {
		t.Fatal(err)
	}
	server := New(a, events.New(db, nil))
	state, err := server.readUIState(ctx, UIStateInput{Views: []string{"settings"}})
	if err != nil || state["config"] == nil {
		t.Fatal("old journal prevented current saved state from opening", state, err)
	}
	change, err := server.watchUIChanges(ctx, WatchUIInput{After: 0, WaitMs: 1})
	if err != nil || change["resync"] != true {
		t.Fatal("old cursor should require full resync", change, err)
	}
}

func TestUITranscriptPublicationLeavesSummariesCurrentAndNotifiesOnlyHistory(t *testing.T) {
	s, ctx := uiFixture(t)
	run := domain.Run{ID: "retained", ItemID: "owner/repo#1", Status: "idle"}
	if err := s.App.Store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.Put("item", run.ItemID, domain.Item{ID: run.ItemID, Repository: "owner/repo", Kind: "issue", State: "open"}); err != nil {
			return err
		}
		if err := tx.Put("user", run.ItemID, domain.UserState{ID: run.ItemID}); err != nil {
			return err
		}
		return tx.Put("run", run.ID, run)
	}); err != nil {
		t.Fatal(err)
	}
	cursor, _ := s.App.Store.UILatest(ctx)
	before := waitProjection(t, s, ctx, cursor)
	if err := s.App.Store.Write(ctx, func(tx *store.Tx) error {
		return tx.Event("run.output", run.ID, map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"delta": "saved output"}})
	}); err != nil {
		t.Fatal(err)
	}
	cursor, _ = s.App.Store.UILatest(ctx)
	waitProjection(t, s, ctx, cursor)
	change, err := s.watchUIChanges(ctx, WatchUIInput{After: before.cursor, WaitMs: 1})
	if err != nil {
		t.Fatal(err)
	}
	domains := change["changes"].(map[string][]string)
	if len(domains) != 1 || len(domains["transcript"]) != 1 || domains["transcript"][0] != run.ID {
		t.Fatal("output invalidated unrelated views", change)
	}
	state, err := s.readUIState(ctx, UIStateInput{Views: []string{"inbox", "messages"}})
	if err != nil {
		t.Fatal(err)
	}
	if state["inbox"].([]domain.InboxEntry)[0].Work.Status != "Inactive" {
		t.Fatal("history altered work state")
	}
	run.Status = "running"
	if err := s.App.Store.Put(ctx, "run", run.ID, run, "run.state"); err != nil {
		t.Fatal(err)
	}
	cursor, _ = s.App.Store.UILatest(ctx)
	waitProjection(t, s, ctx, cursor)
	state, err = s.readUIState(ctx, UIStateInput{Views: []string{"inbox", "messages"}})
	if err != nil || state["inbox"].([]domain.InboxEntry)[0].Work.Status != "Active" {
		t.Fatal("real run changes did not refresh summaries", state, err)
	}
}

func BenchmarkUIOutputPublication(b *testing.B) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(b.TempDir(), "state.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	a := &app.App{Store: db}
	if err := a.Init(ctx); err != nil {
		b.Fatal(err)
	}
	if err := db.Write(ctx, func(tx *store.Tx) error {
		for n := 0; n < 1000; n++ {
			id := fmt.Sprintf("fixture/repo#%04d", n)
			if err := tx.Put("item", id, domain.Item{ID: id, Kind: "issue", State: "open"}); err != nil {
				return err
			}
			if err := tx.Put("user", id, domain.UserState{ID: id}); err != nil {
				return err
			}
		}
		for n := 0; n < 3000; n++ {
			link := domain.Relationship{ID: fmt.Sprint(n), Source: fmt.Sprintf("fixture/repo#%04d", n%1000), Target: fmt.Sprintf("fixture/repo#%04d", (n+1)%1000)}
			if err := tx.Put("relationship", link.ID, link); err != nil {
				return err
			}
		}
		for n := 0; n < 100; n++ {
			run := domain.Run{ID: fmt.Sprint(n), ItemID: fmt.Sprintf("fixture/repo#%04d", n), Status: "idle"}
			if err := tx.Put("run", run.ID, run); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	s := &Server{App: a, ui: &uiState{changed: make(chan struct{})}}
	s.rebuildUIState(ctx)
	if s.ui.current == nil {
		b.Fatal("initial projection unavailable")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if err := db.Write(ctx, func(tx *store.Tx) error {
			return tx.Event("run.output", "0", map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"delta": "fixture output"}})
		}); err != nil {
			b.Fatal(err)
		}
		s.rebuildUIState(ctx)
		if s.ui.err != nil {
			b.Fatal(s.ui.err)
		}
	}
}
