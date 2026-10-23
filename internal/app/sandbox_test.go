package app

import (
	"codefactory/internal/codex"
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
