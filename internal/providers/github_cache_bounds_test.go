package providers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"codefactory/internal/store"
)

func TestGitHubPersistentCacheAccountingPreservesLegacyEntriesAndReplacement(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Older rows may omit their embedded ID. The database key still owns them.
	key := "fixture:query"
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	now := time.Now().UTC()
	if err := db.Put(ctx, "github-cache", id, cacheEntry{Body: []byte("old"), CheckedAt: now}, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "github-cache", "other", cacheEntry{Body: []byte("retained"), CheckedAt: now}, ""); err != nil {
		t.Fatal(err)
	}
	g := &GitHub{usageStore: db}
	if err := g.remember(ctx, key, cacheEntry{Body: []byte("replacement"), CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	// A fresh provider must read the replacement from durable state, not memory.
	restarted := &GitHub{usageStore: db}
	entry, ok, err := restarted.cached(ctx, key)
	if err != nil || !ok || string(entry.Body) != "replacement" || !entry.CheckedAt.Equal(now) {
		t.Fatal("persistent replacement lost after restart", entry, ok, err)
	}
	var retained cacheEntry
	if err := db.Get(ctx, "github-cache", "other", &retained); err != nil || string(retained.Body) != "retained" {
		t.Fatal("unrelated legacy body changed", retained, err)
	}
	sizes, err := db.JSONByteSizes(ctx, "github-cache", "$.Body")
	if err != nil || len(sizes) != 2 || sizes[id] != int64(len("replacement")) || sizes["other"] != int64(len("retained")) {
		t.Fatal("replacement was double counted or legacy row omitted", sizes, err)
	}
}
