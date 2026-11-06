package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"testing"
)

func TestAssessmentContextTracksBothRelationshipDirectionsAndRelatedState(t *testing.T) {
	a, ctx := fixture(t)
	selected := item()
	selected.Body = ""
	selected.Revision = selected.ContentRevision()
	importOne(t, a, ctx, selected)
	related := item()
	related.ID = "owner/repo#2"
	related.Number = 2
	related.Revision = related.ContentRevision()
	if err := a.Store.Put(ctx, "item", related.ID, related, ""); err != nil {
		t.Fatal(err)
	}
	baseline := a.AssessmentContext(ctx, selected)
	for _, incoming := range []bool{false, true} {
		link := domain.Relationship{ID: "local:relation", Source: selected.ID, Target: related.ID, Kind: "local", Manual: true}
		if incoming {
			link.Source, link.Target = link.Target, link.Source
		}
		if err := a.Store.Put(ctx, "relationship", link.ID, link, ""); err != nil {
			t.Fatal(err)
		}
		added := a.AssessmentContext(ctx, selected)
		if added == baseline {
			t.Fatal("relationship direction omitted", incoming)
		}
		related.Body += " changed"
		related.Revision = related.ContentRevision()
		if err := a.Store.Put(ctx, "item", related.ID, related, ""); err != nil {
			t.Fatal(err)
		}
		changed := a.AssessmentContext(ctx, selected)
		if changed == added {
			t.Fatal("linked revision omitted", incoming)
		}
		link.Evidence = "updated evidence"
		if err := a.Store.Put(ctx, "relationship", link.ID, link, ""); err != nil {
			t.Fatal(err)
		}
		if a.AssessmentContext(ctx, selected) == changed {
			t.Fatal("relationship metadata omitted")
		}
		if err := a.Store.Write(ctx, func(tx *store.Tx) error { return tx.Delete("relationship", link.ID) }); err != nil {
			t.Fatal(err)
		}
		if a.AssessmentContext(ctx, selected) != baseline {
			t.Fatal("removed relationship remains in context")
		}
	}
	ticket := domain.Ticket{ID: "PROJ-1", Summary: "first"}
	link := domain.Relationship{ID: "ticket", Source: ticket.ID, Target: selected.ID, Kind: "local"}
	for _, kind := range []string{"relationship", "ticket"} {
		var id string
		var value any
		if kind == "relationship" {
			id, value = link.ID, link
		} else {
			id, value = ticket.ID, ticket
		}
		if err := a.Store.Put(ctx, kind, id, value, ""); err != nil {
			t.Fatal(err)
		}
	}
	before := a.AssessmentContext(ctx, selected)
	ticket.Status = "urgent"
	if err := a.Store.Put(ctx, "ticket", ticket.ID, ticket, ""); err != nil {
		t.Fatal(err)
	}
	after := a.AssessmentContext(ctx, selected)
	if before == after {
		t.Fatal("incoming ticket changes omitted")
	}
	unrelated := domain.Relationship{ID: "unrelated", Source: "other#1", Target: "other#2"}
	if err := a.Store.Put(ctx, "relationship", unrelated.ID, unrelated, ""); err != nil {
		t.Fatal(err)
	}
	if a.AssessmentContext(ctx, selected) != after {
		t.Fatal("unrelated relationship invalidated assessment")
	}
	links := []domain.Relationship{link, {ID: "outgoing", Source: selected.ID, Target: related.ID}}
	getItem := func(id string) (domain.Item, error) {
		var i domain.Item
		e := a.Store.Get(ctx, "item", id, &i)
		return i, e
	}
	getTicket := func(id string) (domain.Ticket, error) {
		var i domain.Ticket
		e := a.Store.Get(ctx, "ticket", id, &i)
		return i, e
	}
	ordered := assessmentContext(selected, links, getItem, getTicket)
	links[0], links[1] = links[1], links[0]
	if assessmentContext(selected, links, getItem, getTicket) != ordered {
		t.Fatal("relationship enumeration changes context")
	}
}
