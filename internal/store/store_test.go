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
