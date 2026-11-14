package factory

import (
	"codefactory/internal/codex"
	"codefactory/internal/providers"
	"context"
	"strings"
	"testing"
	"time"
)

func TestConfigurationInspectionReportsSavedGitHubAccounting(t *testing.T) {
	s, native, _ := fixture(t)
	ctx := context.Background()
	s.setClient("configuration", native)
	s.App.GitHub = &providers.GitHub{Token: func(context.Context) (string, error) {
		t.Error("configuration inspection resolved GitHub credentials")
		return "", nil
	}}
	now := time.Now().UTC()
	must(t, s.Store.Put(ctx, "github-usage", "application", map[string]any{"Calls": []time.Time{now, now}, "NotModified": []time.Time{now}}, ""))
	before, err := s.App.Config(ctx)
	must(t, err)
	value, held, err := s.dynamic(ctx, &Session{ID: "configuration", Kind: "configuration"}, "code_factory_inspect_configuration", bytes(map[string]string{"cwd": s.Dir}), codex.Message{})
	must(t, err)
	result := value.(map[string]any)
	usage := result["githubUsage"].(providers.GitHubUsage)
	if held || usage.RequestsLastHour != 2 || usage.RequestAttemptsLastHour != 2 || usage.NotModifiedLastHour != 1 || usage.AccountingAsOf.IsZero() || usage.RESTQuota != nil || usage.GraphQLQuota != nil || result["native"] == nil || result["skills"] == nil {
		t.Fatal("configuration inspection lost local accounting or invented remote quotas", result)
	}
	if contains(native.methods, "turn/start") || contains(native.methods, "config/batchWrite") {
		t.Fatal("accounting inspection launched work or changed configuration", native.methods)
	}
	after, err := s.App.Config(ctx)
	must(t, err)
	if after.PolicyVersion != before.PolicyVersion {
		t.Fatal("accounting inspection invalidated assessment policy")
	}
	for _, tool := range toolsFor("configuration") {
		if tool["name"] == "code_factory_inspect_configuration" && !strings.Contains(tool["description"].(string), "GitHub request accounting") {
			t.Fatal("configuration agent cannot discover saved accounting")
		}
	}
}
