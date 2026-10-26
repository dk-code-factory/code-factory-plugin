package codex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrivateNativeContextCannotBroadenWritableScope(t *testing.T) {
	scratch := "/private/task"
	context := scratch + "/state/native-context"
	policy := TaskSandbox{ID: "native-context", Mode: "read-only", Workspace: "/work", Scratch: scratch, Readable: []string{context}, WritableScratch: []string{context + "/sessions"}}
	config, err := policy.Config()
	if err != nil {
		t.Fatal(err)
	}
	fs := config["permissions.code-factory-native-context"].(map[string]any)["filesystem"].(map[string]any)
	if fs[context] != "read" || fs[context+"/sessions"] != "write" || fs["/work"] != "read" {
		t.Fatal("native state permissions changed source or reference protection", fs)
	}
	for _, outside := range []string{"/owner", "/private/other", "relative"} {
		policy.WritableScratch = []string{outside}
		if _, err := policy.Config(); err == nil {
			t.Fatal("native writable state escaped private scratch", outside)
		}
	}
}

func TestTaskSandboxPolicyAndInvalidRoots(t *testing.T) {
	for _, mode := range []string{"read-only", "workspace-write"} {
		s := TaskSandbox{ID: "fixture", Mode: mode, Workspace: "/workspace", Scratch: "/scratch", Readable: []string{"/skills"}}
		cfg, err := s.Config()
		if err != nil {
			t.Fatal(err)
		}
		env := cfg["shell_environment_policy.set"].(map[string]any)
		if env["XDG_STATE_HOME"] != "/scratch/state" {
			t.Fatal("workflow lifecycle state is not privately persistent", env["XDG_STATE_HOME"])
		}
		profile := cfg["permissions.code-factory-fixture"].(map[string]any)
		fs := profile["filesystem"].(map[string]any)
		want := "read"
		if mode == "workspace-write" {
			want = "write"
		}
		if fs["/workspace"] != want || fs["/scratch"] != "write" || fs["/skills"] != "read" || fs["/"] != nil || fs[":workspace_roots"] != nil {
			t.Fatal("sandbox widened a root or lost scratch/skill access", fs)
		}
		network := profile["network"].(map[string]any)
		domains := network["domains"].(map[string]any)
		if cfg["features.network_proxy"] != true || network["enabled"] != true || network["allow_local_binding"] != false || domains["api.github.com"] != "allow" || domains["ab.chatgpt.com"] != "allow" || domains["sdmntprcentralus.oaiusercontent.com"] != "allow" || domains["developers.openai.com"] != "allow" || domains["learn.chatgpt.com"] != "allow" || domains["*"] != nil || domains["localhost"] != nil {
			t.Fatal("network allowlist is not enforced", cfg)
		}
		if mode == "workspace-write" && fs["/workspace/.git"] != "read" {
			t.Fatal("Git metadata is writable")
		}
	}
	for _, s := range []TaskSandbox{
		{ID: "bad", Mode: "danger-full-access", Workspace: "/work", Scratch: "/scratch"},
		{ID: "../bad", Mode: "read-only", Workspace: "/work", Scratch: "/scratch"},
		{ID: "bad", Mode: "read-only", Workspace: "/", Scratch: "/scratch"},
		{ID: "bad", Mode: "read-only", Workspace: "/work", Scratch: "/"},
		{ID: "bad", Mode: "read-only", Workspace: "/work", Scratch: "/work"},
		{ID: "bad", Mode: "read-only", Workspace: "/work", Scratch: "/scratch", Readable: []string{"/"}},
	} {
		if _, err := s.Args(); err == nil {
			t.Fatal("unsafe sandbox accepted", s)
		}
	}
}

// Explicit opt-in: execute fixture commands through the installed provider,
// without a model turn, authentication export or HTTP request to the GitHub API.
func TestPinnedTaskSandboxEnforcement(t *testing.T) {
	if os.Getenv("CODE_FACTORY_REAL_CODEX") != "1" {
		t.Skip("opt-in pinned provider sandbox check")
	}
	binary := os.Getenv("CODE_FACTORY_SANDBOX_BINARY")
	if binary == "" {
		binary = "codex"
	}
	binary, err := exec.LookPath(binary)
	if err != nil {
		t.Fatal(err)
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Version(context.Background(), binary); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"read-only", "workspace-write"} {
		t.Run(mode, func(t *testing.T) {
			cache, err := os.UserCacheDir()
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(cache, 0700); err != nil {
				t.Fatal(err)
			}
			root, err := os.MkdirTemp(cache, "code-factory-native-sandbox-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(root)
			work := filepath.Join(root, "workspace with spaces")
			scratch := filepath.Join(root, "scratch")
			for _, p := range []string{work, filepath.Join(scratch, "tmp"), filepath.Join(work, ".git")} {
				if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(work, "fixture.txt"), []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "outside.txt"), []byte("preserve owner data"), 0600); err != nil {
				t.Fatal(err)
			}
			s := TaskSandbox{ID: "native-fixture", Mode: mode, Workspace: work, Scratch: scratch, Readable: []string{binary}}
			args, err := s.Args()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c, err := Start(ctx, binary, args, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err = c.Initialize(ctx); err != nil {
				t.Fatal(err)
			}
			var thread struct{ Sandbox map[string]any }
			if err = c.Call(ctx, "thread/start", map[string]any{"cwd": work, "ephemeral": true, "approvalPolicy": "on-request"}, &thread); err != nil {
				t.Fatal(err)
			}
			t.Logf("resolved sandbox: %v", thread.Sandbox)
			code := `import pathlib,json,os,http.client,urllib.parse,socket,sys
r={}
root=pathlib.Path(sys.argv[1]);work=root/'workspace with spaces'
print('cwd='+os.getcwd(),file=sys.stderr)
for key,path,op in [('readWorkspace',work/'fixture.txt','read'),('writeWorkspace',work/'new.txt','write'),('readUnrelated',root/'unrelated.txt','read'),('writeOutside',root/'outside.txt','write'),('writeScratch',root/'scratch'/'tmp'/'result.txt','write'),('writeGit',work/'.git'/'new','write')]:
 try:
  path.read_text() if op=='read' else path.write_text('fixture')
  r[key]=True
 except OSError:r[key]=False
for key,url in [('githubTLS','https://api.github.com'),('codexFlagsTLS','https://ab.chatgpt.com'),('codexContentTLS','https://sdmntprcentralus.oaiusercontent.com'),('developerDocsTLS','https://developers.openai.com'),('learnDocsTLS','https://learn.chatgpt.com'),('otherTLS','https://example.com'),('privateTLS','https://127.0.0.1:8787')]:
 u=urllib.parse.urlsplit(url);proxy=urllib.parse.urlsplit(os.environ.get('HTTPS_PROXY') or os.environ.get('https_proxy',''))
 try:
  c=http.client.HTTPSConnection(proxy.hostname,proxy.port,timeout=5);c.set_tunnel(u.hostname,u.port or 443);c.connect();c.close();r[key]=True
 except Exception:r[key]=False
try:
 c=socket.create_connection(('127.0.0.1',8787),timeout=2);c.close();r['directLocal']=True
except OSError:r['directLocal']=False
print(json.dumps(r))`
			var result struct {
				ExitCode       int
				Stdout, Stderr string
			}
			if err = c.Call(ctx, "command/exec", map[string]any{"cwd": work, "command": []string{"/usr/bin/python3", "-c", code, root}, "timeoutMs": 20000}, &result); err != nil {
				t.Fatal(err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("sandbox command failed: %s", result.Stderr)
			}
			var observed map[string]bool
			if err = json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &observed); err != nil {
				t.Fatal(err, result.Stdout)
			}
			if !observed["readWorkspace"] || !observed["writeScratch"] || !observed["githubTLS"] || !observed["codexFlagsTLS"] || !observed["codexContentTLS"] || !observed["developerDocsTLS"] || !observed["learnDocsTLS"] || observed["writeWorkspace"] != (mode == "workspace-write") || observed["readUnrelated"] || observed["writeGit"] || observed["otherTLS"] || observed["privateTLS"] || observed["directLocal"] {
				t.Fatalf("sandbox boundary failed: %v; %s", observed, result.Stderr)
			}
			// The Linux sandbox can materialize inaccessible parent directories in
			// a private mount. Writes there must never change the owner's host file.
			outside, _ := os.ReadFile(filepath.Join(root, "outside.txt"))
			if string(outside) != "preserve owner data" {
				t.Fatal("host file outside sandbox changed")
			}
			if b, err := os.ReadFile(filepath.Join(scratch, "tmp", "result.txt")); err != nil || string(b) != "fixture" {
				t.Fatal("private scratch did not persist", err)
			}
			t.Logf("enforced filesystem/network boundaries: %v; GitHub HTTP requests: 0", observed)
			if err = c.Call(ctx, "command/exec", map[string]any{"cwd": work, "command": []string{"/usr/bin/zsh", "-c", "cat <<'CF_SANDBOX_HEREDOC'\nPRIVATE_SCRATCH_HEREDOC_OK\nCF_SANDBOX_HEREDOC"}, "env": map[string]string{"TMPDIR": filepath.Join(scratch, "tmp"), "TMPPREFIX": filepath.Join(scratch, "tmp", "zsh")}, "timeoutMs": 5000}, &result); err != nil {
				t.Fatal(err)
			}
			if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "PRIVATE_SCRATCH_HEREDOC_OK" {
				t.Fatalf("shell heredoc did not use private scratch: %s", result.Stderr)
			}
		})
	}
}
