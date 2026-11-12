package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestRevisionSnapshotsMatchSavedDataAcrossCommits(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Put(ctx, "transcript", "session", map[string]string{"body": "before"}, ""); err != nil {
		t.Fatal(err)
	}
	before := s.Revision("transcript/session")
	err = s.ReadView(ctx, func(view *Store) error {
		if err := s.Put(ctx, "transcript", "session", map[string]string{"body": "after"}, ""); err != nil {
			return err
		}
		var data map[string]string
		if err := view.Get(ctx, "transcript", "session", &data); err != nil {
			return err
		}
		if data["body"] != "before" || view.Revision("transcript/session") != before {
			t.Fatal("snapshot mixed committed revisions with older data", data)
		}
		if s.Revision("transcript/session") <= before {
			t.Fatal("latest commit did not advance revision")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]string
	if err := s.Get(ctx, "transcript", "session", &data); err != nil || data["body"] != "after" {
		t.Fatal("latest data unavailable", data, err)
	}
}

func TestTranscriptRevisionAllocationWithRetainedHistory(t *testing.T) {
	s := &Store{}
	keys := make(map[string]bool, 200001)
	for n := 0; n < 100000; n++ {
		keys[fmt.Sprintf("item/%06d", n)] = true
		keys[fmt.Sprintf("user/%06d", n)] = true
	}
	keys["transcript/session"] = true
	s.publishRevisions(keys)
	snapshot := &Store{}
	snapshot.revisions.Store(s.revisions.Load())
	original := snapshot.Revision("transcript/session")
	changes := map[string]bool{"transcript": true, "transcript/session": true}
	result := testing.Benchmark(func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			s.publishRevisions(changes)
		}
	})
	if result.AllocedBytesPerOp() > 32<<10 {
		t.Fatalf("transcript commit copied retained history: %d bytes/op", result.AllocedBytesPerOp())
	}
	if snapshot.Revision("transcript/session") != original || s.Revision("transcript/session") <= original {
		t.Fatal("publication mutated pinned revisions")
	}
	for n := 0; n < 100000; n++ {
		if s.Revision(fmt.Sprintf("item/%06d", n)) != original || s.Revision(fmt.Sprintf("user/%06d", n)) != original {
			t.Fatal("unrelated revision lost", n)
		}
	}
	t.Logf("200001 retained revision keys: %d bytes/update", result.AllocedBytesPerOp())
}

func TestRelationshipCASEndpointsAdvanceOnlyOnSuccessfulWrites(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first := map[string]string{"id": "link", "source": "a", "target": "b"}
	if changed, err := s.CompareAndSwap(ctx, "relationship", "link", nil, first); err != nil || !changed {
		t.Fatal(changed, err)
	}
	a, b := s.Revision("related/a"), s.Revision("related/b")
	if a == 0 || b != a || s.Revision("related/c") != 0 {
		t.Fatal("CAS insert endpoint revisions missing")
	}
	var previous json.RawMessage
	if err := s.Get(ctx, "relationship", "link", &previous); err != nil {
		t.Fatal(err)
	}
	next := map[string]string{"id": "link", "source": "c", "target": "d"}
	if changed, err := s.CompareAndSwap(ctx, "relationship", "link", previous, next); err != nil || !changed {
		t.Fatal(changed, err)
	}
	current := s.Revision("relationship/link")
	for _, id := range []string{"a", "b", "c", "d"} {
		if s.Revision("related/"+id) != current || current <= a {
			t.Fatal("CAS replacement missed old/new endpoint", id)
		}
	}
	if changed, err := s.CompareAndSwap(ctx, "relationship", "link", previous, first); err != nil || changed {
		t.Fatal("stale CAS changed link", changed, err)
	}
	if changed, err := s.CompareAndSwap(ctx, "relationship", "link", nil, first); err != nil || changed {
		t.Fatal("absence CAS overwrote link", changed, err)
	}
	if s.Revision("relationship/link") != current {
		t.Fatal("rejected CAS advanced revisions")
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if s.Revision("related/"+id) != current {
			t.Fatal("rejected CAS changed endpoint", id)
		}
	}
	if s.Revision("related/unrelated") != 0 {
		t.Fatal("unrelated endpoint changed")
	}
}
