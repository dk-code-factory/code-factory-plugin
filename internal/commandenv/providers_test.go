package commandenv

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func providersFixture(t *testing.T, label string) map[string]string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "selected ' $(touch UNEXPECTED)")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, name := range []string{"git", "gh", "codex", "systemctl"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+label+"-"+name+"' \"$@\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		out[name] = path
	}
	return out
}

func TestSelectedProvidersWinWithoutGlobalReplacement(t *testing.T) {
	selected := providersFixture(t, "selected")
	fallback := providersFixture(t, "system")
	base := filepath.Join(t.TempDir(), "provider-bin")
	directory, err := Prepare(base, selected)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"git", "gh", "codex", "systemctl"} {
		cmd := exec.Command("/bin/sh", "-c", name+" 'literal argument'")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "PATH="+Path(directory, filepath.Dir(fallback[name])))
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != "selected-"+name+"\nliteral argument\n" {
			t.Fatal(name, err, string(out))
		}
		if _, err := os.Stat(filepath.Join(cmd.Dir, "UNEXPECTED")); !os.IsNotExist(err) {
			t.Fatal("provider path was interpreted as shell code")
		}
	}
	newer := providersFixture(t, "new")
	next, err := Prepare(base, newer)
	if err != nil || next == directory {
		t.Fatal("changed provider selection overwrote rollback launchers", err)
	}
	cmd := exec.Command(filepath.Join(directory, "gh"))
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "selected-gh") {
		t.Fatal("prior provider launchers changed", err, string(out))
	}
}

func TestUnavailableProvidersNeverFallBack(t *testing.T) {
	providers := providersFixture(t, "selected")
	providers["gh"] = "/nonexistent/gh"
	if _, err := Prepare(filepath.Join(t.TempDir(), "bin"), providers); err == nil {
		t.Fatal("missing selected provider accepted")
	}
	providers["gh"] = "relative/gh"
	if _, err := Prepare(filepath.Join(t.TempDir(), "bin"), providers); err == nil {
		t.Fatal("relative selected provider accepted")
	}
}

func TestProviderDirectoryRejectsEscapingSymlinks(t *testing.T) {
	providers := providersFixture(t, "selected")
	base := filepath.Join(t.TempDir(), "provider-bin")
	directory, err := Prepare(base, providers)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "preserve")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, "gh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "gh")); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(base, providers); err == nil {
		t.Fatal("provider launcher escaped its directory")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "original" {
		t.Fatal("outside file changed", err)
	}
}

func TestChildCodexOverridesRemainLiteralAndVersioned(t *testing.T) {
	selected := providersFixture(t, "selected")
	base := filepath.Join(t.TempDir(), "child-bin")
	overrides := []string{`features.apps=false`, `mcp_servers."name'$(touch UNEXPECTED)".enabled=false`}
	first, err := PrepareCodex(base, selected["codex"], overrides)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(first, "codex"), "exec", "literal argument")
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	want := "selected-codex\n-c\n" + overrides[0] + "\n-c\n" + overrides[1] + "\nexec\nliteral argument\n"
	if err != nil || string(out) != want {
		t.Fatal("child provider arguments changed", err, string(out))
	}
	if _, err := os.Stat(filepath.Join(cmd.Dir, "UNEXPECTED")); !os.IsNotExist(err) {
		t.Fatal("child configuration executed as shell code")
	}
	second, err := PrepareCodex(base, selected["codex"], []string{`features.apps=true`})
	if err != nil || first == second {
		t.Fatal("changed child configuration overwrote a retained launcher", err)
	}
	target := filepath.Join(t.TempDir(), "preserve")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(first, "codex")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(first, "codex")); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareCodex(base, selected["codex"], overrides); err == nil {
		t.Fatal("child launcher escaped its directory")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "original" {
		t.Fatal("outside child launcher target changed", err)
	}
	if err := os.RemoveAll(filepath.Join(base, "provider-bin")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "provider-bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareCodex(base, selected["codex"], overrides); err == nil {
		t.Fatal("child provider directory escaped private scratch")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("child provider resume wrote outside private scratch", err)
	}
}

func TestChildCodexUsesOnlyItsDistributionCompanion(t *testing.T) {
	selected := providersFixture(t, "selected")
	companion := filepath.Join(filepath.Dir(selected["codex"]), "codex-code-mode-host")
	if err := os.WriteFile(companion, []byte("#!/bin/sh\nprintf '%s\\n' 'selected-companion' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	path, err := CodexCompanion(selected["codex"])
	if err != nil || path != companion {
		t.Fatal("wrong companion selected", path, err)
	}
	bin, err := PrepareCodex(t.TempDir(), selected["codex"], nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join(bin, "codex-code-mode-host"), "literal argument").CombinedOutput()
	if err != nil || string(out) != "selected-companion\nliteral argument\n" {
		t.Fatal("selected companion or arguments changed", err, string(out))
	}
	if err := os.Chmod(companion, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexCompanion(selected["codex"]); err == nil {
		t.Fatal("non-executable companion accepted")
	}
	if err := os.Remove(companion); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(selected["codex"], companion); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexCompanion(selected["codex"]); err == nil {
		t.Fatal("unrelated companion symlink accepted")
	}
}

func TestChildProviderUsesVerifiedInventoryAndRequiresOuterRestrictions(t *testing.T) {
	selected := providersFixture(t, "selected")
	registry := filepath.Join(t.TempDir(), "registry ' literal.json")
	data := []byte(`{"installed":[{"name":"reviewer","enabled":true}],"available":[]}`)
	if err := os.WriteFile(registry, data, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	bin, err := PrepareCodex(t.TempDir(), selected["codex"], []string{"features.apps=false"}, ChildOptions{Registry: registry, RegistrySHA256: fmt.Sprintf("%x", digest), ExternallyReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(bin, "codex")
	out, err := exec.Command(launcher, "plugin", "list", "--json").CombinedOutput()
	var result map[string]any
	if err != nil || json.Unmarshal(out, &result) != nil || len(result["installed"].([]any)) != 1 {
		t.Fatal("native inventory was not retained", err, string(out))
	}
	if err := os.WriteFile(registry, []byte(`{"installed":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(launcher, "plugin", "list", "--json").CombinedOutput(); err == nil || !strings.Contains(string(out), "no live fallback") {
		t.Fatal("changed inventory fell back to another registry", err, string(out))
	}
	setpriv, err := exec.LookPath("setpriv")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"exec", "--ephemeral", "--sandbox", "read-only", "--skip-git-repo-check", "-C", t.TempDir(), "-"}
	cmd := exec.Command(setpriv, append([]string{"--no-new-privs", launcher}, args...)...)
	out, err = cmd.CombinedOutput()
	status, statusErr := os.ReadFile("/proc/self/status")
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if strings.Contains(string(status), "Seccomp:\t2\n") {
		if err != nil || !strings.Contains(string(out), "\ndanger-full-access\n") || strings.Contains(string(out), "\nread-only\n") {
			t.Fatal("supported child did not select the existing outer boundary", err, string(out))
		}
	} else if err == nil || !strings.Contains(string(out), "requires the enforced outer task sandbox") {
		t.Fatal("unconfined child was accepted", err, string(out))
	}
	// A differently shaped invocation retains its own sandbox arguments.
	out, err = exec.Command(launcher, "exec", "--sandbox", "read-only", "-").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "\nread-only\n") {
		t.Fatal("unrelated child invocation was rewritten", err, string(out))
	}
}

func TestChildAuditRetainsOnlyNativeExecutionMetadata(t *testing.T) {
	selected := providersFixture(t, "selected")
	program := `#!/usr/bin/python3
import json
print(json.dumps({'type':'item.completed','thread_id':'thread-fixture','item':{'id':'collab-1','type':'collab_tool_call','tool':'spawn_agent','status':'completed','receiver_thread_ids':['child-standards'],'prompt':'Standards private-fixture-text','agents_states':{'child-standards':{'status':'completed','message':'private-fixture-text'}}}}))
print(json.dumps({'type':'item.completed','item':{'id':'message-1','type':'agent_message','text':'private-fixture-text','command':'https://example.invalid/private-fixture-text?private-fixture-text'}}))
`
	if err := os.WriteFile(selected["codex"], []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	audit := filepath.Join(t.TempDir(), "audit")
	bin, err := PrepareCodex(t.TempDir(), selected["codex"], nil, ChildOptions{AuditDir: audit})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"exec", "--ephemeral", "--sandbox", "read-only", "--skip-git-repo-check", "-C", t.TempDir(), "-"}
	if _, err := exec.Command(filepath.Join(bin, "codex"), args...).Output(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(audit)
	if err != nil || len(files) != 1 {
		t.Fatal("missing native audit", err)
	}
	path := filepath.Join(audit, files[0].Name())
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "private-fixture-text") || strings.Contains(string(data), "https://") {
		t.Fatal("audit retained prompt, message, command or URL content", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || !strings.Contains(string(data), `"roleHints":["Standards"]`) || !strings.Contains(string(data), `"returncode":0`) {
		t.Fatal("audit lost actual execution metadata or private permissions", err)
	}
}

func TestChildAuditProjectsOnlyItsOwnCompletedDelegates(t *testing.T) {
	selected := providersFixture(t, "selected")
	context := t.TempDir()
	sessions := filepath.Join(context, "sessions")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"root.jsonl": `{"type":"session_meta","payload":{"id":"thread-root","creator_account_id":"private-fixture-text"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"text":"Apply the installed procedure at /native/codex/skills/code-review-ad/SKILL.md. private-fixture-text"}]}}
{"type":"event_msg","payload":{"type":"task_complete"}}`,
		"child.jsonl": `{"type":"session_meta","payload":{"id":"thread-child","parent_thread_id":"thread-root","agent_path":"/root/standards_probe","creator_user_id":"private-fixture-text"}}
{"type":"session_meta","payload":{"id":"thread-root","creator_account_id":"private-fixture-text"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"text":"private-fixture-text"}]}}
{"type":"event_msg","payload":{"type":"task_started"}}
{"type":"event_msg","payload":{"type":"task_complete"}}`,
		"other.jsonl": `{"type":"session_meta","payload":{"id":"other-private-thread","parent_thread_id":"other-root","agent_path":"/root/spec"}}
{"type":"event_msg","payload":{"type":"task_complete"}}`,
	} {
		if err := os.WriteFile(filepath.Join(sessions, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(selected["codex"], []byte("#!/usr/bin/python3\nprint('{\"type\":\"thread.started\",\"thread_id\":\"thread-root\"}')\n"), 0700); err != nil {
		t.Fatal(err)
	}
	audit := filepath.Join(t.TempDir(), "audit")
	bin, err := PrepareCodex(t.TempDir(), selected["codex"], nil, ChildOptions{ContextHome: context, AuditDir: audit})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"exec", "--ephemeral", "--sandbox", "read-only", "--skip-git-repo-check", "-C", t.TempDir(), "-"}
	if _, err := exec.Command(filepath.Join(bin, "codex"), args...).Output(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(audit)
	if err != nil || len(files) != 1 {
		t.Fatal("missing projected audit", err)
	}
	data, err := os.ReadFile(filepath.Join(audit, files[0].Name()))
	if err != nil || strings.Contains(string(data), "private-fixture-text") || strings.Contains(string(data), "other-private-thread") || strings.Contains(string(data), "creator_account_id") {
		t.Fatal("projection retained content or unrelated context metadata", err)
	}
	if !strings.Contains(string(data), `"event":"native.root","thread_id":"thread-root","reviewer":"code-review-ad","status":"completed"`) || !strings.Contains(string(data), `"event":"native.agent","thread_id":"thread-child","parent_thread_id":"thread-root"`) || !strings.Contains(string(data), `"roleHints":["Standards"]`) {
		t.Fatal("projection lost authoritative provider/delegate identity or completion", string(data))
	}
}
