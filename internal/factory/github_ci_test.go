package factory

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
)

type ciHTTPFixture struct {
	Requests        atomic.Int32
	Clock           atomic.Int64
	Head            atomic.Value
	CI              atomic.Value
	Token           atomic.Value
	ExpectedToken   atomic.Value
	WrongCredential atomic.Bool
	Fail            atomic.Bool
	OnRead          func()
}

func ciHTTPService(t *testing.T) (*Service, *ciHTTPFixture) {
	t.Helper()
	s, _, clock := fixture(t)
	f := &ciHTTPFixture{}
	f.Clock.Store(clock.UnixNano())
	f.Head.Store("head-1")
	f.CI.Store("success")
	f.Token.Store("fixture-one")
	f.ExpectedToken.Store("")
	s.Now = func() time.Time { return time.Unix(0, f.Clock.Load()).UTC() }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.Requests.Add(1)
		if expected := f.ExpectedToken.Load().(string); expected != "" && r.Header.Get("Authorization") != "Bearer "+expected {
			f.WrongCredential.Store(true)
		}
		if f.Fail.Load() {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		if f.OnRead != nil {
			f.OnRead()
		}
		switch {
		case r.URL.Path == "/graphql":
			fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"headRefOid":%q,"reviewDecision":"APPROVED","statusCheckRollup":{"state":%q},"latestOpinionatedReviews":{"nodes":[],"pageInfo":{"hasNextPage":false}},"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}},"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`, f.Head.Load(), strings.ToUpper(f.CI.Load().(string)))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			fmt.Fprintf(w, `{"total_count":1,"check_runs":[{"name":"build","status":"completed","conclusion":%q}]}`, f.CI.Load())
		case strings.HasSuffix(r.URL.Path, "/statuses"):
			fmt.Fprint(w, `[]`)
		case strings.Contains(r.URL.Path, "/pulls/"):
			parts := strings.Split(r.URL.Path, "/")
			number, err := strconv.Atoi(parts[len(parts)-1])
			if err != nil {
				t.Errorf("unexpected provider request: %s", r.URL.Path)
			}
			fmt.Fprintf(w, `{"number":%d,"state":"open","title":"current PR","updated_at":%q,"head":{"sha":%q}}`, number, time.Unix(0, f.Clock.Load()).UTC().Format(time.RFC3339Nano), f.Head.Load())
		default:
			t.Errorf("unexpected provider request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	g := providers.NewGitHub()
	g.BaseURL, g.Client = server.URL, server.Client()
	g.Token = func(context.Context) (string, error) { return f.Token.Load().(string), nil }
	s.App.GitHub = providers.NewCachedGitHub(g)
	s.App.GitHub.ConfigureUsage(s.Store, func(context.Context) (providers.GitHubPolicy, error) {
		return providers.GitHubPolicy{RequestsPerHour: 1000}, nil
	})
	settings, err := s.Settings(context.Background())
	must(t, err)
	settings.Rules = []Rule{{ID: "ci", Repositories: []string{"*"}, Types: []string{"ci"}, Instructions: "Observe CI changes"}}
	must(t, s.Store.Put(context.Background(), "factory-settings", "effective", settings, ""))
	return s, f
}

func queueCI(t *testing.T, s *Service, id, typ, head string, at time.Time, numbers ...int) delivery {
	t.Helper()
	p := map[string]any{"repository": map[string]string{"full_name": "o/r"}}
	prs := []map[string]int{}
	for _, n := range numbers {
		prs = append(prs, map[string]int{"number": n})
	}
	if typ == "status" {
		p["sha"] = head
	} else {
		p[typ] = map[string]any{"head_sha": head, "pull_requests": prs}
	}
	d := delivery{ID: id, Type: typ, State: "pending", At: at, Body: bytes(p)}
	must(t, s.Store.Put(context.Background(), "factory-delivery", id, d, ""))
	return d
}

func TestCIWebhookBurstSharesLiveRequestsAcrossDeliveryPages(t *testing.T) {
	s, f := ciHTTPService(t)
	ctx := context.Background()
	second := domain.Item{ID: "o/r#2", Repository: "o/r", Number: 2, Kind: "pr", State: "open", Head: "head-1"}
	second.Revision = second.ContentRevision()
	must(t, s.Store.Put(ctx, "item", second.ID, second, ""))
	kinds := []string{"check_run", "check_suite", "workflow_run", "status"}
	for n := 0; n < 70; n++ {
		queueCI(t, s, fmt.Sprintf("burst-%03d", n), kinds[n%len(kinds)], "head-1", s.Now().Add(-time.Second), 1, 2)
	}
	must(t, s.Poll(ctx))
	if got := f.Requests.Load(); got != 8 {
		t.Fatalf("70 CI deliveries for two PRs made %d requests; want one four-request live read per PR", got)
	}
	for n := 0; n < 70; n++ {
		var d delivery
		must(t, s.Store.Get(ctx, "factory-delivery", fmt.Sprintf("burst-%03d", n), &d))
		if d.State != "processed" {
			t.Fatal("coalesced delivery did not commit its processing", d)
		}
	}
	sessions, err := s.Sessions(ctx)
	must(t, err)
	if len(sessions) != 2 {
		t.Fatal("CI burst lost a PR association", sessions)
	}
	for _, session := range sessions {
		if len(session.Pending) != 1 || session.Pending[0].Event.Type != "ci" {
			t.Fatal("CI burst duplicated or lost its rule observation", session)
		}
	}
	// No snapshot survives a poll boundary, even with an unchanged head.
	f.CI.Store("failure")
	f.Clock.Add(int64(time.Second))
	queueCI(t, s, "later-failure", "check_run", "head-1", s.Now().Add(-time.Millisecond), 1)
	must(t, s.Poll(ctx))
	if got := f.Requests.Load(); got != 16 { // Both current-head PRs must refresh.
		t.Fatalf("next poll reused an earlier CI snapshot: %d requests", got)
	}
	for _, id := range []string{"o/r#1", "o/r#2"} {
		var saved domain.Item
		must(t, s.Store.Get(ctx, "item", id, &saved))
		if saved.PR == nil || saved.PR.CI != "failure" {
			t.Fatal("later CI transition was suppressed", saved)
		}
		session, err := s.Session(ctx, id)
		must(t, err)
		if len(session.Pending) != 2 {
			t.Fatal("later CI rule observation was suppressed", session.Pending)
		}
	}
}

func TestCIWebhookBatchRefreshesDeliveriesReceivedDuringRead(t *testing.T) {
	s, f := ciHTTPService(t)
	ctx := context.Background()
	// A full first page makes Poll visit another page after the read, where
	// the delivery received by the in-flight request is now waiting.
	for n := 0; n < 64; n++ {
		queueCI(t, s, fmt.Sprintf("before-%02d", n), "check_run", "head-1", s.Now().Add(-time.Second), 1)
	}
	var added atomic.Bool
	f.OnRead = func() {
		if added.CompareAndSwap(false, true) {
			f.Clock.Add(int64(time.Second))
			queueCI(t, s, "during", "workflow_run", "head-1", s.Now(), 1)
		}
	}
	must(t, s.Poll(ctx))
	if got := f.Requests.Load(); got != 8 {
		t.Fatalf("delivery received during the first provider read reused that read: %d requests", got)
	}
}

func TestCIWebhookBatchEqualArrivalTimesReadLive(t *testing.T) {
	s, f := ciHTTPService(t)
	for _, id := range []string{"equal-1", "equal-2"} {
		queueCI(t, s, id, "check_run", "head-1", s.Now(), 1)
	}
	must(t, s.Poll(context.Background()))
	if got := f.Requests.Load(); got != 8 {
		t.Fatalf("ambiguous equal timestamps reused a provider read: %d requests", got)
	}
}

func TestCIWebhookBatchFailureRemainsDurableAndRetryable(t *testing.T) {
	s, f := ciHTTPService(t)
	ctx := context.Background()
	f.Fail.Store(true)
	for _, id := range []string{"retry-1", "retry-2"} {
		queueCI(t, s, id, "check_run", "head-1", s.Now().Add(-time.Second), 1)
	}
	must(t, s.Poll(ctx))
	if got := f.Requests.Load(); got != 2 {
		t.Fatalf("failed refresh was cached: %d requests", got)
	}
	for _, id := range []string{"retry-1", "retry-2"} {
		var d delivery
		must(t, s.Store.Get(ctx, "factory-delivery", id, &d))
		if d.State != "pending" || d.Error == "" || d.RetryAt.IsZero() {
			t.Fatal("failed CI delivery lost durable retry", d)
		}
	}
	f.Fail.Store(false)
	f.Clock.Add(int64(time.Minute))
	must(t, s.Poll(ctx))
	if got := f.Requests.Load(); got != 6 {
		t.Fatalf("retry burst did not share one successful fresh read: %d requests", got)
	}
}

func TestCIWebhookBatchSeparatesHeads(t *testing.T) {
	s, f := ciHTTPService(t)
	ctx := context.Background()
	batch := &ciRefreshBatch{Snapshots: map[ciRefreshKey]*ciRefreshSnapshot{}}
	ctx = context.WithValue(ctx, ciRefreshContext{}, batch)
	first := queueCI(t, s, "head-first", "check_run", "head-1", s.Now().Add(-time.Second), 1)
	must(t, s.processDelivery(ctx, first))
	f.Head.Store("head-2")
	d := delivery{ID: "synchronize", Type: "pull_request", State: "pending", At: s.Now(), Body: bytes(map[string]any{"repository": map[string]string{"full_name": "o/r"}, "action": "synchronize", "pull_request": map[string]any{"number": 1, "state": "open", "updated_at": s.Now().Add(time.Second), "head": map[string]string{"sha": "head-2"}}})}
	must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
	must(t, s.processDelivery(ctx, d))
	stale := queueCI(t, s, "head-old", "check_run", "head-1", s.Now().Add(-time.Second), 1)
	must(t, s.processDelivery(ctx, stale))
	var saved domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &saved))
	if saved.Head != "head-2" {
		t.Fatal("old-head CI snapshot reversed a lifecycle delivery", saved)
	}
	fresh := queueCI(t, s, "head-new", "check_suite", "head-2", s.Now().Add(-time.Second), 1)
	must(t, s.processDelivery(ctx, fresh))
	if got := f.Requests.Load(); got != 12 {
		t.Fatalf("head/lifecycle changes reused another observation: %d requests", got)
	}
}

func TestCIWebhookBatchInvalidatesAfterLifecycleWithUnchangedVersion(t *testing.T) {
	s, f := ciHTTPService(t)
	ctx := context.WithValue(context.Background(), ciRefreshContext{}, &ciRefreshBatch{Snapshots: map[ciRefreshKey]*ciRefreshSnapshot{}})
	at := s.Now().Add(-time.Second)
	first := queueCI(t, s, "lifecycle-first", "check_run", "head-1", at, 1)
	must(t, s.processDelivery(ctx, first))
	var before, after domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &before))
	// This older lifecycle payload retains the saved content. Its receipt time
	// also matches, so neither head nor saved-version guards can invalidate it.
	d := delivery{ID: "same-head-edited", Type: "pull_request", State: "pending", At: at, Body: bytes(map[string]any{"repository": map[string]string{"full_name": "o/r"}, "action": "edited", "pull_request": map[string]any{"number": 1, "state": "open", "head": map[string]string{"sha": "head-1"}}})}
	must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
	must(t, s.processDelivery(ctx, d))
	must(t, s.Store.Get(ctx, "item", before.ID, &after))
	if before.Head != after.Head || ciVersion(before) != ciVersion(after) {
		t.Fatal("fixture changed an independent snapshot-reuse guard", before, after)
	}
	f.CI.Store("failure")
	next := queueCI(t, s, "lifecycle-next", "check_suite", "head-1", at, 1)
	must(t, s.processDelivery(ctx, next))
	must(t, s.Store.Get(ctx, "item", before.ID, &after))
	if f.Requests.Load() != 8 || after.PR == nil || after.PR.CI != "failure" {
		t.Fatal("intervening lifecycle delivery failed to invalidate the snapshot", f.Requests.Load(), after)
	}
}

func TestCIWebhookBatchPinsCredentialScopeToLiveRead(t *testing.T) {
	s, f := ciHTTPService(t)
	ctx := context.WithValue(context.Background(), ciRefreshContext{}, &ciRefreshBatch{Snapshots: map[ciRefreshKey]*ciRefreshSnapshot{}})
	f.ExpectedToken.Store("fixture-one")
	f.Token.Store("fixture-two")
	var resolutions atomic.Int32
	providers.LiveGitHub(s.App.GitHub).Token = func(context.Context) (string, error) {
		if resolutions.Add(1) == 1 {
			return "fixture-one", nil
		}
		return f.Token.Load().(string), nil
	}
	first := queueCI(t, s, "credential-first", "check_run", "head-1", s.Now().Add(-time.Second), 1)
	must(t, s.processDelivery(ctx, first))
	if f.WrongCredential.Load() {
		t.Fatal("live observation used a different credential from its snapshot identity")
	}
	// Return to the first identity after the read. A second independent token
	// lookup during the read must not have retained fixture-two under this key.
	f.Token.Store("fixture-one")
	next := queueCI(t, s, "credential-return", "check_suite", "head-1", s.Now().Add(-time.Second), 1)
	must(t, s.processDelivery(ctx, next))
	if f.Requests.Load() != 4 || f.WrongCredential.Load() {
		t.Fatal("same-identity delivery failed safe snapshot reuse", f.Requests.Load())
	}
}

func TestCIWebhookBatchSeparatesCredentialsAndSavedObservations(t *testing.T) {
	s, f := ciHTTPService(t)
	ctx := context.WithValue(context.Background(), ciRefreshContext{}, &ciRefreshBatch{Snapshots: map[ciRefreshKey]*ciRefreshSnapshot{}})
	for n := 0; n < 3; n++ {
		if n == 1 {
			f.Token.Store("fixture-two")
		}
		if n == 2 {
			var saved domain.Item
			must(t, s.Store.Get(ctx, "item", "o/r#1", &saved))
			saved.Title = "new concurrent context"
			saved.Revision = saved.ContentRevision()
			must(t, s.Store.Put(ctx, "item", saved.ID, saved, ""))
		}
		d := queueCI(t, s, fmt.Sprintf("scope-%d", n), "check_run", "head-1", s.Now().Add(-time.Second), 1)
		must(t, s.processDelivery(ctx, d))
	}
	if got := f.Requests.Load(); got != 12 {
		t.Fatalf("new credential/saved observation reused an earlier snapshot: %d requests", got)
	}
}

func TestCIWebhookBatchWithoutHeadNeverSharesSnapshot(t *testing.T) {
	s, f := ciHTTPService(t)
	ctx := context.WithValue(context.Background(), ciRefreshContext{}, &ciRefreshBatch{Snapshots: map[ciRefreshKey]*ciRefreshSnapshot{}})
	for _, id := range []string{"unknown-1", "unknown-2"} {
		d := queueCI(t, s, id, "check_run", "", s.Now().Add(-time.Second), 1)
		must(t, s.processDelivery(ctx, d))
	}
	if got := f.Requests.Load(); got != 8 {
		t.Fatalf("unknown-head deliveries shared an unvalidated snapshot: %d requests", got)
	}
	var saved domain.Item
	must(t, s.Store.Get(context.Background(), "item", "o/r#1", &saved))
	if saved.PR != nil {
		t.Fatal("unknown-head delivery applied unassociated CI", saved)
	}
}
