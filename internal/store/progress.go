package store

import (
	"codefactory/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const ExcerptLimit = 2048

type Progress struct {
	State     string    `json:"state"`
	Kind      string    `json:"kind,omitempty"`
	Text      string    `json:"text,omitempty"`
	At        time.Time `json:"at"`
	TurnID    string    `json:"turnId,omitempty"`
	ItemID    string    `json:"itemId,omitempty"`
	Cursor    int64     `json:"cursor"`
	Truncated bool      `json:"truncated"`
	Boundary  string    `json:"boundary,omitempty"`
}

func excerpt(s string) (string, bool) {
	if len(s) <= ExcerptLimit {
		return s, false
	}
	n := ExcerptLimit
	for !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n], true
}
func (s *Store) Progress(ctx context.Context, id string) (Progress, error) {
	out := Progress{State: "unavailable"}
	err := s.Get(ctx, "run-progress", id, &out)
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	return out, err
}

// Projection receives only redacted agent-message output. Tool commands, user
// inputs, questions and reasoning are deliberately excluded from excerpts.
func (t *Tx) ProjectProgress(id, method string, params json.RawMessage, seq int64, at time.Time) error {
	var p struct {
		TurnID, ItemID, Delta string
		Item                  struct{ ID, Type, Text, Phase string }
		Turn                  struct{ ID, Status string }
	}
	if json.Unmarshal(params, &p) != nil {
		return nil
	}
	var prev Progress
	err := t.Get("run-progress", id, &prev)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if seq <= prev.Cursor {
		return nil
	}
	next := prev
	switch method {
	case "turn/started":
		if p.Turn.ID == "" {
			return nil
		}
		return t.Put("run-progress", id, Progress{State: "unavailable", TurnID: p.Turn.ID, Cursor: seq, At: at.UTC()})
	case "item/agentMessage/delta":
		if p.Delta == "" {
			return nil
		}
		next.Kind = "progress"
		if prev.ItemID != p.ItemID || prev.TurnID != p.TurnID {
			next.Text = ""
			next.Truncated = false
		}
		var cut bool
		next.Text, cut = excerpt(next.Text + p.Delta)
		next.Truncated = next.Truncated || cut
		next.ItemID = p.ItemID
		next.TurnID = p.TurnID
	case "item/completed":
		if p.Item.Type != "agentMessage" || strings.TrimSpace(p.Item.Text) == "" {
			return nil
		}
		next.Text, next.Truncated = excerpt(p.Item.Text)
		next.ItemID = p.Item.ID
		next.TurnID = p.TurnID
		if next.TurnID == "" {
			next.TurnID = prev.TurnID
		}
		next.Kind = "progress"
		if p.Item.Phase == "final_answer" {
			next.Kind = "final"
		}
	case "turn/completed":
		if prev.Text == "" || prev.TurnID != p.Turn.ID || p.Turn.Status != "completed" {
			return nil
		}
		next.Kind = "final"
	default:
		return nil
	}
	next.State = "ready"
	next.Cursor = seq
	next.At = at.UTC()
	next.Boundary = "start of agent message; up to 2048 UTF-8 bytes; read_transcript from cursor 0 for full retained output"
	return t.Put("run-progress", id, next)
}

// Backfill runs once at startup, outside any read request. The cursor guard
// prevents older history from replacing concurrent live projections.
func (s *Store) BackfillProgress(ctx context.Context) error {
	runs, err := ListWithout[struct{ ID string }](ctx, s, "run", "$.skills", "$.sourceSnapshot", "$.prompt", "$.profile", "$.reviewPromptSnapshot")
	if err != nil {
		return err
	}
	for _, run := range runs {
		progress, err := s.Progress(ctx, run.ID)
		if err != nil {
			return err
		}
		after := progress.Cursor
		var chunks strings.Builder
		for {
			page, err := s.RunEvents(ctx, run.ID, after, 200)
			if err != nil {
				return err
			}
			events := page["events"].([]domain.Event)
			if err = s.Write(ctx, func(tx *Tx) error {
				for _, event := range events {
					var body struct {
						Method string
						Params json.RawMessage
					}
					if json.Unmarshal(event.Data, &body) == nil {
						if body.Method == "item/completed/chunk" {
							var chunk struct{ Delta string }
							if json.Unmarshal(body.Params, &chunk) == nil {
								chunks.WriteString(chunk.Delta)
							}
							if !json.Valid([]byte(chunks.String())) {
								continue
							}
							body.Method = "item/completed"
							body.Params = json.RawMessage(chunks.String())
							chunks.Reset()
						} else {
							chunks.Reset()
						}
						if e := tx.ProjectProgress(run.ID, body.Method, body.Params, event.Seq, event.At); e != nil {
							return e
						}
					}
				}
				return nil
			}); err != nil {
				return err
			}
			after = page["cursor"].(int64)
			if !page["hasMore"].(bool) {
				break
			}
		}
	}
	return nil
}

// ProjectLatest preserves the original agent message even when its journal
// representation needed chunking. The continuation cursor names the last chunk.
func (t *Tx) ProjectLatest(id, method string, params json.RawMessage) error {
	var seq int64
	if err := t.tx.QueryRowContext(t.ctx, "SELECT COALESCE(MAX(seq),0) FROM events WHERE entity=? AND kind='run.output'", id).Scan(&seq); err != nil {
		return err
	}
	return t.ProjectProgress(id, method, params, seq, time.Now().UTC())
}
