package factory

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfirmedGitHubCadencesReachRuntimeWithoutChangingPriorityPolicy(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	before, err := s.App.Config(ctx)
	must(t, err)
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.GitHubPollSeconds = 600
	settings.GitHubRepositoryPollSeconds = 3600
	settings.GitHubDetailPollSeconds = 7200
	proposal, err := s.Propose(ctx, &settings, nil)
	must(t, err)
	for _, changed := range []string{"githubPollSeconds\n- null\n+ 600\n", "githubRepositoryPollSeconds\n- null\n+ 3600\n", "githubDetailPollSeconds\n- null\n+ 7200\n"} {
		if !strings.Contains(proposal.Diff, changed) {
			t.Fatal("cadence proposal omitted complete before/after values", changed, proposal.Diff)
		}
	}
	if strings.Contains(proposal.Diff, "quietSeconds") || strings.Contains(proposal.Diff, "repositories") {
		t.Fatal("cadence proposal included unrelated unchanged settings", proposal.Diff)
	}
	unchanged, err := s.App.Config(ctx)
	must(t, err)
	if unchanged.GitHubDetailPollSeconds != 0 || unchanged.GitHubRepositoryPollSeconds != 0 {
		t.Fatal("unconfirmed proposal changed runtime cadence", unchanged)
	}
	_, err = s.Confirm(ctx, proposal.ID, true)
	must(t, err)
	must(t, s.applyPending(ctx))
	after, err := s.App.Config(ctx)
	must(t, err)
	if after.GitHubPollSeconds != 600 || after.GitHubRepositoryPollInterval() != time.Hour || after.GitHubDetailPollInterval() != 2*time.Hour || after.PolicyVersion != before.PolicyVersion {
		t.Fatal("confirmed cadences lost in projection or changed priority policy", before, after)
	}
	effective, err := s.Settings(ctx)
	must(t, err)
	if effective.GitHubRepositoryPollSeconds != 3600 || effective.GitHubDetailPollSeconds != 7200 {
		t.Fatal("effective settings lost confirmed cadences", effective)
	}
	for _, value := range []int{-1, 59, 86401} {
		for _, field := range []string{"repository", "detail"} {
			bad := effective
			if field == "repository" {
				bad.GitHubRepositoryPollSeconds = value
			} else {
				bad.GitHubDetailPollSeconds = value
			}
			if _, err = s.Propose(ctx, &bad, nil); err == nil {
				t.Fatalf("accepted invalid %s cadence %d", field, value)
			}
		}
	}
}

func TestBackgroundDetailCadenceSuppressesHTTPUntilDue(t *testing.T) {
	s, _, clock := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.GitHubDetailPollSeconds = 3600
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	must(t, s.projectSettings(ctx))
	must(t, s.Store.Put(ctx, "factory-baseline", "o/r", map[string]bool{"ready": true}, ""))
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	item.Kind = "issue"
	item.PR = nil
	item.DetailSyncedAt = clock.Add(-20 * time.Minute)
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/repos/o/r/issues/1":
			fmt.Fprint(w, `{"number":1,"state":"open","title":"updated detail"}`)
		case "/repos/o/r/issues/1/comments", "/repos/o/r/issues/1/timeline":
			fmt.Fprint(w, `[]`)
		default:
			t.Errorf("unexpected background request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	g := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Now: s.Now, Token: func(context.Context) (string, error) { return "fixture", nil }}
	g.ConfigureUsage(s.Store, func(ctx context.Context) (providers.GitHubPolicy, error) {
		c, err := s.App.Config(ctx)
		return providers.GitHubPolicy{RequestsPerHour: 100, DetailTTL: c.GitHubDetailPollInterval()}, err
	})
	s.App.GitHub = g
	must(t, s.refreshDetails(ctx, settings))
	if calls.Load() != 0 {
		t.Fatal("background refresh ignored hourly detail cadence", calls.Load())
	}
	*clock = clock.Add(41 * time.Minute)
	must(t, s.refreshDetails(ctx, settings))
	if calls.Load() != 3 {
		t.Fatal("due background detail did not fetch source and history", calls.Load())
	}
	var current domain.Item
	must(t, s.Store.Get(ctx, "item", item.ID, &current))
	if current.Title != "updated detail" || !current.DetailSyncedAt.Equal(*clock) {
		t.Fatal("background refresh did not save current observation", current)
	}
}
