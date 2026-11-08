package providers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codefactory/internal/store"
)

func TestGitHubRESTTransportsSharePersistentCacheAndLiveReads(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	calls, validations := 0, 0
	value := "one"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("ETag", `"`+value+`"`)
		if r.Header.Get("If-None-Match") == `"`+value+`"` {
			validations++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		fmt.Fprintf(w, `{"login":%q}`, value)
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	newClient := func() *GitHub { return managedFixture(t, server, db, &now, 100, 0) }
	g := newClient()
	var out struct{ Login string }
	if _, err := g.get(ctx, "/user", &out); err != nil {
		t.Fatal(err)
	}
	g = newClient()
	batch := g.StartOperation(ctx, db, true)
	body, _, err := g.Request(batch, http.MethodGet, "/user", nil, "")
	if err != nil || string(body) != `{"login":"one"}` || calls != 1 {
		t.Fatal("generic REST bypassed persisted typed cache", string(body), calls, err)
	}
	body[0] = 'x'
	value = "two"
	body, _, err = g.Request(WithGitHubRead(batch, RequireLive), http.MethodGet, "/user", nil, "")
	if err != nil || string(body) != `{"login":"two"}` || calls != 2 {
		t.Fatal("live REST reused TTL or batch memo", string(body), calls, err)
	}
	body[0] = 'x'
	body, _, err = g.Request(batch, http.MethodGet, "/user", nil, "")
	if err != nil || string(body) != `{"login":"two"}` || calls != 2 {
		t.Fatal("live response body aliased the memory cache", string(body), calls, err)
	}
	body[0] = 'x'
	body, _, err = g.Request(batch, http.MethodGet, "/user", nil, "")
	if err != nil || string(body) != `{"login":"two"}` || calls != 2 {
		t.Fatal("cached response body aliased the memory cache", string(body), calls, err)
	}
	g = newClient()
	if _, err := g.get(ctx, "/user", &out); err != nil || out.Login != "two" || calls != 2 {
		t.Fatal("typed REST bypassed generic write-through or body was aliased", out, calls, err)
	}
	now = now.Add(6 * time.Minute)
	body, _, err = g.Request(ctx, http.MethodGet, "/user", nil, "")
	if err != nil || string(body) != `{"login":"two"}` || calls != 3 || validations != 1 {
		t.Fatal("expired generic cache did not conditionally revalidate", string(body), calls, validations, err)
	}
	g = newClient()
	if _, err := g.get(ctx, "/user", &out); err != nil || calls != 3 {
		t.Fatal("304 validation was not durable", calls, err)
	}
	if _, _, err := g.Request(Revalidate(ctx), http.MethodGet, "/user", nil, ""); err != nil || calls != 4 || validations != 2 {
		t.Fatal("explicit revalidation used TTL or lost its validator", calls, validations, err)
	}
	g.Token = func(context.Context) (string, error) { return "other", nil }
	if _, _, err := g.Request(ctx, http.MethodGet, "/user", nil, ""); err != nil || calls != 5 {
		t.Fatal("another principal reused cache", calls, err)
	}
}

func TestGitHubGenericCachePersistsLastModifiedAndNeverMemoizesWrites(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	calls, writes := 0, 0
	modified := now.Truncate(time.Second).Format(http.TimeFormat)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == http.MethodPost {
			writes++
			fmt.Fprint(w, `{}`)
			return
		}
		w.Header().Set("Last-Modified", modified)
		w.Header().Set("Link", `</data?page=2>; rel="next"`)
		w.Header().Set("Set-Cookie", fmt.Sprintf("private-cookie-%d", calls))
		w.Header().Set("X-Private", "private-response-header")
		if r.Header.Get("If-Modified-Since") == modified {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if calls > 1 {
			t.Error("persistent Last-Modified validator was bypassed")
		}
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertStoredHeaders := func() {
		t.Helper()
		rows, err := db.List(ctx, "github-cache")
		if err != nil || len(rows) != 1 {
			t.Fatal("missing persistent cache record", len(rows), err)
		}
		for _, row := range rows {
			var entry cacheEntry
			if err := json.Unmarshal(row, &entry); err != nil {
				t.Fatal(err)
			}
			if len(entry.Header) != 2 || entry.Header.Get("Last-Modified") != modified || entry.Header.Get("Link") == "" {
				t.Fatal("persistent cache retained unexpected headers or lost validators", entry.Header)
			}
		}
	}
	g := managedFixture(t, server, db, &now, 100, 0)
	if _, header, err := g.Request(ctx, http.MethodGet, "/data", nil, ""); err != nil || header.Get("Set-Cookie") != "private-cookie-1" || header.Get("X-Private") == "" {
		t.Fatal("live response headers were not preserved", header, err)
	}
	assertStoredHeaders()
	g = managedFixture(t, server, db, &now, 100, 0)
	body, header, err := g.Request(ctx, http.MethodGet, "/data", nil, "")
	if err != nil || string(body) != `[]` || header.Get("Link") == "" || calls != 2 {
		t.Fatal("restart lost validator, body or pagination", string(body), header, calls, err)
	}
	if header.Get("Set-Cookie") != "private-cookie-2" || header.Get("X-Private") == "" {
		t.Fatal("304 response headers were not preserved", header)
	}
	assertStoredHeaders()
	ctx = g.StartOperation(ctx, db, true)
	for range 2 {
		if _, _, err := g.Request(ctx, http.MethodPost, "/data", map[string]string{"body": "write"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 2 {
		t.Fatal("mutation was memoized", writes)
	}
	for range 2 {
		if _, _, err := g.Request(ctx, http.MethodGet, "/data", nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 5 {
		t.Fatal("batch GET was not memoized", calls)
	}
	if _, _, err := g.Request(WithGitHubRead(ctx, RequireLive), http.MethodGet, "/data", nil, ""); err != nil || calls != 6 {
		t.Fatal("live GET reused batch memo", calls, err)
	}
}

func TestGitHubGraphCacheSeparatesOriginsAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, value := range []string{"first-origin", "second-origin"} {
		t.Run(value, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprintf(w, `{"data":{"value":%q}}`, value) }))
			defer server.Close()
			for range 2 {
				g := managedFixture(t, server, db, &now, 100, 0)
				var out struct{ Value string }
				if err := g.graph(ctx, "query { value }", nil, &out); err != nil || out.Value != value {
					t.Fatal("GraphQL reused another origin", out, err)
				}
			}
			if calls != 1 {
				t.Fatal("GraphQL restart bypassed cache", calls)
			}
		})
	}
}

func TestGitHubRevocationInvalidatesCredentialCacheAcrossTransportsAndRestart(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, transport := range []string{"generic", "typed", "graphql"} {
			for _, persistent := range []bool{false, true} {
				for _, bodyMode := range []string{"empty", "truncated", "oversized", "graphql-type", "graphql-code", "graphql-mixed-rate-first-type", "graphql-mixed-auth-first-type", "graphql-mixed-rate-first-code", "graphql-mixed-auth-first-code", "graphql-rate-header"} {
					if strings.HasPrefix(bodyMode, "graphql-") && transport != "graphql" {
						continue
					}
					t.Run(fmt.Sprintf("%d/%s/persistent=%t/%s", status, transport, persistent, bodyMode), func(t *testing.T) {
						ctx := context.Background()
						now := time.Now().UTC()
						revoked, calls := false, 0
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls++
							if revoked && r.Header.Get("Authorization") == "Bearer fixture-secret-token" {
								if r.URL.Path == "/graphql" && strings.HasPrefix(bodyMode, "graphql-") {
									if strings.Contains(bodyMode, "mixed") {
										auth := `{"type":"FORBIDDEN"}`
										rate := `{"type":"RATE_LIMITED"}`
										if strings.HasSuffix(bodyMode, "code") {
											auth = `{"extensions":{"code":"UNAUTHORIZED"}}`
											rate = `{"extensions":{"code":"RATE_LIMITED"}}`
										}
										if strings.Contains(bodyMode, "rate-first") {
											fmt.Fprintf(w, `{"errors":[%s,%s]}`, rate, auth)
										} else {
											fmt.Fprintf(w, `{"errors":[%s,%s]}`, auth, rate)
										}
									} else if bodyMode == "graphql-rate-header" {
										w.Header().Set("X-RateLimit-Remaining", "0")
										fmt.Fprint(w, `{"errors":[{"type":"UNAUTHORIZED"}]}`)
									} else if bodyMode == "graphql-type" {
										fmt.Fprint(w, `{"errors":[{"type":"FORBIDDEN"}]}`)
									} else {
										fmt.Fprint(w, `{"errors":[{"extensions":{"code":"UNAUTHORIZED"}}]}`)
									}
									return
								}
								if bodyMode == "truncated" {
									w.Header().Set("Content-Length", "1000")
								}
								w.WriteHeader(status)
								if bodyMode == "truncated" {
									fmt.Fprint(w, "{")
								}
								if bodyMode == "oversized" {
									fmt.Fprint(w, strings.Repeat("x", (16<<20)+1))
								}
								return
							}
							switch r.URL.Path {
							case "/user":
								fmt.Fprint(w, `{"login":"fixture"}`)
							case "/user/teams":
								fmt.Fprint(w, `[]`)
							case "/graphql":
								fmt.Fprint(w, `{"data":{"value":"fixture"}}`)
							default:
								t.Errorf("unexpected request %s", r.URL.Path)
							}
						}))
						defer server.Close()
						var db *store.Store
						var err error
						if persistent {
							db, err = store.Open(filepath.Join(t.TempDir(), "state.db"))
							if err != nil {
								t.Fatal(err)
							}
							defer db.Close()
						}
						newClient := func() (*GitHub, *CachedGitHub) {
							g := managedFixture(t, server, db, &now, 1000, 0)
							cached := NewCachedGitHub(g)
							cached.ConfigureUsage(db, g.policy)
							return g, cached
						}
						g, cached := newClient()
						if _, err := cached.User(ctx); err != nil {
							t.Fatal(err)
						}
						if _, err := cached.Teams(ctx); err != nil {
							t.Fatal(err)
						}
						var graph map[string]string
						if err := g.graph(ctx, "query { value }", nil, &graph); err != nil {
							t.Fatal(err)
						}
						other, otherCached := newClient()
						other.Token = func(context.Context) (string, error) { return "other-token", nil }
						if _, err := otherCached.User(ctx); err != nil {
							t.Fatal(err)
						}
						revoked = true
						live := WithGitHubRead(ctx, RequireLive)
						switch transport {
						case "generic":
							_, _, err = g.Request(live, http.MethodGet, "/user", nil, "")
						case "typed":
							_, err = g.User(live)
						case "graphql":
							err = g.graph(live, "query { value }", nil, &graph)
						}
						if err == nil {
							t.Fatal("live rejection was hidden")
						}
						throttled := strings.Contains(bodyMode, "mixed") || bodyMode == "graphql-rate-header"
						if throttled {
							var limited *RateLimitError
							if !errors.As(err, &limited) || !limited.Until.After(now) {
								t.Fatal("mixed auth/rate evidence lost retry classification", err)
							}
						}
						for _, restart := range []bool{false, true} {
							if restart {
								g, cached = newClient()
							}
							before := calls
							if _, _, err := g.Request(ctx, http.MethodGet, "/user", nil, ""); err == nil {
								t.Fatal("generic GET revived rejected credential cache", restart)
							}
							if _, err := g.Teams(ctx); err == nil {
								t.Fatal("typed teams revived rejected credential cache", restart)
							}
							if err := g.graph(ctx, "query { value }", nil, &graph); err == nil {
								t.Fatal("GraphQL revived rejected credential cache", restart)
							}
							if _, err := cached.User(ctx); err == nil {
								t.Fatal("query wrapper revived rejected identity", restart)
							}
							if _, err := cached.Teams(ctx); err == nil || !throttled && calls != before+5 {
								t.Fatal("query wrapper revived rejected teams or skipped live requests", restart, calls, before, err)
							}
						}
						before := calls
						if _, err := otherCached.User(ctx); err != nil || calls != before {
							t.Fatal("another credential query cache was invalidated", calls, before, err)
						}
						if _, _, err := other.Request(ctx, http.MethodGet, "/user", nil, ""); err != nil || calls != before {
							t.Fatal("another credential response cache was invalidated", calls, before, err)
						}
					})
				}
			}
		}
	}
}

func TestGitHubCacheGenerationFailuresCannotReturnCachedSuccess(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	revoked, calls := false, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if revoked {
			if r.URL.Path == "/graphql" {
				fmt.Fprint(w, `{"errors":[{"type":"RATE_LIMITED"},{"extensions":{"code":"FORBIDDEN"}}]}`)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"login":"fixture"}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := managedFixture(t, server, db, &now, 100, 0)
	cached := NewCachedGitHub(g)
	cached.ConfigureUsage(db, g.policy)
	if _, err := cached.User(ctx); err != nil {
		t.Fatal(err)
	}
	control, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if _, err := control.Exec(`CREATE TRIGGER reject_epoch BEFORE INSERT ON entities WHEN NEW.kind='github-cache-epoch' BEGIN SELECT RAISE(ABORT, 'fixture generation failure'); END`); err != nil {
		t.Fatal(err)
	}
	revoked = true
	if _, _, err := g.Request(WithGitHubRead(ctx, RequireLive), http.MethodGet, "/user", nil, ""); err == nil {
		t.Fatal("generation write failure was hidden")
	}
	before := calls
	if _, _, err := g.Request(ctx, http.MethodGet, "/user", nil, ""); err == nil {
		t.Fatal("generation write failure revived response cache")
	}
	if _, err := cached.User(ctx); err == nil || calls != before {
		t.Fatal("generation write failure revived query cache", calls, before, err)
	}
	mixed := managedFixture(t, server, db, &now, 100, 0)
	mixedCached := NewCachedGitHub(mixed)
	mixedCached.ConfigureUsage(db, mixed.policy)
	var graph map[string]string
	err = mixed.graph(WithGitHubRead(ctx, RequireLive), "query { value }", nil, &graph)
	var limited *RateLimitError
	if err == nil || errors.As(err, &limited) {
		t.Fatal("mixed-envelope generation failure was hidden by rate classification", err)
	}
	before = calls
	if _, err := mixedCached.User(ctx); err == nil || calls != before {
		t.Fatal("mixed-envelope generation failure revived query cache", calls, before, err)
	}
	if _, err := control.Exec(`DROP TRIGGER reject_epoch`); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "github-cache-epoch", CooldownKey(g.BaseURL, "fixture-secret-token"), 1, ""); err != nil {
		t.Fatal(err)
	}
	g = managedFixture(t, server, db, &now, 100, 0)
	cached = NewCachedGitHub(g)
	cached.ConfigureUsage(db, g.policy)
	if _, _, err := g.Request(ctx, http.MethodGet, "/user", nil, ""); err == nil {
		t.Fatal("invalid generation read revived response cache")
	}
	if _, err := cached.User(ctx); err == nil || calls != before {
		t.Fatal("invalid generation read revived query cache", calls, before, err)
	}
}
