package store

import (
	"codefactory/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestDueRecordsCompareInstantsAcrossTimezones(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	type record struct {
		ID      string    `json:"id"`
		State   string    `json:"state"`
		RetryAt time.Time `json:"retryAt"`
	}
	for _, r := range []record{{"past-positive", "pending", at.Add(-time.Second).In(time.FixedZone("east", 4*3600))}, {"future-negative", "pending", at.Add(time.Second).In(time.FixedZone("west", -4*3600))}} {
		if err = s.Put(ctx, "due", r.ID, r, ""); err != nil {
			t.Fatal(err)
		}
	}
	due, err := DueRecords[record](ctx, s, "due", at, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != "past-positive" {
		t.Fatalf("due page compared wall-clock strings instead of instants: %+v", due)
	}
}

func TestSessionOutputFragmentsBoundRowsAndPreserveExactJSON(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	text := strings.Repeat("🙂\"\\\n\x00<>雪", 40000)
	original := map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]string{"type": "agentMessage", "id": "original-message", "text": text}}}
	if err = s.Write(ctx, func(tx *Tx) error { return tx.Event("session.output", "s", original) }); err != nil {
		t.Fatal(err)
	}
	after := int64(0)
	fragments := []string{}
	id := ""
	expected := 0
	for {
		page, e := s.SessionEvents(ctx, "s", after, 100)
		if e != nil {
			t.Fatal(e)
		}
		wire, _ := json.Marshal(page)
		if len(wire) > 100*(SessionOutputMaxBytes+512) {
			t.Fatal("page byte bound removed", len(wire))
		}
		for _, event := range page["events"].([]domain.Event) {
			if len(event.Data) > SessionOutputMaxBytes || !utf8.Valid(event.Data) {
				t.Fatal("invalid or oversized row", len(event.Data))
			}
			var part struct {
				FragmentID string `json:"fragmentId"`
				Index      int    `json:"fragmentIndex"`
				Count      int    `json:"fragmentCount"`
				Fragment   string `json:"fragment"`
			}
			if err = json.Unmarshal(event.Data, &part); err != nil {
				t.Fatal(err)
			}
			if id == "" {
				id = part.FragmentID
				expected = part.Count
			}
			if part.FragmentID != id || part.Index != len(fragments) || part.Count != expected {
				t.Fatal("fragment order or identity changed")
			}
			fragments = append(fragments, part.Fragment)
		}
		after = page["cursor"].(int64)
		if !page["hasMore"].(bool) {
			break
		}
	}
	want, _ := json.Marshal(original)
	if len(fragments) != expected || strings.Join(fragments, "") != string(want) {
		t.Fatal("serialized output changed during fragmentation")
	}
}

func TestSessionRuntimeProjectionIsAtomicForPutCASDeleteAndRollback(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v := map[string]any{"epoch": "one", "threadId": "thread", "pending": strings.Repeat("large", 10000)}
	if err = s.Put(ctx, "factory-session", "s", v, ""); err != nil {
		t.Fatal(err)
	}
	check := func(epoch string) {
		t.Helper()
		var identity struct {
			Epoch    string
			ThreadID string
		}
		if e := s.Get(ctx, "factory-session-runtime", "s", &identity); e != nil || identity.Epoch != epoch || identity.ThreadID != "thread" {
			t.Fatal("stale compact identity", identity, e)
		}
	}
	check("one")
	records, err := s.Records(ctx, "factory-session")
	if err != nil {
		t.Fatal(err)
	}
	v["epoch"] = "two"
	if changed, e := s.CompareAndSwap(ctx, "factory-session", "s", records["s"], v); e != nil || !changed {
		t.Fatal(changed, e)
	}
	check("two")
	v["epoch"] = "three"
	sentinel := fmt.Errorf("rollback")
	err = s.Write(ctx, func(tx *Tx) error {
		if e := tx.Put("factory-session", "s", v); e != nil {
			return e
		}
		return sentinel
	})
	if err != sentinel {
		t.Fatal(err)
	}
	check("two")
	if changed, e := s.CompareAndSwap(ctx, "factory-session", "s", records["s"], v); e != nil || changed {
		t.Fatal(changed, e)
	}
	check("two")
	if err = s.Write(ctx, func(tx *Tx) error { return tx.Delete("factory-session", "s") }); err != nil {
		t.Fatal(err)
	}
	var identity any
	if e := s.Get(ctx, "factory-session-runtime", "s", &identity); e != ErrNotFound {
		t.Fatal("deleted session retained live identity", e)
	}
}
