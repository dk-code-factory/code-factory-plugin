package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRecordsKeepStorageKeysAndProjectionPreservesSource(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	source := map[string]any{"id": "legacy-embedded-id", "title": "visible", "activity": []string{strings.Repeat("history", 1000)}, "pr": map[string]any{"state": "open", "checks": []string{"large"}}}
	if err := s.Put(ctx, "item", "storage-id", source, ""); err != nil {
		t.Fatal(err)
	}
	records, err := s.Records(ctx, "item")
	if err != nil || records["storage-id"] == nil || records["legacy-embedded-id"] != nil {
		t.Fatal("database identity lost", err)
	}
	before := string(records["storage-id"])
	projected, err := ListWithout[map[string]any](ctx, s, "item", "$.activity", "$.pr.checks")
	if err != nil || len(projected) != 1 {
		t.Fatal("projection", err)
	}
	if projected[0]["activity"] != nil || projected[0]["pr"].(map[string]any)["checks"] != nil || projected[0]["title"] != "visible" || projected[0]["pr"].(map[string]any)["state"] != "open" {
		t.Fatal("incorrect projection")
	}
	after, err := s.Records(ctx, "item")
	if err != nil || string(after["storage-id"]) != before {
		t.Fatal("projection modified retained data", err)
	}
	empty, err := ListWithout[map[string]any](ctx, s, "item' OR 1=1 --", "$.activity")
	if err != nil || len(empty) != 0 {
		t.Fatal("bucket must be a bound parameter", err)
	}
}

func TestJSONByteSizesPreservePaddingStorageKeysAndBuckets(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for id, body := range map[string][]byte{"zero": {}, "one": {1}, "two": {1, 2}, "three": {1, 2, 3}, "four": {1, 2, 3, 4}, "null": nil} {
		if err := s.Put(ctx, "cache", id, map[string]any{"ID": "old-embedded-id", "Body": body}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Put(ctx, "cache", "missing", map[string]any{"Other": "retained"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "unrelated", "other-bucket", map[string]any{"Body": []byte("private")}, ""); err != nil {
		t.Fatal(err)
	}
	before, err := s.Records(ctx, "cache")
	if err != nil {
		t.Fatal(err)
	}
	sizes, err := s.JSONByteSizes(ctx, "cache", "$.Body")
	if err != nil || len(sizes) != 7 {
		t.Fatal("cache size inventory", sizes, err)
	}
	for id, want := range map[string]int64{"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "null": 0, "missing": 0} {
		if got, ok := sizes[id]; !ok || got != want {
			t.Errorf("%s: size %d, present %t; want %d", id, got, ok, want)
		}
	}
	after, err := s.Records(ctx, "cache")
	if err != nil {
		t.Fatal(err)
	}
	for id, data := range before {
		if string(after[id]) != string(data) {
			t.Fatal("size projection modified stored record", id)
		}
	}
	if sizes, err := s.JSONByteSizes(ctx, "cache' OR 1=1 --", "$.Body"); err != nil || len(sizes) != 0 {
		t.Fatal("bucket must remain a bound parameter", sizes, err)
	}
	if sizes, err := s.JSONByteSizes(ctx, "cache", `$.Body') FROM entities --`); err != nil || sizes["four"] != 0 {
		t.Fatal("path must remain a bound parameter", sizes, err)
	}
}

func TestJSONByteEntriesKeepBodylessMetadataAndTransactionalIdentity(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.Write(ctx, func(tx *Tx) error {
		for id, body := range map[string][]byte{"one": {1}, "two": {1, 2}, "three": {1, 2, 3}, "four": {1, 2, 3, 4}, "null": nil} {
			if err := tx.Put("cache", id, map[string]any{"ID": "legacy", "Body": body, "CheckedAt": "retained", "Header": map[string]string{"Link": strings.Repeat("h", 128<<10)}, "ETag": strings.Repeat("e", 128<<10), "Redirect": strings.Repeat("r", 128<<10)}); err != nil {
				return err
			}
		}
		entries, err := tx.JSONByteEntries("cache", "$.Body", "CheckedAt", "ID")
		if err != nil {
			return err
		}
		if len(entries) != 5 {
			t.Fatalf("missing current transaction rows: %d", len(entries))
		}
		want := map[string]int64{"one": 1, "two": 2, "three": 3, "four": 4, "null": 0}
		for _, entry := range entries {
			var metadata map[string]any
			if err := json.Unmarshal(entry.Metadata, &metadata); err != nil {
				return err
			}
			if entry.Size != want[entry.ID] || metadata["CheckedAt"] != "retained" || metadata["ID"] != "legacy" {
				t.Fatal("storage identity, byte size or metadata changed", entry, metadata)
			}
			if len(entry.Metadata) > 128 || len(metadata) != 2 {
				t.Fatalf("inventory transferred unselected payloads: metadata bytes=%d fields=%d", len(entry.Metadata), len(metadata))
			}
		}
		if entries, err := tx.JSONByteEntries("cache' OR 1=1 --", "$.Body"); err != nil || len(entries) != 0 {
			t.Fatal("bucket must be a bound parameter", entries, err)
		}
		if entries, err := tx.JSONByteEntries("cache", `$.Body') FROM entities --`); err != nil || len(entries) != 5 {
			t.Fatal("path must be a bound parameter", entries, err)
		}
		entries, err = tx.JSONByteEntries("cache", "$.Body", "CheckedAt') FROM entities --")
		if err != nil || len(entries) != 5 {
			t.Fatal("metadata fields must be bound values", err)
		}
		for _, entry := range entries {
			var metadata map[string]any
			if err := json.Unmarshal(entry.Metadata, &metadata); err != nil {
				return err
			}
			if len(metadata) != 1 || metadata["CheckedAt') FROM entities --"] != nil {
				t.Fatal("metadata field changed the query", metadata)
			}
		}
		entries, err = tx.JSONByteEntries("cache", "$.Body")
		if err != nil || len(entries) != 5 {
			t.Fatal("empty metadata selection", err)
		}
		for _, entry := range entries {
			if string(entry.Metadata) != "{}" {
				t.Fatal("empty selection transferred metadata", len(entry.Metadata))
			}
		}
		if err := tx.Put("selector-contract", "fields", map[string]any{
			"Body":     []byte{1},
			"a.b":      "literal dotted name",
			"a":        map[string]string{"b": "nested value"},
			"items[0]": "literal bracket name",
			"items":    []string{"array value"},
		}); err != nil {
			return err
		}
		entries, err = tx.JSONByteEntries("selector-contract", "$.Body", "a.b", "items[0]")
		if err != nil {
			return err
		}
		if len(entries) != 1 {
			t.Fatalf("metadata selector rows: %d", len(entries))
		}
		var selected map[string]any
		if err := json.Unmarshal(entries[0].Metadata, &selected); err != nil {
			return err
		}
		if entries[0].ID != "fields" || entries[0].Size != 1 || len(selected) != 2 || selected["a.b"] != "nested value" || selected["items[0]"] != "array value" {
			t.Fatal("metadata selectors must traverse JSON paths and retain their output names", entries[0], selected)
		}
		return tx.Delete("cache", "one")
	})
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := s.Get(ctx, "cache", "four", &row); err != nil || row["Body"] != "AQIDBA==" || row["ETag"] != strings.Repeat("e", 128<<10) {
		t.Fatal("metadata projection modified persisted body", row, err)
	}
	if err := s.Get(ctx, "cache", "one", &row); !errors.Is(err, ErrNotFound) {
		t.Fatal("inventory did not release its query before mutation", err)
	}
}

func TestTransactionsAndVault(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	boom := errors.New("rollback")
	e = s.Write(ctx, func(tx *Tx) error {
		if e := tx.Put("item", "1", map[string]string{"title": "test"}); e != nil {
			return e
		}
		if e := tx.Event("changed", "1", nil); e != nil {
			return e
		}
		return boom
	})
	if !errors.Is(e, boom) {
		t.Fatal(e)
	}
	var value any
	if !errors.Is(s.Get(ctx, "item", "1", &value), ErrNotFound) {
		t.Fatal("rollback left state")
	}
	events, e := s.Events(ctx, 0, 100)
	if e != nil || len(events) != 0 {
		t.Fatal("rollback left events", e)
	}
	v, e := NewVault(s, dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = v.Set(ctx, "jira", "secret-token"); e != nil {
		t.Fatal(e)
	}
	var ciphertext string
	if e = s.Get(ctx, "secret", "jira", &ciphertext); e != nil || ciphertext == "secret-token" {
		t.Fatal("plaintext credential", e)
	}
	got, e := v.Get(ctx, "jira")
	if e != nil || got != "secret-token" {
		t.Fatal(got, e)
	}
	e = s.Write(ctx, func(tx *Tx) error {
		if err := v.SetTx(tx, "answer", "private-answer"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(e, boom) {
		t.Fatal(e)
	}
	if _, err := v.Get(ctx, "answer"); !errors.Is(err, ErrNotFound) {
		t.Fatal("rolled-back answer remained in vault", err)
	}
	info, e := os.Stat(filepath.Join(dir, "vault.key"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe key permissions", e)
	}
	var wg sync.WaitGroup
	for n := 0; n < 10; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.Write(ctx, func(tx *Tx) error { return tx.Event("concurrent", "1", nil) }); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	events, e = s.Events(ctx, 0, 100)
	if e != nil || len(events) != 10 {
		t.Fatal(e, len(events))
	}
	for n := 1; n < len(events); n++ {
		if events[n].Seq <= events[n-1].Seq {
			t.Fatal("cursor is not monotonic")
		}
	}
}

func TestCredentialsAreRedactedBeforeJournaling(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v, e := NewVault(s, dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = v.Set(ctx, "jira", `secret-"token`); e != nil {
		t.Fatal(e)
	}
	b := v.RedactJSON(ctx, json.RawMessage(`{"delta":"command used secret-\"token and user-secret","nested":["user-secret"]}`), []string{"user-secret"})
	if !json.Valid(b) || strings.Contains(string(b), "user-secret") || strings.Contains(string(b), "secret-") {
		t.Fatal("redaction failed", string(b))
	}
	var out map[string]any
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out["delta"].(string), "[redacted]") {
		t.Fatal("missing redaction")
	}
}

func TestCompareAndSwapPreservesConcurrentEntities(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cas.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	other, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	first := map[string]int{"generation": 1}
	if ok, e := s.CompareAndSwap(ctx, "index", "repo", nil, first); e != nil || !ok {
		t.Fatal("initial insert failed", ok, e)
	}
	var prior json.RawMessage
	if e = s.Get(ctx, "index", "repo", &prior); e != nil {
		t.Fatal(e)
	}
	if ok, e := other.CompareAndSwap(ctx, "index", "repo", nil, first); e != nil || ok {
		t.Fatal("absence check overwrote entity", ok, e)
	}
	if ok, e := other.CompareAndSwap(ctx, "index", "repo", prior, map[string]int{"generation": 2}); e != nil || !ok {
		t.Fatal("current update failed", ok, e)
	}
	if ok, e := s.CompareAndSwap(ctx, "index", "repo", prior, map[string]int{"generation": 3}); e != nil || ok {
		t.Fatal("stale generation overwrote current entity", ok, e)
	}
	var current map[string]int
	if e = s.Get(ctx, "index", "repo", &current); e != nil || current["generation"] != 2 {
		t.Fatal("concurrent value lost", current, e)
	}
}
