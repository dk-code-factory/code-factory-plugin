package store

import (
	"codefactory/internal/domain"
	"codefactory/internal/prreview"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Observe the bytes delivered by the real store facade before domain decoding
// can ignore a legacy field and make an oversized SQL projection look compact.
type runProjectionProbe struct {
	domain.Run
	transferred int
}

func (p *runProjectionProbe) UnmarshalJSON(b []byte) error {
	if strings.Contains(string(b), `"findings"`) || strings.Contains(string(b), "private-finding-prose") {
		return fmt.Errorf("SQL transferred legacy finding bodies (%d bytes)", len(b))
	}
	p.transferred = len(b)
	return json.Unmarshal(b, &p.Run)
}

func TestRunProjectionCountsLegacyFindingsBeforeDecoding(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	findings := make([]prreview.AggregateFinding, 20)
	for n := range findings {
		findings[n].Message = "private-finding-prose" + strings.Repeat("x", 2048)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		for n := 0; n < 100; n++ {
			id := fmt.Sprintf("run-%03d", n)
			if err := tx.Put("run", id, map[string]any{"id": id, "itemId": "o/r#1", "status": "idle", "reviewResult": prreview.Result{Complete: true, Findings: findings, Skipped: []prreview.ReviewerReason{{Reviewer: "limited", Reason: "usage exhausted"}}}}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Inspect the actual SQL bytes, before domain decoding could discard fields.
	var projected []byte
	if err := s.reader().QueryRowContext(ctx, "SELECT "+publicRunSQL+" FROM entities WHERE kind='run' LIMIT 1").Scan(&projected); err != nil || len(projected) > 2048 || strings.Contains(string(projected), "private-finding-prose") || strings.Contains(string(projected), `"findings"`) {
		t.Fatal("SQL transferred finding bodies", len(projected), err)
	}
	for name, read := range map[string]func() ([]runProjectionProbe, error){
		"inbox and queue run projection": func() ([]runProjectionProbe, error) {
			return ListWithout[runProjectionProbe](ctx, s, "run", "$.skills", "$.profile", "$.prompt", "$.reviewPromptSnapshot", "$.sourceSnapshot")
		},
		"run projection without omissions": func() ([]runProjectionProbe, error) {
			return ListWithout[runProjectionProbe](ctx, s, "run")
		},
		"selected run projection": func() ([]runProjectionProbe, error) {
			return Select[runProjectionProbe](ctx, s, "run", "itemId", "o/r#1")
		},
	} {
		t.Run(name, func(t *testing.T) {
			runs, err := read()
			if err != nil || len(runs) != 100 {
				t.Fatal("SQL run projection transferred legacy bodies", len(runs), err)
			}
			transferred := 0
			for _, run := range runs {
				transferred += run.transferred
				if run.Status != "idle" || run.ReviewResult == nil || run.ReviewResult.FindingCount == nil || *run.ReviewResult.FindingCount != 20 || len(run.ReviewResult.Skipped) != 1 {
					t.Fatal("projection lost run status or review accounting", run)
				}
			}
			if transferred > 256<<10 {
				t.Fatal("broad SQL transfer was unbounded", transferred)
			}
		})
	}
	for _, read := range []func() ([]domain.Run, error){
		func() ([]domain.Run, error) { return Runs(ctx, s, "o/r#1", "") },
		func() ([]domain.Run, error) { return Select[domain.Run](ctx, s, "run", "itemId", "o/r#1") },
	} {
		runs, err := read()
		if err != nil || len(runs) != 100 {
			t.Fatal(len(runs), err)
		}
		for _, run := range runs {
			if run.ReviewResult == nil || run.ReviewResult.FindingCount == nil || *run.ReviewResult.FindingCount != 20 || len(run.ReviewResult.Skipped) != 1 {
				t.Fatal("compact review accounting changed", run)
			}
		}
		data, _ := json.Marshal(runs)
		if len(data) > 256<<10 || strings.Contains(string(data), "private-finding-prose") {
			t.Fatal("historical review projection is unbounded", len(data))
		}
	}
	for _, check := range []struct {
		id     string
		result any
		known  bool
	}{
		{"clean-legacy", map[string]any{"complete": true, "findings": []any{}}, true},
		{"clean-current", (prreview.Result{Complete: true}).Summary(), true},
		{"unknown", map[string]any{"complete": true}, false},
	} {
		if err := s.Put(ctx, "run", check.id, map[string]any{"id": check.id, "reviewResult": check.result}, ""); err != nil {
			t.Fatal(err)
		}
		runs, err := Runs(ctx, s, "", check.id)
		if err != nil || len(runs) != 1 || (runs[0].ReviewResult.FindingCount != nil) != check.known || (check.known && *runs[0].ReviewResult.FindingCount != 0) {
			t.Fatal("unknown findings falsely became clean", check.id, runs, err)
		}
	}
	projectedRuns, err := ListWithout[runProjectionProbe](ctx, s, "run", "$.skills", "$.profile", "$.prompt", "$.reviewPromptSnapshot", "$.sourceSnapshot")
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range projectedRuns {
		switch run.ID {
		case "unknown":
			if run.ReviewResult.FindingCount != nil {
				t.Fatal("broad projection falsely assigned a known count", run)
			}
		case "clean-legacy", "clean-current":
			if run.ReviewResult.FindingCount == nil || *run.ReviewResult.FindingCount != 0 {
				t.Fatal("broad projection lost known zero count", run)
			}
		}
	}
	var original map[string]any
	if err := s.Get(ctx, "run", "run-000", &original); err != nil || len(original["reviewResult"].(map[string]any)["findings"].([]any)) != 20 {
		t.Fatal("projection changed private historical data", err)
	}
}

func TestListWithoutKeepsOtherKindsAndPrivateReadsUnchanged(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	row := json.RawMessage(`{"id":"same","body":"omit","reviewResult":{"findings":[{"message":"private-finding-prose"}]}}`)
	for _, kind := range []string{"run", "item", "linked-pr"} {
		if err := s.Put(ctx, kind, "same", row, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"item", "linked-pr"} {
		rows, err := ListWithout[json.RawMessage](ctx, s, kind, "$.body")
		if err != nil || len(rows) != 1 || !strings.Contains(string(rows[0]), "private-finding-prose") || strings.Contains(string(rows[0]), `"body"`) || strings.Contains(string(rows[0]), "finding_count") {
			t.Fatal("run compaction changed another entity kind", kind, rows, err)
		}
	}
	rows, err := s.List(ctx, "run")
	if err != nil || len(rows) != 1 || !strings.Contains(string(rows[0]), "private-finding-prose") {
		t.Fatal("private operational read lost original result", rows, err)
	}
}

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

func TestTurnFinalTextRestoresFullOwningResponseAcrossPages(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "started-boundary", true: "explicit-turn"}[explicit], func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			emit := func(run, method string, params any) {
				t.Helper()
				if err := s.Write(ctx, func(tx *Tx) error {
					return tx.Event("run.output", run, map[string]any{"method": method, "params": params})
				}); err != nil {
					t.Fatal(err)
				}
			}
			emit("other", "item/completed", map[string]any{"turnId": "target", "item": map[string]string{"type": "agentMessage", "text": "Other run question?"}})
			emit("run", "turn/started", map[string]any{"turn": map[string]string{"id": "older"}})
			emit("run", "item/completed", map[string]any{"item": map[string]string{"type": "agentMessage", "text": "Older question?"}})
			emit("run", "turn/started", map[string]any{"turn": map[string]string{"id": "target"}})
			text := strings.Repeat("Saved \"quoted\" facts ✓ ", 700) + "Which target?"
			params := map[string]any{"item": map[string]string{"type": "agentMessage", "text": text}}
			if explicit {
				params["turnId"] = "target"
			}
			payload, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			chunks := 0
			for len(payload) > 0 {
				n := min(31, len(payload))
				for n < len(payload) && !utf8.RuneStart(payload[n]) {
					n--
				}
				emit("run", "item/completed/chunk", map[string]string{"delta": string(payload[:n])})
				payload = payload[n:]
				chunks++
			}
			if chunks <= 200 {
				t.Fatal("fixture did not cross an event page")
			}
			for _, kind := range []string{"userMessage", "commandExecution"} {
				emit("run", "item/completed", map[string]any{"turnId": "target", "item": map[string]string{"type": kind, "text": "Unrelated question?"}})
			}
			emit("run", "turn/started", map[string]any{"turn": map[string]string{"id": "newer"}})
			emit("run", "item/completed", map[string]any{"item": map[string]string{"type": "agentMessage", "text": "Newer response"}})
			got, err := s.TurnFinalText(ctx, "run", "target")
			if err != nil || got != text {
				t.Fatal("full owning response lost, truncated or replaced", len(got), len(text), err)
			}
			got, err = s.TurnFinalText(ctx, "run", "missing")
			if err != nil || got != "" {
				t.Fatal("unrelated turn supplied text", got, err)
			}
			got, err = s.TurnFinalText(ctx, "run", "")
			if err != nil || got != "" {
				t.Fatal("missing turn identity supplied text", got, err)
			}
		})
	}
}

func TestTurnFinalTextRejectsIncompleteAndNonAgentSources(t *testing.T) {
	for _, payload := range []string{`{"item":{"type":"agentMessage","text":"partial`, `{"item":]}`, `{"item":{"type":"userMessage","text":"User question?"}}`, `{"item":{"type":"commandExecution","text":"Tool question?"}}`} {
		t.Run(payload, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.Write(ctx, func(tx *Tx) error {
				if err := tx.Event("run.output", "run", map[string]any{"method": "turn/started", "params": map[string]any{"turn": map[string]string{"id": "target"}}}); err != nil {
					return err
				}
				return tx.Event("run.output", "run", map[string]any{"method": "item/completed/chunk", "params": map[string]string{"delta": payload}})
			}); err != nil {
				t.Fatal(err)
			}
			got, err := s.TurnFinalText(ctx, "run", "target")
			if err != nil || got != "" {
				t.Fatal("partial, malformed or non-agent payload became a question source", got, err)
			}
		})
	}
}
