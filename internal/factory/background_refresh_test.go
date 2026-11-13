package factory

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type backgroundDetails struct {
	providers.GitHubSource
	now    func() time.Time
	calls  []string
	fail   map[string]bool
	onCall func(domain.Item)
}

func (g *backgroundDetails) ItemDetail(ctx context.Context, item domain.Item) (domain.Item, error) {
	g.calls = append(g.calls, item.ID)
	if g.onCall != nil {
		g.onCall(item)
	}
	if err := ctx.Err(); err != nil {
		return domain.Item{}, err
	}
	if g.fail[item.ID] {
		return domain.Item{}, fmt.Errorf("fixture provider unavailable")
	}
	item.DetailSyncedAt, item.ReviewObservedAt = g.now(), g.now()
	item.PR = &domain.PullRequest{ID: item.ID, State: item.State, Head: item.Head, SyncedAt: g.now()}
	return item, nil
}

func TestBackgroundDetailTrafficUsesActivityAndDormantDeadlines(t *testing.T) {
	s, _, clock := fixture(t)
	ctx := context.Background()
	c, err := s.Settings(ctx)
	must(t, err)
	c.Rules = nil
	must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
	must(t, s.Store.Put(ctx, "factory-baseline", "o/r", map[string]bool{"ready": true}, ""))
	g := &backgroundDetails{now: s.Now}
	s.App.GitHub = g
	for n, state := range []string{"open", "open", "merged", "open"} {
		item := domain.Item{ID: fmt.Sprintf("o/r#%d", n+1), Repository: "o/r", Number: n + 1, Kind: "pr", State: state, DetailSyncedAt: clock.Add(-20 * time.Minute), Updated: clock.Add(-48 * time.Hour)}
		if n == 3 {
			item.Kind = "issue"
		}
		must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
		must(t, s.Store.Put(ctx, "user", item.ID, domain.UserState{ID: item.ID}, ""))
	}
	must(t, s.Store.Put(ctx, "factory-session", "o/r#1", Session{ID: "o/r#1", ItemID: "o/r#1", Execution: "waiting"}, ""))
	must(t, s.refreshDetails(ctx, c))
	if !slices.Equal(g.calls, []string{"o/r#1"}) {
		t.Fatal("dormant items consumed the active detail budget", g.calls)
	}
	*clock = clock.Add(40 * time.Minute)
	must(t, s.refreshDetails(ctx, c))
	hourly := slices.Clone(g.calls[1:])
	slices.Sort(hourly)
	if !slices.Equal(hourly, []string{"o/r#1", "o/r#2"}) {
		t.Fatal("untracked open PR did not wait for its hourly deadline", g.calls)
	}
	*clock = clock.Add(22*time.Hour + 59*time.Minute)
	must(t, s.refreshDetails(ctx, c))
	if len(g.calls) != 5 {
		t.Fatal("terminal PR or issue refreshed before its daily deadline", g.calls)
	}
	*clock = clock.Add(20 * time.Minute)
	must(t, s.refreshDetails(ctx, c))
	if !slices.Contains(g.calls, "o/r#3") || !slices.Contains(g.calls, "o/r#4") {
		t.Fatal("daily reconciliation dropped dormant work", g.calls)
	}
}

func TestBackgroundDetailsPromoteSummaryChangesRulesAndRecentFocus(t *testing.T) {
	for _, reason := range []string{"summary", "recent", "rule", "focus", "session", "pending-after-done", "running-after-done"} {
		t.Run(reason, func(t *testing.T) {
			s, _, clock := fixture(t)
			ctx := context.Background()
			c, err := s.Settings(ctx)
			must(t, err)
			c.Rules = nil
			item := domain.Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "pr", State: "closed", Updated: clock.Add(-48 * time.Hour), DetailSyncedAt: clock.Add(-20 * time.Minute)}
			switch reason {
			case "summary":
				item.Updated = clock.Add(-time.Minute)
			case "recent":
				item.Updated = clock.Add(-time.Hour)
			case "rule":
				item.State = "open"
				// Relevance cannot depend on labels that may change remotely.
				c.Rules = []Rule{{Repositories: []string{"o/r"}, Types: []string{"pull_request_review"}, Labels: []string{"new-label"}}}
			case "focus":
				must(t, s.Store.Put(ctx, "github-detail-focus", item.ID, map[string]any{"ID": item.ID, "SeenAt": clock.Add(-time.Minute)}, ""))
			case "session":
				must(t, s.Store.Put(ctx, "factory-session", item.ID, Session{ID: item.ID, ItemID: item.ID, Execution: "running"}, ""))
			case "running-after-done":
				must(t, s.Store.Put(ctx, "factory-session", item.ID, Session{ID: item.ID, ItemID: item.ID, Execution: "running", Reported: "done"}, ""))
			case "pending-after-done":
				must(t, s.Store.Put(ctx, "factory-session", item.ID, Session{ID: item.ID, ItemID: item.ID, Execution: "idle", Reported: "done", Due: clock.Add(-time.Minute), Hold: true, Pending: []Pending{{Event: Event{ID: "queued", ItemID: item.ID}, Actions: []Action{{RuleID: "retained"}}}}}, ""))
			}
			must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
			must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
			must(t, s.Store.Put(ctx, "user", item.ID, domain.UserState{ID: item.ID}, ""))
			must(t, s.Store.Put(ctx, "factory-baseline", "o/r", map[string]bool{"ready": true}, ""))
			g := &backgroundDetails{now: s.Now}
			s.App.GitHub = g
			must(t, s.refreshDetails(ctx, c))
			if !slices.Equal(g.calls, []string{item.ID}) {
				t.Fatal("current work did not receive the ordinary detail cadence", g.calls)
			}
			if reason == "pending-after-done" {
				*clock = clock.Add(providers.GitHubDetailTTL)
				must(t, s.refreshDetails(ctx, c))
				if !slices.Equal(g.calls, []string{item.ID, item.ID}) {
					t.Fatal("retained pending work lost its cadence after a successful read", g.calls)
				}
			}
		})
	}
}

func TestBackgroundDetailRotationReservesDormantWorkDespiteHotFailures(t *testing.T) {
	s, _, clock := fixture(t)
	ctx := context.Background()
	c, err := s.Settings(ctx)
	must(t, err)
	c.Rules = nil
	must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
	must(t, s.Store.Put(ctx, "factory-baseline", "o/r", map[string]bool{"ready": true}, ""))
	g := &backgroundDetails{now: s.Now, fail: map[string]bool{}}
	s.App.GitHub = g
	for n := 1; n <= 14; n++ {
		item := domain.Item{ID: fmt.Sprintf("o/r#%02d", n), Repository: "o/r", Number: n, Kind: "pr", State: "open", Updated: clock.Add(-time.Hour), DetailSyncedAt: clock.Add(-25 * time.Hour)}
		if n > 11 {
			item.State, item.Updated = "closed", clock.Add(-48*time.Hour)
		} else {
			g.fail[item.ID] = true
		}
		must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
		must(t, s.Store.Put(ctx, "user", item.ID, domain.UserState{ID: item.ID}, ""))
	}
	// Remove the fixture's differently keyed item before the fairness assertion.
	must(t, s.Store.Write(ctx, func(tx *store.Tx) error { return tx.Delete("item", "o/r#1") }))
	for pass := 0; pass < 3; pass++ {
		before := len(g.calls)
		if err := s.refreshDetails(ctx, c); err == nil {
			t.Fatal("failed hot reads were hidden")
		}
		batch := g.calls[before:]
		if len(batch) != 8 {
			t.Fatal("detail pass did not enforce its eight-attempt budget", batch)
		}
		coldAttempts := 0
		for _, id := range batch {
			if slices.Contains([]string{"o/r#12", "o/r#13", "o/r#14"}, id) {
				coldAttempts++
			}
		}
		if coldAttempts != 1 {
			t.Fatal("full hot queue did not retain seven hot attempts and one dormant attempt", batch)
		}
		if !slices.Contains(batch, fmt.Sprintf("o/r#%02d", 12+pass)) {
			t.Fatal("dormant reconciliation starved", batch)
		}
	}
	for n := 1; n <= 14; n++ {
		if !slices.Contains(g.calls, fmt.Sprintf("o/r#%02d", n)) {
			t.Fatal("rotating attempts starved an item after repeated failures", n, g.calls)
		}
	}
}

func TestBackgroundTerminalReconciliationCountsHTTPAndDetectsReopen(t *testing.T) {
	s, _, clock := fixture(t)
	ctx := context.Background()
	c, err := s.Settings(ctx)
	must(t, err)
	must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
	must(t, s.Store.Put(ctx, "factory-baseline", "o/r", map[string]bool{"ready": true}, ""))
	item := domain.Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "pr", State: "closed", Updated: clock.Add(-48 * time.Hour), DetailSyncedAt: clock.Add(-20 * time.Minute)}
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	must(t, s.Store.Put(ctx, "user", item.ID, domain.UserState{ID: item.ID}, ""))
	must(t, s.Observe(ctx, observations(item), true))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case r.URL.Path == "/repos/o/r/issues/1":
			fmt.Fprint(w, `{"number":1,"state":"open","pull_request":{}}`)
		case r.URL.Path == "/repos/o/r/pulls/1":
			fmt.Fprint(w, `{"number":1,"state":"open","head":{"sha":"head","ref":"topic","repo":{"full_name":"o/r"}},"base":{"sha":"base","ref":"main","repo":{"full_name":"o/r"}}}`)
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		case r.URL.Path == "/graphql":
			fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"headRefOid":"head","reviewDecision":"APPROVED","statusCheckRollup":{"state":"SUCCESS"},"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}},"latestOpinionatedReviews":{"nodes":[],"pageInfo":{"hasNextPage":false}},"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	live := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Now: s.Now, Token: func(context.Context) (string, error) { return "fixture", nil }}
	s.App.GitHub = providers.NewCachedGitHub(live)
	s.App.GitHub.ConfigureUsage(s.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 100}, nil
	})
	for pass := 0; pass < 4; pass++ {
		must(t, s.refreshDetails(ctx, c))
		*clock = clock.Add(15 * time.Minute)
	}
	if requests.Load() != 0 {
		t.Fatal("idle terminal polling consumed HTTP requests", requests.Load())
	}
	*clock = item.DetailSyncedAt.Add(24 * time.Hour)
	must(t, s.refreshDetails(ctx, c))
	if requests.Load() != 10 {
		t.Fatal("daily detail reconciliation did not execute the complete provider read", requests.Load())
	}
	var saved domain.Item
	must(t, s.Store.Get(ctx, "item", item.ID, &saved))
	if saved.State != "open" || saved.Head != "head" || saved.PR == nil || saved.PR.Error != "" {
		t.Fatal("daily terminal reconciliation missed the reopened PR", saved)
	}
	var session Session
	must(t, s.Store.Get(ctx, "factory-session", item.ID, &session))
	found := false
	for _, pending := range session.Pending {
		if pending.Event.Facet == "lifecycle" && pending.Event.Action == "open" && len(pending.Actions) == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("reopened PR did not retain its matching lifecycle rule actions", session.Pending)
	}
}

func TestBackgroundHotDetailPreservesProviderRetryDeadline(t *testing.T) {
	s, _, clock := fixture(t)
	ctx := context.Background()
	c, err := s.Settings(ctx)
	must(t, err)
	must(t, s.Store.Put(ctx, "factory-baseline", "o/r", map[string]bool{"ready": true}, ""))
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	item.DetailSyncedAt = clock.Add(-time.Hour)
	item.PR = &domain.PullRequest{RetryAt: clock.Add(time.Minute)}
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	g := &backgroundDetails{now: s.Now}
	s.App.GitHub = g
	must(t, s.refreshDetails(ctx, c))
	if len(g.calls) != 0 {
		t.Fatal("hot work bypassed the provider retry deadline", g.calls)
	}
	*clock = clock.Add(time.Minute)
	must(t, s.refreshDetails(ctx, c))
	if !slices.Equal(g.calls, []string{item.ID}) {
		t.Fatal("hot work did not resume at the retained retry deadline", g.calls)
	}
}

func TestBackgroundDetailRotationSurvivesCancellation(t *testing.T) {
	for _, slowState := range []string{"hot", "cold"} {
		t.Run(slowState, func(t *testing.T) {
			s, _, clock := fixture(t)
			ctx := context.Background()
			c, err := s.Settings(ctx)
			must(t, err)
			c.Rules = nil
			must(t, s.Store.Put(ctx, "factory-baseline", "o/r", map[string]bool{"ready": true}, ""))
			hot := domain.Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "pr", State: "open", Updated: clock.Add(-time.Hour), DetailSyncedAt: clock.Add(-25 * time.Hour)}
			cold := hot
			cold.ID, cold.Number, cold.State, cold.Updated = "o/r#2", 2, "closed", clock.Add(-48*time.Hour)
			for _, item := range []domain.Item{hot, cold} {
				must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
				must(t, s.Store.Put(ctx, "user", item.ID, domain.UserState{ID: item.ID}, ""))
			}
			g := &backgroundDetails{now: s.Now}
			s.App.GitHub = g
			slowID := hot.ID
			if slowState == "cold" {
				slowID = cold.ID
			}
			for pass := 0; pass < 2; pass++ {
				current, cancel := context.WithCancel(ctx)
				g.onCall = func(item domain.Item) {
					if item.ID == slowID {
						cancel()
					}
				}
				if err := s.refreshDetails(current, c); err == nil {
					t.Fatal("cancelled provider read was hidden")
				}
				cancel()
			}
			if !slices.Contains(g.calls, hot.ID) || !slices.Contains(g.calls, cold.ID) {
				t.Fatal("one work class starved behind repeated cancelled reads", g.calls)
			}
		})
	}
}

func TestBackgroundDetailActivityUsesCompactRuntimeAndPendingCount(t *testing.T) {
	s, _, clock := fixture(t)
	ctx := context.Background()
	pending := []Pending{{Event: Event{ID: "retained", Context: domain.Item{Body: strings.Repeat("q", 100*4096)}}}}
	// An unrelated cached Agent shape must not be decoded by this scheduler.
	// Runtime projection keeps the authoritative activity fields and queue count.
	must(t, s.Store.Put(ctx, "factory-session", "o/r#1", map[string]any{"id": "o/r#1", "itemId": "o/r#1", "execution": "idle", "reported": "done", "due": clock.Add(-time.Hour), "pending": pending, "agent": "unused cached value"}, ""))
	activity, err := s.detailRefreshActivity(ctx)
	must(t, err)
	if !activity["o/r#1"].Equal(*clock) {
		t.Fatal("compact pending count did not retain active work", activity)
	}
	runtime, err := s.Store.Records(ctx, "factory-session-runtime")
	must(t, err)
	if len(runtime["o/r#1"]) > 512 || strings.Contains(string(runtime["o/r#1"]), "retained") {
		t.Fatal("activity runtime retained source payload", len(runtime["o/r#1"]))
	}
}
