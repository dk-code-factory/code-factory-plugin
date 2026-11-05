package transport

import (
	"codefactory/internal/app"
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

type factoryEventsInput struct {
	Empty
	Kind   string `json:"kind,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// Bound every string by encoded bytes, including non-ASCII metadata.
func archiveText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func factoryEvents(ctx context.Context, a *app.App, in factoryEventsInput) (any, error) {
	if in.Kind == "" {
		in.Kind = "events"
	}
	if in.Kind != "events" && in.Kind != "deliveries" && in.Kind != "batches" && in.Kind != "rules" {
		return nil, fmt.Errorf("unknown archive kind")
	}
	if in.Offset < 0 {
		return nil, fmt.Errorf("offset must be nonnegative")
	}
	if in.Limit <= 0 {
		in.Limit = 50
	}
	if in.Limit > 200 {
		in.Limit = 200
	}
	records, total, err := a.Store.FactoryArchive(ctx, in.Kind, in.Offset, in.Limit)
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for _, b := range records {
		var row map[string]any
		if err = json.Unmarshal(b, &row); err != nil {
			return nil, err
		}
		for key, v := range row {
			if text, ok := v.(string); ok {
				row[key] = archiveText(text, 512)
			}
			if values, ok := v.([]any); ok {
				for i, value := range values {
					if text, ok := value.(string); ok {
						values[i] = archiveText(text, 128)
					}
				}
			}
		}
		if text, ok := row["previewText"].(string); ok {
			n, _ := row["previewBytes"].(float64)
			text = archiveText(text, 512)
			row["preview"] = map[string]any{"text": text, "originalBytes": n, "truncated": int(n) > len(text)}
		}
		delete(row, "previewText")
		delete(row, "previewBytes")
		rows = append(rows, row)
	}
	return map[string]any{"kind": in.Kind, "offset": in.Offset, "limit": in.Limit, "total": total, "rows": rows}, nil
}
