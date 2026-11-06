package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codefactory/internal/domain"
)

func projection(kind string, paths []string) (string, []any) {
	base := "data"
	if len(paths) == 0 {
		return base, nil
	}
	args := make([]any, len(paths))
	for i, p := range paths {
		args[i] = p
	}
	return "json_remove(" + base + "," + strings.TrimSuffix(strings.Repeat("?,", len(paths)), ",") + ")", args
}

// Select filters in SQLite before decoding. The small field vocabulary keeps
// indexed expressions literal while all caller values remain bound parameters.
func Select[T any](ctx context.Context, s *Store, kind, field, value string, paths ...string) ([]T, error) {
	expression, args := projection(kind, paths)
	predicates := map[string]string{"id": "id=?", "itemId": "json_extract(data,'$.itemId')=?", "sessionId": "json_extract(data,'$.sessionId')=?", "state": "json_extract(data,'$.state')=?", "status": "json_extract(data,'$.status')=?", "runId": "json_extract(data,'$.runId')=?", "related": "(json_extract(data,'$.source')=? OR json_extract(data,'$.target')=?)"}
	predicate, ok := predicates[field]
	if !ok {
		return nil, fmt.Errorf("unsupported selection field")
	}
	args = append(args, kind, value)
	if field == "related" {
		args = append(args, value)
	}
	rows, err := s.reader().QueryContext(ctx, "SELECT "+expression+" FROM entities WHERE kind=? AND "+predicate+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var v T
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ReviewSummaries(ctx context.Context, id string) (domain.PRReviews, error) {
	var out domain.PRReviews
	err := decode(s.reader().QueryRowContext(ctx, `SELECT json_set(data,'$.reviews',json(COALESCE((SELECT json_group_array(json_set(json_remove(value,'$.body','$.comments'),'$.commentCount',COALESCE(json_array_length(value,'$.comments'),0))) FROM json_each(data,'$.reviews')),'[]'))) FROM entities WHERE kind='pr-reviews' AND id=?`, id), &out)
	return out, err
}

// RunnableSessions reads only small transactional runtime records. Held, busy,
// empty and not-yet-due queues never enter the scheduler's Go decoding path.
type RunnableSession struct {
	ID         string
	Repository string
}

func (s *Store) RunnableSessions(ctx context.Context, at time.Time) ([]RunnableSession, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT id,coalesce(json_extract(data,'$.repository'),'') FROM entities WHERE kind='factory-session-runtime' AND json_extract(data,'$.pendingCount')>0 AND coalesce(json_extract(data,'$.hold'),0)=0 AND coalesce(json_extract(data,'$.reported'),'')!='blocked' AND json_extract(data,'$.execution') NOT IN ('running','starting','waiting','cleaned') AND julianday(json_extract(data,'$.due'))<=julianday(?) ORDER BY julianday(json_extract(data,'$.due')),id`, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []RunnableSession{}
	for rows.Next() {
		var id RunnableSession
		if err = rows.Scan(&id.ID, &id.Repository); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) ActiveSession(ctx context.Context) (bool, error) {
	var active bool
	err := s.reader().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE kind='factory-session-runtime' AND json_extract(data,'$.execution') IN ('running','starting','waiting'))`).Scan(&active)
	return active, err
}

// DueRecords excludes retained completed payloads in SQLite. The indexed state
// predicate and bounded due page keep idle reconciliation independent of history.
func DueRecords[T any](ctx context.Context, s *Store, kind string, at time.Time, limit int) ([]T, error) {
	return DueRecordsPage[T](ctx, s, kind, at, limit, 0)
}

// DueRecordsPage lets an observer pass pending rows held by busy conversations.
// Its offset counts only rows that remain due after earlier pages are processed.
func DueRecordsPage[T any](ctx context.Context, s *Store, kind string, at time.Time, limit, offset int) ([]T, error) {
	if limit < 1 || limit > 200 || offset < 0 {
		return nil, fmt.Errorf("invalid due-record page size")
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT data FROM entities WHERE kind=? AND json_extract(data,'$.state')='pending' AND (COALESCE(json_extract(data,'$.retryAt'),'')='' OR julianday(json_extract(data,'$.retryAt'))<=julianday(?)) ORDER BY julianday(json_extract(data,'$.at')),id LIMIT ? OFFSET ?`, kind, at.UTC().Format(time.RFC3339Nano), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		var value T
		if err = json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// SearchIDs applies body-inclusive text matching before returning compact rows.
func (s *Store) SearchIDs(ctx context.Context, kind, search string) (map[string]bool, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT id FROM entities WHERE kind=? AND instr(lower(coalesce(json_extract(data,'$.id'),'')||' '||coalesce(json_extract(data,'$.title'),'')||' '||coalesce(json_extract(data,'$.body'),'')||' '||coalesce(json_extract(data,'$.author'),'')||' '||coalesce(json_extract(data,'$.labels'),'')),lower(?))>0`, kind, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Recent projects bounded history before decoding, including request metadata.
func Recent[T any](ctx context.Context, s *Store, kind string, limit int, paths ...string) ([]T, error) {
	if limit < 1 || limit > 200 {
		return nil, fmt.Errorf("invalid recent-record limit")
	}
	expression, args := projection(kind, paths)
	args = append(args, kind, limit)
	rows, err := s.reader().QueryContext(ctx, "SELECT "+expression+" FROM entities WHERE kind=? ORDER BY json_extract(data,'$.created') DESC,id DESC LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var value T
		if err = json.Unmarshal(b, &value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// SessionSummaries count pending events in SQLite and omit their source bodies.
func SessionSummaries[T any](ctx context.Context, s *Store) ([]T, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT json_remove(json_set(data,'$.pendingCount',coalesce(json_array_length(data,'$.pending'),0)),'$.pending') FROM entities WHERE kind='factory-session' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var value T
		if err = json.Unmarshal(b, &value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// PullRequestHeads discovers only candidate identities. CI bursts never decode
// issue bodies, discussion archives, checks, or unrelated repository items.
func (s *Store) PullRequestHeads(ctx context.Context, repository, head string) ([]domain.Item, error) {
	if head == "" {
		return []domain.Item{}, nil
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT id,json_extract(data,'$.number') FROM entities WHERE kind='item' AND json_extract(data,'$.repository')=? AND json_extract(data,'$.kind')='pr' AND json_extract(data,'$.head')=? ORDER BY id`, repository, head)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Item{}
	for rows.Next() {
		var i domain.Item
		if err = rows.Scan(&i.ID, &i.Number); err != nil {
			return nil, err
		}
		i.Repository, i.Kind, i.Head = repository, "pr", head
		out = append(out, i)
	}
	return out, rows.Err()
}

// FactoryArchive projects a bounded page in SQLite before Go decoding. Each
// scalar and array element is bounded here, so retained payload size cannot
// inflate transport buffers even when a row has a large body or instruction.
func (s *Store) FactoryArchive(ctx context.Context, kind string, offset, limit int) ([]json.RawMessage, int, error) {
	if offset < 0 || limit < 1 || limit > 200 {
		return nil, 0, fmt.Errorf("invalid archive page")
	}
	entity := map[string]string{"events": "factory-event", "deliveries": "factory-delivery", "batches": "factory-batch"}[kind]
	var from, predicate, expression, order string
	args := []any{}
	if kind == "rules" {
		from = "entities,json_each(entities.data,'$.rules') AS rule"
		predicate = "entities.kind='factory-settings' AND entities.id='effective'"
		order = "CAST(rule.key AS INTEGER)"
		// Each metadata array contains at most sixteen 128-character entries.
		array := func(path string) string {
			return "json(COALESCE((SELECT json_group_array(substr(value,1,128)) FROM (SELECT value FROM json_each(rule.value,'" + path + "') LIMIT 16)),'[]'))"
		}
		expression = "json_object('kind','rules','id',substr(json_extract(rule.value,'$.id'),1,512),'index',CAST(rule.key AS INTEGER),'enabled',json(CASE WHEN coalesce(json_extract(rule.value,'$.disabled'),0)=0 THEN 'true' ELSE 'false' END),'repositories'," + array("$.repositories") + ",'eventTypes'," + array("$.types") + ",'actions'," + array("$.actions") + ",'skills'," + array("$.skills") + ",'repositoriesCount',coalesce(json_array_length(rule.value,'$.repositories'),0),'eventTypesCount',coalesce(json_array_length(rule.value,'$.types'),0),'actionsCount',coalesce(json_array_length(rule.value,'$.actions'),0),'skillsCount',coalesce(json_array_length(rule.value,'$.skills'),0))"
	} else {
		if entity == "" {
			return nil, 0, fmt.Errorf("unknown archive kind")
		}
		from, predicate, order = "entities", "kind=?", "id DESC"
		args = append(args, entity)
		field := func(path string) string { return "substr(json_extract(data,'" + path + "'),1,512)" }
		preview := "coalesce(json_extract(data,'$.details'),json_extract(data,'$.value'),'')"
		if kind != "events" {
			preview = "''"
		}
		expression = "json_object('kind','" + kind + "','id',substr(id,1,512),'repository'," + field("$.repository") + ",'itemId'," + field("$.itemId") + ",'sessionId'," + field("$.sessionId") + ",'at'," + field("$.at") + ",'type'," + field("$.type") + ",'action'," + field("$.action") + ",'source'," + field("$.source") + ",'state'," + field("$.state") + ",'eventCount',coalesce(json_array_length(data,'$.events'),0),'actionCount',coalesce(json_array_length(data,'$.actions'),0),'previewText',substr(" + preview + ",1,512),'previewBytes',length(CAST(" + preview + " AS BLOB)))"
	}
	var total int
	if err := s.reader().QueryRowContext(ctx, "SELECT count(*) FROM "+from+" WHERE "+predicate, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.reader().QueryContext(ctx, "SELECT "+expression+" FROM "+from+" WHERE "+predicate+" ORDER BY "+order+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, 0, err
		}
		out = append(out, json.RawMessage(b))
	}
	return out, total, rows.Err()
}
