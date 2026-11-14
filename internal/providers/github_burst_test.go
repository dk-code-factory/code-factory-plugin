package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codefactory/internal/store"
)

func TestGitHubThrottleDeadlineUsesApplicableServerHeaders(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, after, remaining, reset string
		want                          time.Duration
	}{
		{name: "secondary fallback", want: time.Minute},
		{name: "seconds shorter than fallback", after: "10", want: 10 * time.Second},
		{name: "date shorter than fallback", after: now.Add(20 * time.Second).Format(http.TimeFormat), want: 20 * time.Second},
		{name: "primary reset shorter than fallback", remaining: "0", reset: fmt.Sprint(now.Add(5 * time.Second).Unix()), want: 6 * time.Second},
		{name: "unexhausted primary does not postpone secondary", remaining: "50", reset: fmt.Sprint(now.Add(time.Hour).Unix()), want: time.Minute},
		{name: "both applicable deadlines", remaining: "0", reset: fmt.Sprint(now.Add(120 * time.Second).Unix()), after: "90", want: 121 * time.Second},
		{name: "invalid seconds", after: "-1", want: time.Minute},
		{name: "invalid date", after: "later", want: time.Minute},
		{name: "expired date is not a retry deadline", after: now.Add(-time.Second).Format(http.TimeFormat), want: time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("Retry-After", tc.after)
			h.Set("X-RateLimit-Remaining", tc.remaining)
			h.Set("X-RateLimit-Reset", tc.reset)
			if got := retryTime(h, now); !got.Equal(now.Add(tc.want)) {
				t.Fatalf("retryTime = %v, want %v", got, now.Add(tc.want))
			}
			if got := retryDeadline(h, now); !got.Equal(now.Add(tc.want)) {
				t.Fatalf("publication retry deadline = %v", got)
			}
		})
	}
}

func TestGitHubBurstPointsAndContentAreDistinctPersistentAllowances(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":true}`) }))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, tc := range []struct {
		name     string
		resource string
		points   int
		content  bool
		seed     []githubBurstCall
		wait     time.Duration
	}{
		{name: "REST points", resource: "core", points: 1, seed: []githubBurstCall{{At: now.Add(-30 * time.Second), Resource: "core", Points: 900}}, wait: 30 * time.Second},
		{name: "REST mutation costs five", resource: "core", points: 5, content: true, seed: []githubBurstCall{{At: now.Add(-30 * time.Second), Resource: "core", Points: 896}}, wait: 30 * time.Second},
		{name: "GraphQL points separate", resource: "graphql", points: 1, seed: []githubBurstCall{{At: now.Add(-30 * time.Second), Resource: "graphql", Points: 2000}}, wait: 30 * time.Second},
		{name: "REST does not consume GraphQL allowance", resource: "graphql", points: 1, seed: []githubBurstCall{{At: now, Resource: "core", Points: 900}}},
		{name: "GraphQL does not consume REST allowance", resource: "core", points: 1, seed: []githubBurstCall{{At: now, Resource: "graphql", Points: 2000}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := db.Put(ctx, "github-usage", "application", githubUsage{Burst: tc.seed}, ""); err != nil {
				t.Fatal(err)
			}
			client := managedFixture(t, server, db, &now, 10000, 0)
			err := client.beginCostedRequest(ctx, "fixture-secret-token", tc.resource, "/fixture", tc.points, tc.content)
			if tc.wait == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var rate *RateLimitError
			if !errors.As(err, &rate) || !rate.Until.Equal(now.Add(tc.wait)) || !strings.Contains(rate.Reason, "point") {
				t.Fatalf("wrong admission: %v", err)
			}
			// Reconstruct the client before attempting the same durable admission.
			client = managedFixture(t, server, db, &now, 10000, 0)
			if err := client.beginCostedRequest(ctx, "another-principal", tc.resource, "/different-endpoint", tc.points, tc.content); !errors.As(err, &rate) {
				t.Fatalf("restart/principal bypass: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name      string
		count     int
		age, wait time.Duration
	}{
		{name: "minute", count: 80, age: 30 * time.Second, wait: 30 * time.Second},
		{name: "hour", count: 500, age: 30 * time.Minute, wait: 30 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := make([]githubBurstCall, tc.count)
			for i := range calls {
				calls[i] = githubBurstCall{At: now.Add(-tc.age), Resource: "core", Points: 5, Content: true}
			}
			if err := db.Put(ctx, "github-usage", "application", githubUsage{Burst: calls}, ""); err != nil {
				t.Fatal(err)
			}
			client := managedFixture(t, server, db, &now, 10000, 0)
			var rate *RateLimitError
			if err := client.beginCostedRequest(ctx, "fixture", "graphql", "/graphql", 5, true); !errors.As(err, &rate) || !rate.Until.Equal(now.Add(tc.wait)) || !strings.Contains(rate.Reason, "content") {
				t.Fatalf("content admission: %v", err)
			}
			if err := client.beginRequest(ctx, "fixture", "core", "/read"); err != nil {
				t.Fatalf("content blocked safe read: %v", err)
			}
			later := now.Add(tc.wait)
			client.clock = func() time.Time { return later }
			if err := client.beginCostedRequest(ctx, "fixture", "core", "/mutation", 5, true); err != nil {
				t.Fatalf("expired window did not recover: %v", err)
			}
		})
	}
}

func TestGitHubSharedContentAdmissionReservesBeforeMutations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	seed := make([]githubBurstCall, 79)
	for i := range seed {
		seed[i] = githubBurstCall{At: now, Resource: "core", Points: 5, Content: true}
	}
	if err := first.Put(ctx, "github-usage", "application", githubUsage{Burst: seed}, ""); err != nil {
		t.Fatal(err)
	}
	clients := []*GitHub{managedFixture(t, server, first, &now, 10000, 0), managedFixture(t, server, second, &now, 10000, 0)}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, g := range clients {
		wg.Add(1)
		go func(g *GitHub) {
			defer wg.Done()
			_, _, err := g.Request(ctx, "POST", "/issues", map[string]string{"title": "fixture"}, "")
			results <- err
		}(g)
	}
	wg.Wait()
	close(results)
	successes, limited := 0, 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		var rate *RateLimitError
		var request *RequestError
		if !errors.As(err, &rate) || !errors.As(err, &request) || request.Phase != BeforeDispatch {
			t.Fatalf("wrong mutation outcome: %v", err)
		}
		limited++
	}
	if successes != 1 || limited != 1 || calls.Load() != 1 {
		t.Fatalf("duplicate content admission: success=%d limited=%d sends=%d", successes, limited, calls.Load())
	}
}

func TestGitHubRepeatedSecondaryBackoffSurvivesRestartAcrossTransports(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	var mode atomic.Int32
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if mode.Load() == 1 {
			w.Header().Set("Retry-After", "5")
		}
		if mode.Load() == 2 {
			fmt.Fprint(w, `{"ok":true}`)
			return
		}
		if r.URL.Path == "/graphql" {
			fmt.Fprint(w, `{"errors":[{"extensions":{"code":"RATE_LIMITED"},"message":"secondary rate limit"}]}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"You have exceeded a secondary rate limit."}`)
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for attempt := 0; attempt < 7; attempt++ {
		g := managedFixture(t, server, db, &now, 10000, 0)
		var out any
		switch attempt % 4 {
		case 0:
			_, err = g.get(ctx, "/rest", &out)
		case 1:
			_, _, err = g.Request(ctx, "GET", "/generic", nil, "")
		case 2:
			err = g.graph(ctx, "query{viewer{login}}", nil, &out)
		case 3:
			_, _, err = g.Request(ctx, "POST", "/graphql", map[string]string{"query": "query{viewer{login}}"}, "")
		}
		var rate *RateLimitError
		want := time.Minute * time.Duration(1<<uint(min(attempt, 5)))
		if !errors.As(err, &rate) || !rate.Until.Equal(now.Add(want)) {
			t.Fatalf("attempt%d backoff: %v, want %v", attempt, err, now.Add(want))
		}
		// Both REST and GraphQL stay paused, with no added upstream calls.
		before := calls.Load()
		_, _, blocked := g.Request(ctx, "POST", "/mutation", nil, "")
		if !errors.As(blocked, &rate) || calls.Load() != before {
			t.Fatalf("mutation bypassed cooldown: %v", blocked)
		}
		now = now.Add(want)
	}
	mode.Store(1)
	g := managedFixture(t, server, db, &now, 10000, 0)
	_, _, err = g.Request(ctx, "GET", "/explicit", nil, "")
	var rate *RateLimitError
	if !errors.As(err, &rate) || !rate.Until.Equal(now.Add(5*time.Second)) {
		t.Fatalf("explicit Retry-After replaced by exponential: %v", err)
	}
	now = now.Add(5 * time.Second)
	mode.Store(2)
	g = managedFixture(t, server, db, &now, 10000, 0)
	if _, _, err = g.Request(ctx, "GET", "/recover", nil, ""); err != nil {
		t.Fatal(err)
	}
	saved, err := g.usage(ctx)
	if err != nil || saved.Strikes != 0 || !saved.SecondaryUntil.IsZero() {
		t.Fatalf("success did not reset backoff: %+v %v", saved, err)
	}
}

func TestGitHubBurstAdmissionCoordinatesSeparateProcesses(t *testing.T) {
	if path := os.Getenv("CODE_FACTORY_BURST_TEST_DB"); path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		db, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
		g := &GitHub{BaseURL: os.Getenv("CODE_FACTORY_BURST_TEST_URL"), Client: &http.Client{Timeout: time.Second}, Token: func(context.Context) (string, error) { return "fixture-secret-token", nil }, clock: func() time.Time { return now }}
		g.ConfigureUsage(db, func(context.Context) (GitHubPolicy, error) { return GitHubPolicy{RequestsPerHour: 10000}, nil })
		_, _, err = g.Request(ctx, "POST", "/mutation", map[string]string{"title": "fixture"}, "")
		var rate *RateLimitError
		if os.Getenv("CODE_FACTORY_BURST_TEST_ALLOWED") == "1" {
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.As(err, &rate) || !rate.Until.Equal(now.Add(time.Minute)) {
			t.Fatalf("process bypassed retained admission: %v", err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seed := make([]githubBurstCall, 80)
	for i := range seed {
		seed[i] = githubBurstCall{At: now, Resource: "core", Points: 5, Content: true}
	}
	if err := db.Put(ctx, "github-usage", "application", githubUsage{Burst: seed}, ""); err != nil {
		t.Fatal(err)
	}
	run := func(allowed bool) {
		t.Helper()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGitHubBurstAdmissionCoordinatesSeparateProcesses$", "-test.count=1")
		flag := "0"
		if allowed {
			flag = "1"
		}
		command.Env = append(os.Environ(), "CODE_FACTORY_BURST_TEST_DB="+path, "CODE_FACTORY_BURST_TEST_URL="+server.URL, "CODE_FACTORY_BURST_TEST_ALLOWED="+flag)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("subprocess fixture: %v: %s", err, output)
		}
	}
	run(false)
	if calls.Load() != 0 {
		t.Fatal("blocked subprocess dispatched a mutation")
	}
	// A successful subprocess request after expiration proves it opened this
	// exact durable store and transport, rather than passing on a local error.
	for i := range seed {
		seed[i].At = now.Add(-time.Minute)
	}
	if err := db.Put(ctx, "github-usage", "application", githubUsage{Burst: seed}, ""); err != nil {
		t.Fatal(err)
	}
	run(true)
	if calls.Load() != 1 {
		t.Fatalf("expired process admission did not recover: %d", calls.Load())
	}
}

func TestGitHubTransportsReserveTheirOwnSecondaryPointCost(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for _, resource := range []string{"core", "graphql"} {
		t.Run(resource, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				fmt.Fprint(w, `{"data":{"ok":true},"ok":true}`)
			}))
			defer server.Close()
			db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			points, path := 896, "/issue"
			if resource == "graphql" {
				points, path = 1996, "/graphql"
			}
			if err := db.Put(ctx, "github-usage", "application", githubUsage{Burst: []githubBurstCall{{At: now, Resource: resource, Points: points}}}, ""); err != nil {
				t.Fatal(err)
			}
			g := managedFixture(t, server, db, &now, 10000, 0)
			_, _, err = g.Request(ctx, "POST", path, map[string]string{"title": "fixture"}, "")
			var rate *RateLimitError
			if !errors.As(err, &rate) || calls.Load() != 0 || !strings.Contains(rate.Reason, "point") {
				t.Fatalf("mutation cost bypassed admission: %v sends%d", err, calls.Load())
			}
			var out any
			if resource == "graphql" {
				err = g.graph(ctx, "query{viewer{login}}", nil, &out)
			} else {
				_, err = g.get(ctx, path, &out)
			}
			if err != nil || calls.Load() != 1 {
				t.Fatalf("one-point read did not fit remaining allowance: %v sends%d", err, calls.Load())
			}
		})
	}
}
