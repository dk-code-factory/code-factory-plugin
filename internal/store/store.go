package store

import (
	"codefactory/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("record not found")

type Store struct {
	db *sql.DB
	mu sync.Mutex
}
type Tx struct {
	tx  *sql.Tx
	ctx context.Context
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", `CREATE TABLE IF NOT EXISTS migrations(version INTEGER PRIMARY KEY)`, `CREATE TABLE IF NOT EXISTS entities(kind TEXT NOT NULL,id TEXT NOT NULL,data BLOB NOT NULL,PRIMARY KEY(kind,id))`, `CREATE TABLE IF NOT EXISTS events(seq INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT NOT NULL,entity TEXT NOT NULL,data BLOB NOT NULL,at TEXT NOT NULL)`, `INSERT OR IGNORE INTO migrations VALUES(1)`} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func decode(row *sql.Row, out any) error {
	var b []byte
	if err := row.Scan(&b); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return json.Unmarshal(b, out)
}
func (s *Store) Get(ctx context.Context, kind, id string, out any) error {
	return decode(s.db.QueryRowContext(ctx, "SELECT data FROM entities WHERE kind=? AND id=?", kind, id), out)
}
func (t *Tx) Get(kind, id string, out any) error {
	return decode(t.tx.QueryRowContext(t.ctx, "SELECT data FROM entities WHERE kind=? AND id=?", kind, id), out)
}
func (s *Store) List(ctx context.Context, kind string) ([]json.RawMessage, error) {
	return s.list(ctx, kind, "data", nil)
}

// ListWithout removes large fields inside SQLite before transferring/decoding
// summary records. Paths are bound parameters, never SQL fragments.
func ListWithout[T any](ctx context.Context, s *Store, kind string, paths ...string) ([]T, error) {
	if len(paths) == 0 {
		return List[T](ctx, s, kind)
	}
	args := make([]any, len(paths))
	for i, path := range paths {
		args[i] = path
	}
	rows, err := s.list(ctx, kind, "json_remove(data,"+strings.TrimSuffix(strings.Repeat("?,", len(paths)), ",")+")", args)
	if err != nil {
		return nil, err
	}
	return decodeList[T](rows)
}

func (s *Store) list(ctx context.Context, kind, expression string, args []any) ([]json.RawMessage, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT "+expression+" FROM entities WHERE kind=? ORDER BY id", append(args, kind)...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}
func List[T any](ctx context.Context, s *Store, kind string) ([]T, error) {
	rows, e := s.List(ctx, kind)
	if e != nil {
		return nil, e
	}
	return decodeList[T](rows)
}

func decodeList[T any](rows []json.RawMessage) ([]T, error) {
	out := make([]T, 0, len(rows))
	for _, b := range rows {
		var v T
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Records reads a bucket once and keeps the database key authoritative, even for
// older records whose embedded ID is missing. Decode only the records needed.
func (s *Store) Records(ctx context.Context, kind string) (map[string]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,data FROM entities WHERE kind=?", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		out[id] = data
	}
	return out, rows.Err()
}
func (t *Tx) Put(kind, id string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = t.tx.ExecContext(t.ctx, `INSERT INTO entities VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`, kind, id, b)
	return e
}
func (t *Tx) Delete(kind, id string) error {
	_, e := t.tx.ExecContext(t.ctx, "DELETE FROM entities WHERE kind=? AND id=?", kind, id)
	return e
}
func (t *Tx) Event(kind, id string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = t.tx.ExecContext(t.ctx, "INSERT INTO events(kind,entity,data,at) VALUES(?,?,?,?)", kind, id, b, time.Now().UTC().Format(time.RFC3339Nano))
	return e
}
func (s *Store) Write(ctx context.Context, f func(*Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = f(&Tx{tx: tx, ctx: ctx}); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Put(ctx context.Context, kind, id string, v any, event string) error {
	return s.Write(ctx, func(t *Tx) error {
		if e := t.Put(kind, id, v); e != nil {
			return e
		}
		if event != "" {
			return t.Event(event, id, v)
		}
		return nil
	})
}
func (s *Store) Events(ctx context.Context, after int64, limit int) ([]domain.Event, error) {
	if after < 0 {
		return nil, fmt.Errorf("invalid cursor")
	}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, e := s.db.QueryContext(ctx, "SELECT seq,kind,entity,data,at FROM events WHERE seq>? ORDER BY seq LIMIT ?", after, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var v domain.Event
		var at string
		if e = rows.Scan(&v.Seq, &v.Kind, &v.Entity, &v.Data, &at); e != nil {
			return nil, e
		}
		v.At, e = time.Parse(time.RFC3339Nano, at)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Latest(ctx context.Context) int64 {
	var n int64
	_ = s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM events").Scan(&n)
	return n
}

func (s *Store) RunEvents(ctx context.Context, id string, after int64, limit int) (map[string]any, error) {
	if after < 0 {
		return nil, fmt.Errorf("invalid cursor")
	}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, e := s.db.QueryContext(ctx, "SELECT seq,kind,entity,data,at FROM events WHERE entity=? AND kind='run.output' AND seq>? ORDER BY seq LIMIT ?", id, after, limit+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Event{}
	next := after
	more := false
	for rows.Next() {
		if len(out) == limit {
			more = true
			break
		}
		var v domain.Event
		var at string
		if e = rows.Scan(&v.Seq, &v.Kind, &v.Entity, &v.Data, &at); e != nil {
			return nil, e
		}
		v.At, e = time.Parse(time.RFC3339Nano, at)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
		next = v.Seq
	}
	return map[string]any{"events": out, "cursor": next, "hasMore": more}, rows.Err()
}
