package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codefactory/internal/domain"
)

func projection(kind string, paths []string) (string, []any) {
	base := "data"
	if kind == "run" {
		base = compactRunSQL
	}
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
	predicates := map[string]string{"id": "id=?", "itemId": "json_extract(data,'$.itemId')=?", "runId": "json_extract(data,'$.runId')=?", "related": "(json_extract(data,'$.source')=? OR json_extract(data,'$.target')=?)"}
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

// Public run records retain the inspectable launch snapshot while excluding
// private source and skill file contents before any Go decoding or allocation.
// Legacy run rows can contain complete finding arrays. Count and remove them in
// SQLite before broad reads transfer or decode those private historical bodies.
const compactRunSQL = `CASE WHEN json_type(data,'$.reviewResult')='object' THEN json_set(json_remove(data,'$.reviewResult.findings'),'$.reviewResult.finding_count',CASE WHEN json_type(data,'$.reviewResult.finding_count')='integer' THEN json_extract(data,'$.reviewResult.finding_count') WHEN json_type(data,'$.reviewResult.findings')='array' THEN json_array_length(data,'$.reviewResult.findings') ELSE NULL END) ELSE data END`

const publicRunSQL = `json_set(json_remove((` + compactRunSQL + `),'$.sourceSnapshot'),'$.skills',json(COALESCE((SELECT json_group_array(json_remove(value,'$.files','$.content')) FROM json_each(data,'$.skills')),'[]')))`

func Runs(ctx context.Context, s *Store, itemID, id string) ([]domain.Run, error) {
	query := "SELECT " + publicRunSQL + " FROM entities WHERE kind='run'"
	args := []any{}
	if itemID != "" {
		query += " AND json_extract(data,'$.itemId')=?"
		args = append(args, itemID)
	}
	if id != "" {
		query += " AND id=?"
		args = append(args, id)
	}
	query += " ORDER BY id"
	rows, err := s.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Run{}
	for rows.Next() {
		var b []byte
		var r domain.Run
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReviewSummaries keeps receipt identities while excluding unbounded review
// bodies/comments from routine tracking before Go decoding.
func (s *Store) ReviewSummaries(ctx context.Context, id string) (domain.PRReviews, error) {
	var out domain.PRReviews
	err := decode(s.reader().QueryRowContext(ctx, `SELECT json_set(data,'$.reviews',json(COALESCE((SELECT json_group_array(json_set(json_remove(value,'$.body','$.comments'),'$.commentCount',COALESCE(json_array_length(value,'$.comments'),0))) FROM json_each(data,'$.reviews')),'[]'))) FROM entities WHERE kind='pr-reviews' AND id=?`, id), &out)
	return out, err
}
