package store

import (
	"codefactory/internal/domain"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProgressProjectionBackfillAndPrivateRunProjection(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := domain.Run{ID: "run", ItemID: "o/r#1", Prompt: "inspectable", SourceSnapshot: &domain.Item{Body: "private source"}, Skills: []domain.Skill{{ID: "selected", Content: "private content", Files: map[string]string{"SKILL.md": "private files"}}}}
	if err = s.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	emit := func(method string, params any) {
		t.Helper()
		b, _ := json.Marshal(params)
		if err := s.Write(ctx, func(tx *Tx) error {
			return tx.Event("run.output", r.ID, map[string]any{"method": method, "params": json.RawMessage(b)})
		}); err != nil {
			t.Fatal(err)
		}
	}
	emit("item/commandExecution/outputDelta", map[string]any{"delta": "do not summarize commands"})
	progress, err := s.Progress(ctx, r.ID)
	if err != nil || progress.State != "unavailable" {
		t.Fatal(progress, err)
	}
	emit("item/agentMessage/delta", map[string]string{"itemId": "a", "turnId": "t", "delta": "Running checks"})
	emit("item/completed", map[string]any{"turnId": "t", "item": map[string]string{"id": "a", "type": "agentMessage", "text": strings.Repeat("✓", 1000)}})
	emit("turn/completed", map[string]any{"turn": map[string]string{"id": "t", "status": "completed"}})
	final, err := s.Progress(ctx, r.ID)
	if err != nil || final.Kind != "final" || !final.Truncated || !utf8.ValidString(final.Text) || len(final.Text) > ExcerptLimit || final.Cursor == 0 {
		t.Fatal(final, err)
	}
	if err = s.Write(ctx, func(tx *Tx) error { return tx.Delete("run-progress", r.ID) }); err != nil {
		t.Fatal(err)
	}
	if err = s.BackfillProgress(ctx); err != nil {
		t.Fatal(err)
	}
	restored, _ := s.Progress(ctx, r.ID)
	if restored != final {
		t.Fatal("backfill did not reproduce projection", restored, final)
	}
	emit("item/agentMessage/delta", map[string]string{"itemId": "next", "turnId": "new", "delta": "New turn"})
	if err = s.BackfillProgress(ctx); err != nil {
		t.Fatal(err)
	}
	latest, _ := s.Progress(ctx, r.ID)
	if latest.Text != "New turn" || latest.TurnID != "new" || latest.Kind != "progress" {
		t.Fatal("old history overwrote new progress", latest)
	}
	runs, err := Runs(ctx, s, "", r.ID)
	if err != nil || len(runs) != 1 || runs[0].Prompt != "inspectable" || runs[0].Skills[0].ID != "selected" || runs[0].SourceSnapshot != nil || runs[0].Skills[0].Content != "" || runs[0].Skills[0].Files != nil {
		t.Fatal(runs, err)
	}
	var original domain.Run
	if err = s.Get(ctx, "run", r.ID, &original); err != nil || original.SourceSnapshot == nil || original.Skills[0].Files == nil {
		t.Fatal("projection mutated saved snapshot", original, err)
	}
}

func TestProgressBackfillReassemblesChunkedFinalWithoutRequestHistoryScan(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Put(ctx, "run", "large", domain.Run{ID: "large"}, "")
	payload, _ := json.Marshal(map[string]any{"turnId": "t", "item": map[string]string{"type": "agentMessage", "phase": "final_answer", "text": strings.Repeat("large final output ", 4000)}})
	if err = s.Write(ctx, func(tx *Tx) error {
		for len(payload) > 0 {
			n := min(len(payload), 32<<10)
			if err := tx.Event("run.output", "large", map[string]any{"method": "item/completed/chunk", "params": map[string]string{"delta": string(payload[:n])}}); err != nil {
				return err
			}
			payload = payload[n:]
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	initial, _ := s.Progress(ctx, "large")
	if initial.State != "unavailable" {
		t.Fatal("partial chunk became output")
	}
	if err = s.BackfillProgress(ctx); err != nil {
		t.Fatal(err)
	}
	final, _ := s.Progress(ctx, "large")
	if final.Kind != "final" || !final.Truncated || !strings.HasPrefix(final.Text, "large final output ") {
		t.Fatal(final)
	}
}
