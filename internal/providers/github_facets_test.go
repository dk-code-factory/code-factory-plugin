package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codefactory/internal/domain"
)

func TestItemStateSeparatesMutableFacetsFromFullHistory(t *testing.T) {
	now := time.Now().UTC()
	var calls, history atomic.Int32
	head, ci, deny := "head", "SUCCESS", false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("ETag", `"facet"`)
		switch {
		case r.URL.Path == "/repos/o/r/issues/1":
			fmt.Fprint(w, `{"number":1,"state":"open","title":"PR"}`)
		case r.URL.Path == "/repos/o/r/pulls/1":
			fmt.Fprintf(w, `{"number":1,"state":"open","head":{"sha":%q,"ref":"topic","repo":{"full_name":"o/r"}},"base":{"sha":"base","ref":"main","repo":{"full_name":"o/r"}}}`, head)
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			if deny {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		case strings.HasSuffix(r.URL.Path, "/statuses"):
			fmt.Fprint(w, `[]`)
		case r.URL.Path == "/graphql":
			fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"headRefOid":%q,"reviewDecision":"APPROVED","statusCheckRollup":{"state":%q},"latestOpinionatedReviews":{"nodes":[],"pageInfo":{"hasNextPage":false}},"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}},"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`, head, ci)
		default:
			history.Add(1)
			if strings.HasSuffix(r.URL.Path, "/comments") {
				fmt.Fprint(w, `[{"id":7,"body":"saved comment","updated_at":"2026-11-01T10:00:00Z"}]`)
			} else {
				fmt.Fprint(w, `[]`)
			}
		}
	}))
	defer server.Close()
	live := managedFixture(t, server, nil, &now, 100, 0)
	cache := NewCachedGitHub(live)
	cache.clock = func() time.Time { return now }
	item := domain.Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "pr", Head: head}
	ctx := context.Background()
	state, err := RefreshItemState(ctx, cache, item)
	if err != nil || !state.Partial || calls.Load() != 5 || history.Load() != 0 || !state.DetailSyncedAt.IsZero() || !state.ReviewObservedAt.Equal(now) || state.PR.CI != "success" {
		t.Fatal("state read downloaded history or claimed full freshness", state, calls.Load(), history.Load(), err)
	}
	full, err := cache.ItemDetail(ctx, item)
	if err != nil || calls.Load() != 15 || history.Load() != 5 || !full.DetailSyncedAt.Equal(now) || len(full.Activity) != 2 {
		t.Fatal("state cache satisfied full detail", full, calls.Load(), history.Load(), err)
	}
	now = now.Add(time.Minute)
	ci = "FAILURE"
	state, err = RefreshItemState(WithGitHubRead(ctx, RequireLive), cache, item)
	if err != nil || calls.Load() != 20 || state.PR.CI != "failure" || !state.ReviewObservedAt.Equal(now) {
		t.Fatal("same issue timestamp suppressed mutable CI", state, calls.Load(), err)
	}
	deny = true
	if _, err = RefreshItemState(WithGitHubRead(ctx, RequireLive), cache, item); err == nil {
		t.Fatal("live state failure fell back to a saved full snapshot")
	}
	deny = false
	head = "new-head"
	state, err = RefreshItemState(WithGitHubRead(ctx, RequireLive), cache, item)
	if err != nil || state.Partial || state.Head != head || state.DetailSyncedAt.IsZero() || history.Load() != 10 {
		t.Fatal("changed head did not promote complete history", state, history.Load(), err)
	}
}

func TestItemStateFallbackRetainsCompleteSourceContract(t *testing.T) {
	source := &cacheSourceFixture{scope: "fixture"}
	item := domain.Item{ID: "o/r#1", Kind: "pr", Activity: []domain.Activity{{ID: "comment:1"}}, DetailSyncedAt: time.Now()}
	for _, at := range []time.Time{item.DetailSyncedAt, {}} {
		item.DetailSyncedAt = at
		for _, g := range []GitHubSource{source, NewCachedGitHub(source)} {
			fresh, err := RefreshItemState(context.Background(), g, item)
			if err != nil || fresh.Partial || len(fresh.Activity) != 1 || !fresh.DetailSyncedAt.Equal(item.DetailSyncedAt) {
				t.Fatal("optional capability changed full-detail fallback", fresh, err)
			}
		}
	}
	if source.calls != 4 {
		t.Fatal("fallback did not use source detail", source.calls)
	}
}
