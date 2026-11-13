package transport

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const responseLimit = 64 << 10
const inlineLimit = 48 << 10
const documentChunkBytes = 24 << 10 // 32 KiB after base64, without JSON escape expansion.

type documentDescriptor struct {
	Version  string `json:"version"`
	Chunks   int    `json:"chunks"`
	Bytes    int    `json:"bytes"`
	Encoding string `json:"encoding"`
}
type documentCache struct {
	mu     sync.Mutex
	chunks map[string][]string
	bytes  int
	order  []string
}

func (d *documentCache) prepare(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) <= inlineLimit {
		return json.RawMessage(data), nil
	}
	version := fmt.Sprintf("%x", sha256.Sum256(data))
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.chunks == nil {
		d.chunks = map[string][]string{}
	}
	chunks, ok := d.chunks[version]
	if !ok {
		if (len(data)+2)/3*4 > payloadLimit {
			return nil, fmt.Errorf("document exceeds 64 MiB cache capacity")
		}
		for d.bytes+(len(data)+2)/3*4 > payloadLimit && len(d.order) > 0 {
			oldest := d.order[0]
			d.order = d.order[1:]
			for _, chunk := range d.chunks[oldest] {
				d.bytes -= len(chunk)
			}
			delete(d.chunks, oldest)
		}
		for start := 0; start < len(data); start += documentChunkBytes {
			chunks = append(chunks, base64.StdEncoding.EncodeToString(data[start:min(start+documentChunkBytes, len(data))]))
		}
		d.chunks[version] = chunks
		for _, chunk := range chunks {
			d.bytes += len(chunk)
		}
		d.order = append(d.order, version)
	}
	return map[string]any{"document": documentDescriptor{version, len(chunks), len(data), "base64-json"}}, nil
}
func (d *documentCache) availability(value any) (bool, bool) {
	wrapper, ok := value.(map[string]any)
	if !ok {
		return true, false
	}
	descriptor, ok := wrapper["document"].(documentDescriptor)
	if !ok {
		return true, false
	}
	if !d.mu.TryLock() {
		return false, true
	}
	defer d.mu.Unlock()
	_, ok = d.chunks[descriptor.Version]
	return ok, false
}
func (d *documentCache) available(value any) bool {
	available, _ := d.availability(value)
	return available
}

func (d *documentCache) chunk(version string, index int) (any, error) {
	if !d.mu.TryLock() {
		return map[string]any{"state": "busy"}, nil
	}
	defer d.mu.Unlock()
	chunks, ok := d.chunks[version]
	if !ok {
		return nil, fmt.Errorf("document version unavailable; refresh its owning read")
	}
	if index < 0 || index >= len(chunks) {
		return nil, fmt.Errorf("invalid document chunk")
	}
	return map[string]any{"state": "ready", "version": version, "index": index, "chunk": chunks[index]}, nil
}

type projectionEntry struct {
	revision     uint64
	dependencies []string
	value        any
	err          string
	preparing    bool
	used         uint64
	retryAt      time.Time
}
type projectionTask struct {
	key          string
	dependencies []string
	run          func(context.Context) (any, error)
}
type projectionCache struct {
	mu        sync.Mutex
	db        *store.Store
	entries   map[string]projectionEntry
	work      chan projectionTask
	clock     uint64
	documents *documentCache
}
type viewKey struct{}

func projectionStore(ctx context.Context, db *store.Store) *store.Store {
	if view, ok := ctx.Value(viewKey{}).(*store.Store); ok {
		return view
	}
	return db
}
func newProjectionCache(db *store.Store, documents *documentCache) *projectionCache {
	c := &projectionCache{db: db, entries: map[string]projectionEntry{}, work: make(chan projectionTask, 256), documents: documents}
	for n := 0; n < 4; n++ {
		go c.worker()
	}
	return c
}
func (c *projectionCache) read(key string, deps []string, run func(context.Context) (any, error)) any {
	revision := c.db.Revision(deps...)
	if !c.mu.TryLock() {
		return map[string]any{"state": "pending", "revision": revision}
	}
	defer c.mu.Unlock()
	entry, exists := c.entries[key]
	if exists && len(entry.dependencies) > 0 {
		revision = c.db.Revision(entry.dependencies...)
	}
	c.clock++
	entry.used = c.clock
	if exists {
		c.entries[key] = entry
	}
	if exists && !entry.preparing && entry.revision == revision && c.documents.available(entry.value) && (entry.err == "" || time.Now().Before(entry.retryAt)) {
		if entry.err != "" {
			return map[string]any{"state": "failed", "revision": revision, "error": entry.err}
		}
		return map[string]any{"state": "ready", "revision": revision, "value": entry.value}
	}
	if !entry.preparing {
		if !exists && len(c.entries) >= 1024 {
			oldest := ""
			var used uint64 = ^uint64(0)
			for id, candidate := range c.entries {
				if !candidate.preparing && candidate.used < used {
					oldest = id
					used = candidate.used
				}
			}
			if oldest == "" {
				return map[string]any{"state": "busy"}
			}
			delete(c.entries, oldest)
		}
		select {
		case c.work <- projectionTask{key, deps, run}:
			entry.preparing = true
			c.entries[key] = entry
		default:
			return map[string]any{"state": "busy"}
		}
	}
	return map[string]any{"state": "pending", "revision": revision}
}
func (c *projectionCache) worker() {
	ctx := c.db.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-c.work:
			var value any
			revision := c.db.Revision(task.dependencies...)
			err := c.db.ReadView(ctx, func(view *store.Store) error {
				// Dependency decoding can fail too; bind that error to this snapshot.
				revision = view.Revision(task.dependencies...)
				expanded, err := expandDependencies(ctx, view, task.dependencies)
				if err != nil {
					return err
				}
				task.dependencies = expanded
				revision = view.Revision(task.dependencies...)
				value, err = task.run(context.WithValue(ctx, viewKey{}, view))
				return err
			})
			if err == nil && strings.HasPrefix(task.key, "factory_state:") {
				if state, ok := value.(map[string]any); ok {
					state["version"] = revision
				}
			}
			if err == nil {
				value, err = c.documents.prepare(value)
			}
			entry := projectionEntry{revision: revision, value: value, dependencies: task.dependencies}
			if err != nil {
				entry.err = err.Error()
				entry.retryAt = time.Now().Add(time.Second)
			}
			c.mu.Lock()
			entry.used = c.entries[task.key].used
			c.entries[task.key] = entry
			c.mu.Unlock()
		}
	}
}

// Dependencies include saved state used by projections, never operation journals
// or transcript traffic belonging to unrelated sessions.
func projectionDependencies(name string, raw json.RawMessage) []string {
	var in struct {
		ID     string `json:"id"`
		ItemID string `json:"itemId"`
		View   string `json:"view"`
		Kind   string `json:"kind"`
	}
	_ = json.Unmarshal(raw, &in)
	switch name {
	case "factory_item":
		return []string{"item/" + in.ID, "related/" + in.ID}
	case "session_transcript":
		return []string{"event:session.output/" + in.ID}
	case "session_questions":
		return []string{"factory-question"}
	case "factory_events":
		switch in.Kind {
		case "deliveries":
			return []string{"factory-delivery"}
		case "batches":
			return []string{"factory-batch"}
		case "rules":
			return []string{"factory-settings"}
		default:
			return []string{"factory-event"}
		}
	case "factory_state":
		deps := []string{"factory-question", "factory-session", "factory-batch"}
		if in.View == "configuration" {
			return append(deps, "factory-settings", "factory-proposal", "github-usage")
		}
		if in.View == "conversation" && in.ItemID != "" {
			return append(deps, "item/"+in.ItemID, "user/"+in.ItemID, "assessment/"+in.ItemID, "related/"+in.ItemID, "ticket", "identity", "config", "repository")
		}
		// Inbox queues derive from item/user/link/identity and session/question state.
		return append(deps, "item", "user", "relationship", "linked-pr", "identity", "repository", "config", "assessment", "ticket", "observation")
	}
	return []string{name}
}
func projectionKey(name string, raw json.RawMessage) string {
	var args map[string]json.RawMessage
	_ = json.Unmarshal(raw, &args)
	delete(args, "freshness")
	delete(args, "sinceVersion")
	delete(args, "sinceInstanceId")
	b, _ := json.Marshal(args)
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%s:%x", name, sum)
}

func expandDependencies(ctx context.Context, view *store.Store, dependencies []string) ([]string, error) {
	expanded := append([]string{}, dependencies...)
	for _, key := range dependencies {
		if !strings.HasPrefix(key, "related/") {
			continue
		}
		id := strings.TrimPrefix(key, "related/")
		links, err := store.Select[domain.Relationship](ctx, view, "relationship", "related", id)
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			expanded = append(expanded, "item/"+link.Source, "item/"+link.Target, "linked-pr/"+link.Source, "linked-pr/"+link.Target)
		}
	}
	return expanded, nil
}
