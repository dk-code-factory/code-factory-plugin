package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"codefactory/internal/domain"
)

func TestInboxBulkReadsPreserveContextKeysAndCurrentRevisions(t *testing.T) {
	a, ctx := fixture(t)
	i := item()
	i.Body = "summary body"
	i.Activity = []domain.Activity{{ID: "history", Body: "retained history"}}
	i.Checks = json.RawMessage(`{"raw":"retained checks"}`)
	i.PR = &domain.PullRequest{ID: i.ID, Checks: []domain.Check{{Name: "keep"}}}
	importOne(t, a, ctx, i)
	if err := a.Store.Get(ctx, "item", i.ID, &i); err != nil {
		t.Fatal(err)
	}
	linked := item()
	linked.ID, linked.Number, linked.Revision = "owner/repo#2", 2, "linked-v1"
	importOne(t, a, ctx, linked)
	if err := a.Store.Get(ctx, "item", linked.ID, &linked); err != nil {
		t.Fatal(err)
	}
	linkedRevision := linked.Revision
	// Lookup keys, rather than optional embedded IDs, define record identity.
	if err := a.Store.Put(ctx, "user", i.ID, domain.UserState{ReadRevision: i.Revision, New: true}, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "ticket", "APP-1", domain.Ticket{Status: "open"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, l := range []domain.Relationship{{ID: "b", Source: i.ID, Target: linked.ID}, {ID: "a", Source: i.ID, Target: "APP-1"}} {
		if err := a.Store.Put(ctx, "relationship", l.ID, l, ""); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := a.Config(ctx)
	assessment := domain.Assessment{Status: "complete", Priority: 1, Revision: i.Revision, Policy: c.PolicyVersion, Context: a.AssessmentContext(ctx, i)}
	if err := a.Store.Put(ctx, "assessment", i.ID, assessment, ""); err != nil {
		t.Fatal(err)
	}
	full, err := a.Inbox(ctx, "all")
	if err != nil {
		t.Fatal(err)
	}
	compact, err := a.InboxSummaries(ctx, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != len(compact) {
		t.Fatal("summary changed membership")
	}
	for n := range full {
		want := full[n]
		if want.Item.ID == i.ID && (want.PrioritySource != "inference" || want.Unread || !want.User.New) {
			t.Fatal("bulk context or record key changed priority/read state", want)
		}
		want.Item.Activity = nil
		want.Item.Checks = nil
		if want.Item.PR != nil {
			pr := *want.Item.PR
			pr.Checks = nil
			want.Item.PR = &pr
		}
		if !reflect.DeepEqual(want, compact[n]) {
			t.Fatal("summary changed non-history fields", want.Item.ID)
		}
	}
	var retained domain.Item
	if err := a.Store.Get(ctx, "item", i.ID, &retained); err != nil || len(retained.Activity) != 1 || len(retained.Checks) == 0 || len(retained.PR.Checks) != 1 {
		t.Fatal("summary destroyed source history", err)
	}
	linked.Revision = "linked-v2"
	if err := a.Store.Put(ctx, "item", linked.ID, linked, ""); err != nil {
		t.Fatal(err)
	}
	rows, err := a.InboxSummaries(ctx, "all")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Item.ID == i.ID && row.PrioritySource == "inference" {
			t.Fatal("bulk reads reused an assessment after linked revision changed")
		}
	}
	linked.Revision = linkedRevision
	if err := a.Store.Put(ctx, "item", linked.ID, linked, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "ticket", "APP-1", domain.Ticket{Status: "closed"}, ""); err != nil {
		t.Fatal(err)
	}
	rows, err = a.InboxSummaries(ctx, "all")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Item.ID == i.ID && row.PrioritySource == "inference" {
			t.Fatal("bulk reads reused an assessment after linked ticket changed")
		}
	}
}
