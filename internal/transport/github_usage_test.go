package transport

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
)

func TestConfigurationProjectionReportsSavedGitHubAccounting(t *testing.T) {
	f := newTrackingFixture(t)
	f.a.GitHub = &providers.GitHub{Token: func(context.Context) (string, error) {
		t.Error("configuration projection resolved GitHub credentials")
		return "", nil
	}}
	now := time.Now().UTC().Truncate(time.Second)
	key := "private-principal-key"
	save := func(attempts int) {
		t.Helper()
		calls := make([]time.Time, attempts)
		for i := range calls {
			calls[i] = now
		}
		if err := f.a.Store.Put(f.ctx, "github-usage", "application", map[string]any{
			"Calls": calls, "NotModified": []time.Time{now}, "ActiveScope": key,
			"Limits": map[string]any{key + ":core": map[string]any{
				"Remaining": 4923, "Used": 77, "Limit": 5000, "Reset": now.Add(time.Hour), "ObservedAt": now,
			}},
		}, ""); err != nil {
			t.Fatal(err)
		}
	}
	before, err := f.a.Config(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	save(2)
	inbox, failed := f.call(t, "factory_state", map[string]any{"view": "issues"})
	if failed || inbox["githubUsage"] != nil {
		t.Fatal("accounting leaked into unrelated inbox projection", inbox)
	}
	configuration, failed := f.call(t, "factory_state", map[string]any{"view": "configuration"})
	if failed {
		t.Fatal(configuration)
	}
	usage := configuration["githubUsage"].(map[string]any)
	quota := usage["restQuota"].(map[string]any)
	captured, ok := usage["accountingAsOf"].(string)
	capturedAt, captureErr := time.Parse(time.RFC3339Nano, captured)
	if !ok || captureErr != nil || capturedAt.IsZero() || capturedAt.Before(now) || capturedAt.After(time.Now().UTC()) {
		t.Fatal("saved accounting lacks a valid capture time for its current counting window", usage["accountingAsOf"], captureErr)
	}
	if usage["requestsLastHour"] != float64(2) || usage["requestAttemptsLastHour"] != float64(2) || usage["notModifiedLastHour"] != float64(1) || quota["unit"] != "requests" || quota["used"] != float64(77) || quota["observedAt"] != now.Format(time.RFC3339) || strings.Contains(string(mustJSON(configuration)), key) {
		t.Fatal("saved accounting was missing, mislabelled or unsanitized", usage)
	}
	save(3)
	updated, failed := f.call(t, "factory_state", map[string]any{"view": "configuration"})
	if failed || updated["version"] == configuration["version"] || updated["githubUsage"].(map[string]any)["requestAttemptsLastHour"] != float64(3) {
		t.Fatal("accounting write left configuration projection stale", updated)
	}
	sameInbox, failed := f.call(t, "factory_state", map[string]any{"view": "issues"})
	if failed || sameInbox["version"] != inbox["version"] {
		t.Fatal("accounting invalidated unrelated inbox data", sameInbox)
	}
	after, err := f.a.Config(f.ctx)
	if err != nil || after.PolicyVersion != before.PolicyVersion {
		t.Fatal("accounting invalidated assessment policy", after, err)
	}
}

func TestGitHubAccountingPreservesEmptyInboxAndSelectedDetail(t *testing.T) {
	f := newTrackingFixture(t)
	f.a.GitHub = &providers.GitHub{Token: func(context.Context) (string, error) {
		t.Error("saved view resolved GitHub credentials")
		return "", nil
	}}
	item := domain.Item{ID: "o/r#1", Repository: "o/r", Kind: "issue", Number: 1, State: "open", Title: "Selected saved item", Body: "Complete retained source body"}
	item.Revision = item.ContentRevision()
	if err := f.a.Store.Put(f.ctx, "item", item.ID, item, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.a.Store.Put(f.ctx, "user", item.ID, domain.UserState{ID: item.ID, New: true}, ""); err != nil {
		t.Fatal(err)
	}
	emptyArgs := map[string]any{"view": "issues", "search": "absent-from-saved-items"}
	empty, failed := f.call(t, "factory_state", emptyArgs)
	if failed || empty["inboxTotal"] != float64(0) || len(empty["inbox"].([]any)) != 0 {
		t.Fatal("empty inbox fixture is not empty", empty)
	}
	conversationArgs := map[string]any{"view": "conversation", "itemId": item.ID}
	conversation, failed := f.call(t, "factory_state", conversationArgs)
	if failed || conversation["selectedEntry"].(map[string]any)["item"].(map[string]any)["revision"] != item.Revision {
		t.Fatal("selected overview lost its source revision", conversation)
	}
	detailArgs := map[string]any{"id": item.ID}
	detail, failed := f.call(t, "factory_item", detailArgs)
	if failed || detail["item"].(map[string]any)["body"] != item.Body || detail["detailVersion"] != conversation["selectedEntry"].(map[string]any)["detailVersion"] {
		t.Fatal("selected detail is incomplete or mismatched", detail)
	}
	configuration, failed := f.call(t, "factory_state", map[string]any{"view": "configuration"})
	if failed {
		t.Fatal(configuration)
	}
	readyResponse := func(name string, args map[string]any) []byte {
		t.Helper()
		result, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, name, args)
		if err != nil || result["isError"] == true {
			t.Fatal("saved view failed", name, result, err)
		}
		response := result["structuredContent"].(map[string]any)["data"].(map[string]any)["response"].(map[string]any)
		if response["state"] != "ready" {
			t.Fatal("unrelated accounting made a prepared saved view unavailable", name, response)
		}
		return mustJSON(response)
	}
	views := []struct {
		name   string
		args   map[string]any
		before []byte
	}{
		{"factory_state", emptyArgs, nil}, {"factory_state", conversationArgs, nil}, {"factory_item", detailArgs, nil},
	}
	for i := range views {
		views[i].before = readyResponse(views[i].name, views[i].args)
	}
	now := time.Now().UTC()
	if err := f.a.Store.Put(f.ctx, "github-usage", "application", map[string]any{"Calls": []time.Time{now, now}, "NotModified": []time.Time{now}}, ""); err != nil {
		t.Fatal(err)
	}
	updated, failed := f.call(t, "factory_state", map[string]any{"view": "configuration"})
	if failed || updated["version"] == configuration["version"] || updated["githubUsage"].(map[string]any)["requestAttemptsLastHour"] != float64(2) {
		t.Fatal("accounting did not refresh its owning configuration view", updated)
	}
	for _, view := range views {
		if after := readyResponse(view.name, view.args); !bytes.Equal(after, view.before) {
			t.Fatal("accounting changed unrelated empty/selected saved content or revision", view.name, string(view.before), string(after))
		}
	}
}
