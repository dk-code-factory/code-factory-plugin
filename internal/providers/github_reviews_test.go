package providers

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublishedReviewsKeepVerdictCommitAndPaginatedInlineEvidence(t *testing.T) {
	var s *httptest.Server
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("not an authenticated read")
		}
		switch r.URL.Path {
		case "/repos/o/r/pulls/1/reviews":
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"id":2,"state":"APPROVED","body":"","commit_id":"old","submitted_at":"2026-10-23T01:00:00Z","html_url":"https://github.com/o/r/pull/1#pullrequestreview-2","user":{"login":"other"}}]`)
			} else {
				w.Header().Set("Link", `<`+s.URL+`/repos/o/r/pulls/1/reviews?page=2>; rel="next"`)
				fmt.Fprint(w, `[{"id":3,"state":"COMMENTED","body":"Verified defect","commit_id":"head","submitted_at":"2026-10-24T01:00:00Z","html_url":"https://github.com/o/r/pull/1#pullrequestreview-3","user":{"login":"me"}},{"id":4,"state":"PENDING","commit_id":"head","body":"private draft"}]`)
			}
		case "/repos/o/r/pulls/1/comments":
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"id":12,"pull_request_review_id":3,"body":"Reply","path":"src.py","line":null,"user":{"login":"author"}}]`)
			} else {
				w.Header().Set("Link", `<`+s.URL+`/repos/o/r/pulls/1/comments?page=2>; rel="next"`)
				fmt.Fprint(w, `[{"id":11,"pull_request_review_id":3,"body":"Concrete failure","path":"src.py","line":8,"html_url":"https://github.com/o/r/pull/1#discussion_r11","user":{"login":"me"}},{"id":13,"pull_request_review_id":4,"body":"unsubmitted draft"}]`)
			}
		default:
			t.Error("unexpected path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	g := &GitHub{BaseURL: s.URL, Client: s.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	got, observed, err := g.PublishedReviews(context.Background(), domain.Item{Repository: "o/r", Number: 1})
	if err != nil {
		t.Fatal(err)
	}
	if observed.IsZero() {
		t.Fatal("missing provider observation time")
	}
	if len(got) != 2 || got[0].ID != "3" || got[0].State != "COMMENTED" || got[0].Head != "head" || got[0].Body != "Verified defect" || got[1].Author != "other" || got[1].Head != "old" {
		t.Fatalf("lost receipt metadata: %+v", got)
	}
	if len(got[0].Comments) != 2 || got[0].Comments[0].Line != 8 || got[0].Comments[1].Body != "Reply" || len(got[1].Comments) != 0 {
		t.Fatalf("lost or misattributed comments: %+v", got)
	}
}

func TestPublishedReviewCacheRetainsActualProviderObservationTime(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var reads atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		w.Header().Set("X-Poll-Interval", "300")
		fmt.Fprint(w, `[]`)
	}))
	defer s.Close()
	now := time.Now().UTC()
	g := &GitHub{BaseURL: s.URL, Client: s.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }, clock: func() time.Time { return now }}
	g.ConfigureUsage(db, func(context.Context) (GitHubPolicy, error) { return GitHubPolicy{RequestsPerHour: 60}, nil })
	i := domain.Item{Repository: "o/r", Number: 1}
	_, first, err := g.PublishedReviews(ctx, i)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	_, cached, err := g.PublishedReviews(ctx, i)
	if err != nil || !cached.Equal(first) || reads.Load() != 2 {
		t.Fatal("cached response claimed a fresh provider observation", first, cached, reads.Load(), err)
	}
}
