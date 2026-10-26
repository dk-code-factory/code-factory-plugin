package app

import (
	"archive/tar"
	"bytes"
	"codefactory/internal/dependencies"
	"codefactory/internal/domain"
	"codefactory/internal/setup"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dependencyDone(t *testing.T, a *App, id string) DependencyOperation {
	t.Helper()
	var op DependencyOperation
	waitFor(t, func() bool {
		op, _ = a.Runs.DependencyOperation(context.Background(), id)
		return op.Status != "queued" && op.Status != "running"
	})
	waitFor(t, func() bool {
		if a.Runs.adminGate.TryLock() {
			a.Runs.adminGate.Unlock()
			return true
		}
		return false
	})
	return op
}

func TestDependencyUpdateIsIdempotentAndKeepsUncertainOutcomes(t *testing.T) {
	root := pluginFixture(t)
	a, ctx := fixture(t)
	if err := os.WriteFile(filepath.Join(root, "installed"), []byte("1.0.0"), 0600); err != nil {
		t.Fatal(err)
	}
	op, err := a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "upgrade-1", Action: "update", Targets: []string{"plugins"}})
	if err != nil {
		t.Fatal(err)
	}
	done := dependencyDone(t, a, op.ID)
	if done.Status != "succeeded" || len(done.Results) != 1 || done.Results[0].Current != "2.0.0" {
		t.Fatal(done)
	}
	before, _ := os.ReadFile(filepath.Join(root, "commands"))
	again, err := a.Runs.UpdateDependencies(ctx, op.DependencyRequest)
	if err != nil || again.Status != done.Status {
		t.Fatal(again, err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "commands"))
	if !bytes.Equal(before, after) {
		t.Fatal("idempotent delivery replayed native mutations")
	}
	if _, err = a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: op.ID, Action: "check", Targets: []string{"plugins"}}); err == nil {
		t.Fatal("id reused for another request")
	}
	t.Setenv("CF_PLUGIN_UNCERTAIN", "1")
	op, err = a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "upgrade-2", Action: "update", Targets: []string{"plugins"}})
	if err != nil {
		t.Fatal(err)
	}
	done = dependencyDone(t, a, op.ID)
	if done.Status != "partial" || done.Results[0].Status != "uncertain" || strings.Contains(fmt.Sprint(done), "private-plugin-secret-value") {
		t.Fatal(done)
	}
	before, _ = os.ReadFile(filepath.Join(root, "commands"))
	interrupted := DependencyOperation{DependencyRequest: DependencyRequest{ID: "crash", Action: "update", Targets: []string{"plugins"}}, Status: "running", Results: []DependencyResult{{ID: "fixture", Status: "running"}}}
	if err = a.Store.Put(ctx, "dependency-operation", interrupted.ID, interrupted, ""); err != nil {
		t.Fatal(err)
	}
	if err = a.Runs.recoverDependencyOperations(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := a.Runs.DependencyOperation(ctx, "crash")
	if got.Status != "uncertain" || got.Results[0].Status != "uncertain" {
		t.Fatal(got)
	}
	after, _ = os.ReadFile(filepath.Join(root, "commands"))
	if !bytes.Equal(before, after) {
		t.Fatal("recovery replayed update")
	}
}

func TestDependencyMaintenanceRejectsActiveWorkAndBlocksNewLaunches(t *testing.T) {
	root := pluginFixture(t)
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	a.Runs.mu.Lock()
	a.Runs.live["busy"] = &session{run: domain.Run{Status: "running"}}
	a.Runs.mu.Unlock()
	if _, err := a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "busy", Action: "check"}); !errors.Is(err, ErrMaintenanceBusy) {
		t.Fatal(err)
	}
	a.Runs.mu.Lock()
	delete(a.Runs.live, "busy")
	a.Runs.mu.Unlock()
	_ = os.WriteFile(filepath.Join(root, "installed"), []byte("1.0.0"), 0600)
	t.Setenv("CF_PLUGIN_SLOW", "1")
	op, err := a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "maintenance", Action: "check", Targets: []string{"plugins"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(ctx, run.ID, false); err == nil {
		t.Fatal("run started during maintenance")
	}
	if _, err = a.Runs.PluginAction(ctx, PluginAction{Action: "refresh"}); err == nil {
		t.Fatal("native maintenance overlapped")
	}
	if done := dependencyDone(t, a, op.ID); done.Status != "succeeded" {
		t.Fatal(done)
	}
}

func TestGitHubSkillUpgradePreservesBackupRejectsEditsAndUnsafeSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".codex/skills/example")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("---\nname: example\n---\noriginal"), 0600)
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	body := "---\nname: example\n---\nupdated"
	_ = tw.WriteHeader(&tar.Header{Name: "repo-sha/skills/example/SKILL.md", Mode: 0644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = gz.Close()
	archive := filepath.Join(t.TempDir(), "repo.tar.gz")
	_ = os.WriteFile(archive, b.Bytes(), 0600)
	sha := strings.Repeat("a", 40)
	gh := filepath.Join(t.TempDir(), "gh")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\ncase \"$2\" in */tarball/*) exec cat " + quote(archive) + ";; *) printf '%s\\n' " + quote(sha) + ";; esac\n"
	_ = os.WriteFile(gh, []byte(script), 0700)
	a, ctx := fixture(t)
	a.Runs.ProviderPaths = map[string]string{"gh": gh}
	source := GitHubSkillSource{Name: "example", Root: "codex", Repository: "owner/repo", Path: "skills/example", Ref: "main"}
	source, err := a.Runs.RegisterGitHubSkill(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	op, err := a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "skill-update", Action: "update", Targets: []string{"skills"}})
	if err != nil {
		t.Fatal(err)
	}
	done := dependencyDone(t, a, op.ID)
	if done.Status != "succeeded" || done.Results[0].Status != "updated" {
		t.Fatal(done)
	}
	current, _ := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if string(current) != body {
		t.Fatal("skill not replaced")
	}
	backup, _ := os.ReadFile(filepath.Join(a.DataDir, "dependency-backups", op.ID, "skills/codex/example/SKILL.md"))
	if !strings.Contains(string(backup), "original") {
		t.Fatal("backup missing")
	}
	var stored GitHubSkillSource
	_ = a.Store.Get(ctx, "dependency-skill-source", source.id(), &stored)
	digest, _ := dependencies.TreeDigest(target)
	if stored.Revision != sha || stored.Digest != digest {
		t.Fatal(stored, digest)
	}
	_ = os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("owner edit"), 0600)
	op, err = a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "skill-local-edit", Action: "update", Targets: []string{"skills"}})
	if err != nil {
		t.Fatal(err)
	}
	done = dependencyDone(t, a, op.ID)
	if done.Status != "partial" || done.Results[0].Status != "conflict" {
		t.Fatal(done)
	}
	current, _ = os.ReadFile(filepath.Join(target, "SKILL.md"))
	if string(current) != "owner edit" {
		t.Fatal("local work overwritten")
	}
	for _, bad := range []GitHubSkillSource{{Name: "example", Root: "codex", Repository: "owner/repo", Path: "skills/example", Ref: "../../other"}, {Name: "example", Root: "codex", Repository: "owner/repo", Path: "../outside", Ref: "main"}, {Name: "example", Root: "codex", Repository: "https://token@github.com/owner/repo", Path: "skills/example", Ref: "main"}} {
		if _, err = a.Runs.RegisterGitHubSkill(ctx, bad); err == nil {
			t.Fatal("unsafe source accepted")
		}
	}
}

func TestGitHubSkillUpgradeRejectsNestedNativePackages(t *testing.T) {
	for _, tc := range []struct {
		name, skillPath, manifest string
	}{
		{"root-native", "skills/example", ".codex-plugin/plugin.json"},
		{"nested-native", "plugin/skills/example", "plugin/.codex-plugin/plugin.json"},
		{"deep-standalone", "nested/skills/example", "unrelated/.codex-plugin/plugin.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			target := filepath.Join(home, ".codex/skills/example")
			if err := os.MkdirAll(target, 0700); err != nil {
				t.Fatal(err)
			}
			original := "---\nname: example\n---\noriginal"
			if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tw := tar.NewWriter(gz)
			updated := "---\nname: example\n---\nupdated"
			for name, body := range map[string]string{
				tc.skillPath + "/SKILL.md": updated,
				tc.manifest:                `{"name":"fixture","skills":"./skills"}`,
				"plugin/assets/helper":     "package resource",
			} {
				if err := tw.WriteHeader(&tar.Header{Name: "repo/" + name, Mode: 0644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				if _, err := tw.Write([]byte(body)); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			archivePath := filepath.Join(t.TempDir(), "repo.tar.gz")
			if err := os.WriteFile(archivePath, archive.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			sha := strings.Repeat("a", 40)
			gh := filepath.Join(t.TempDir(), "gh")
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
			script := "#!/bin/sh\ncase \"$2\" in */tarball/*) exec cat " + quote(archivePath) + ";; *) printf '%s\\n' " + quote(sha) + ";; esac\n"
			if err := os.WriteFile(gh, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			a, ctx := fixture(t)
			a.Runs.ProviderPaths = map[string]string{"gh": gh}
			source, err := a.Runs.RegisterGitHubSkill(ctx, GitHubSkillSource{Name: "example", Root: "codex", Repository: "owner/repo", Path: tc.skillPath, Ref: "main"})
			if err != nil {
				t.Fatal(err)
			}
			op, err := a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "package-check", Action: "update", Targets: []string{"skills"}})
			if err != nil {
				t.Fatal(err)
			}
			done := dependencyDone(t, a, op.ID)
			current, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			var stored GitHubSkillSource
			if err := a.Store.Get(ctx, "dependency-skill-source", source.id(), &stored); err != nil {
				t.Fatal(err)
			}
			if tc.name != "deep-standalone" {
				if done.Status != "partial" || len(done.Results) != 1 || done.Results[0].Status != "failed" || !strings.Contains(done.Results[0].Detail, "native plugin") {
					t.Fatal("native skill leaf was not rejected", done)
				}
				if string(current) != original || string(raw(stored)) != string(raw(source)) {
					t.Fatal("native rejection changed installed skill or provenance")
				}
				if _, err := os.Stat(filepath.Join(a.DataDir, "dependency-backups", op.ID)); !os.IsNotExist(err) {
					t.Fatal("native rejection began replacement", err)
				}
			} else {
				backup, err := os.ReadFile(filepath.Join(a.DataDir, "dependency-backups", op.ID, "skills/codex/example/SKILL.md"))
				if done.Status != "succeeded" || string(current) != updated || stored.Revision != sha || err != nil || string(backup) != original {
					t.Fatal("deep standalone skill lost update or backup", done, err)
				}
			}
		})
	}
}

func TestCodexActivationUpdatesRecordedProviderAndKeepsPriorRuntime(t *testing.T) {
	a, ctx := fixture(t)
	bin := a.Runs.CodexBinary()
	paths := map[string]string{"git": bin, "gh": bin, "systemctl": bin, "codex": bin}
	before := setup.Runtime{Paths: paths, Path: "/usr/bin", DataDir: a.DataDir, Listen: "127.0.0.1:8787"}
	if err := setup.AtomicJSON(filepath.Join(a.DataDir, "runtime.json"), before); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), "codex")
	data, _ := os.ReadFile(bin)
	_ = os.WriteFile(candidate, data, 0700)
	if err := a.Runs.activateCodex(ctx, "activation", candidate); err != nil {
		t.Fatal(err)
	}
	after, err := setup.ReadRuntime(a.DataDir)
	if err != nil || after.Paths["codex"] != candidate || a.Runs.CodexBinary() != candidate || after.Listen != before.Listen {
		t.Fatal(after, err)
	}
	var saved setup.Runtime
	b, err := os.ReadFile(filepath.Join(a.DataDir, "dependency-backups/activation/runtime.json"))
	if err != nil || json.Unmarshal(b, &saved) != nil || saved.Paths["codex"] != bin {
		t.Fatal("previous runtime not retained", err)
	}
}

type dependencyRoundTrip func(*http.Request) (*http.Response, error)

func (f dependencyRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCodexUpdateProbesBeforeSwitchAndRetainsProviderOnFailure(t *testing.T) {
	pluginFixture(t)
	a, ctx := fixture(t)
	fake := a.Runs.CodexBinary()
	t.Setenv("CF_FAKE_VERSION", "0.164.0")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	old := filepath.Join(t.TempDir(), "codex")
	wrapper := "#!/bin/sh\nif [ \"$1\" = '--version' ]; then echo 'codex-cli 0.161.0'; else exec " + quote(fake) + " \"$@\"; fi\n"
	_ = os.WriteFile(old, []byte(wrapper), 0700)
	a.Runs.Binary = old
	paths := map[string]string{"git": fake, "gh": fake, "codex": old, "systemctl": fake}
	a.Runs.ProviderPaths = paths
	if err := setup.AtomicJSON(filepath.Join(a.DataDir, "runtime.json"), setup.Runtime{Paths: paths, Path: "/usr/bin", DataDir: a.DataDir, Listen: "127.0.0.1:8787"}); err != nil {
		t.Fatal(err)
	}
	makeArchive := func(binary []byte) []byte {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		tw := tar.NewWriter(gz)
		_ = tw.WriteHeader(&tar.Header{Name: "bin/codex", Mode: 0700, Size: int64(len(binary)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(binary)
		_ = tw.Close()
		_ = gz.Close()
		return b.Bytes()
	}
	data, _ := os.ReadFile(fake)
	archive := makeArchive(data)
	version := "0.164.0"
	a.Runs.dependencyHTTP = &http.Client{Transport: dependencyRoundTrip(func(r *http.Request) (*http.Response, error) {
		body := archive
		if r.URL.Host == "api.github.com" {
			hash := sha256.Sum256(archive)
			release := dependencies.Release{Tag: "rust-v" + version, Assets: []dependencies.Asset{{Name: "codex-package-x86_64-unknown-linux-musl.tar.gz", URL: "https://github.com/dkropachev/codex/releases/download/rust-v" + version + "/package.tar.gz", Digest: "sha256:" + hex.EncodeToString(hash[:])}}}
			body, _ = json.Marshal(release)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	op, err := a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "codex-new", Action: "update", Targets: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	done := dependencyDone(t, a, op.ID)
	if done.Status != "succeeded" || done.Results[0].Status != "updated" || done.Results[0].Current != "0.164.0" || a.Runs.CodexBinary() == old {
		t.Fatal(done, a.Runs.CodexBinary())
	}
	active := a.Runs.CodexBinary()
	previous, _ := os.ReadFile(old)
	if string(previous) != wrapper {
		t.Fatal("global/previous provider was modified")
	}
	version = "0.165.0"
	archive = makeArchive([]byte("#!/bin/sh\nif [ \"$1\" = '--version' ]; then echo 'codex-cli 0.165.0'; else exit 1; fi\n"))
	op, err = a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "codex-incompatible", Action: "update", Targets: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	done = dependencyDone(t, a, op.ID)
	if done.Status != "partial" || done.Results[0].Status != "failed" || a.Runs.CodexBinary() != active {
		t.Fatal("incompatible provider replaced working one", done)
	}
	runtime, err := setup.ReadRuntime(a.DataDir)
	if err != nil || runtime.Paths["codex"] != active {
		t.Fatal("failed update changed persisted provider", runtime, err)
	}
}

func TestLegacyDKSkillMigrationRebindsProfilesAndRecoversMissingReferences(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, ".codex/skills/dk-pr-post-review")
	_ = os.MkdirAll(legacy, 0700)
	_ = os.WriteFile(filepath.Join(legacy, "SKILL.md"), []byte("---\nname: dk-pr-post-review\n---\nlegacy"), 0600)
	root := filepath.Join(t.TempDir(), "native")
	_ = os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0700)
	_ = os.WriteFile(filepath.Join(root, ".codex-plugin/plugin.json"), []byte(`{"name":"dk-workflows","repository":"https://github.com/dk-code-factory/dk-workflows","skills":"./codex/skills"}`), 0600)
	native := filepath.Join(root, "codex/skills/dk-pr-post-review")
	_ = os.MkdirAll(native, 0700)
	_ = os.WriteFile(filepath.Join(native, "SKILL.md"), []byte("---\nname: dk-pr-post-review\n---\ncurrent native"), 0600)
	t.Setenv("CF_FAKE_SKILL_ROOT", native)
	t.Setenv("CF_FAKE_SKILL_NAME", "dk-workflows:dk-pr-post-review")
	a, ctx := fixture(t)
	var profile domain.Profile
	_ = a.Store.Get(ctx, "profile", "review", &profile)
	profile.Skills = []string{filepath.Join(legacy, "SKILL.md")}
	if err := a.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	op, err := a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "migrate-dk", Action: "update", Targets: []string{"skills"}, MigrateLegacyDK: true})
	if err != nil {
		t.Fatal(err)
	}
	done := dependencyDone(t, a, op.ID)
	if done.Status != "succeeded" {
		t.Fatal(done)
	}
	_ = a.Store.Get(ctx, "profile", "review", &profile)
	if len(profile.Skills) != 1 || profile.Skills[0] != filepath.Join(native, "SKILL.md") {
		t.Fatal(profile)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy duplicate remains active")
	}
	if _, err := os.Stat(filepath.Join(a.DataDir, "dependency-backups", op.ID, "legacy-skills/dk-pr-post-review/SKILL.md")); err != nil {
		t.Fatal("legacy backup missing", err)
	}
	// A crash between the file archive and profile transaction restores any old
	// path still referenced by a profile, without replaying the migration.
	profile.Skills = []string{filepath.Join(legacy, "SKILL.md")}
	_ = a.SaveProfile(ctx, profile)
	interrupted := done
	interrupted.Status = "running"
	_ = a.Store.Put(ctx, "dependency-operation", interrupted.ID, interrupted, "")
	if err := a.Runs.recoverDependencyOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "SKILL.md")); err != nil {
		t.Fatal("interrupted migration left a broken configured skill", err)
	}
	// Reviewer duplicates use the same upgrade path, but unrelated lookalike
	// plugins and escaping skill identities cannot supply replacements.
	if !canonicalDKSkill("code-review-ad:code-review-ad") || !canonicalDKSkill("code-review-matpocock:code-review-matpocock") || canonicalDKSkill("other:code-review-ad") || canonicalDKSkill("dk-workflows:dk-../../outside") {
		t.Fatal("canonical migration scope")
	}
	adLegacy := filepath.Join(home, ".codex/skills/code-review-ad")
	_ = os.MkdirAll(adLegacy, 0700)
	_ = os.WriteFile(filepath.Join(adLegacy, "SKILL.md"), []byte("---\nname: code-review-ad\n---\nlegacy"), 0600)
	_ = os.WriteFile(filepath.Join(root, ".codex-plugin/plugin.json"), []byte(`{"name":"code-review-ad","repository":"https://github.com/dk-code-factory/code-review-ad","skills":"./codex/skills"}`), 0600)
	adNative := filepath.Join(root, "codex/skills/code-review-ad")
	_ = os.MkdirAll(adNative, 0700)
	_ = os.WriteFile(filepath.Join(adNative, "SKILL.md"), []byte("---\nname: code-review-ad\n---\ncurrent native"), 0600)
	t.Setenv("CF_FAKE_SKILL_ROOT", adNative)
	t.Setenv("CF_FAKE_SKILL_NAME", "code-review-ad:code-review-ad")
	profile.Skills = []string{filepath.Join(adLegacy, "SKILL.md")}
	_ = a.SaveProfile(ctx, profile)
	adOp, err := a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "migrate-reviewer", Action: "update", Targets: []string{"skills"}, MigrateLegacyDK: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := dependencyDone(t, a, adOp.ID); got.Status != "succeeded" {
		t.Fatal(got)
	}
	_ = a.Store.Get(ctx, "profile", "review", &profile)
	if profile.Skills[0] != filepath.Join(adNative, "SKILL.md") {
		t.Fatal("reviewer skill not rebound", profile)
	}
}

func TestUpdatedPluginProfilesDeduplicateDiscoveryAndKeepPreparedSnapshots(t *testing.T) {
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	var p domain.Profile
	_ = a.Store.Get(ctx, "profile", "review", &p)
	p.Skills = []string{"old-plugin-path", "new-plugin-path"}
	_ = a.SaveProfile(ctx, p)
	old := []domain.Skill{{ID: "old-plugin-path", Name: "plugin:review"}}
	current := []domain.Skill{{ID: "new-plugin-path", Name: "plugin:review"}, {ID: "new-plugin-path", Name: "plugin:review"}}
	if err := a.Runs.remapProfileSkills(ctx, old, current); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Get(ctx, "profile", "review", &p)
	if len(p.Skills) != 1 || p.Skills[0] != "new-plugin-path" {
		t.Fatal("repeated repository discovery prevented update", p)
	}
	var retained domain.Run
	_ = a.Store.Get(ctx, "run", run.ID, &retained)
	if string(raw(retained)) != string(raw(run)) {
		t.Fatal("dependency update changed prepared snapshot")
	}
}

func TestDependencyInventoryExcludesSkillContents(t *testing.T) {
	a, ctx := fixture(t)
	secret := "synthetic-private-skill-content"
	if err := a.Store.Put(ctx, "skill", "fixture", domain.Skill{ID: "fixture", Name: "fixture", Content: secret, Files: map[string]string{"SKILL.md": secret}}, ""); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Put(ctx, "plugin-inventory", "current", domain.PluginInventory{Plugins: []domain.CodexPlugin{{ID: "installed@fixture", Installed: true}, {ID: "available@fixture"}}}, "")
	state, err := a.Runs.DependencyState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(state)
	if bytes.Contains(body, []byte(secret)) || !bytes.Contains(body, []byte(`"name":"fixture"`)) {
		t.Fatal("inventory exposed contents or lost safe metadata")
	}
	if bytes.Contains(body, []byte("available@fixture")) || !bytes.Contains(body, []byte("installed@fixture")) {
		t.Fatal("owner dependency inventory contains uninstalled catalog entries")
	}
}

func TestDependencyMarketplaceFailureDoesNotBlockIndependentPlugins(t *testing.T) {
	root := pluginFixture(t)
	t.Setenv("CF_PLUGIN_GITMARKETS", "1")
	_ = os.WriteFile(filepath.Join(root, "installed"), []byte("1.0.0"), 0600)
	a, ctx := fixture(t)
	op, err := a.Runs.UpdateDependencies(ctx, DependencyRequest{ID: "independent-markets", Action: "update", Targets: []string{"plugins"}})
	if err != nil {
		t.Fatal(err)
	}
	done := dependencyDone(t, a, op.ID)
	states := map[string]string{}
	for _, r := range done.Results {
		states[r.ID] = r.Status
	}
	if done.Status != "partial" || states["marketplace:unavailable"] != "failed" || states["marketplace:fixture"] != "checked" || states["fixture@fixture"] != "updated" {
		t.Fatal(done)
	}
	if strings.Contains(fmt.Sprint(done), "private-marketplace-secret") {
		t.Fatal("marketplace failure leaked output")
	}
}
