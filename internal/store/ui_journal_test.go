package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIJournalCommitsRollbackCASAndLegacyCursor(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fail := errors.New("rollback")
	err = s.Write(ctx, func(tx *Tx) error {
		if err := tx.Put("item", "one", map[string]string{"body": "private retained body"}); err != nil {
			return err
		}
		return fail
	})
	if !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if n, _ := s.UILatest(ctx); n != 0 {
		t.Fatal("rollback emitted an invalidation")
	}
	if err := s.Put(ctx, "item", "one", map[string]string{"body": "private retained body"}, "item.changed"); err != nil {
		t.Fatal(err)
	}
	if s.Latest(ctx) != 1 {
		t.Fatal("UI journal changed the legacy event cursor")
	}
	n, err := s.UILatest(ctx)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	changes, err := s.UIChanges(ctx, 0, n)
	if err != nil || !changes["item"]["one"] {
		t.Fatal(changes, err)
	}
	data, _ := json.Marshal(changes)
	if strings.Contains(string(data), "private retained body") {
		t.Fatal("invalidation included entity data")
	}
	if err := s.Put(ctx, "secret", "owner", map[string]string{"token": "never public"}, ""); err != nil {
		t.Fatal(err)
	}
	if now, _ := s.UILatest(ctx); now != n {
		t.Fatal("private bucket disclosed")
	}
	if changed, err := s.CompareAndSwap(ctx, "item", "one", nil, map[string]string{}); changed || err != nil {
		t.Fatal(changed, err)
	}
	if now, _ := s.UILatest(ctx); now != n {
		t.Fatal("failed CAS emitted invalidation")
	}
	records, _ := s.Records(ctx, "item")
	if changed, err := s.CompareAndSwap(ctx, "item", "one", records["one"], map[string]string{"body": "new"}); !changed || err != nil {
		t.Fatal(changed, err)
	}
	if now, _ := s.UILatest(ctx); now != n+1 {
		t.Fatal("CAS missing invalidation")
	}
	if err := s.Put(ctx, "observation", "detail:one", map[string]string{"error": "provider failed"}, ""); err != nil {
		t.Fatal(err)
	}
	latest, _ := s.UILatest(ctx)
	changes, err = s.UIChanges(ctx, n+1, latest)
	if err != nil || !changes["observation"]["one"] || changes["observation"]["detail:one"] {
		t.Fatal("observation did not invalidate its owning item", changes, err)
	}
	if err := s.Put(ctx, "pr-review-policy", "one", map[string]bool{"autoPostReview": true}, "review.policy"); err != nil {
		t.Fatal(err)
	}
	policyCursor, _ := s.UILatest(ctx)
	changes, err = s.UIChanges(ctx, latest, policyCursor)
	if err != nil || !changes["pr-review-policy"]["one"] {
		t.Fatal("review policy did not invalidate its owning item", changes, err)
	}
	data, _ = json.Marshal(changes)
	if strings.Contains(string(data), "autoPostReview") {
		t.Fatal("policy invalidation included stored policy data")
	}

}

func TestReadViewPinsDataAndCursorAcrossConcurrentConnection(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.Put(ctx, "item", "one", "before", ""); err != nil {
		t.Fatal(err)
	}
	if err := first.ReadView(ctx, func(view *Store) error {
		cursor, err := view.UILatest(ctx)
		if err != nil {
			return err
		}
		if err := second.Put(ctx, "item", "one", "after", ""); err != nil {
			return err
		}
		var value string
		if err := view.Get(ctx, "item", "one", &value); err != nil {
			return err
		}
		end, err := view.UILatest(ctx)
		if value != "before" || cursor != end {
			t.Fatal("publication mixed transactions", value, cursor, end)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := first.Get(ctx, "item", "one", &value); err != nil || value != "after" {
		t.Fatal("concurrent commit lost", value, err)
	}
}
