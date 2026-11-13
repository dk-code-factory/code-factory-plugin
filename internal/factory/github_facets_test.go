package factory

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
)

type facetHTTPCounts struct {
	Requests atomic.Int32
	History  atomic.Int32
}

func setupFacetHTTP(t *testing.T, s *Service, item domain.Item, comment *string, deny *bool, intercept ...func(http.ResponseWriter, *http.Request) bool) *facetHTTPCounts {
	t.Helper()
	counts := &facetHTTPCounts{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counts.Requests.Add(1)
		w.Header().Set("ETag", `"facet"`)
		if len(intercept) > 0 && intercept[0](w, r) {
			return
		}
		switch {
		case r.URL.Path == "/repos/o/r/issues/1":
			fmt.Fprintf(w, `{"number":1,"state":"open","title":"current PR","updated_at":%q}`, item.Updated.Format(time.RFC3339))
		case r.URL.Path == "/repos/o/r/pulls/1":
			fmt.Fprintf(w, `{"number":1,"state":"open","head":{"sha":%q,"ref":"topic","repo":{"full_name":"o/r"}},"base":{"sha":"base","ref":"main","repo":{"full_name":"o/r"}}}`, item.Head)
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			if *deny {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		case strings.HasSuffix(r.URL.Path, "/statuses"):
			fmt.Fprint(w, `[]`)
		case r.URL.Path == "/graphql":
			fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"headRefOid":%q,"reviewDecision":"APPROVED","statusCheckRollup":{"state":"SUCCESS"},"latestOpinionatedReviews":{"nodes":[],"pageInfo":{"hasNextPage":false}},"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}},"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`, item.Head)
		default:
			counts.History.Add(1)
			if r.URL.Path == "/repos/o/r/issues/1/comments" {
				fmt.Fprintf(w, `[{"id":7,"body":%q,"updated_at":"2026-11-05T11:00:00Z"}]`, *comment)
			} else {
				fmt.Fprint(w, `[]`)
			}
		}
	}))
	t.Cleanup(server.Close)
	g := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Now: s.Now, Token: func(context.Context) (string, error) { return "fixture", nil }}
	g.ConfigureUsage(s.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 1000}, nil
	})
	s.App.GitHub = g
	return counts
}

func savedFacetItem(t *testing.T, s *Service) domain.Item {
	t.Helper()
	var item domain.Item
	must(t, s.Store.Get(context.Background(), "item", "o/r#1", &item))
	item.Updated = s.Now().Add(-30 * time.Minute)
	item.DetailSyncedAt = s.Now().Add(-20 * time.Minute)
	item.ReviewObservedAt = item.DetailSyncedAt
	item.Activity = []domain.Activity{{ID: "comment:7", Kind: "comment", Body: "saved history", Updated: "2026-11-05T11:00:00Z"}}
	item.PR = &domain.PullRequest{Head: item.Head, CI: "success", ReviewDecision: "approved", SyncedAt: item.DetailSyncedAt}
	item.Revision = item.ContentRevision()
	must(t, s.Store.Put(context.Background(), "item", item.ID, item, ""))
	must(t, s.Store.Put(context.Background(), "factory-baseline", item.Repository, map[string]bool{"ready": true}, ""))
	return item
}

func TestRoutineFacetsReduceHTTPWithoutThreeSecondAmplification(t *testing.T) {
	s, _, now := fixture(t)
	ctx := context.Background()
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.Rules = nil
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	item := savedFacetItem(t, s)
	comment, deny := "missed comment", false
	counts := setupFacetHTTP(t, s, item, &comment, &deny)
	must(t, s.refreshDetails(ctx, settings))
	var saved domain.Item
	must(t, s.Store.Get(ctx, "item", item.ID, &saved))
	if counts.Requests.Load() != 5 || counts.History.Load() != 0 || saved.Activity[0].Body != "saved history" || !saved.DetailSyncedAt.Equal(item.DetailSyncedAt) || !saved.ReviewObservedAt.Equal(*now) {
		t.Fatal("state refresh did not retain distinct history freshness", counts.Requests.Load(), counts.History.Load(), saved)
	}
	for n := 0; n < 10; n++ {
		*now = now.Add(3 * time.Second)
		must(t, s.refreshDetails(ctx, settings))
	}
	if counts.Requests.Load() != 5 {
		t.Fatal("old full-history timestamp amplified routine polls", counts.Requests.Load())
	}
	*now = now.Add(15 * time.Minute)
	must(t, s.refreshDetails(ctx, settings))
	if counts.Requests.Load() != 10 || counts.History.Load() != 0 {
		t.Fatal("routine state did not retain independent cadence", counts.Requests.Load(), counts.History.Load())
	}
	*now = now.Add(25 * time.Minute)
	must(t, s.refreshDetails(ctx, settings))
	must(t, s.Store.Get(ctx, "item", item.ID, &saved))
	if counts.Requests.Load() != 20 || counts.History.Load() != 5 || len(saved.Activity) != 1 || saved.Activity[0].Body != comment || !saved.DetailSyncedAt.Equal(*now) {
		t.Fatal("hourly fallback missed complete history", counts.Requests.Load(), counts.History.Load(), saved)
	}
}

func TestRoutineFacetsPromoteHistoryForRulesWorkAndViewers(t *testing.T) {
	for _, reason := range []string{"rule", "work", "viewer"} {
		t.Run(reason, func(t *testing.T) {
			s, _, now := fixture(t)
			ctx := context.Background()
			settings, err := s.Settings(ctx)
			must(t, err)
			settings.Rules = nil
			must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
			item := savedFacetItem(t, s)
			comment, deny := "current discussion", false
			counts := setupFacetHTTP(t, s, item, &comment, &deny)
			must(t, s.refreshDetails(ctx, settings))
			switch reason {
			case "rule":
				settings.Rules = []Rule{{ID: "discussion", Repositories: []string{"*"}, Types: []string{"issue_comment"}, Instructions: "handle discussion"}}
				must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
			case "work":
				must(t, s.Store.Put(ctx, "factory-session", item.ID, newSession(item.ID, "item", item.ID, item.Repository, Agent{}, *now), ""))
			case "viewer":
				must(t, s.Store.Put(ctx, "github-detail-focus", item.ID, map[string]any{"seenAt": *now}, ""))
			}
			must(t, s.refreshDetails(ctx, settings))
			var saved domain.Item
			must(t, s.Store.Get(ctx, "item", item.ID, &saved))
			if counts.Requests.Load() != 14 || counts.History.Load() != 5 || saved.Activity[0].Body != comment || !saved.DetailSyncedAt.Equal(*now) {
				t.Fatal("new history requirement was suppressed by state checkpoint", counts.Requests.Load(), counts.History.Load(), saved)
			}
			if reason == "rule" {
				v, err := s.Session(ctx, item.ID)
				must(t, err)
				if len(v.Pending) != 1 || v.Pending[0].Event.Type != "issue_comment" {
					t.Fatal("discussion rule lost its current-cadence event", v.Pending)
				}
			}
		})
	}
}

func TestRoutineFacetCheckpointRequiresAcceptedSuccessfulObservation(t *testing.T) {
	s, _, now := fixture(t)
	ctx := context.Background()
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.Rules = nil
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	before := savedFacetItem(t, s)
	comment, deny := "discussion", true
	counts := setupFacetHTTP(t, s, before, &comment, &deny)
	if err := s.refreshDetails(ctx, settings); err == nil {
		t.Fatal("failed component was accepted")
	}
	var checkpoint detailFacetCheckpoint
	must(t, s.Store.Get(ctx, "factory-detail-facets", before.ID, &checkpoint))
	if !checkpoint.StateAt.IsZero() || !checkpoint.RetryAt.After(*now) {
		t.Fatal("failed read advanced successful freshness or omitted backoff", checkpoint)
	}
	requests := counts.Requests.Load()
	must(t, s.refreshDetails(ctx, settings))
	if counts.Requests.Load() != requests {
		t.Fatal("failed facets amplified the next polling pass", counts.Requests.Load(), requests)
	}
	retryAt := now.Add(time.Hour)
	must(t, s.deferDetailFacets(ctx, before, retryAt))
	must(t, s.Store.Get(ctx, "factory-detail-facets", before.ID, &checkpoint))
	if !checkpoint.RetryAt.Equal(retryAt) || !checkpoint.StateAt.IsZero() {
		t.Fatal("provider retry deadline became successful freshness or was shortened", checkpoint)
	}
	fresh := before
	fresh.Activity = nil
	fresh.DetailSyncedAt = time.Time{}
	fresh.ReviewObservedAt = *now
	delivery := delivery{ID: "newer-discussion", Type: "issue_comment", State: "pending", At: now.Add(time.Second), Body: bytes(map[string]any{
		"repository": map[string]string{"full_name": "o/r"}, "action": "edited",
		"issue":   map[string]any{"number": 1, "pull_request": map[string]any{}},
		"comment": map[string]any{"id": 7, "body": "webhook edit", "updated_at": "2026-11-05T12:00:01Z"},
	})}
	must(t, s.Store.Put(ctx, "factory-delivery", delivery.ID, delivery, ""))
	must(t, s.processDelivery(ctx, delivery))
	must(t, s.importDetailFacets(ctx, settings.Repositories[0], before, fresh, *now, true))
	var saved domain.Item
	must(t, s.Store.Get(ctx, "item", before.ID, &saved))
	if saved.Activity[0].Body != "webhook edit" || !saved.ReviewObservedAt.Equal(delivery.At) {
		t.Fatal("partial read reversed a concurrent webhook", saved)
	}
	must(t, s.Store.Get(ctx, "factory-detail-facets", before.ID, &checkpoint))
	if !checkpoint.StateAt.IsZero() {
		t.Fatal("rejected snapshot advanced checkpoint", checkpoint, err)
	}
	stop, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.importDetailFacets(stop, settings.Repositories[0], before, fresh, *now, true); err == nil {
		t.Fatal("canceled refresh completed")
	}
}

func TestRoutineStateRuleRefreshesHistoryBeforeQueuingWork(t *testing.T) {
	for _, duringRead := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabledDuringRead=%v", duringRead), func(t *testing.T) {
			s, _, now := fixture(t)
			ctx := context.Background()
			settings, err := s.Settings(ctx)
			must(t, err)
			rules := []Rule{{ID: "ci", Repositories: []string{"*"}, Types: []string{"ci"}, Instructions: "handle CI"}}
			settings.Rules = rules
			if duringRead {
				settings.Rules = nil
			}
			must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
			item := savedFacetItem(t, s)
			item.PR.CI = "pending"
			item.Revision = item.ContentRevision()
			must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
			comment, deny := "discussion needed by CI work", false
			var configured atomic.Bool
			configuration := make(chan error, 1)
			counts := setupFacetHTTP(t, s, item, &comment, &deny, func(_ http.ResponseWriter, r *http.Request) bool {
				if duringRead && r.URL.Path == "/graphql" && configured.CompareAndSwap(false, true) {
					updated := settings
					updated.Rules = rules
					proposal, err := s.Propose(ctx, &updated, nil)
					if err == nil {
						_, err = s.Confirm(ctx, proposal.ID, true)
					}
					configuration <- err
				}
				return false
			})
			must(t, s.refreshDetails(ctx, settings))
			if duringRead {
				select {
				case err := <-configuration:
					must(t, err)
				default:
					t.Fatal("rule was not enabled during provider I/O")
				}
			}
			var saved domain.Item
			must(t, s.Store.Get(ctx, "item", item.ID, &saved))
			v, err := s.Session(ctx, item.ID)
			must(t, err)
			if counts.Requests.Load() != 15 || counts.History.Load() != 5 || saved.Activity[0].Body != comment || !saved.DetailSyncedAt.Equal(*now) || len(v.Pending) != 1 || v.Pending[0].Event.Type != "ci" {
				t.Fatal("state rule queued work without first refreshing its full context", counts.Requests.Load(), counts.History.Load(), saved, v.Pending)
			}
			*now = now.Add(3 * time.Second)
			must(t, s.refreshDetails(ctx, settings))
			if counts.Requests.Load() != 15 {
				t.Fatal("promoted history immediately repeated", counts.Requests.Load())
			}
		})
	}
}

func TestRoutineFacetEnrichmentErrorRetainsSavedDiagnostics(t *testing.T) {
	s, _, now := fixture(t)
	ctx := context.Background()
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.Rules = nil
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	before := savedFacetItem(t, s)
	comment, deny := "discussion", false
	var fail atomic.Bool
	fail.Store(true)
	counts := setupFacetHTTP(t, s, before, &comment, &deny, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/graphql" && fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return true
		}
		return false
	})
	if err := s.refreshDetails(ctx, settings); err == nil {
		t.Fatal("enrichment failure was accepted")
	}
	var saved domain.Item
	must(t, s.Store.Get(ctx, "item", before.ID, &saved))
	var checkpoint detailFacetCheckpoint
	must(t, s.Store.Get(ctx, "factory-detail-facets", before.ID, &checkpoint))
	if saved.PR == nil || saved.PR.Error == "" || saved.PR.CI != before.PR.CI || !saved.PR.SyncedAt.Equal(before.PR.SyncedAt) || !saved.DetailSyncedAt.Equal(before.DetailSyncedAt) || !saved.ReviewObservedAt.Equal(before.ReviewObservedAt) || !checkpoint.StateAt.IsZero() || !checkpoint.RetryAt.After(*now) {
		t.Fatal("saved diagnostic or successful freshness was lost", saved, checkpoint)
	}
	requests := counts.Requests.Load()
	*now = now.Add(3 * time.Second)
	must(t, s.refreshDetails(ctx, settings))
	if counts.Requests.Load() != requests {
		t.Fatal("enrichment failure amplified the next poll")
	}
	fail.Store(false)
	*now = now.Add(15 * time.Minute)
	must(t, s.refreshDetails(ctx, settings))
	saved = domain.Item{}
	must(t, s.Store.Get(ctx, "item", before.ID, &saved))
	must(t, s.Store.Get(ctx, "factory-detail-facets", before.ID, &checkpoint))
	if saved.PR.Error != "" || !saved.ReviewObservedAt.Equal(*now) || !checkpoint.StateAt.Equal(*now) || !checkpoint.RetryAt.IsZero() {
		t.Fatal("successful retry did not recover diagnostics and freshness", *saved.PR, saved.ReviewObservedAt, checkpoint)
	}
}

func TestFreshFacetSelectionDoesNotDecodeRetainedQueue(t *testing.T) {
	s, _, now := fixture(t)
	ctx := context.Background()
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.Rules = nil
	item := savedFacetItem(t, s)
	item.DetailSyncedAt, item.ReviewObservedAt = *now, *now
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	session := newSession(item.ID, "item", item.ID, item.Repository, Agent{}, *now)
	session.Hold = true
	must(t, s.save(ctx, session))
	measure := func() float64 {
		return testing.AllocsPerRun(3, func() {
			history, due, err := s.detailFacetsDue(ctx, settings, item)
			if err != nil || !history || due {
				t.Fatalf("fresh retained work lost its history policy: %v %v %v", history, due, err)
			}
		})
	}
	empty := measure()
	for range 512 {
		session.Pending = append(session.Pending, Pending{Event: Event{Type: "issue_comment", Details: bytes(strings.Repeat("x", 32<<10))}})
	}
	must(t, s.save(ctx, session))
	queued := measure()
	if queued > empty+100 {
		t.Fatalf("fresh selection allocations grew with retained queue: empty=%.0f queued=%.0f", empty, queued)
	}
}

func TestRoutineFacetRecoveryUsesDurableRoutingBaseline(t *testing.T) {
	s, _, now := fixture(t)
	ctx := context.Background()
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.Rules = nil
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	item := savedFacetItem(t, s)
	item.PR.CI = "pending"
	item.Revision = item.ContentRevision()
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	baseline := observations(item)
	must(t, s.Observe(ctx, baseline, true))
	var ci Event
	for _, e := range baseline {
		if e.Type == "ci" {
			ci = e
		}
	}
	if ci.Facet == "" {
		t.Fatal("fixture lacks a durable CI baseline")
	}
	raw, err := sql.Open("sqlite", filepath.Join(s.Dir, "state.db"))
	must(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	_, err = raw.ExecContext(ctx, `CREATE TRIGGER reject_facet_routing BEFORE INSERT ON entities WHEN NEW.kind='factory-observation' BEGIN SELECT RAISE(ABORT,'fixture routing failure'); END`)
	must(t, err)
	comment, deny := "current discussion after interrupted routing", false
	counts := setupFacetHTTP(t, s, item, &comment, &deny)
	if err := s.refreshDetails(ctx, settings); err == nil || !strings.Contains(err.Error(), "fixture routing failure") {
		t.Fatal("routing transaction failure was not stimulated", err)
	}
	var saved domain.Item
	must(t, s.Store.Get(ctx, "item", item.ID, &saved))
	var routed struct{ Value string }
	must(t, s.Store.Get(ctx, "factory-observation", item.ID+":"+ci.Facet, &routed))
	if saved.PR.CI != "success" || !saved.DetailSyncedAt.Equal(item.DetailSyncedAt) || routed.Value != ci.Value || counts.Requests.Load() != 5 || counts.History.Load() != 0 {
		t.Fatal("fixture did not separate committed item and failed routing", saved, routed, counts.Requests.Load(), counts.History.Load())
	}
	_, err = raw.ExecContext(ctx, `DROP TRIGGER reject_facet_routing`)
	must(t, err)
	settings.Rules = []Rule{{ID: "ci", Repositories: []string{"*"}, Types: []string{"ci"}, Instructions: "handle CI"}}
	proposal, err := s.Propose(ctx, &settings, nil)
	must(t, err)
	_, err = s.Confirm(ctx, proposal.ID, true)
	must(t, err)
	*now = now.Add(3 * time.Second)
	must(t, s.refreshDetails(ctx, settings))
	saved = domain.Item{}
	must(t, s.Store.Get(ctx, "item", item.ID, &saved))
	session, err := s.Session(ctx, item.ID)
	must(t, err)
	if counts.Requests.Load() != 20 || counts.History.Load() != 5 || saved.Activity[0].Body != comment || !saved.DetailSyncedAt.Equal(*now) || len(session.Pending) != 1 || session.Pending[0].Event.Type != "ci" {
		t.Fatal("interrupted routing queued old context", counts.Requests.Load(), counts.History.Load(), saved, session.Pending)
	}
}
