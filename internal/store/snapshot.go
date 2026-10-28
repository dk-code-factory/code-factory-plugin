package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

type reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) reader() reader {
	if s.view != nil {
		return s.view
	}
	return s.db
}

// ReadView pins every read, including the journal cursor, to one SQLite WAL
// snapshot. The callback must not retain the view or attempt a write.
func (s *Store) ReadView(ctx context.Context, f func(*Store) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return f(&Store{db: s.db, view: tx})
}
func (s *Store) Context() context.Context { return s.ctx }
func (s *Store) Changes() <-chan struct{} { return s.changed }

// UI cursors deliberately do not change the legacy events cursor or payloads.
func (s *Store) UILatest(ctx context.Context) (int64, error) {
	var n int64
	err := s.reader().QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM ui_changes").Scan(&n)
	return n, err
}
func (s *Store) UIChanges(ctx context.Context, after, through int64) (map[string]map[string]bool, error) {
	rows, err := s.reader().QueryContext(ctx, "SELECT data FROM ui_changes WHERE seq>? AND seq<=? ORDER BY seq", after, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var changes map[string]map[string]bool
		if err := json.Unmarshal(b, &changes); err != nil {
			return nil, err
		}
		for kind, ids := range changes {
			if out[kind] == nil {
				out[kind] = map[string]bool{}
			}
			for id := range ids {
				out[kind][id] = true
			}
		}
	}
	return out, rows.Err()
}
func (s *Store) signal() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}
func (t *Tx) invalidate(kind, id string) {
	if kind == "observation" {
		// Observation storage keys include a phase; public invalidations name
		// the owning item so selected views can match affected IDs.
		id = strings.TrimPrefix(strings.TrimPrefix(id, "detail:"), "reviews:")
	}
	// Private buckets are never disclosed through the UI invalidation channel.
	switch kind {
	case "item", "user", "assessment", "ticket", "relationship", "linked-pr", "identity", "repository", "profile", "config", "run", "transcript", "attention", "question-recovery", "skill", "pr-reviews", "pr-review-policy", "observation", "issue-report", "plugin-inventory", "plugin-operation", "github-usage", "github-notifications":
	default:
		return
	}
	if t.changes == nil {
		t.changes = map[string]map[string]bool{}
	}
	if t.changes[kind] == nil {
		t.changes[kind] = map[string]bool{}
	}
	t.changes[kind][id] = true
}
