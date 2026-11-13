package store

import (
	"codefactory/internal/domain"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
)

var ErrNotFound = errors.New("record not found")

// ApplicationFile isolates the clean session-wrapper state from retired application data.
const ApplicationFile = "factory-v1.db"

type Store struct {
	db        *sql.DB
	view      *sql.Tx
	ctx       context.Context
	cancel    context.CancelFunc
	changed   chan struct{}
	mu        sync.Mutex
	path      string
	revisions atomic.Pointer[revisionState]
}
type Tx struct {
	tx           *sql.Tx
	ctx          context.Context
	changes      map[string]map[string]bool
	revisionKeys map[string]bool
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
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: absolute, RawQuery: url.Values{"_pragma": []string{"busy_timeout(5000)", "foreign_keys(1)"}}.Encode()}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	// WAL readers must not queue behind an uncommitted synchronization write.
	// Store.Write still serializes writers; DSN pragmas apply to every connection.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", `CREATE TABLE IF NOT EXISTS migrations(version INTEGER PRIMARY KEY)`, `CREATE TABLE IF NOT EXISTS entities(kind TEXT NOT NULL,id TEXT NOT NULL,data BLOB NOT NULL,PRIMARY KEY(kind,id))`, `CREATE TABLE IF NOT EXISTS events(seq INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT NOT NULL,entity TEXT NOT NULL,data BLOB NOT NULL,at TEXT NOT NULL)`, `INSERT OR IGNORE INTO migrations VALUES(1)`,
		`CREATE INDEX IF NOT EXISTS entities_pr_head ON entities(json_extract(data,'$.repository'),json_extract(data,'$.kind'),json_extract(data,'$.head')) WHERE kind='item'`,
		`CREATE INDEX IF NOT EXISTS entities_item ON entities(kind,json_extract(data,'$.itemId'))`,
		`CREATE INDEX IF NOT EXISTS entities_run ON entities(kind,json_extract(data,'$.runId'))`,
		`CREATE INDEX IF NOT EXISTS entities_state ON entities(kind,json_extract(data,'$.state'))`,
		`CREATE INDEX IF NOT EXISTS entities_status ON entities(kind,json_extract(data,'$.status'))`,
		`CREATE INDEX IF NOT EXISTS entities_session ON entities(kind,json_extract(data,'$.sessionId'))`,
		`CREATE INDEX IF NOT EXISTS entities_source ON entities(kind,json_extract(data,'$.source'))`,
		`CREATE INDEX IF NOT EXISTS entities_target ON entities(kind,json_extract(data,'$.target'))`,
		`CREATE INDEX IF NOT EXISTS events_run_output ON events(entity,kind,seq)`,
		`CREATE TABLE IF NOT EXISTS ui_changes(seq INTEGER PRIMARY KEY AUTOINCREMENT,data BLOB NOT NULL)`} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Store{db: db, path: absolute, ctx: ctx, cancel: cancel, changed: make(chan struct{}, 1)}, nil
}
func (s *Store) Close() error { s.cancel(); return s.db.Close() }

// ProcessLock coordinates external effects across connections and processes.
// No database transaction remains open while the caller holds the lock. Closing
// its descriptor, including on process exit, releases ownership automatically.
func (s *Store) ProcessLock(ctx context.Context, name string) (func(), error) {
	if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return nil, fmt.Errorf("invalid process lock name")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.path+".lock-"+name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			var once sync.Once
			return func() { once.Do(func() { _ = f.Close() }) }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
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
	return decode(s.reader().QueryRowContext(ctx, "SELECT data FROM entities WHERE kind=? AND id=?", kind, id), out)
}
func (t *Tx) Get(kind, id string, out any) error {
	return decode(t.tx.QueryRowContext(t.ctx, "SELECT data FROM entities WHERE kind=? AND id=?", kind, id), out)
}
func (s *Store) List(ctx context.Context, kind string) ([]json.RawMessage, error) {
	return s.list(ctx, kind, "data", nil)
}

// ListWithout removes large fields inside SQLite before transferring/decoding
// summary records. Run results are compacted even without additional omissions.
// Paths are bound parameters, never SQL fragments.
func ListWithout[T any](ctx context.Context, s *Store, kind string, paths ...string) ([]T, error) {
	expression, args := projection(kind, paths)
	rows, err := s.list(ctx, kind, expression, args)
	if err != nil {
		return nil, err
	}
	return decodeList[T](rows)
}

func (s *Store) list(ctx context.Context, kind, expression string, args []any) ([]json.RawMessage, error) {
	rows, e := s.reader().QueryContext(ctx, "SELECT "+expression+" FROM entities WHERE kind=? ORDER BY id", append(args, kind)...)
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
	rows, err := s.reader().QueryContext(ctx, "SELECT id,data FROM entities WHERE kind=?", kind)
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

// JSONByteSizes counts JSON-encoded byte slices without transferring or decoding
// their base64 values. Missing/null fields count as zero; storage keys own identity.
func (s *Store) JSONByteSizes(ctx context.Context, kind, path string) (map[string]int64, error) {
	rows, err := s.reader().QueryContext(ctx, "SELECT id,coalesce(length(rtrim(json_extract(data, ?), '='))*3/4,0) FROM entities WHERE kind=?", path, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var size int64
		if err := rows.Scan(&id, &size); err != nil {
			return nil, err
		}
		out[id] = size
	}
	return out, rows.Err()
}

// JSONByteEntry keeps a row's storage identity and byte size beside explicitly
// selected metadata. Unselected fields never leave SQLite.
type JSONByteEntry struct {
	ID       string
	Size     int64
	Metadata json.RawMessage
}

// JSONByteEntries inventories byte fields inside the same transaction that
// will replace or evict rows. Metadata selectors are appended to "$." as JSON
// paths and retained as output member names; dots and brackets traverse nested
// values, not literal names. An empty selection returns {}. Bucket, path and
// metadata selectors are bound parameters.
func (t *Tx) JSONByteEntries(kind, path string, fields ...string) ([]JSONByteEntry, error) {
	metadata := make([]string, 0, len(fields))
	args := []any{path}
	for _, field := range fields {
		metadata = append(metadata, "?,json_extract(data, ?)")
		args = append(args, field, "$."+field)
	}
	args = append(args, kind)
	query := "SELECT id,coalesce(length(rtrim(json_extract(data, ?), '='))*3/4,0),json_object(" + strings.Join(metadata, ",") + ") FROM entities WHERE kind=?"
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JSONByteEntry{}
	for rows.Next() {
		var entry JSONByteEntry
		var metadata []byte
		if err := rows.Scan(&entry.ID, &entry.Size, &metadata); err != nil {
			return nil, err
		}
		entry.Metadata = metadata
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (t *Tx) Put(kind, id string, v any) error {
	t.touch(kind, id)
	t.invalidate(kind, id)
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	t.touchRelationships(kind, id, b)
	_, e = t.tx.ExecContext(t.ctx, `INSERT INTO entities VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`, kind, id, b)
	if e != nil {
		return e
	}
	return t.sessionRuntime(kind, id, b)
}

// Small derived runtime fields avoid decoding retained queues for streaming,
// scheduling and safe-boundary checks. They share the canonical transaction.
func (t *Tx) sessionRuntime(kind, id string, data []byte) error {
	if kind != "factory-session" {
		return nil
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO entities(kind,id,data) VALUES('factory-session-runtime',?1,json_object('repository',json_extract(?2,'$.repository'),'kind',json_extract(?2,'$.kind'),'epoch',json_extract(?2,'$.epoch'),'threadId',json_extract(?2,'$.threadId'),'execution',json_extract(?2,'$.execution'),'hold',json(CASE WHEN json_extract(?2,'$.hold') THEN 'true' ELSE 'false' END),'reported',json_extract(?2,'$.reported'),'due',json_extract(?2,'$.due'),'pendingCount',coalesce(json_array_length(?2,'$.pending'),0))) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`, id, data)
	return err
}
func (t *Tx) Delete(kind, id string) error {
	t.touch(kind, id)
	t.touchRelationships(kind, id, nil)
	t.invalidate(kind, id)
	_, e := t.tx.ExecContext(t.ctx, "DELETE FROM entities WHERE kind=? AND id=?", kind, id)
	if e == nil && kind == "factory-session" {
		_, e = t.tx.ExecContext(t.ctx, "DELETE FROM entities WHERE kind='factory-session-runtime' AND id=?", id)
	}
	return e
}
func (t *Tx) Event(kind, id string, v any) error {
	t.touch("event:"+kind, id)
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if kind == "session.output" && len(b) > SessionOutputMaxBytes {
		var nonce [16]byte
		if _, e = rand.Read(nonce[:]); e != nil {
			return e
		}
		// Serialized JSON contains no raw control characters. Encoding each
		// fragment as a JSON string uses at most 2n + 2 bytes, including quotes.
		ends := []int{0}
		for start := 0; start < len(b); {
			end := min(start+15*1024, len(b))
			for end < len(b) && !utf8.RuneStart(b[end]) {
				end--
			}
			ends = append(ends, end)
			start = end
		}
		for index := 0; index < len(ends)-1; index++ {
			if e = t.Event(kind, id, map[string]any{"fragmentId": hex.EncodeToString(nonce[:]), "fragmentIndex": index, "fragmentCount": len(ends) - 1, "fragment": string(b[ends[index]:ends[index+1]])}); e != nil {
				return e
			}
		}
		return nil
	}
	at := time.Now().UTC()
	_, e = t.tx.ExecContext(t.ctx, "INSERT INTO events(kind,entity,data,at) VALUES(?,?,?,?)", kind, id, b, at.Format(time.RFC3339Nano))
	if e != nil {
		return e
	}
	return nil
}

// SessionOutputMaxBytes bounds each retained transcript row before pagination.
const SessionOutputMaxBytes = 32 * 1024

func (s *Store) Write(ctx context.Context, f func(*Tx) error) error {
	if s.view != nil {
		return fmt.Errorf("cannot write through a read view")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	t := &Tx{tx: tx, ctx: ctx}
	if e = f(t); e != nil {
		return e
	}
	if len(t.changes) > 0 {
		b, err := json.Marshal(t.changes)
		if err != nil {
			return err
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO ui_changes(data) VALUES(?)", b); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.publishRevisions(t.revisionKeys)
	s.signal()
	return nil
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
	rows, e := s.reader().QueryContext(ctx, "SELECT seq,kind,entity,data,at FROM events WHERE seq>? ORDER BY seq LIMIT ?", after, limit)
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
	_ = s.reader().QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM events").Scan(&n)
	return n
}

func (s *Store) RunEvents(ctx context.Context, id string, after int64, limit int) (map[string]any, error) {
	return s.entityEvents(ctx, id, after, limit, "run.output")
}

func (s *Store) SessionEvents(ctx context.Context, id string, after int64, limit int) (map[string]any, error) {
	return s.entityEvents(ctx, id, after, limit, "session.output")
}

func (s *Store) entityEvents(ctx context.Context, id string, after int64, limit int, kind string) (map[string]any, error) {
	if after < 0 {
		return nil, fmt.Errorf("invalid cursor")
	}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, e := s.reader().QueryContext(ctx, "SELECT seq,kind,entity,data,at FROM events WHERE entity=? AND kind=? AND seq>? ORDER BY seq LIMIT ?", id, kind, after, limit+1)
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

// CompareAndSwap replaces exactly the entity bytes previously read by the caller.
// A nil previous value requires absence. One SQLite statement both acquires the
// writer lock and checks the value, avoiding deferred read-transaction upgrades
// when separate processes reconcile the same private entity.
func (s *Store) CompareAndSwap(ctx context.Context, kind, id string, previous json.RawMessage, value any) (bool, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	var changed bool
	err = s.Write(ctx, func(t *Tx) error {
		var result sql.Result
		var err error
		if previous == nil {
			result, err = t.tx.ExecContext(ctx, "INSERT OR IGNORE INTO entities(kind,id,data) VALUES(?,?,?)", kind, id, b)
		} else {
			result, err = t.tx.ExecContext(ctx, "UPDATE entities SET data=? WHERE kind=? AND id=? AND data=?", b, kind, id, []byte(previous))
		}
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		changed = n == 1
		if changed {
			t.touch(kind, id)
			if kind == "relationship" {
				// The successful predicate proves these are the replaced bytes.
				t.touchRelationshipEndpoints(previous)
				t.touchRelationshipEndpoints(b)
			}
			t.invalidate(kind, id)
			if err == nil {
				err = t.sessionRuntime(kind, id, b)
			}
		}
		return err
	})
	return changed, err
}
