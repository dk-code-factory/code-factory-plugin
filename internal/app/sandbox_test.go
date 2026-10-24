package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/commandenv"
	"codefactory/internal/domain"
	"codefactory/internal/testutil"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedTaskSandboxSupportsGitHubAuthAndReviewerConfiguration(t *testing.T) {
	if os.Getenv("CODE_FACTORY_REAL_CODEX") != "1" {
		t.Skip("opt-in pinned provider check; no model/API call")
	}
	binary := os.Getenv("CODE_FACTORY_SANDBOX_BINARY")
	if binary == "" {
		binary = "codex"
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(cache, "code-factory-auth-sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	work := filepath.Join(root, "work")
	if err = os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--quiet", work).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	a, ctx := fixture(t)
	a.DataDir = root
	a.Runs.Binary = binary
	r := domain.Run{ID: ID(), Worktree: work, Profile: domain.Profile{Sandbox: "read-only", Approval: "on-request", Publishing: "ask"}}
	args, err := a.Runs.taskSandbox(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	c, err := codex.Start(ctx, binary, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	code := `import subprocess,json,os,sys
r={}
for key,args in [('git',['git','status','--porcelain']),('auth',['gh','auth','token']),('reviewers',['python3',sys.argv[1],'--cwd',os.getcwd(),'reviewers','list'])]:
 try:r[key]=subprocess.run(args,capture_output=True,timeout=15).returncode==0
 except Exception:r[key]=False
print(json.dumps(r))`
	var result struct {
		ExitCode       int
		Stdout, Stderr string
	}
	scratch := filepath.Join(root, "scratch", r.ID)
	if err = c.Call(ctx, "command/exec", map[string]any{"cwd": work, "command": []string{"python3", "-c", code, filepath.Join(home, ".codex", "skills", "dk-config", "scripts", "repo_rules.py")}, "env": map[string]string{"XDG_DATA_HOME": filepath.Join(scratch, "data"), "TMPDIR": filepath.Join(scratch, "tmp"), "GIT_OPTIONAL_LOCKS": "0", "PYTHONDONTWRITEBYTECODE": "1"}, "timeoutMs": 20000}, &result); err != nil {
		t.Fatal(err)
	}
	var checks map[string]bool
	if err = json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &checks); err != nil {
		t.Fatal(err, result.Stderr)
	}
	if result.ExitCode != 0 || !checks["git"] || !checks["auth"] || !checks["reviewers"] {
		t.Fatalf("read-only review setup failed: %v", checks)
	}
	t.Log("Git status, existing GitHub authentication and configured reviewer lookup succeeded; no model/GitHub API call")
}

func TestTaskSandboxRetainsIsolationAcrossSessionWake(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	argsPath := filepath.Join(t.TempDir(), "args.jsonl")
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("CF_FAKE_ARGS_RECORD", argsPath)
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
	before := r
	a.Runs.Close()
	a.Runs = NewManager(ctx, a, testutil.FakeCodex(t))
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Message(ctx, r.ID, "Continue the same isolated task.", ""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	boots := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(boots) > 0 && strings.Contains(boots[0], "/.codex/accounts") {
		t.Fatal("standalone task exposed native provider credential stores without a selected plugin package")
	}
	if len(boots) != 2 || boots[0] != boots[1] || !strings.Contains(boots[0], "features.network_proxy=true") || !strings.Contains(boots[0], "default_permissions=") || !strings.Contains(boots[0], "api.github.com") || strings.Contains(boots[0], "danger-full-access") {
		t.Fatal("start/resume lost the invocation-local sandbox", string(b))
	}
	b, err = os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var call struct {
			Method string
			Params map[string]any
		}
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		if call.Method == "thread/start" || call.Method == "thread/resume" {
			seen++
			if _, ok := call.Params["sandbox"]; ok {
				t.Fatal("legacy sandbox overrides scoped permissions")
			}
			if call.Params["approvalPolicy"] != before.Profile.Approval {
				t.Fatal("approval authority changed")
			}
		}
	}
	if seen != 2 {
		t.Fatal("missing initial/resumed thread")
	}
	var after domain.Run
	_ = a.Store.Get(ctx, "run", r.ID, &after)
	if after.ThreadID != before.ThreadID || after.Worktree != before.Worktree {
		t.Fatal("wake replaced the retained session")
	}
	if err := a.Runs.Interrupt(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "interrupted" })
	if err := a.Runs.Cleanup(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.DataDir, "scratch", r.ID)); !os.IsNotExist(err) {
		t.Fatal("cleanup retained private scratch")
	}
}

func TestExplicitFullAccessKeepsScratchAndDisablesTechnicalApprovals(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	ownerData := t.TempDir()
	t.Setenv("XDG_DATA_HOME", ownerData)
	for _, name := range []string{"codex-repo-rules", "dk-pr-decisions"} {
		if err := os.MkdirAll(filepath.Join(ownerData, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ownerData, name, "settings.json"), []byte(`{"owner":"fixture"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	argsPath := filepath.Join(t.TempDir(), "args.jsonl")
	record := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("CF_FAKE_ARGS_RECORD", argsPath)
	t.Setenv("CF_FAKE_RECORD", record)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	r.Profile.Sandbox, r.Profile.Approval, r.Profile.Publishing = "danger-full-access", "never", "authorized"
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
	for _, name := range []string{"codex-repo-rules", "dk-pr-decisions"} {
		target := filepath.Join(a.DataDir, "scratch", r.ID, "data", name, "settings.json")
		data, err := os.ReadFile(target)
		if err != nil || string(data) != `{"owner":"fixture"}` {
			t.Fatal("full-access launch lost existing owner review context", name, err)
		}
		if err := os.WriteFile(target, []byte(`{"run":"retained"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Runs.taskSandbox(ctx, r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex-repo-rules", "dk-pr-decisions"} {
		data, err := os.ReadFile(filepath.Join(a.DataDir, "scratch", r.ID, "data", name, "settings.json"))
		if err != nil || string(data) != `{"run":"retained"}` {
			t.Fatal("continuation overwrote retained review context", name, err)
		}
		owner, err := os.ReadFile(filepath.Join(ownerData, name, "settings.json"))
		if err != nil || string(owner) != `{"owner":"fixture"}` {
			t.Fatal("task changed owner review settings", name, err)
		}
	}
	b, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "features.network_proxy=false") || !strings.Contains(string(b), filepath.Join(a.DataDir, "scratch", r.ID, "tmp")) || strings.Contains(string(b), "default_permissions=") {
		t.Fatal("full access inherited bounded network or lost scratch", string(b))
	}
	b, err = os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var call struct {
			Method string
			Params map[string]any
		}
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		if call.Method == "thread/start" {
			found = true
			if call.Params["sandbox"] != "danger-full-access" || call.Params["approvalPolicy"] != "never" || !strings.Contains(call.Params["developerInstructions"].(string), "Ask the user only for a material decision") {
				t.Fatal("owner autonomy not forwarded", call.Params)
			}
		}
	}
	if !found {
		t.Fatal("missing native thread authority")
	}
}

func TestInvalidTaskSandboxFailsWithoutProviderFallback(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	argsPath := filepath.Join(t.TempDir(), "provider-args.jsonl")
	t.Setenv("CF_FAKE_ARGS_RECORD", argsPath)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	r.Profile.Sandbox = "invalid"
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "failed" })
	if !strings.Contains(r.Error, "could not initialize task sandbox") {
		t.Fatal(r.Error)
	}
	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatal("failed sandbox started an unrestricted provider")
	}
}

func TestSandboxSkillDataIsPrivateBoundedAndRetained(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "copy")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "reviewers.json"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := seedSandboxData(source, target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "reviewers.json"), []byte("run cache"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := seedSandboxData(source, target); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(source, "reviewers.json"))
	if string(b) != "original" {
		t.Fatal("owner settings changed")
	}
	b, _ = os.ReadFile(filepath.Join(target, "reviewers.json"))
	if string(b) != "run cache" {
		t.Fatal("resume overwrote run cache")
	}
	if err := os.Symlink(filepath.Join(source, "reviewers.json"), filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := seedSandboxData(source, filepath.Join(root, "rejected")); err == nil {
		t.Fatal("symlink escaped policy snapshot")
	}
	if _, err := os.Stat(filepath.Join(root, "rejected")); !os.IsNotExist(err) {
		t.Fatal("failed copy published partial data")
	}
}

func TestPinnedWorkflowLifecyclePersistsAcrossSandboxCommands(t *testing.T) {
	if os.Getenv("CODE_FACTORY_REAL_CODEX") != "1" || os.Getenv("CODE_FACTORY_WORKFLOW_PLUGIN") == "" {
		t.Skip("opt-in installed DK lifecycle command check; no model calls")
	}
	binary := os.Getenv("CODE_FACTORY_SANDBOX_BINARY")
	if binary == "" {
		binary = "codex"
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(cache, "code-factory-lifecycle-sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--quiet", work).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	a, ctx := fixture(t)
	a.DataDir = root
	a.Runs.Binary = binary
	selected := map[string]string{"codex": binary}
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("private synthetic fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"git", "gh", "systemctl"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		selected[name] = path
	}
	if path := os.Getenv("CODE_FACTORY_GITHUB_BINARY"); path != "" {
		selected["gh"] = path
	}
	bin, err := commandenv.Prepare(filepath.Join(root, "provider-bin"), selected)
	if err != nil {
		t.Fatal(err)
	}
	a.Runs.ProviderBin, a.Runs.ProviderPaths = bin, selected

	r := domain.Run{ID: ID(), Worktree: work, Profile: domain.Profile{Sandbox: "read-only", Approval: "on-request", Publishing: "ask"}}
	skill, err := snapshotSkill(domain.Skill{Path: filepath.Join(os.Getenv("CODE_FACTORY_WORKFLOW_PLUGIN"), "codex/skills/dk-pr-review/SKILL.md")})
	if err != nil {
		t.Fatal(err)
	}
	r.Skills = []domain.Skill{skill}
	packageRoot := filepath.Join(root, "snapshots", r.ID, "0")
	if _, err := materializeSkill(packageRoot, skill); err != nil {
		t.Fatal(err)
	}
	args, err := a.Runs.taskSandbox(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(root, "scratch", r.ID)
	config := filepath.Join(scratch, "config")
	if err := os.MkdirAll(filepath.Join(config, "dk-code-factory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "dk-code-factory/updates.json"), []byte(`{"mode":"off"}`), 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := (codex.TaskSandbox{ID: r.ID, Mode: "read-only", Workspace: work, Scratch: scratch, CommandPath: commandenv.Path(bin, os.Getenv("PATH"))}).Config()
	if err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{"XDG_CONFIG_HOME": config, "PATH": filepath.Dir(binary) + string(os.PathListSeparator) + os.Getenv("PATH")}
	if os.Getenv("CODE_FACTORY_NATIVE_CONTEXT_TRACE") == "1" {
		environment["CODE_FACTORY_NATIVE_CONTEXT_TRACE"] = "1"
	}
	for k, v := range policy["shell_environment_policy.set"].(map[string]any) {
		environment[k] = v.(string)
	}
	children, err := os.ReadDir(filepath.Join(scratch, "provider-bin"))
	if err != nil || len(children) != 1 {
		t.Fatal("missing task-specific child provider launcher", err)
	}
	environment["PATH"] = commandenv.Path(filepath.Join(scratch, "provider-bin", children[0].Name()), environment["PATH"])
	serverBinary, hostTrace := binary, ""
	if os.Getenv("CODE_FACTORY_NATIVE_CONTEXT_TRACE") == "1" {
		hostTrace = filepath.Join(root, "native-filesystem.trace")
		serverBinary = filepath.Join(root, "trace-codex")
		quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
		wrapper := "#!/bin/sh\nexec /usr/bin/strace -f -e trace=open,openat,openat2,creat,mkdir,mkdirat,rename,renameat,renameat2,link,linkat,symlink,symlinkat,unlink,unlinkat,truncate,ftruncate,chmod,fchmod,fchmodat -o " + quote(hostTrace) + " " + quote(binary) + " \"$@\"\n"
		if err := os.WriteFile(serverBinary, []byte(wrapper), 0700); err != nil {
			t.Fatal(err)
		}
	}
	c, err := codex.Start(ctx, serverBinary, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	var result struct {
		ExitCode       int
		Stdout, Stderr string
	}
	entry := `import subprocess,json,pathlib,sys,os
import shutil
assert shutil.which('gh')==sys.argv[3], 'selected gh launcher is not first in the command PATH'
login=subprocess.run(['/usr/bin/zsh','-lc','command -v gh'],capture_output=True,text=True,timeout=10)
assert login.returncode==0 and login.stdout.strip()==sys.argv[3], 'login shell replaced the selected provider PATH'
version=subprocess.run(['gh','version'],capture_output=True,text=True,timeout=10)
assert version.returncode==0 and version.stdout.splitlines()[0]==sys.argv[4], 'task selected a different gh version'
p=subprocess.run(['python3',sys.argv[1],'entry','--host','codex'],capture_output=True,text=True,timeout=90)
assert p.returncode==0, 'lifecycle entry failed'
r=json.loads(p.stdout)
assert r.get('runtime_root') and r.get('token'), 'missing retained runtime receipt'
pathlib.Path(sys.argv[2]).write_text(json.dumps(r))
registry=subprocess.run(['codex','plugin','list','--json'],capture_output=True,text=True,timeout=30)
assert registry.returncode==0, 'Installed plugin registry is unavailable inside bounded commands'
installed=json.loads(registry.stdout).get('installed',[])
assert any(p.get('name')=='code-review-ad' for p in installed), 'Installed AD reviewer is absent from bounded registry'
assert any(p.get('name')=='code-review-matpocock' for p in installed), 'Installed Matt Pocock reviewer is absent from bounded registry'
auth=subprocess.run(['codex','login','status'],capture_output=True,text=True,timeout=15)
assert auth.returncode==0, 'Existing Codex authentication is unavailable to bounded nested execution'
mcp=subprocess.run(['codex','mcp','list','--json'],capture_output=True,text=True,timeout=30)
assert mcp.returncode==0, 'Child provider MCP configuration unavailable'
assert all(not s.get('enabled') for s in json.loads(mcp.stdout)), 'Nested provider inherited an ambient MCP connection'
if sys.argv[5]=='1':
 output=pathlib.Path(sys.argv[2]+'.nested-result')
 proof=pathlib.Path(sys.argv[2]+'.nested-command')
 source=pathlib.Path(os.getcwd())/'nested-source-write-forbidden'
 unrelated=pathlib.Path(os.getcwd()).parent/'unrelated.txt'
 probe="""import json,socket,shutil,subprocess
from pathlib import Path
r={'answer':4,'sourceWrite':False,'unrelatedRead':False,'directLocal':False,'selectedGh':False}
r['selectedGh']=shutil.which('gh')==%r and subprocess.check_output(['gh','version'],text=True).splitlines()[0]==%r
try:
 Path(%r).write_text('forbidden');r['sourceWrite']=True
except OSError: pass
try:
 Path(%r).read_text();r['unrelatedRead']=True
except OSError: pass
try:
 s=socket.create_connection(('127.0.0.1',8787),timeout=1);s.close();r['directLocal']=True
except OSError: pass
Path(%r).write_text(json.dumps(r))
""" % (sys.argv[3],sys.argv[4],str(source),str(unrelated),str(proof))
 prompt='Use your code or command tools to execute this exact private acceptance probe: '+repr('python3 -c '+__import__('shlex').quote(probe))+'. The two denied file operations and denied direct localhost connection are expected; the scratch JSON should contain answer4 and false for all three permission checks. Verify the command succeeded, then reply exactly CF_NESTED_PROVIDER_OK. Do not inspect other files or contact external systems.'
 if sys.argv[6]=='1':
  prompt='Use native spawn_agent with task_name standards_probe and fork_turns="all" to delegate the following Standards acceptance probe with a full-history fork to one independent subagent, then wait for its completed result. Do not execute the probe yourself. If native delegation is unavailable, report that and do not write the proof. '+prompt
 model_command=['codex','exec','--ephemeral','--sandbox','read-only','--skip-git-repo-check','-C',os.getcwd(),'-o',str(output),'-']
 model=subprocess.run(model_command,input=prompt,capture_output=True,text=True,timeout=120)
 assert model.returncode==0, 'Nested model call failed: '+model.stderr[-1800:]
 final=output.read_text().strip()
 if not proof.is_file():
  import re
  for line in model.stdout.splitlines():
   try:
    event=json.loads(line);item=event.get('item',{})
    if event.get('type')=='error' or item.get('type')=='error':
     detail=str(item.get('message') or item.get('text') or event.get('message') or '')
     detail=re.sub(r'https?://[^\s\"\']+',lambda m:m.group().split('?')[0].split('#')[0],detail)
     detail=re.sub(r'(?i)authorization:[^\n]+|bearer\s+\S+|(?:sk|rk|sess)-[A-Za-z0-9_-]{8,}','[redacted]',detail)
     print('NATIVE_CONTEXT_DIAGNOSTIC '+detail[:1200],file=sys.stderr)
   except (ValueError,TypeError,AttributeError):pass
 assert proof.is_file() and json.loads(proof.read_text())=={'answer':4,'sourceWrite':False,'unrelatedRead':False,'directLocal':False,'selectedGh':True}, 'Nested command proof missing, provider changed or outer bounds changed; model response: '+final[-1500:]
 assert not source.exists(), 'Child changed the read-only source checkout'
 assert final=='CF_NESTED_PROVIDER_OK', 'Nested model output was not the requested marker: '+final[-1500:]
print('ENTRY_RETAINED')`
	versionBytes, err := exec.Command(selected["gh"], "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	expectedVersion := strings.SplitN(string(versionBytes), "\n", 2)[0]
	receipt := filepath.Join(scratch, "tmp", "receipt.json")
	if err := c.Call(ctx, "command/exec", map[string]any{"cwd": work, "command": []string{"python3", "-c", entry, filepath.Join(packageRoot, "scripts/update.py"), receipt, filepath.Join(bin, "gh"), expectedVersion, os.Getenv("CODE_FACTORY_REAL_NESTED_CODEX"), os.Getenv("CODE_FACTORY_REAL_NESTED_AGENT")}, "env": environment, "timeoutMs": 180000}, &result); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "ENTRY_RETAINED" {
		if hostTrace != "" {
			data, _ := os.ReadFile(hostTrace)
			for _, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "EROFS") {
					t.Log("Native context denied write:", line)
				}
			}
		}
		t.Fatal("lifecycle entry did not retain its runtime", result.ExitCode, result.Stderr)
	}
	completion := `import json,pathlib,subprocess,sys
r=json.loads(pathlib.Path(sys.argv[1]).read_text())
root=pathlib.Path(r['runtime_root'])
assert (root/'scripts/dk.py').is_file(), 'retained router disappeared between commands'
assert pathlib.Path(r['completion_script']).is_file(), 'retained completion script disappeared'
p=subprocess.run(['python3',r['completion_script'],'complete','--host','codex','--token',r['token']],capture_output=True,text=True,timeout=90)
assert p.returncode==0, 'lifecycle completion failed'
print('LIFECYCLE_PERSISTED_AND_COMPLETED')`
	if err := c.Call(ctx, "command/exec", map[string]any{"cwd": work, "command": []string{"python3", "-c", completion, receipt}, "env": environment, "timeoutMs": 180000}, &result); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "LIFECYCLE_PERSISTED_AND_COMPLETED" {
		t.Fatal("cross-command lifecycle persistence failed", result.ExitCode, result.Stderr)
	}
	var saved struct {
		RuntimeRoot string `json:"runtime_root"`
	}
	data, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(saved.RuntimeRoot, filepath.Join(scratch, "state")+string(os.PathSeparator)) {
		t.Fatal("lifecycle state escaped private scratch")
	}
	if _, err := os.Stat(filepath.Join(saved.RuntimeRoot, "scripts/dk.py")); err != nil {
		t.Fatal("retained runtime did not persist on the host", err)
	}
	if os.Getenv("CODE_FACTORY_REAL_NESTED_CODEX") == "1" {
		if os.Getenv("CODE_FACTORY_NATIVE_CHILD_AUDIT") == "1" {
			files, err := os.ReadDir(filepath.Join(scratch, "child-audit"))
			if err != nil || len(files) == 0 {
				t.Fatal("nested command did not retain native metadata", err)
			}
			kinds := map[string]int{}
			spawned, completed := false, false
			projectedDelegate := false
			rootThread := ""
			for _, file := range files {
				data, err := os.ReadFile(filepath.Join(scratch, "child-audit", file.Name()))
				if err != nil || strings.Contains(string(data), "CF_NESTED_PROVIDER_OK") || strings.Contains(string(data), "nested-source-write-forbidden") {
					t.Fatal("native metadata retained message or command content", err)
				}
				for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
					var event struct {
						Event, Type, Tool string
						ToolName          string `json:"tool_name"`
						ThreadID          string `json:"thread_id"`
					}
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatal(err)
					}
					kinds[event.Event+":"+event.Type]++
					if event.Event == "thread.started" && rootThread == "" {
						rootThread = event.ThreadID
					}
					if event.Event == "item.completed" && strings.Contains(event.Type, "collab") {
						t.Log("Native collaboration metadata:", line)
						spawned = spawned || event.Tool == "spawn_agent" || event.Tool == "spawnAgent" || event.ToolName == "spawn_agent" || event.ToolName == "spawnAgent"
					}
					var fields map[string]json.RawMessage
					_ = json.Unmarshal([]byte(line), &fields)
					for _, key := range []string{"agents_states", "agent_states", "agentsStates", "agentStates"} {
						var agents map[string]struct{ Status string }
						_ = json.Unmarshal(fields[key], &agents)
						for _, agent := range agents {
							completed = completed || agent.Status == "completed"
						}
					}
					var agents []struct{ Status string }
					_ = json.Unmarshal(fields["agents"], &agents)
					for _, agent := range agents {
						completed = completed || agent.Status == "completed"
					}
					if event.Event == "native.agent" {
						var node struct {
							Root   string `json:"root_thread_id"`
							Parent string `json:"parent_thread_id"`
							Status string
						}
						_ = json.Unmarshal([]byte(line), &node)
						projectedDelegate = projectedDelegate || (rootThread != "" && node.Root == rootThread && node.Parent == rootThread && node.Status == "completed")
					}
				}
			}
			// MultiAgentV2 emits activity rather than legacy spawn items. Its own
			// persisted child metadata and terminal turn events are independent
			// execution evidence, without inspecting any owner session directory.
			if os.Getenv("CODE_FACTORY_REAL_NESTED_AGENT") == "1" {
				sessions := filepath.Join(scratch, "state", "native-context", "sessions")
				_ = filepath.WalkDir(sessions, func(path string, entry os.DirEntry, walkErr error) error {
					if walkErr != nil || entry.IsDir() || !entry.Type().IsRegular() || filepath.Ext(path) != ".jsonl" {
						return nil
					}
					data, err := os.ReadFile(path)
					if err != nil || len(data) > 16<<20 {
						return nil
					}
					id, parent, state := "", "", "unknown"
					for _, line := range strings.Split(string(data), "\n") {
						var record struct {
							Type    string
							Payload struct {
								ID, Type string
								Parent   string `json:"parent_thread_id"`
							}
						}
						if json.Unmarshal([]byte(line), &record) != nil {
							continue
						}
						if record.Type == "session_meta" && id == "" {
							id, parent = record.Payload.ID, record.Payload.Parent
						}
						if record.Type == "event_msg" {
							switch record.Payload.Type {
							case "task_started", "turn_started":
								state = "running"
							case "task_complete", "turn_complete":
								state = "completed"
							case "turn_aborted":
								state = "interrupted"
							}
						}
					}
					if rootThread != "" && parent == rootThread && id != "" && id != rootThread {
						spawned = true
						completed = completed || state == "completed"
						t.Log("Native persisted delegate metadata:", id, "parent", parent, "state", state)
					}
					return nil
				})
			}
			if kinds["thread.started:"] == 0 || rootThread == "" || kinds["exit:"] == 0 {
				t.Fatal("native CLI lifecycle missing from metadata", kinds)
			}
			t.Log("Native CLI metadata event types:", kinds)
			if os.Getenv("CODE_FACTORY_REAL_NESTED_AGENT") == "1" && (!spawned || !completed) {
				t.Fatal("independent nested agent execution was not observed", spawned, completed)
			}
			if os.Getenv("CODE_FACTORY_REAL_NESTED_AGENT") == "1" && !projectedDelegate {
				t.Fatal("native metadata projection omitted the independently verified completed delegate")
			}
		}
		t.Log("Actual nested Codex model executed private scratch command and returned CF_NESTED_PROVIDER_OK; source write, unrelated read and direct localhost access stayed denied; lifecycle, installed reviewers and existing login remained available")
	} else {
		t.Log("Actual DK lifecycle/router/completion persist across two sandbox commands; installed reviewer registry and existing Codex authentication are accessible read-only; no model calls")
	}
}

func TestMarketplaceMetadataDoesNotGrantOwnerDirectories(t *testing.T) {
	paths, err := marketplaceMetadataPaths([]byte(`{"marketplaces":[{"root":"/home/owner"},{"root":"/private/pilot-dependency"}]}`))
	if err != nil || len(paths) != 4 {
		t.Fatal(paths, err)
	}
	want := map[string]bool{"/home/owner/.agents/plugins/marketplace.json": true, "/home/owner/.codex-plugin/plugin.json": true, "/private/pilot-dependency/.agents/plugins/marketplace.json": true, "/private/pilot-dependency/.codex-plugin/plugin.json": true}
	for _, path := range paths {
		if !want[path] {
			t.Fatal("marketplace granted a directory or unrelated path", path)
		}
	}
	for _, input := range []string{`null`, `{"marketplaces":null}`, `{"marketplaces":[{"root":"../owner"}]}`} {
		if _, err := marketplaceMetadataPaths([]byte(input)); err == nil {
			t.Fatal("malformed marketplace metadata accepted", input)
		}
	}
}

func TestNativePluginInventoryRetainsValidatedPrivateSnapshot(t *testing.T) {
	a, ctx := fixture(t)
	id := ID()
	path, digest, err := a.Runs.nativeRegistrySnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || len(digest) != 64 {
		t.Fatal("invalid private registry snapshot", err)
	}
	a.Runs.Binary = "/unavailable/native/provider"
	again, second, err := a.Runs.nativeRegistrySnapshot(ctx, id)
	if err != nil || again != path || second != digest {
		t.Fatal("retained run queried or substituted another registry", err)
	}
	if err := os.WriteFile(path, []byte(`{"installed":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Runs.nativeRegistrySnapshot(ctx, id); err == nil {
		t.Fatal("malformed retained registry accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "preserve")
	if err := os.WriteFile(outside, []byte(`{"installed":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Runs.nativeRegistrySnapshot(ctx, id); err == nil {
		t.Fatal("retained registry followed an owner-file symlink")
	}
}
