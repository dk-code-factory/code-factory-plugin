package providers

import (
	"codefactory/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubPaginationPREnrichmentAndRecovery(t *testing.T) {
	var server *httptest.Server
	var comment atomic.Int32
	comment.Store(1)
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing GitHub authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/issues":
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"number":2,"title":"Second","body":"","state":"closed","updated_at":"2026-01-01T00:00:00Z","user":{"login":"you"}}]`)
			} else {
				w.Header().Set("Link", `<`+server.URL+`/repos/o/r/issues?page=2>; rel="next"`)
				fmt.Fprint(w, `[{"number":1,"title":"PR","state":"open","updated_at":"2026-01-01T00:00:00Z","user":{"login":"me"},"pull_request":{}}]`)
			}
		case "/repos/o/r/pulls":
			fmt.Fprint(w, `[{"number":1,"title":"PR","state":"open","updated_at":"2026-01-01T00:00:00Z","user":{"login":"me"}}]`)
		case "/repos/o/r/pulls/1":
			fmt.Fprint(w, `{"draft":true,"head":{"sha":"abc123","ref":"APP-42-fix"}}`)
		case "/repos/o/r/issues/1/comments":
			fmt.Fprintf(w, `[{"id":%d,"body":"Updated","user":{"login":"me"}}]`, comment.Load())
		case "/repos/o/r/pulls/1/reviews":
			fmt.Fprint(w, `[{"id":10,"body":"Looks good","state":"APPROVED","user":{"login":"me"}}]`)
		case "/repos/o/r/commits/abc123/check-runs":
			fmt.Fprint(w, `{"total_count":1,"check_runs":[{"name":"ci","status":"completed"}]}`)
		case "/repos/o/r/pulls/1/commits":
			fmt.Fprint(w, `[{"sha":"abc123","author":{"login":"me"},"commit":{"message":"fix"}}]`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "test-token", nil }}
	repo := domain.Repository{ID: "o/r", LastSync: time.Now().Add(-time.Minute)}
	items, e := g.Sync(context.Background(), repo, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(items) != 2 {
		t.Fatal("pagination lost items", len(items))
	}
	var pr domain.Item
	for _, i := range items {
		if i.Kind == "pr" {
			pr = i
		}
	}
	if !pr.Draft || pr.Head != "abc123" || pr.Branch != "APP-42-fix" || len(pr.Activity) != 3 || !strings.Contains(string(pr.Checks), "ci") {
		b, _ := json.Marshal(pr)
		t.Fatal(string(b))
	}
	comment.Store(2)
	items, e = g.Sync(context.Background(), repo, false)
	if e != nil {
		t.Fatal(e)
	}
	for _, i := range items {
		if i.Kind == "pr" && i.Revision == pr.Revision {
			t.Fatal("PR activity changed without revision change")
		}
	}
}
func TestGitHubRateLimitsRevocationAndPaginationOrigin(t *testing.T) {
	for _, code := range []int{401, 403, 429} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if code == 403 {
					w.Header().Set("X-RateLimit-Remaining", "0")
				}
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(code)
			}))
			defer s.Close()
			g := &GitHub{BaseURL: s.URL, Client: s.Client(), Token: func(context.Context) (string, error) { return "token", nil }}
			_, e := g.User(context.Background())
			if e == nil {
				t.Fatal("error missing")
			}
			var rate *RateLimitError
			if code != 401 && (!errors.As(e, &rate) || time.Until(rate.Until) < 55*time.Second) {
				t.Fatal("retry time missing", e)
			}
		})
	}
	g := NewGitHub()
	if _, e := g.get(context.Background(), "https://example.net/leak", &map[string]any{}); e == nil {
		t.Fatal("accepted cross-origin pagination")
	}
}

func TestConditionalCacheKeepsPaginationAndScopesAuthentication(t *testing.T) {
	calls := 0
	token := "first"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("If-None-Match") == `"one"` {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"one"`)
		w.Header().Set("Link", `<https://api.github.com/example?page=2>; rel="next"`)
		fmt.Fprint(w, `{"login":"me"}`)
	}))
	defer s.Close()
	g := &GitHub{BaseURL: s.URL, Client: s.Client(), Token: func(context.Context) (string, error) { return token, nil }}
	ctx := context.Background()
	var out map[string]string
	if _, e := g.get(ctx, "/user", &out); e != nil {
		t.Fatal(e)
	}
	header, e := g.get(ctx, "/user", &out)
	if e != nil || out["login"] != "me" || header.Get("Link") == "" {
		t.Fatal("304 lost body/pagination", e)
	}
	token = "second"
	if _, e = g.get(ctx, "/user", &out); e != nil {
		t.Fatal(e)
	}
	if len(g.cache) != 2 || calls != 3 {
		t.Fatal("cache reused another principal", len(g.cache), calls)
	}
}

func TestSyncEnrichmentHasBoundedParallelismAndCancelsOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			var active, peak atomic.Int32
			entered := make(chan struct{}, 8)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/repos/o/r/issues":
					fmt.Fprint(w, `[`)
					for n := 1; n <= 8; n++ {
						if n > 1 {
							fmt.Fprint(w, `,`)
						}
						fmt.Fprintf(w, `{"number":%d,"state":"open","title":"item"}`, n)
					}
					fmt.Fprint(w, `]`)
				case strings.HasSuffix(r.URL.Path, "/comments"):
					n := active.Add(1)
					defer active.Add(-1)
					for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
					}
					entered <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					if fail {
						w.WriteHeader(500)
						return
					}
					fmt.Fprint(w, `[]`)
				default:
					fmt.Fprint(w, `[]`)
				}
			}))
			defer server.Close()
			g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			var items []domain.Item
			go func() { var err error; items, err = g.Sync(ctx, domain.Repository{ID: "o/r"}, true); finished <- err }()
			for n := 0; n < 4; n++ {
				select {
				case <-entered:
				case <-ctx.Done():
					close(release)
					t.Fatal("independent items did not make progress")
				}
			}
			close(release)
			err := <-finished
			if peak.Load() != 4 {
				t.Fatalf("worker bound: %d", peak.Load())
			}
			if fail {
				if err == nil || items != nil {
					t.Fatal("failed hydration published partial repository", err)
				}
			} else if err != nil || len(items) != 8 {
				t.Fatal("lost hydrated items", len(items), err)
			}
		})
	}
}
