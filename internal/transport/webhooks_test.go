package transport

import (
	"bytes"
	"codefactory/internal/factory"
	"testing"
)

func TestConfigurationWebhookProjectionRefreshesWithoutSecrets(t *testing.T) {
	f := newTrackingFixture(t)
	settings := factory.Defaults()
	settings.Repositories = []factory.Repository{{ID: "o/r"}}
	if err := f.a.Store.Put(f.ctx, "factory-settings", "effective", settings, ""); err != nil {
		t.Fatal(err)
	}
	before, failed := f.call(t, "factory_state", map[string]any{"view": "configuration"})
	if failed || before["webhooks"].([]any)[0].(map[string]any)["state"] != "pending" {
		t.Fatal(before)
	}
	if status := before["webhooks"].([]any)[0].(map[string]any); status["retryAt"] != nil || status["at"] != nil {
		t.Fatal("pending setup must not invent a check timestamp", status)
	}
	// Private provisioning metadata must not cross the projection boundary.
	if err := f.a.Store.Put(f.ctx, "factory-webhook", "o/r", map[string]any{"repository": "o/r", "state": "polling", "error": "Webhook setup failed; API polling remains active.", "input": "private-secret-fingerprint", "hookId": 42}, ""); err != nil {
		t.Fatal(err)
	}
	after, failed := f.call(t, "factory_state", map[string]any{"view": "configuration", "sinceVersion": before["version"], "sinceInstanceId": f.server.operations.instance})
	if failed || after["version"] == before["version"] || after["webhooks"].([]any)[0].(map[string]any)["state"] != "polling" {
		t.Fatal(after)
	}
	if bytes.Contains(mustJSON(after), []byte("private-secret")) || bytes.Contains(mustJSON(after), []byte("hookId")) {
		t.Fatal("private setup metadata escaped")
	}
	issues, failed := f.call(t, "factory_state", map[string]any{"view": "issues"})
	if failed || issues["webhooks"] != nil {
		t.Fatal("webhook diagnostics escaped configuration view", issues)
	}
}
