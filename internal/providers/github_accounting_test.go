package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codefactory/internal/store"
)

func TestGitHubAccountingKeepsConditionalReadsInLocalBudget(t *testing.T) {
	for _, transport := range []string{"typed", "generic"} {
		t.Run(transport, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("X-RateLimit-Remaining", "4223")
				w.Header().Set("X-RateLimit-Reset", fmt.Sprint(now.Add(time.Hour).Unix()))
				if r.URL.Path == "/data" {
					w.Header().Set("ETag", `"saved"`)
					w.Header().Set("X-RateLimit-Used", "777")
					w.Header().Set("X-RateLimit-Limit", "5000")
					if r.Header.Get("If-None-Match") == `"saved"` {
						w.WriteHeader(http.StatusNotModified)
						return
					}
				}
				fmt.Fprint(w, `{"ok":true}`)
			}))
			defer server.Close()
			db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			g := managedFixture(t, server, db, &now, 2, 0)
			read := func(path string) error {
				if transport == "generic" {
					_, _, err := g.Request(ctx, http.MethodGet, path, nil, "")
					return err
				}
				var result map[string]bool
				_, err := g.get(ctx, path, &result)
				return err
			}
			if err := read("/data"); err != nil {
				t.Fatal(err)
			}
			if err := read("/data"); err != nil {
				t.Fatal(err)
			}
			status, err := g.UsageStatus(ctx)
			if err != nil || calls != 2 || status.RequestsLastHour != 2 || status.RequestAttemptsLastHour != 2 || status.NotModifiedLastHour != 1 {
				t.Fatal("304 was confused with a cache hit or refunded", status, calls, err)
			}
			quota := status.RESTQuota
			if quota == nil || quota.Unit != "requests" || quota.Remaining != 4223 || quota.Used == nil || *quota.Used != 777 || quota.Limit == nil || *quota.Limit != 5000 || quota.ObservedAt == nil || !quota.ObservedAt.Equal(now) || !status.AccountingAsOf.Equal(now) {
				t.Fatal("shared GitHub quota was confused with local attempts", status)
			}
			var limited *RateLimitError
			if err := read("/other"); !errors.As(err, &limited) || calls != 2 {
				t.Fatal("304 bypassed the local traffic allowance", err, calls)
			}
			g = managedFixture(t, server, db, &now, 3, 0)
			status, err = g.UsageStatus(ctx)
			if err != nil || status.RequestAttemptsLastHour != 2 || status.NotModifiedLastHour != 1 {
				t.Fatal("accounting did not survive restart", status, err)
			}
			if err := read("/user"); err != nil {
				t.Fatal(err)
			}
			if err := read("/user"); err != nil || calls != 3 {
				t.Fatal("warm cache hit issued another request", calls, err)
			}
			status, err = g.UsageStatus(ctx)
			if err != nil || status.RequestAttemptsLastHour != 3 || status.NotModifiedLastHour != 1 || status.RESTQuota.Used != nil || status.RESTQuota.Limit != nil {
				t.Fatal("missing shared-quota headers inherited or inferred usage", status, err)
			}
			g.Token = func(context.Context) (string, error) { t.Fatal("status resolved credentials"); return "", nil }
			if _, err := g.UsageStatus(ctx); err != nil || calls != 3 {
				t.Fatal("local inspection used the provider", err, calls)
			}
			now = now.Add(time.Hour + time.Second)
			status, err = g.UsageStatus(ctx)
			if err != nil || status.RequestAttemptsLastHour != 0 || status.NotModifiedLastHour != 0 || status.RESTQuota != nil || status.RESTRemaining != nil || calls != 3 {
				t.Fatal("old accounting did not age out locally", status, err, calls)
			}
		})
	}
}

func TestGitHubAccountingSeparatesGraphQLPointsAndLegacyUnknowns(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-RateLimit-Remaining", "4973")
		w.Header().Set("X-RateLimit-Used", "27")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(now.Add(time.Hour).Unix()))
		fmt.Fprint(w, `{"data":{"value":true}}`)
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// This is the pre-change persisted shape: optional observation data is absent.
	if err := db.Put(ctx, "github-usage", "application", map[string]any{
		"Calls": []time.Time{now}, "ActiveScope": strings.TrimSuffix(usageKey("fixture-secret-token", "core"), ":core"),
		"Limits": map[string]any{usageKey("fixture-secret-token", "core"): map[string]any{"Remaining": 4999, "Reset": now.Add(time.Hour)}},
	}, ""); err != nil {
		t.Fatal(err)
	}
	g := managedFixture(t, server, db, &now, 20, 0)
	status, err := g.UsageStatus(ctx)
	if err != nil || status.RequestAttemptsLastHour != 1 || status.NotModifiedLastHour != 0 || status.RESTQuota == nil || status.RESTQuota.Unit != "requests" || status.RESTQuota.Used != nil || status.RESTQuota.ObservedAt != nil {
		t.Fatal("legacy state fabricated missing observations", status, err)
	}
	var result map[string]bool
	if err := g.graph(ctx, "query{value}", nil, &result); err != nil {
		t.Fatal(err)
	}
	status, err = g.UsageStatus(ctx)
	if err != nil || calls != 1 || status.RequestAttemptsLastHour != 2 || status.NotModifiedLastHour != 0 || status.GraphQLQuota == nil || status.GraphQLQuota.Unit != "points" || *status.GraphQLQuota.Used != 27 || *status.GraphQLQuota.Limit != 5000 || *status.GraphQLRemaining != 4973 || status.RESTQuota.Remaining != 4999 {
		t.Fatal("GraphQL point snapshot lost its separate unit", status, err)
	}
	encoded, err := json.Marshal(status)
	if err != nil || strings.Contains(string(encoded), "fixture-secret-token") || strings.Contains(string(encoded), usageKey("fixture-secret-token", "core")) || strings.Contains(string(encoded), "cost") {
		t.Fatal("status leaked principal metadata or invented query cost", string(encoded), err)
	}
	// Observations returned to callers must not mutate the retained quota state.
	*status.GraphQLQuota.Used = 999
	if again, err := g.UsageStatus(ctx); err != nil || *again.GraphQLQuota.Used != 27 {
		t.Fatal("caller changed retained quota accounting", again, err)
	}
	t.Setenv("PATH", t.TempDir())
	if saved, err := SavedGitHubUsage(ctx, db, 20, nil); err != nil || saved.RequestAttemptsLastHour != 2 || calls != 1 {
		t.Fatal("saved accounting needed a credential executable or provider call", saved, err, calls)
	}
	g.Token = func(context.Context) (string, error) { return "other-principal", nil }
	if err := g.graph(ctx, "query{value}", nil, &result); err != nil {
		t.Fatal(err)
	}
	status, err = g.UsageStatus(ctx)
	if err != nil || status.RESTQuota != nil || status.RESTRemaining != nil || status.GraphQLQuota == nil || status.RequestAttemptsLastHour != 3 {
		t.Fatal("new principal inherited old REST quota", status, err)
	}
}

type accountingRoundTripper func(*http.Request) (*http.Response, error)

func (f accountingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestGitHubAccountingRetainsUncertainAttempts(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	var sends int
	g := &GitHub{BaseURL: "https://fixture.invalid", clock: func() time.Time { return now }, Token: func(context.Context) (string, error) { return "fixture", nil }, Client: &http.Client{Transport: accountingRoundTripper(func(*http.Request) (*http.Response, error) {
		sends++
		return nil, errors.New("response lost")
	})}}
	g.ConfigureUsage(nil, func(context.Context) (GitHubPolicy, error) { return GitHubPolicy{RequestsPerHour: 2}, nil })
	if _, _, err := g.Request(ctx, http.MethodGet, "/uncertain", nil, ""); err == nil {
		t.Fatal("uncertain generic read succeeded")
	}
	var result any
	if _, err := g.get(ctx, "/uncertain", &result); err == nil {
		t.Fatal("uncertain typed read succeeded")
	}
	var limited *RateLimitError
	if _, _, err := g.Request(ctx, http.MethodGet, "/blocked", nil, ""); !errors.As(err, &limited) || sends != 2 {
		t.Fatal("uncertain sends were refunded", err, sends)
	}
	status, err := g.UsageStatus(ctx)
	if err != nil || status.RequestsLastHour != 2 || status.RequestAttemptsLastHour != 2 || status.NotModifiedLastHour != 0 || status.RESTQuota != nil || status.GraphQLQuota != nil {
		t.Fatal("uncertain responses fabricated quota or conditional savings", status, err)
	}
}

func TestGitHubAccountingCountsOnlyValidatedNotModifiedResponses(t *testing.T) {
	for _, transport := range []string{"typed", "generic"} {
		for _, mode := range []string{"missing-cache", "missing-validator", "empty-body", "malformed-json"} {
			if transport == "generic" && mode == "malformed-json" {
				continue // The generic transport intentionally returns opaque cached bytes.
			}
			t.Run(transport+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				now := time.Now().UTC()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotModified)
				}))
				defer server.Close()
				g := managedFixture(t, server, nil, &now, 20, 0)
				if mode != "missing-cache" {
					body := []byte(`{"ok":true}`)
					if mode == "empty-body" {
						body = nil
					} else if mode == "malformed-json" {
						body = []byte("invalid-json")
					}
					validator := `"saved"`
					if mode == "missing-validator" {
						validator = ""
					}
					key, err := g.responseCacheKey(ctx, server.URL+"/data", "fixture-secret-token", "application/vnd.github+json")
					if err != nil {
						t.Fatal(err)
					}
					if err = g.remember(ctx, key, cacheEntry{ETag: validator, Body: body, Header: http.Header{}, CheckedAt: now}); err != nil {
						t.Fatal(err)
					}
				}
				if transport == "typed" {
					var result any
					_, _ = g.get(ctx, "/data", &result)
				} else {
					_, _, _ = g.Request(ctx, http.MethodGet, "/data", nil, "")
				}
				status, err := g.UsageStatus(ctx)
				if err != nil || status.NotModifiedLastHour != 0 || status.RequestAttemptsLastHour != 1 {
					t.Fatal("unvalidated304 counted as a successful conditional response", status, err)
				}
			})
		}
	}
}

func TestGitHubAccountingRetainsBurstReservationsAndThrottleState(t *testing.T) {
	for _, transport := range []string{"typed", "generic"} {
		t.Run(transport, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/limited" {
					w.Header().Set("Retry-After", "7")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				if r.URL.Path == "/data" {
					w.Header().Set("ETag", `"saved"`)
					w.Header().Set("X-RateLimit-Remaining", "4223")
					w.Header().Set("X-RateLimit-Reset", fmt.Sprint(now.Add(time.Hour).Unix()))
					w.Header().Set("X-RateLimit-Used", "777")
					if r.Header.Get("If-None-Match") == `"saved"` {
						w.WriteHeader(http.StatusNotModified)
						return
					}
				}
				fmt.Fprint(w, `{"ok":true}`)
			}))
			defer server.Close()
			db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			g := managedFixture(t, server, db, &now, 10000, 0)
			read := func(path string) error {
				if transport == "generic" {
					_, _, err := g.Request(ctx, http.MethodGet, path, nil, "")
					return err
				}
				var result map[string]bool
				_, err := g.get(ctx, path, &result)
				return err
			}
			if err := read("/data"); err != nil {
				t.Fatal(err)
			}
			if err := read("/data"); err != nil {
				t.Fatal(err)
			}
			var rate *RateLimitError
			if err := read("/limited"); !errors.As(err, &rate) || !rate.Until.Equal(now.Add(7*time.Second)) {
				t.Fatal("accounting lost explicit throttle deadline", err)
			}
			g = managedFixture(t, server, db, &now, 10000, 0)
			status, err := g.UsageStatus(ctx)
			if err != nil || status.RequestAttemptsLastHour != 3 || status.NotModifiedLastHour != 1 || !status.NextRequest.Equal(now.Add(7*time.Second)) || status.RESTQuota == nil || status.RESTQuota.Used == nil || *status.RESTQuota.Used != 777 {
				t.Fatal("restart confused attempts, validation, shared quota or cooldown", status, err)
			}
			saved, err := g.usage(ctx)
			if err != nil || len(saved.Burst) != 3 || saved.Strikes != 1 || !strings.Contains(saved.SecondaryReason, "secondary") {
				t.Fatal("accounting discarded durable burst or throttle fields", saved, err)
			}
			for _, reservation := range saved.Burst {
				if reservation.Resource != "core" || reservation.Points != 1 || reservation.Content {
					t.Fatal("read accounting changed conservative secondary cost", reservation)
				}
			}
			if err := read("/blocked"); !errors.As(err, &rate) || calls.Load() != 3 {
				t.Fatal("restart bypassed cooldown", err, calls.Load())
			}
			now = now.Add(7 * time.Second)
			if err := read("/recover"); err != nil {
				t.Fatal(err)
			}
			saved, err = g.usage(ctx)
			if err != nil || len(saved.Burst) != 4 || len(saved.NotModified) != 1 || saved.Strikes != 0 || saved.SecondaryReason != "" || !saved.SecondaryUntil.IsZero() || calls.Load() != 4 {
				t.Fatal("recovery failed to clear throttle while preserving reservations", saved, err, calls.Load())
			}
		})
	}
}
