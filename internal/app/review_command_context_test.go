package app

import (
	"codefactory/internal/domain"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionCommandWithoutSkillsRetainsNativeContext(t *testing.T) {
	for _, mode := range []string{"read-only", "workspace-write"} {
		t.Run(mode, func(t *testing.T) {
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			var repo domain.Repository
			if err := a.Store.Get(ctx, "repository", "owner/repo", &repo); err != nil {
				t.Fatal(err)
			}
			run.Worktree, run.Profile.Sandbox, run.Skills, run.Profile.Skills = repo.Checkout, mode, nil, nil
			root := t.TempDir()
			owner := filepath.Join(root, "owner")
			if err := os.MkdirAll(filepath.Join(owner, ".codex"), 0700); err != nil {
				t.Fatal(err)
			}
			auth := `{"fixture":"dummy-login-only"}`
			for name, data := range map[string]string{"auth.json": auth, "config.toml": "# dummy owner config\n", "AGENTS.md": "Fixture instructions"} {
				if err := os.WriteFile(filepath.Join(owner, ".codex", name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			sibling := filepath.Join(owner, "private-sibling")
			if err := os.WriteFile(sibling, []byte("not granted"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", owner)
			t.Setenv("CODEX_HOME", filepath.Join(owner, ".codex"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(owner, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(owner, ".local", "share"))
			record := filepath.Join(root, "command.json")
			t.Setenv("CF_CONTEXT_REQUEST", record)
			provider := filepath.Join(root, "provider.py")
			code := `#!/usr/bin/python3
import json,os,subprocess,sys,tomllib
args=sys.argv[1:];config={}
if args[:1]==['app-server']:args=args[1:]
def merge(dst,src):
 for key,value in src.items():
  if isinstance(value,dict):merge(dst.setdefault(key,{}),value)
  else:dst[key]=value
while args and args[0]=='-c':
 merge(config,tomllib.loads(args[1]));args=args[2:]
if args[:1]==['plugin']:
 print(json.dumps({'marketplaces':[]} if args[1:3]==['marketplace','list'] else {'installed':[{'name':'fixture-native','enabled':True}],'available':[]}));sys.exit(0)
if args[:1]==['exec']:
 home=os.environ.get('CODEX_HOME',os.path.join(os.environ['HOME'],'.codex'))
 print(json.dumps({'home':home,'auth':open(os.path.join(home,'auth.json')).read(),'args':args,'config':config}));sys.exit(0)
for line in sys.stdin:
 request=json.loads(line)
 if 'id' not in request:continue
 result={}
 if request['method']=='config/read':result={'config':{'mcp_servers':{'ambient':{'enabled':True}}}}
 if request['method']=='command/exec':
  params=request['params'];json.dump({'config':config,'params':params},open(os.environ['CF_CONTEXT_REQUEST'],'w'))
  env=dict(os.environ);env.update(config['shell_environment_policy']['set']);env.update(params['env'])
  child=subprocess.run(params['command'],cwd=params['cwd'],env=env,capture_output=True,text=True)
  result={'exitCode':child.returncode,'stdout':child.stdout,'stderr':child.stderr}
 print(json.dumps({'id':request['id'],'result':result}),flush=True)
`
			if err := os.WriteFile(provider, []byte(code), 0700); err != nil {
				t.Fatal(err)
			}
			a.Runs.Binary = provider
			if err := a.Store.Put(ctx, "review-workflow", run.ID, retainedReview{}, ""); err != nil {
				t.Fatal(err)
			}
			// Synthetic allow-all seccomp plus NNP exercises the existing supported
			// invocation guard, not native filesystem/network enforcement.
			child := `import ctypes,json,os,shutil,subprocess,sys
assert (shutil.which('codex') or '').startswith(sys.argv[1]+os.sep), 'task-private native child launcher missing; no provider fallback'
class Filter(ctypes.Structure):_fields_=[('code',ctypes.c_ushort),('jt',ctypes.c_ubyte),('jf',ctypes.c_ubyte),('k',ctypes.c_uint)]
class Program(ctypes.Structure):_fields_=[('len',ctypes.c_ushort),('filter',ctypes.POINTER(Filter))]
libc=ctypes.CDLL(None,use_errno=True);f=Filter(6,0,0,0x7fff0000);p=Program(1,ctypes.pointer(f))
assert libc.prctl(38,1,0,0,0)==0 and libc.prctl(22,2,ctypes.byref(p),0,0)==0
inventory=json.loads(subprocess.check_output(['codex','plugin','list','--json']))
native=json.loads(subprocess.check_output(['codex','exec','--ephemeral','--sandbox','read-only','--skip-git-repo-check','-C',os.getcwd(),'-'],input=b'fixture prompt'))
print(json.dumps({'inventory':inventory,'native':native,'extra':os.environ.get('UNTRUSTED_COMMAND_ENV'),'stdin':sys.stdin.read()}))`
			worker := reviewModelWorkers{m: a.Runs, run: run}
			output, err := worker.command(ctx, run.Worktree, []string{"python3", "-c", child, filepath.Join(a.DataDir, "scratch", run.ID, "provider-bin")}, []string{"UNTRUSTED_COMMAND_ENV=must-not-forward"}, "retained context stdin")
			if err != nil {
				t.Fatal("empty-skill command lost native child setup", err)
			}
			var result struct {
				Inventory struct{ Installed []struct{ Name string } }
				Native    struct {
					Home, Auth string
					Args       []string
					Config     map[string]json.RawMessage
				}
				Extra *string
				Stdin string
			}
			if err := json.Unmarshal(output, &result); err != nil || len(result.Inventory.Installed) != 1 || result.Inventory.Installed[0].Name != "fixture-native" || result.Native.Auth != auth || result.Extra != nil || result.Stdin != "retained context stdin" {
				t.Fatalf("native context or filtered environment lost: %s %v", output, err)
			}
			scratch := filepath.Join(a.DataDir, "scratch", run.ID)
			wantHome := filepath.Join(owner, ".codex")
			if mode == "read-only" {
				wantHome = filepath.Join(scratch, "state", "native-context")
				if strings.Contains(strings.Join(result.Native.Args, " "), "--ephemeral") || !strings.Contains(strings.Join(result.Native.Args, " "), "danger-full-access") {
					t.Fatal("supported read-only command lost existing outer-boundary context", result.Native)
				}
				if link, err := os.Readlink(filepath.Join(wantHome, "auth.json")); err != nil || link != filepath.Join(owner, ".codex", "auth.json") {
					t.Fatal("native login was copied or retargeted", link, err)
				}
			}
			var features map[string]bool
			var servers map[string]struct{ Enabled bool }
			if err := json.Unmarshal(result.Native.Config["features"], &features); err != nil {
				t.Fatal("native feature overrides unavailable", err)
			}
			if err := json.Unmarshal(result.Native.Config["mcp_servers"], &servers); err != nil {
				t.Fatal("native connector overrides unavailable", err)
			}
			if result.Native.Home != wantHome || features["apps"] || features["workflows"] || len(servers) != 1 || servers["ambient"].Enabled {
				t.Fatal("native overrides or private context missing", string(output))
			}
			var request struct {
				Config struct {
					Default     string `json:"default_permissions"`
					Permissions map[string]struct{ Filesystem map[string]string }
					Shell       struct{ Set map[string]string } `json:"shell_environment_policy"`
				}
				Params struct{ Env map[string]string }
			}
			data, err := os.ReadFile(record)
			if err != nil || json.Unmarshal(data, &request) != nil {
				t.Fatal("command protocol record unavailable", err)
			}
			fs := request.Config.Permissions[request.Config.Default].Filesystem
			if fs[":root"] != "deny" || fs[filepath.Join(owner, ".codex", "auth.json")] != "read" || fs[filepath.Join(owner, ".codex", "config.toml")] != "read" || fs[owner] != "" || fs[sibling] != "" || fs[run.Worktree] != map[string]string{"read-only": "read", "workspace-write": "write"}[mode] || !strings.Contains(request.Config.Shell.Set["PATH"], filepath.Join(scratch, "provider-bin")) || request.Params.Env["PATH"] != "" {
				t.Fatal("native setup widened bounded roots or lost selected permissions", string(data))
			}
			if mode == "read-only" && (fs[wantHome] != "read" || fs[filepath.Join(wantHome, "sessions")] != "write") {
				t.Fatal("native context root/state permissions changed", fs)
			}
			if data, err := os.ReadFile(filepath.Join(owner, ".codex", "auth.json")); err != nil || string(data) != auth {
				t.Fatal("nested command changed original dummy authentication", err)
			}
			if len(run.Skills) != 0 || len(run.Profile.Skills) != 0 {
				t.Fatal("native setup invented a selected profile skill")
			}
		})
	}
}
