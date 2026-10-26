package app

import (
	"codefactory/internal/domain"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAttentionQueueFiltersAndPaginatesWithoutSkippingResolvedRequests(t *testing.T) {
	a, ctx := fixture(t)
	created := time.Now().UTC()
	for _, status := range []string{"waiting", "running", "idle", "interrupted", "prepared", "cleaned"} {
		r := domain.Run{ID: status, ItemID: "pilot/repo#1", Status: status, Prompt: "private-run-prompt", Profile: domain.Profile{ID: "investigate", Name: "Plan", Prompt: "private-profile-prompt"}, Skills: []domain.Skill{{Content: "private-skill-bundle"}}}
		if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Retained work without a pending question does not own this read. Even an
	// unrelated invalid summary must not force run-history decoding on dashboard
	// reads or on filtered queue pages.
	if err := a.Store.Put(ctx, "run", "unrelated-history", map[string]any{"id": "unrelated-history", "status": map[string]string{"legacy": "unreadable"}}, ""); err != nil {
		t.Fatal(err)
	}
	requests := []domain.Attention{
		{ID: "a", RunID: "waiting", Status: "pending", Blocking: true, RequestID: json.RawMessage(`7`), Method: questionMethod, Params: raw(map[string]any{"questions": []question{{ID: "scope", Question: "Which scope?", Options: []questionOption{{Label: "Focused"}}}}})},
		{ID: "b", RunID: "running", Status: "pending", Blocking: false, Method: "item/commandExecution/requestApproval"},
		{ID: "c", RunID: "interrupted", Status: "pending", Origin: "text"},
		{ID: "d", RunID: "idle", Status: "pending", Origin: "async"},
		{ID: "answered", RunID: "waiting", Status: "answered"},
		{ID: "stale", RunID: "waiting", Status: "stale"},
		{ID: "orphan", RunID: "missing", Status: "pending"},
		{ID: "rpc-idle", RunID: "idle", Status: "pending"},
		{ID: "rpc-interrupted", RunID: "interrupted", Status: "pending"},
		{ID: "text-prepared", RunID: "prepared", Status: "pending", Origin: "text"},
		{ID: "text-cleaned", RunID: "cleaned", Status: "pending", Origin: "text"},
	}
	for i := range requests {
		requests[i].Created = created
		if err := a.Store.Put(ctx, "attention", requests[i].ID, requests[i], ""); err != nil {
			t.Fatal(err)
		}
	}
	before := a.Store.Latest(ctx)
	first, err := a.ListAttention(ctx, AttentionInput{Limit: 1, Freshness: "first"})
	if err != nil || len(first.Requests) != 1 || first.Requests[0].Request.ID != "a" || first.NextCursor == "" {
		t.Fatal("oldest request missing", first, err)
	}
	entry := first.Requests[0]
	if !entry.Request.Blocking || len(entry.Request.RequestID) != 0 || entry.Run.ItemID != "pilot/repo#1" || entry.Run.ProfileName != "Plan" {
		t.Fatal("missing question context or leaked provider ID", entry)
	}
	encoded, _ := json.Marshal(first)
	if !strings.Contains(string(encoded), "Which scope?") || !strings.Contains(string(encoded), "Focused") || strings.Contains(string(encoded), "private-") {
		t.Fatal("lost form or leaked unrelated run content", string(encoded))
	}
	if a.Store.Latest(ctx) != before {
		t.Fatal("reading the queue changed state")
	}
	// Resolving a previous-page request must not shift or skip remaining asks.
	requests[0].Status = "answered"
	if err := a.Store.Put(ctx, "attention", "a", requests[0], ""); err != nil {
		t.Fatal(err)
	}
	next, err := a.ListAttention(ctx, AttentionInput{Cursor: first.NextCursor})
	if err != nil || len(next.Requests) != 3 || next.Requests[0].Request.ID != "b" || next.Requests[1].Request.ID != "c" || next.Requests[2].Request.ID != "d" || next.NextCursor != "" {
		t.Fatal("resolved, stale or orphaned requests broke pagination", next, err)
	}
	if next.Requests[0].Request.Blocking {
		t.Fatal("nonblocking approval fabricated a paused run")
	}
	filtered, err := a.ListAttention(ctx, AttentionInput{RunID: "interrupted"})
	if err != nil || len(filtered.Requests) != 1 || filtered.Requests[0].Request.ID != "c" {
		t.Fatal("retained question cannot be discovered", filtered, err)
	}
	filtered, err = a.ListAttention(ctx, AttentionInput{ItemID: "pilot/repo#1", Limit: 1})
	if err != nil || len(filtered.Requests) != 1 || filtered.Requests[0].Request.ID != "b" || filtered.NextCursor == "" {
		t.Fatal("item-filtered queue changed pending ownership or pagination", filtered, err)
	}
	next, err = a.ListAttention(ctx, AttentionInput{ItemID: "pilot/repo#1", Cursor: filtered.NextCursor})
	if err != nil || len(next.Requests) != 2 || next.Requests[0].Request.ID != "c" || next.Requests[1].Request.ID != "d" {
		t.Fatal("item-filtered continuation skipped pending requests", next, err)
	}
	empty, err := a.ListAttention(ctx, AttentionInput{ItemID: "other/repo#1"})
	if err != nil || len(empty.Requests) != 0 {
		t.Fatal("item filter leaked another item's questions", empty, err)
	}
	empty, err = a.ListAttention(ctx, AttentionInput{RunID: "missing"})
	if err != nil || empty.Requests == nil || len(empty.Requests) != 0 {
		t.Fatal("empty queue must be an explicit array", empty, err)
	}
	for _, in := range []AttentionInput{{Limit: -1}, {Limit: 101}, {Cursor: "invalid!"}, {Cursor: first.NextCursor, RunID: "waiting"}, {Cursor: strings.Repeat("a", 1025)}} {
		if _, err := a.ListAttention(ctx, in); err == nil {
			t.Fatal("invalid queue input accepted", in)
		}
	}
}
