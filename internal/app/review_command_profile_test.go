package app

import (
	"codefactory/internal/domain"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionReviewCommandUsesSelectedSandboxProfile(t *testing.T) {
	for _, profile := range []string{"danger-full-access", "read-only", "workspace-write"} {
		t.Run(profile, func(t *testing.T) {
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			var repo domain.Repository
			if err := a.Store.Get(ctx, "repository", "owner/repo", &repo); err != nil {
				t.Fatal(err)
			}
			run.Worktree = repo.Checkout
			run.Profile.Sandbox = profile
			if err := a.Store.Put(ctx, "review-workflow", run.ID, retainedReview{}, ""); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			record := filepath.Join(root, "request.json")
			t.Setenv("CF_PROFILE_PROTOCOL_RECORD", record)
			t.Setenv("CF_PROFILE_SELECTED", profile)
			// Model the documented command/exec policy precedence: a per-command
			// override wins over the provider's conflicting ambient read-only policy.
			// No native provider or OS sandbox is invoked by this protocol fixture.
			provider := filepath.Join(root, "provider.py")
			code := `#!/usr/bin/python3
import json,os,subprocess,sys,tomllib
if sys.argv[1:2]==['plugin']:
 print(json.dumps({'marketplaces':[]} if sys.argv[2:4]==['marketplace','list'] else {'installed':[],'available':[]}));sys.exit(0)
config={}
for i,arg in enumerate(sys.argv):
 if arg=='-c':
  config.update(tomllib.loads(sys.argv[i+1]))
for line in sys.stdin:
 request=json.loads(line)
 if 'id' not in request:
  continue
 result={}
 if request['method']=='command/exec':
  params=request['params']
  with open(os.environ['CF_PROFILE_PROTOCOL_RECORD'],'w') as record:
   json.dump({'params':params,'config':config},record)
  policy=params.get('sandboxPolicy',{'type':'readOnly'})
  if os.environ['CF_PROFILE_SELECTED']=='danger-full-access' and policy['type']!='dangerFullAccess':
   result={'exitCode':73,'stdout':'','stderr':'ambient read-only policy denied retained reviewer access'}
  else:
   env=dict(os.environ)
   env.update(config['shell_environment_policy']['set'])
   env.update(params['env'])
   child=subprocess.run(params['command'],cwd=params['cwd'],env=env,capture_output=True,text=True)
   result={'exitCode':child.returncode,'stdout':child.stdout,'stderr':child.stderr}
 print(json.dumps({'id':request['id'],'result':result}),flush=True)
`
			if err := os.WriteFile(provider, []byte(code), 0700); err != nil {
				t.Fatal(err)
			}
			a.Runs.Binary = provider
			worker := reviewModelWorkers{m: a.Runs, run: run}
			output, err := worker.command(ctx, run.Worktree, []string{"python3", "-c", `import json,os,sys; print(json.dumps({'stdin':sys.stdin.read(),'data':os.environ['XDG_DATA_HOME'],'tmp':os.environ['TMPDIR'],'attempt':os.environ['DK_PROVIDER_ATTEMPT'],'extra':os.environ.get('UNTRUSTED_COMMAND_ENV')}))`}, []string{"DK_PROVIDER_ATTEMPT=2", "UNTRUSTED_COMMAND_ENV=must-not-forward"}, "retained stdin")
			if err != nil {
				t.Fatal(err)
			}
			var child struct {
				Stdin, Data, Tmp, Attempt string
				Extra                     *string
			}
			if err := json.Unmarshal(output, &child); err != nil {
				t.Fatal(err)
			}
			scratch := filepath.Join(a.DataDir, "scratch", run.ID)
			if child.Stdin != "retained stdin" || child.Data != filepath.Join(scratch, "data") || child.Tmp != filepath.Join(scratch, "tmp") || child.Attempt != "2" || child.Extra != nil {
				t.Fatalf("command lost retained stdin or private environment: %+v", child)
			}
			var request struct {
				Params map[string]json.RawMessage
				Config struct {
					DefaultPermissions string                                            `json:"default_permissions"`
					Permissions        map[string]struct{ Filesystem map[string]string } `json:"permissions"`
				}
			}
			data, err := os.ReadFile(record)
			if err != nil || json.Unmarshal(data, &request) != nil {
				t.Fatalf("read protocol request: %s %v", data, err)
			}
			if profile == "danger-full-access" {
				var policy map[string]string
				if err := json.Unmarshal(request.Params["sandboxPolicy"], &policy); err != nil || policy["type"] != "dangerFullAccess" {
					t.Fatalf("selected full access omitted native command override: %s %v", request.Params["sandboxPolicy"], err)
				}
			} else {
				if _, ok := request.Params["sandboxPolicy"]; ok {
					t.Fatal("command override bypassed bounded native permissions")
				}
				name := request.Config.DefaultPermissions
				mode := map[string]string{"read-only": "read", "workspace-write": "write"}[profile]
				if !strings.HasPrefix(name, "code-factory-") || request.Config.Permissions[name].Filesystem[run.Worktree] != mode {
					t.Fatal("bounded selected profile lost its workspace permissions", string(data))
				}
			}
			var env map[string]string
			if err := json.Unmarshal(request.Params["env"], &env); err != nil {
				t.Fatal(err)
			}
			if _, ok := env["PATH"]; ok {
				t.Fatal("command overrides provider-prepared PATH")
			}
		})
	}
}
