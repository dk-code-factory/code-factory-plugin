package reports

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

func TestNativeWorkflowEngineWithFakeProviderAndLifecycle(t *testing.T) {
	// Build the pinned real controller, never a model. Production still calls the
	// installed CLI and does not install or update any dependency.
	binary := filepath.Join(t.TempDir(), "agent-workflow")
	cmd := exec.Command("go", "build", "-o", binary, "github.com/dk-code-factory/agent-workflow/cmd/agent-workflow")
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build pinned workflow fixture: %v\n%s", e, b)
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	provider := filepath.Join(dir, "codex")
	f := findings()
	raw, _ := json.Marshal(f)
	script := `#!/usr/bin/env python3
import json, os, sys, time
from pathlib import Path
if '--version' in sys.argv:
 print('codex-cli 0.161.0'); sys.exit(0)
if 'login' in sys.argv:
 print('Logged in using ChatGPT'); sys.exit(0)
prompt = sys.stdin.read()
if 'Read the complete investigation instructions in investigation-prompt.txt' in prompt:
 prompt = Path('investigation-prompt.txt').read_text()
 assert len(prompt) > 150 * 1024
 assert 'Additional host evidence:' in prompt
receipt = json.loads(os.environ['DK_UPDATE_MANAGED_RECEIPT'])
assert receipt['identity']['name'] == 'dk-workflows'
assert os.environ['DK_UPDATE_PARENT'] == receipt['token']
with open(os.path.join(os.path.dirname(__file__), 'provider-calls'), 'a') as file: file.write(receipt['token']+'\n')
if os.path.exists(os.path.join(os.path.dirname(__file__), 'slow')): time.sleep(20)
text = ` + string(mustJSON(string(raw))) + `
print(json.dumps({'type':'thread.started','thread_id':'fixture-thread'}))
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':text}}))
print(json.dumps({'type':'turn.completed','usage':{'input_tokens':1,'output_tokens':1}}))
`
	if e := os.WriteFile(provider, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(dir, "dk-workflows")
	if e := os.MkdirAll(filepath.Join(root, "scripts"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(root, "codex/skills/dk-issue-description"), 0700); e != nil {
		t.Fatal(e)
	}
	lifecycle := `import json, os, sys
root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
operation = sys.argv[1]
if operation == 'complete': operation += ' ' + sys.argv[sys.argv.index('--token')+1]
with open(os.path.join(root, 'calls'), 'a') as file: file.write(operation+'\n')
if sys.argv[1]=='entry':
 if os.path.exists(os.path.join(root, 'fail-entry')): sys.exit(1)
 token = 'fixture-token-'+str(open(os.path.join(root, 'calls')).read().splitlines().count('entry'))
 print(json.dumps({'token':token,'runtime_root':root,'completion_script':__file__,'action':'continue','identity':{'name':'dk-workflows','version':'fixture'}}))
else:
 assert os.environ['DK_UPDATE_PARENT']==sys.argv[sys.argv.index('--token')+1]
 assert json.loads(os.environ['DK_UPDATE_MANAGED_RECEIPT'])['identity']['name']=='dk-workflows'
 print(json.dumps({'status':'completed','action':'continue'}))
`
	if e := os.WriteFile(filepath.Join(root, "scripts/update.py"), []byte(lifecycle), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "codex/skills/dk-issue-description/SKILL.md"), []byte("Draft only fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	n := &NativeEngine{Dir: dir, Binary: binary, Codex: provider, SkillRoot: root, Fixture: true}
	rt := Runtime{Generation: 1, AdmissionID: strings.Repeat("a", 32)}
	r := Report{ID: rt.AdmissionID, Observation: observation()}
	r.Observation.ToolEvidence = strings.Repeat("x", 150<<10)
	r.Additions = []string{strings.Repeat("first-host-evidence-", 5<<10), strings.Repeat("second-host-evidence-", 5<<10)}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if e := n.Prepare(ctx, r, &rt); e != nil {
		t.Fatal(e)
	}
	defer func() {
		_, _ = command(context.Background(), rt.Workspace, nil, binary, "stop", "--workspace", rt.Workspace)
	}()
	first, e := n.Start(ctx, rt)
	if e != nil {
		b, _ := os.ReadFile(filepath.Join(rt.Workspace, "boundary-errors.log"))
		t.Fatalf("%v: %s", e, b)
	}
	// Losing the admission acknowledgement is recovered by the same request ID.
	second, e := n.Start(ctx, rt)
	if e != nil || second.ID != first.ID {
		t.Fatal("admission was not deduplicated", first, second, e)
	}
	retained, e := os.ReadFile(filepath.Join(rt.Workspace, "investigation-prompt.txt"))
	if e != nil || string(retained) != rt.Prompt || len(retained) < 256<<10 {
		t.Fatal("large observation and accumulated evidence were not retained intact", len(retained), e)
	}
	changed := rt
	changed.Prompt += "changed admission content"
	if _, err := n.Start(ctx, changed); err == nil {
		t.Fatal("changed large prompt reused the immutable admission file")
	}
	var state WorkflowState
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state, e = n.Status(ctx, rt, first.ID)
		if e != nil {
			t.Fatal(e)
		}
		if state.Status != "running" && state.Status != "queued" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state.Status != "succeeded" || state.Outputs.Findings.Title != f.Title {
		t.Fatalf("real controller result: %+v error=%v", state, e)
	}
	// Inspection survives a stopped controller, and a new client still reads the result.
	if _, e = command(ctx, rt.Workspace, nil, binary, "stop", "--workspace", rt.Workspace); e != nil {
		t.Fatal(e)
	}
	state, e = n.Status(ctx, rt, first.ID)
	if e != nil || state.Status != "succeeded" {
		t.Fatal(state, e)
	}
	if e = n.Complete(ctx, rt); e != nil {
		t.Fatal(e)
	}
	calls, _ := os.ReadFile(filepath.Join(root, "calls"))
	if string(calls) != "entry\ncomplete fixture-token-1\n" {
		t.Fatal("lifecycle was not finalized", string(calls))
	}
	calls, _ = os.ReadFile(filepath.Join(dir, "provider-calls"))
	if string(calls) != "fixture-token-1\n" {
		t.Fatal("duplicate provider invocation", string(calls))
	}

	t.Run("controller interruption requires explicit resume", func(t *testing.T) {
		rt := Runtime{Generation: 2, AdmissionID: r.ID + "-2"}
		if e := n.Prepare(ctx, r, &rt); e != nil {
			t.Fatal(e)
		}
		defer func() { _ = n.Complete(context.Background(), rt) }()
		defer command(context.Background(), rt.Workspace, nil, binary, "stop", "--workspace", rt.Workspace)
		slow := filepath.Join(dir, "slow")
		if e := os.WriteFile(slow, []byte("slow fixture"), 0600); e != nil {
			t.Fatal(e)
		}
		state, e := n.Start(ctx, rt)
		if e != nil {
			t.Fatal(e)
		}
		time.Sleep(150 * time.Millisecond)
		if _, e = command(ctx, rt.Workspace, nil, binary, "stop", "--workspace", rt.Workspace); e != nil {
			t.Fatal(e)
		}
		stopped, e := n.Status(ctx, rt, state.ID)
		stopDeadline := time.Now().Add(10 * time.Second)
		for (e != nil || stopped.Status == "running" || stopped.Status == "queued") && time.Now().Before(stopDeadline) {
			time.Sleep(30 * time.Millisecond)
			stopped, e = n.Status(ctx, rt, state.ID)
		}
		if e != nil || (stopped.Status != "needs_attention" && stopped.Status != "canceled") {
			b, _ := os.ReadFile(filepath.Join(rt.Workspace, "boundary-errors.log"))
			log, _ := os.ReadFile(filepath.Join(rt.Workspace, ".agent-workflow/controller.log"))
			t.Fatalf("controller loss not retained: %+v %v %s %s", stopped, e, b, log)
		}
		if e = os.Remove(slow); e != nil {
			t.Fatal(e)
		}
		rt.ResumeID = "explicit-reconciled-recovery"
		failure := filepath.Join(root, "fail-entry")
		if e = os.WriteFile(failure, []byte("lose lifecycle entry response"), 0600); e != nil {
			t.Fatal(e)
		}
		if _, err := n.Resume(ctx, rt, state.ID); err == nil {
			t.Fatal("lost lifecycle entry receipt accepted")
		}
		callsAfterFailure, _ := os.ReadFile(filepath.Join(root, "calls"))
		if _, err := n.Resume(ctx, rt, state.ID); err == nil {
			t.Fatal("uncertain lifecycle entry replayed")
		}
		callsAfterRetry, _ := os.ReadFile(filepath.Join(root, "calls"))
		if string(callsAfterRetry) != string(callsAfterFailure) {
			t.Fatal("retry touched lifecycle before checking the retained entry intent", string(callsAfterRetry))
		}
		if e = os.Remove(failure); e != nil {
			t.Fatal(e)
		}
		// The owner has inspected/finalized the uncertain invocation; the service
		// retains its intent and gives acknowledged recovery a fresh lifecycle ID.
		rt.ResumeID = "acknowledged-new-lifecycle-attempt"
		if e = os.WriteFile(slow, []byte("interrupt resumed controller"), 0600); e != nil {
			t.Fatal(e)
		}
		resumed, e := n.Resume(ctx, rt, state.ID)
		if e != nil || resumed.ID != state.ID {
			b, _ := os.ReadFile(filepath.Join(rt.Workspace, "boundary-errors.log"))
			log, _ := os.ReadFile(filepath.Join(rt.Workspace, ".agent-workflow/controller.log"))
			t.Fatalf("resume failed: %+v %v %s %s", resumed, e, b, log)
		}
		// Lose the controller after a successful resume, without completing its
		// lifecycle. A later recovery must finish that resumed token, not token2.
		time.Sleep(150 * time.Millisecond)
		if _, e = command(ctx, rt.Workspace, nil, binary, "stop", "--workspace", rt.Workspace); e != nil {
			t.Fatal(e)
		}
		activePath := filepath.Join(rt.Workspace, "active-lifecycle.json")
		activeReceipt, err := os.ReadFile(activePath)
		if err != nil {
			t.Fatal(err)
		}
		rt.ResumeID = "second-explicit-recovery"
		callsBeforeSecond, _ := os.ReadFile(filepath.Join(root, "calls"))
		if err := privateWrite(activePath, []byte(`{"token":"broken"}`)); err != nil {
			t.Fatal(err)
		}
		if err := n.Complete(ctx, rt); err == nil {
			t.Fatal("malformed previous active receipt fell back to original token")
		}
		if err := os.Remove(activePath); err != nil {
			t.Fatal(err)
		}
		if err := n.Complete(ctx, rt); err == nil {
			t.Fatal("missing previous active receipt fell back to original token")
		}
		callsAfterInvalid, _ := os.ReadFile(filepath.Join(root, "calls"))
		if string(callsAfterInvalid) != string(callsBeforeSecond) {
			t.Fatal("invalid previous receipt caused lifecycle effects")
		}
		if err := privateWrite(activePath, activeReceipt); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(slow); err != nil {
			t.Fatal(err)
		}
		resumed, e = n.Resume(ctx, rt, state.ID)
		if e != nil || resumed.ID != state.ID {
			t.Fatal("second resume failed", resumed, e)
		}
		callsAfterSecond, _ := os.ReadFile(filepath.Join(root, "calls"))
		if string(callsAfterSecond) != string(callsBeforeSecond)+"complete fixture-token-4\nentry\n" {
			t.Fatal("previous resumed token was not completed before next lifecycle entry", string(callsAfterSecond))
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			resumed, e = n.Status(ctx, rt, state.ID)
			if e != nil {
				t.Fatal(e)
			}
			if resumed.Status != "running" && resumed.Status != "queued" {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		if resumed.Status != "succeeded" {
			t.Fatal(resumed)
		}
		providerCalls, _ := os.ReadFile(filepath.Join(dir, "provider-calls"))
		if !strings.Contains(string(providerCalls), "fixture-token-5\n") {
			t.Fatal("resumed provider did not receive its new lifecycle token", string(providerCalls))
		}
	})
	t.Run("engine deadline stops fake provider", func(t *testing.T) {
		rt := Runtime{Generation: 3, AdmissionID: r.ID + "-3"}
		if e := n.Prepare(ctx, r, &rt); e != nil {
			t.Fatal(e)
		}
		defer n.Complete(context.Background(), rt)
		defer command(context.Background(), rt.Workspace, nil, binary, "stop", "--workspace", rt.Workspace)
		config := filepath.Join(rt.Workspace, "agents.yml")
		b, e := os.ReadFile(config)
		if e != nil {
			t.Fatal(e)
		}
		if e = privateWrite(config, []byte(strings.Replace(string(b), "run-timeout: 30m", "run-timeout: 250ms", 1))); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "slow"), []byte("slow fixture"), 0600); e != nil {
			t.Fatal(e)
		}
		state, e := n.Start(ctx, rt)
		if e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			state, e = n.Status(ctx, rt, state.ID)
			if e != nil {
				t.Fatal(e)
			}
			if state.Status != "running" && state.Status != "queued" {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		if state.Status == "succeeded" || state.Status == "running" || state.Status == "queued" {
			t.Fatal("deadline not enforced", state)
		}
	})
	t.Run("automatic dependency upgrades block without changing owner settings", func(t *testing.T) {
		file := filepath.Join(dir, "config/dk-code-factory/updates.json")
		original := []byte(`{"mode":"auto"}`)
		if err := privateWrite(file, original); err != nil {
			t.Fatal(err)
		}
		if err := requireManualUpdates(); err == nil {
			t.Fatal("automatic upgrade policy accepted")
		}
		actual, _ := os.ReadFile(file)
		if string(actual) != string(original) {
			t.Fatal("owner policy changed")
		}
		if err := privateWrite(file, []byte(`{"mode":"auto","plugins":{"dk-workflows":"notify"}}`)); err != nil {
			t.Fatal(err)
		}
		if err := requireManualUpdates(); err != nil {
			t.Fatal(err)
		}
	})

}
func mustJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}

func TestNativeAuthenticationPrerequisitesUseOnlyFakeBoundaries(t *testing.T) {
	dir := t.TempDir()
	codex := filepath.Join(dir, "codex")
	github := filepath.Join(dir, "gh")
	success := []byte("#!/bin/sh\nexit 0\n")
	failure := []byte("#!/bin/sh\nprintf '%s\\n' '> Cookie: session=unknown-stderr-cookie' '< Set-Cookie: session=unknown-stderr-response; Secure' 'id_token=unknown-stderr-identity' \"{'id_token': 'unknown-stderr-single-id', 'Cookie': 'session=unknown-stderr-single-cookie', 'Set-Cookie': ['unknown-stderr-single-response'], 'password': 'unknown-stderr-password unknown-stderr-suffix'}\" 'retained-boundary-diagnostic' >&2\nexit 1\n")
	for _, file := range []string{codex, github} {
		if err := os.WriteFile(file, success, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := checkAuthentication(ctx, dir, codex, github); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codex, failure, 0700); err != nil {
		t.Fatal(err)
	}
	if err := checkAuthentication(ctx, dir, codex, github); err == nil || !strings.Contains(err.Error(), "codex login") {
		t.Fatal("missing Codex authentication is not actionable", err)
	}
	diagnostics, err := os.ReadFile(filepath.Join(dir, "boundary-errors.log"))
	if err != nil || strings.Contains(string(diagnostics), "unknown-stderr-") || !strings.Contains(string(diagnostics), "retained-boundary-diagnostic") {
		t.Fatal("command diagnostics retained authentication material or lost adjacent evidence", string(diagnostics), err)
	}
	if err := os.WriteFile(codex, success, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(github, failure, 0700); err != nil {
		t.Fatal(err)
	}
	if err := checkAuthentication(ctx, dir, codex, github); err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatal("missing GitHub authentication is not actionable", err)
	}
}
