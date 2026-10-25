package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pluginFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CF_FAKE_PLUGIN_ROOT", root)
	t.Setenv("CF_PLUGIN_RECORD", filepath.Join(root, "commands"))
	if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex-plugin/plugin.json"), []byte(`{"name":"fixture","version":"2.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}
func pluginDone(t *testing.T, a *App, id string) domain.PluginOperation {
	t.Helper()
	var op domain.PluginOperation
	waitFor(t, func() bool {
		_ = a.Store.Get(a.Runs.ctx, "plugin-operation", id, &op)
		return op.Status != "queued" && op.Status != "running"
	})
	// Completion is persisted immediately before the worker releases its gate.
	waitFor(t, func() bool {
		if a.Runs.adminGate.TryLock() {
			a.Runs.adminGate.Unlock()
			return true
		}
		return false
	})
	return op
}
func TestPluginInventoryInstallUpdateAndRemoval(t *testing.T) {
	root := pluginFixture(t)
	a, ctx := fixture(t)
	for _, action := range []string{"refresh", "install", "check-updates", "update", "remove"} {
		if action == "check-updates" {
			if err := os.WriteFile(filepath.Join(root, ".codex-plugin/plugin.json"), []byte(`{"name":"fixture","version":"3.0.0"}`), 0600); err != nil {
				t.Fatal(err)
			}
		}
		target := ""
		if action == "install" || action == "update" || action == "remove" {
			target = "fixture@fixture"
		}
		op, err := a.Runs.PluginAction(ctx, PluginAction{Action: action, Target: target})
		if err != nil {
			t.Fatal(err)
		}
		if got := pluginDone(t, a, op.ID); got.Status != "succeeded" {
			t.Fatal(got)
		}
		state, err := a.Runs.PluginState(ctx, PluginAdminInput{Available: true})
		if err != nil {
			t.Fatal(err)
		}
		plugins := state["inventory"].(domain.PluginInventory).Plugins
		if len(plugins) != 1 {
			t.Fatal(plugins)
		}
		p := plugins[0]
		switch action {
		case "refresh", "remove":
			if p.Installed {
				t.Fatal("not installed plugin reported installed")
			}
		case "install":
			if !p.Installed || !p.Enabled || p.InstalledVersion != "2.0.0" || p.UpdateStatus != "current" {
				t.Fatal(p)
			}
		case "check-updates":
			if p.InstalledVersion != "2.0.0" || p.AvailableVersion != "3.0.0" || p.UpdateStatus != "available" {
				t.Fatal("check installed an update", p)
			}
		case "update":
			if p.InstalledVersion != "3.0.0" || p.UpdateStatus != "current" {
				t.Fatal(p)
			}
		}
	}
	commands, err := os.ReadFile(filepath.Join(root, "commands"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(commands), `["plugin","add","--json","--","fixture@fixture"]`) {
		t.Fatal("selector not a distinct positional argument")
	}
}
func TestPluginUncertainDeliveryIsObservedWithoutReplay(t *testing.T) {
	root := pluginFixture(t)
	t.Setenv("CF_PLUGIN_UNCERTAIN", "1")
	a, ctx := fixture(t)
	op, err := a.Runs.PluginAction(ctx, PluginAction{Action: "install", Target: "fixture@fixture"})
	if err != nil {
		t.Fatal(err)
	}
	done := pluginDone(t, a, op.ID)
	if done.Status != "uncertain" || strings.Contains(done.Error, "private-plugin-secret-value") {
		t.Fatal(done)
	}
	var inv domain.PluginInventory
	if err := a.Store.Get(ctx, "plugin-inventory", "current", &inv); err != nil || len(inv.Plugins) != 1 || !inv.Plugins[0].Installed {
		t.Fatal("partial success lost", inv, err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "commands"))
	if err := a.Runs.recoverPluginOperations(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "commands"))
	if string(before) != string(after) {
		t.Fatal("operation replayed")
	}
	events, _ := a.Store.Events(ctx, 0, 200)
	for _, e := range events {
		if strings.Contains(string(e.Data), "private-plugin-secret-value") {
			t.Fatal("provider stderr escaped into events")
		}
	}
}
func TestPluginRestartAndMalformedInventoryRetainObservedState(t *testing.T) {
	pluginFixture(t)
	a, ctx := fixture(t)
	op, err := a.Runs.PluginAction(ctx, PluginAction{Action: "install", Target: "fixture@fixture"})
	if err != nil {
		t.Fatal(err)
	}
	pluginDone(t, a, op.ID)
	t.Setenv("CF_PLUGIN_BAD_JSON", "1")
	op, err = a.Runs.PluginAction(ctx, PluginAction{Action: "refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if pluginDone(t, a, op.ID).Status != "failed" {
		t.Fatal("invalid inventory succeeded")
	}
	var inv domain.PluginInventory
	_ = a.Store.Get(ctx, "plugin-inventory", "current", &inv)
	if len(inv.Plugins) != 1 || !inv.Plugins[0].Installed || inv.Error == "" {
		t.Fatal("failed read erased inventory", inv)
	}
	lost := domain.PluginOperation{ID: "lost-install", Action: "install", Target: "fixture@fixture", Status: "running"}
	if err := a.Store.Put(ctx, "plugin-operation", lost.ID, lost, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.recoverPluginOperations(ctx); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Get(ctx, "plugin-operation", lost.ID, &lost)
	if lost.Status != "uncertain" {
		t.Fatal(lost)
	}
}
func TestPluginMaintenanceCannotRaceStartsOrAcceptInjectedArguments(t *testing.T) {
	pluginFixture(t)
	t.Setenv("CF_PLUGIN_SLOW", "1")
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	for _, in := range []PluginAction{{Action: "install", Target: "--config=bad"}, {Action: "add-marketplace", Target: "https://user:secret@example.org/repo"}, {Action: "add-marketplace", Target: "https://example.org/repo?token=secret"}, {Action: "refresh", Target: "unexpected"}, {Action: "unknown"}} {
		if _, err := a.Runs.PluginAction(ctx, in); err == nil {
			t.Fatal("invalid action accepted", in.Action)
		}
	}
	op, err := a.Runs.PluginAction(ctx, PluginAction{Action: "refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Runs.Start(ctx, run.ID, false); err == nil {
		t.Fatal("start raced plugin maintenance")
	}
	if err := a.Runs.Message(ctx, run.ID, "Retain this message draft", ""); err == nil || !strings.Contains(err.Error(), "maintenance") {
		t.Fatal("message bypassed maintenance gate")
	}
	if err := a.Runs.Answer(ctx, "synthetic-request", raw(map[string]any{})); err == nil || !strings.Contains(err.Error(), "maintenance") {
		t.Fatal("answer bypassed maintenance gate")
	}
	if _, err := a.Runs.PluginAction(ctx, PluginAction{Action: "remove", Target: "fixture@fixture"}); err == nil {
		t.Fatal("second maintenance accepted")
	}
	if _, err := os.Stat(run.Worktree); !os.IsNotExist(err) {
		t.Fatal("maintenance rejection created a workspace")
	}
	pluginDone(t, a, op.ID)
	s := &session{run: domain.Run{ID: "busy", Status: "running"}}
	a.Runs.mu.Lock()
	a.Runs.live[s.run.ID] = s
	a.Runs.mu.Unlock()
	if _, err := a.Runs.PluginAction(ctx, PluginAction{Action: "install", Target: "fixture@fixture"}); err == nil {
		t.Fatal("maintenance changed active plugin state")
	}
}
func TestPluginInventoryPagingAndUnknownRemoteVersion(t *testing.T) {
	a, ctx := fixture(t)
	inv := domain.PluginInventory{ID: "current", Plugins: []domain.CodexPlugin{{ID: "remote@remote", Name: "remote", Installed: true, UpdateStatus: "unknown", SourceType: "remote"}, {ID: "other@market", Name: "other", Installed: false}}}
	if err := a.Store.Put(ctx, "plugin-inventory", "current", inv, ""); err != nil {
		t.Fatal(err)
	}
	state, err := a.Runs.PluginState(ctx, PluginAdminInput{})
	if err != nil {
		t.Fatal(err)
	}
	if state["total"] != 1 || state["inventory"].(domain.PluginInventory).Plugins[0].UpdateStatus != "unknown" {
		t.Fatal(state)
	}
	state, err = a.Runs.PluginState(ctx, PluginAdminInput{Available: true, Query: "other", Limit: 1})
	if err != nil || state["total"] != 1 || state["inventory"].(domain.PluginInventory).Plugins[0].ID != "other@market" {
		t.Fatal(state, err)
	}
}
func TestSelectedDiscoveredSkillKeepsItsBundleAndAutomaticChecksDoNotInstall(t *testing.T) {
	root := pluginFixture(t)
	skill := filepath.Join(root, "skill")
	if err := os.MkdirAll(skill, 0700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: fixture-plugin-skill\ndescription: Test plugin skill.\n---\nUse the test helper.\n"
	_ = os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(content), 0600)
	_ = os.WriteFile(filepath.Join(skill, "helper.sh"), []byte("#!/bin/sh\ntrue\n"), 0700)
	t.Setenv("CF_FAKE_SKILL_ROOT", skill)
	a, ctx := fixture(t)
	op, err := a.Runs.PluginAction(ctx, PluginAction{Action: "discover-skills"})
	if err != nil {
		t.Fatal(err)
	}
	if pluginDone(t, a, op.ID).Status != "succeeded" {
		t.Fatal("discovery failed")
	}
	skills, err := store.List[domain.Skill](ctx, a.Store, "skill")
	if err != nil || len(skills) != 1 {
		t.Fatal(skills, err)
	}
	run := prepareRun(t, a, ctx)
	prepared, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: run.ItemID, ProfileID: "review", SkillIDs: []string{skills[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Skills) != 1 || prepared.Skills[0].Content != content || prepared.Skills[0].Files["helper.sh"] == "" || prepared.Skills[0].Modes["helper.sh"]&0111 == 0 {
		t.Fatal("skill bundle not preserved")
	}
	config, _ := a.Config(ctx)
	config.PluginCheckMinutes = 15
	if err := a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	inv := domain.PluginInventory{ID: "current", CheckedAt: time.Now().Add(-time.Hour), Plugins: []domain.CodexPlugin{}, Marketplaces: []domain.PluginMarketplace{}}
	_ = a.Store.Put(ctx, "plugin-inventory", "current", inv, "")
	a.Runs.maybeCheckPlugins(ctx)
	var scheduled domain.PluginOperation
	waitFor(t, func() bool {
		ops, _ := store.List[domain.PluginOperation](ctx, a.Store, "plugin-operation")
		for _, o := range ops {
			if o.Action == "check-updates" {
				scheduled = o
				return true
			}
		}
		return false
	})
	pluginDone(t, a, scheduled.ID)
	commands, _ := os.ReadFile(filepath.Join(root, "commands"))
	if strings.Contains(string(commands), `"add"`) {
		t.Fatal("scheduled check installed a plugin")
	}
}

func TestPluginMaintenanceDefersInferenceAndRejectsUnavailableSkills(t *testing.T) {
	pluginFixture(t)
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	a.Runs.adminGate.Lock()
	if !a.Runs.busy() {
		t.Fatal("background inference did not yield to plugin maintenance")
	}
	config, _ := a.Config(ctx)
	if _, err := a.infer(ctx, item(), config); err == nil {
		t.Fatal("inference started during maintenance")
	}
	a.Runs.adminGate.Unlock()
	skill := domain.Skill{ID: "disabled", Name: "disabled-skill", Path: "/unavailable/SKILL.md", Disabled: true}
	if err := a.Store.Put(ctx, "skill", skill.ID, skill, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Runs.Prepare(ctx, LaunchInput{ItemID: run.ItemID, ProfileID: "review", SkillIDs: []string{skill.ID}}); err == nil {
		t.Fatal("disabled skill was prepared")
	}
}
