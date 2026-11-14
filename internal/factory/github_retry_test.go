package factory

import (
	"context"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
)

type webhookRetryGitHub struct {
	*fixtureGitHub
	until time.Time
	fail  bool
	calls int
}

func (g *webhookRetryGitHub) PullRequest(ctx context.Context, repo string, number int) (domain.Item, error) {
	g.calls++
	if g.fail {
		return domain.Item{}, &providers.RateLimitError{Until: g.until, Reason: "GitHub secondary request limit reached"}
	}
	return g.fixtureGitHub.PullRequest(ctx, repo, number)
}
func TestWebhookThrottleRetainsProviderDeadlineAndResumesSavedDelivery(t *testing.T) {
	s, _, clock := fixture(t)
	ctx := context.Background()
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	item.DetailSyncedAt = s.Now()
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	g := &webhookRetryGitHub{fixtureGitHub: &fixtureGitHub{items: map[int]domain.Item{1: item}}, until: s.Now().Add(5 * time.Minute), fail: true}
	s.App.GitHub = g
	d := delivery{ID: "throttle-ci", Type: "check_run", State: "pending", At: s.Now(), Body: bytes(map[string]any{"repository": map[string]string{"full_name": "o/r"}, "check_run": map[string]any{"head_sha": item.Head, "pull_requests": []any{map[string]int{"number": 1}}}})}
	must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
	must(t, s.Poll(ctx))
	var saved delivery
	must(t, s.Store.Get(ctx, "factory-delivery", d.ID, &saved))
	if saved.State != "pending" || !saved.RetryAt.Equal(g.until) || saved.Error == "" || g.calls != 1 {
		t.Fatal("webhook lost provider wait", saved, g.calls)
	}
	*clock = clock.Add(time.Minute)
	must(t, s.Poll(ctx))
	if g.calls != 1 {
		t.Fatal("webhook retried before provider deadline", g.calls)
	}
	*clock = g.until
	g.fail = false
	must(t, s.Poll(ctx))
	saved = delivery{}
	must(t, s.Store.Get(ctx, "factory-delivery", d.ID, &saved))
	if saved.State != "processed" || g.calls != 2 {
		t.Fatal("webhook did not resume original delivery", saved, g.calls)
	}
}
