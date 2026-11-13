package providers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
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

func TestGitHubPersistentCachePressureRetainsHotConditionalResponses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	now := time.Now().UTC()
	full, validations := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"user"`)
		if r.Header.Get("If-None-Match") == `"user"` {
			validations++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		full++
		fmt.Fprint(w, `{"login":"fixture"}`)
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := managedFixture(t, server, db, &now, 100, 0)
	var user struct{ Login string }
	if _, err := g.get(ctx, "/user", &user); err != nil {
		t.Fatal(err)
	}
	// Fill durable storage just below its real 64 MiB body allowance. The
	// recently validated user remains much newer than these cold responses.
	body := make([]byte, cacheLimit/4)
	for n := 0; n < 4; n++ {
		part := body
		if n == 0 {
			part = part[:len(part)-1024]
		}
		if err := db.Put(ctx, "github-cache", fmt.Sprint("cold-", n), cacheEntry{Body: part, CheckedAt: now.Add(time.Duration(n-4) * time.Hour)}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.remember(ctx, "incoming", cacheEntry{Body: make([]byte, 1024), CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	sizes, err := db.JSONByteSizes(ctx, "github-cache", "$.Body")
	if err != nil || len(sizes) != 5 {
		t.Fatal("pressure flushed unrelated responses", sizes, err)
	}
	if _, exists := sizes["cold-0"]; exists {
		t.Fatal("oldest validated response was retained under pressure")
	}
	total := int64(0)
	for _, size := range sizes {
		total += size
	}
	if total > cacheLimit {
		t.Fatal("persistent response bodies exceed allowance", total)
	}
	if _, err := g.get(ctx, "/user", &user); err != nil || full != 1 || validations != 0 {
		t.Fatal("hot memory response was lost", full, validations, err)
	}
	restarted := managedFixture(t, server, db, &now, 100, 0)
	if _, err := restarted.get(WithGitHubRead(ctx, RequireLive), "/user", &user); err != nil || full != 1 || validations != 1 || user.Login != "fixture" {
		t.Fatal("pressure discarded conditional validation across restart", full, validations, user, err)
	}
}

func TestGitHubPersistentCacheEvictsExpiredMemoryAndKeepsReplacement(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := &GitHub{usageStore: db, Now: func() time.Time { return now }}
	for key, entry := range map[string]cacheEntry{
		"expired-age": {Body: []byte("old"), CheckedAt: now.Add(-25 * time.Hour)},
		"expired-at":  {Body: []byte("old"), CheckedAt: now, Expires: now},
		"retained":    {Body: []byte("fresh"), CheckedAt: now},
	} {
		id := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
		if err := db.Put(ctx, "github-cache", id, entry, ""); err != nil {
			t.Fatal(err)
		}
		g.cachePut(key, entry)
	}
	if err := g.remember(ctx, "retained", cacheEntry{Body: []byte("replacement"), CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"expired-age", "expired-at"} {
		if _, ok := g.cacheGet(key); ok {
			t.Fatal("persistent eviction left a memory response", key)
		}
	}
	restarted := &GitHub{usageStore: db, Now: func() time.Time { return now }}
	for _, source := range []*GitHub{g, restarted} {
		entry, ok, err := source.cached(ctx, "retained")
		if err != nil || !ok || string(entry.Body) != "replacement" {
			t.Fatal("replacement differs across memory and restart", entry, ok, err)
		}
		for _, key := range []string{"expired-age", "expired-at"} {
			if _, ok, err := source.cached(ctx, key); err != nil || ok {
				t.Fatal("expired response survived eviction", key, ok, err)
			}
		}
	}
	sizes, err := db.JSONByteSizes(ctx, "github-cache", "$.Body")
	if err != nil || len(sizes) != 1 {
		t.Fatal("expired rows remain in the durable cache", sizes, err)
	}
}

func TestGitHubPersistentCacheRejectedWriteDoesNotPublishOrEvictMemory(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), "cache.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := &GitHub{usageStore: db, Now: func() time.Time { return now }}
	old := cacheEntry{Body: []byte("old"), CheckedAt: now.Add(-25 * time.Hour)}
	oldID := fmt.Sprintf("%x", sha256.Sum256([]byte("old")))
	if err := db.Put(ctx, "github-cache", oldID, old, ""); err != nil {
		t.Fatal(err)
	}
	g.cachePut("old", old)
	control, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if _, err := control.Exec(`CREATE TRIGGER reject_cache BEFORE INSERT ON entities WHEN NEW.kind='github-cache' BEGIN SELECT RAISE(ABORT, 'fixture cache failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := g.remember(ctx, "new", cacheEntry{Body: []byte("fresh"), CheckedAt: now}); err == nil {
		t.Fatal("failed durable insertion was hidden")
	}
	if _, ok := g.cacheGet("new"); ok {
		t.Fatal("failed durable response was published in memory")
	}
	if _, ok := g.cacheGet("old"); !ok {
		t.Fatal("rolled-back eviction removed the memory response")
	}
	if err := db.Get(ctx, "github-cache", oldID, &cacheEntry{}); err != nil {
		t.Fatal("failed replacement did not roll back eviction", err)
	}
}

func TestGitHubPersistentCacheRejectedRevalidationPreservesMemoryHeaders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("ETag", `"user"`)
		if r.Header.Get("If-None-Match") == `"user"` {
			w.Header().Set("Link", `<https://example.invalid/new>; rel="next"`)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Link", `<https://example.invalid/old>; rel="next"`)
		fmt.Fprint(w, `{"login":"fixture"}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "cache.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := managedFixture(t, server, db, &now, 100, 0)
	var user struct{ Login string }
	initial, err := g.get(ctx, "/user", &user)
	if err != nil {
		t.Fatal(err)
	}
	key, err := g.responseCacheKey(ctx, server.URL+"/user", "fixture-secret-token", "application/vnd.github+json")
	if err != nil {
		t.Fatal(err)
	}
	before, ok, err := g.cached(ctx, key)
	if err != nil || !ok {
		t.Fatal("initial response not cached", ok, err)
	}
	control, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if _, err := control.ExecContext(ctx, `CREATE TRIGGER reject_cache BEFORE INSERT ON entities WHEN NEW.kind='github-cache' BEGIN SELECT RAISE(ABORT, 'fixture cache failure'); END`); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := g.get(WithGitHubRead(ctx, RequireLive), "/user", &user); err == nil {
		t.Fatal("failed durable revalidation was hidden")
	}
	for _, source := range []*GitHub{g, managedFixture(t, server, db, &now, 100, 0)} {
		entry, ok, err := source.cached(ctx, key)
		if err != nil || !ok || entry.Header.Get("Link") != initial.Get("Link") || string(entry.Body) != string(before.Body) || !entry.CheckedAt.Equal(before.CheckedAt) || !entry.Until.Equal(before.Until) {
			t.Fatal("failed revalidation changed retained observation", entry, ok, err)
		}
	}
	header, err := g.get(ctx, "/user", &user)
	if err != nil || header.Get("Link") != initial.Get("Link") || user.Login != "fixture" || calls.Load() != 2 {
		t.Fatal("ordinary read exposed rejected revalidation", header, user, calls.Load(), err)
	}
}

func TestGitHubPersistentCacheKeepsLiveHeadersInMemoryAndFiltersDurableCopy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Fixture-Observation", "retained-in-memory")
		w.Header().Set("ETag", `"user"`)
		w.Header().Set("Link", `<https://example.invalid/next>; rel="next"`)
		fmt.Fprint(w, `{"login":"fixture"}`)
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := managedFixture(t, server, db, &now, 100, 0)
	for _, phase := range []string{"live", "memory"} {
		body, header, err := g.Request(ctx, http.MethodGet, "/user", nil, "")
		if err != nil || string(body) != `{"login":"fixture"}` || header.Get("Content-Type") != "application/json" || header.Get("X-Fixture-Observation") != "retained-in-memory" || calls.Load() != 1 {
			t.Fatal("live headers lost across local reuse", phase, string(body), header, calls.Load(), err)
		}
	}
	restarted := managedFixture(t, server, db, &now, 100, 0)
	body, header, err := restarted.Request(ctx, http.MethodGet, "/user", nil, "")
	if err != nil || string(body) != `{"login":"fixture"}` || header.Get("Content-Type") != "" || header.Get("X-Fixture-Observation") != "" || header.Get("ETag") != `"user"` || header.Get("Link") == "" || calls.Load() != 1 {
		t.Fatal("durable copy did not retain only approved headers", string(body), header, calls.Load(), err)
	}
}

func TestGitHubPersistentCacheOversizedReplacementPreservesOtherEntries(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := &GitHub{usageStore: db, Now: func() time.Time { return now }}
	for _, key := range []string{"replaced", "retained"} {
		if err := g.remember(ctx, key, cacheEntry{Body: []byte(key), CheckedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.remember(ctx, "replaced", cacheEntry{Body: make([]byte, cacheLimit+1), CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, source := range []*GitHub{g, {usageStore: db, Now: func() time.Time { return now }}} {
		if _, ok, err := source.cached(ctx, "replaced"); err != nil || ok {
			t.Fatal("oversized response retained an old replacement", ok, err)
		}
		if entry, ok, err := source.cached(ctx, "retained"); err != nil || !ok || string(entry.Body) != "retained" {
			t.Fatal("oversized response evicted unrelated data", entry, ok, err)
		}
	}
	id := fmt.Sprintf("%x", sha256.Sum256([]byte("replaced")))
	if err := db.Get(ctx, "github-cache", id, &cacheEntry{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("oversized response was persisted", err)
	}
}

func TestGitHubPersistentCacheEntryPressureEvictsOneOldestRow(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Write(ctx, func(tx *store.Tx) error {
		for n := 0; n < cacheEntries; n++ {
			if err := tx.Put("github-cache", fmt.Sprintf("old-%04d", n), cacheEntry{Body: []byte("x"), CheckedAt: now.Add(-time.Hour)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	g := &GitHub{usageStore: db, Now: func() time.Time { return now }}
	if err := g.remember(ctx, "incoming", cacheEntry{Body: []byte("new"), CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	sizes, err := db.JSONByteSizes(ctx, "github-cache", "$.Body")
	if err != nil || len(sizes) != cacheEntries {
		t.Fatal("entry pressure did not evict exactly one response", len(sizes), err)
	}
	if _, exists := sizes["old-0000"]; exists {
		t.Fatal("oldest validation tie did not use stable storage identity")
	}
	if _, exists := sizes["old-0001"]; !exists {
		t.Fatal("entry pressure evicted unnecessary responses")
	}
}
