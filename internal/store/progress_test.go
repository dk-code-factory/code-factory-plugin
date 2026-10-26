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
	text := "large final output ✓ escaped \" } [ \\ \n" + strings.Repeat("large final output ", 400000)
	payload, _ := json.Marshal(map[string]any{"turnId": "t", "item": map[string]string{"id": "large-message", "type": "agentMessage", "phase": "final_answer", "text": text}})
	if len(payload) <= 200*(32<<10) {
		t.Fatal("fixture must cross a complete event page")
	}
	if err = s.Write(ctx, func(tx *Tx) error {
		for len(payload) > 0 {
			n := min(len(payload), 32<<10)
			for n < len(payload) && n > 0 && (payload[n]&0xc0) == 0x80 {
				n--
			}
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
	want, _ := excerpt(text)
	if final.Kind != "final" || !final.Truncated || final.Text != want || !utf8.ValidString(final.Text) || final.Cursor != s.Latest(ctx) || final.ItemID != "large-message" {
		t.Fatal(final)
	}
	last, err := s.RunEvents(ctx, "large", final.Cursor-1, 1)
	if err != nil || !final.At.Equal(last["events"].([]domain.Event)[0].At) {
		t.Fatal("projection lost final chunk time", final.At, err)
	}
}

func TestCompletionChunksPreserveEscapesAndWaitForValidWholeObjects(t *testing.T) {
	payload := ` {"item":{"type":"agentMessage","text":"✓ escaped \" } [ \\ and {"},"nested":[{}]} `
	for split := 1; split < len(payload); split++ {
		if !utf8.ValidString(payload[:split]) {
			continue
		}
		var chunks completionChunks
		first := chunks.append(payload[:split])
		if first != nil {
			// Trailing whitespace may arrive after a complete object.
			if !json.Valid(first) || strings.TrimSpace(payload[split:]) != "" {
				t.Fatal("partial object became complete", split)
			}
			continue
		}
		if got := chunks.append(payload[split:]); string(got) != payload {
			t.Fatal("escaped object did not reconstruct", split, string(got))
		}
	}
	var chunks completionChunks
	if chunks.append(`{"item":]}`) != nil || chunks.append(`{"item":{}}`) != nil {
		t.Fatal("malformed prefix became valid without reset")
	}
	chunks = completionChunks{}
	for _, payload := range []string{`{"item":{"text":"first"}}`, `{"item":{"text":"second"}}`} {
		if got := chunks.append(payload); string(got) != payload {
			t.Fatal("adjacent objects merged", string(got))
		}
	}
}

func TestProgressBackfillDiscardsPartialAndMalformedChunksAtMethodBoundary(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Put(ctx, "run", "run", domain.Run{ID: "run"}, ""); err != nil {
		t.Fatal(err)
	}
	emit := func(method string, params any) {
		t.Helper()
		if err := s.Write(ctx, func(tx *Tx) error {
			return tx.Event("run.output", "run", map[string]any{"method": method, "params": params})
		}); err != nil {
			t.Fatal(err)
		}
	}
	emit("item/completed/chunk", map[string]string{"delta": `{"item":{"type":"agentMessage","text":"partial`})
	if err := s.BackfillProgress(ctx); err != nil {
		t.Fatal(err)
	}
	if progress, err := s.Progress(ctx, "run"); err != nil || progress.State != "unavailable" || progress.Cursor != 0 {
		t.Fatal("partial object became output", progress, err)
	}
	emit("ignored/method", map[string]string{})
	emit("item/completed/chunk", map[string]string{"delta": `{"item":]}`})
	emit("item/completed/chunk", map[string]string{"delta": `{"item":{"type":"agentMessage","text":"untrusted suffix"}}`})
	if err := s.BackfillProgress(ctx); err != nil {
		t.Fatal(err)
	}
	if progress, err := s.Progress(ctx, "run"); err != nil || progress.State != "unavailable" {
		t.Fatal("malformed object became output", progress, err)
	}
	emit("ignored/method", map[string]string{})
	for _, text := range []string{"first valid", "second valid"} {
		payload, _ := json.Marshal(map[string]any{"item": map[string]string{"type": "agentMessage", "phase": "final_answer", "text": text}})
		emit("item/completed/chunk", map[string]string{"delta": string(payload)})
	}
	if err := s.BackfillProgress(ctx); err != nil {
		t.Fatal(err)
	}
	progress, err := s.Progress(ctx, "run")
	if err != nil || progress.Text != "second valid" || progress.Kind != "final" || progress.Cursor != s.Latest(ctx) {
		t.Fatal("method reset or adjacent message lost", progress, err)
	}
	// A newer live projection wins even if an older completion was reconstructed
	// outside the writer transaction before the live event committed.
	emit("item/completed", map[string]any{"turnId": "new", "item": map[string]string{"id": "new", "type": "agentMessage", "text": "live projection"}})
	if err := s.Write(ctx, func(tx *Tx) error {
		older, _ := json.Marshal(map[string]any{"item": map[string]string{"type": "agentMessage", "text": "stale reconstructed output"}})
		return tx.ProjectProgress("run", "item/completed", older, progress.Cursor, progress.At)
	}); err != nil {
		t.Fatal(err)
	}
	if progress, err := s.Progress(ctx, "run"); err != nil || progress.Text != "live projection" || progress.TurnID != "new" {
		t.Fatal("stale backfill overwrote newer live cursor", progress, err)
	}
}
