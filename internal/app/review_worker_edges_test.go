package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/prreview"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func isolatedReviewProvider(t *testing.T, code string) string {
	t.Helper()
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	if err := os.MkdirAll(filepath.Join(owner, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", owner)
	t.Setenv("CODEX_HOME", filepath.Join(owner, ".codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(owner, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(owner, ".local", "share"))
	provider := filepath.Join(root, "provider.py")
	if err := os.WriteFile(provider, []byte(code), 0700); err != nil {
		t.Fatal(err)
	}
	return provider
}

const failedReviewTurnProvider = `#!/usr/bin/python3
import json,os,sys
def emit(value):print(json.dumps(value),flush=True)
for line in sys.stdin:
 request=json.loads(line)
 if 'id' not in request:continue
 result={};method=request['method']
 if method=='config/read':result={'config':{}}
 if method=='thread/start':result={'thread':{'id':'fixture-thread'},'model':'fixture-model'}
 if method=='turn/start':
  params=request['params'];inputs=params['input']
  reviewer=next((x['name'] for x in inputs if x['type']=='skill'),'non-review')
  with open(os.environ['CF_EDGE_CALLS'],'a') as f:f.write(reviewer+'\n')
  emit({'id':request['id'],'result':{'turn':{'id':'fixture-turn'}}})
  mode=os.environ['CF_EDGE_TURN']
  if reviewer=='one' or reviewer=='non-review' or mode=='all-exhausted':
   if mode=='incomplete':
    emit({'method':'item/completed','params':{'threadId':'fixture-thread','turnId':'fixture-turn','item':{'type':'agentMessage','text':json.dumps({'status':'incomplete','findings':[],'message':'Fixture could not complete review'})}}})
    emit({'method':'turn/completed','params':{'turn':{'id':'fixture-turn','status':'completed'}}})
   else:
    info='internalServerError' if mode=='failed' else 'usageLimitExceeded'
    emit({'method':'turn/completed','params':{'turn':{'id':'fixture-turn','status':'failed','error':{'message':'Fixture terminal failure','codexErrorInfo':info}}}})
  else:
   emit({'method':'item/completed','params':{'threadId':'fixture-thread','turnId':'fixture-turn','item':{'type':'agentMessage','text':'{"findings":[]}'}}})
   emit({'method':'turn/completed','params':{'turn':{'id':'fixture-turn','status':'completed'}}})
  continue
 emit({'id':request['id'],'result':result})
`

type nativeReviewerWorkers struct {
	prreview.Workers
	native reviewModelWorkers
}

func (w nativeReviewerWorkers) Review(ctx context.Context, b prreview.Binding, h prreview.Handoff, correction string) ([]byte, error) {
	return w.native.Review(ctx, b, h, correction)
}

func TestProductionReviewerPreservesNativeUsageExhaustion(t *testing.T) {
	for _, mode := range []string{"exhausted", "failed", "incomplete", "all-exhausted"} {
		t.Run(mode, func(t *testing.T) {
			a, item, models, _ := retainedFixture(t)
			ctx := context.Background()
			a.Runs.Binary = isolatedReviewProvider(t, failedReviewTurnProvider)
			record := filepath.Join(t.TempDir(), "reviewers")
			t.Setenv("CF_EDGE_CALLS", record)
			t.Setenv("CF_EDGE_TURN", mode)
			run, err := a.Runs.PreparePRReview(ctx, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			a.ReviewWorkers = nativeReviewerWorkers{Workers: models, native: reviewModelWorkers{m: a.Runs, run: run}}
			if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			waitGuardReviewDone(t, a, run.ID)
			if err = a.Store.Get(ctx, "run", run.ID, &run); err != nil || run.ReviewResult == nil {
				t.Fatal("native failed-turn result missing", run, err)
			}
			calls, err := os.ReadFile(record)
			if err != nil || string(calls) != "one\ntwo\n" {
				t.Fatal("native failure changed ordered reviewer dispatch or retried", string(calls), err)
			}
			result := run.ReviewResult
			if mode == "exhausted" {
				if !run.ReviewComplete || run.Status != "idle" || len(result.Skipped) != 1 || result.Skipped[0].Reviewer != "one" || len(result.Failed) != 0 || len(result.Executed) != 1 || result.Executed[0] != "two" || models.completionCalls != 1 {
					t.Fatal("native usage exhaustion was not an assessed skip beside success", run, models.completionCalls)
				}
			} else if mode == "all-exhausted" {
				if run.ReviewComplete || len(result.Skipped) != 2 || len(result.Failed) != 0 || models.completionCalls != 0 {
					t.Fatal("all-exhausted review completed or became a provider failure", run)
				}
			} else if run.ReviewComplete || len(result.Failed) != 1 || len(result.Skipped) != 0 || models.completionCalls != 0 {
				t.Fatal("generic failure/incomplete output was converted to exhaustion", run)
			} else if result.Failed[0].Status != map[string]string{"failed": "failed", "incomplete": "incomplete"}[mode] {
				t.Fatal("generic failed and incomplete reviewer outcomes were conflated", result.Failed)
			}
			var saved retainedReview
			if err = a.Store.Get(ctx, "review-workflow", run.ID, &saved); err != nil || saved.PendingWorker != nil || saved.CleanupError != "" {
				t.Fatal("joined native terminal result left cleanup uncertainty", saved, err)
			}
		})
	}
}

func TestProductionUsageExhaustionOutsideReviewRemainsFailure(t *testing.T) {
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	a.Runs.Binary = isolatedReviewProvider(t, failedReviewTurnProvider)
	t.Setenv("CF_EDGE_CALLS", filepath.Join(t.TempDir(), "stages"))
	t.Setenv("CF_EDGE_TURN", "exhausted")
	if err := a.Store.Put(ctx, "review-workflow", run.ID, retainedReview{}, ""); err != nil {
		t.Fatal(err)
	}
	native := reviewModelWorkers{m: a.Runs, run: run}
	in := prreview.PreparationInput{Evidence: prreview.Evidence{Checkout: run.Worktree}}
	if output, err := native.Prepare(ctx, in, ""); err == nil || len(output) != 0 {
		t.Fatal("preparation exhaustion became a reviewer response", string(output), err)
	}
	if _, err := native.MaterialChoice(ctx, in, prreview.Analysis{}); err == nil {
		t.Fatal("material-check exhaustion became success")
	}
	if _, err := native.Completion(ctx, prreview.Preparation{Workspace: run.Worktree}, prreview.Result{}); err == nil {
		t.Fatal("completion exhaustion became success")
	}
}

func TestProductionCommandBlockedSendHasBoundedCleanup(t *testing.T) {
	a, item, models, _ := retainedFixture(t)
	ctx := context.Background()
	root := t.TempDir()
	stalled, release, pidPath := filepath.Join(root, "stalled"), filepath.Join(root, "release"), filepath.Join(root, "pid")
	t.Setenv("CF_STALLED_SEND", stalled)
	t.Setenv("CF_STALLED_RELEASE", release)
	t.Setenv("CF_STALLED_PID", pidPath)
	a.Runs.Binary = isolatedReviewProvider(t, `#!/usr/bin/python3
import array,fcntl,json,os,sys,termios,time
for line in sys.stdin.buffer:
 request=json.loads(line)
 if request.get('method')=='initialize':print(json.dumps({'id':request['id'],'result':{}}),flush=True)
 if request.get('method')=='initialized':
  # Stop consuming input after initialization, then prove request bytes occupy
  # the pipe. The valid large command request cannot finish transmission.
  while True:
   pending=array.array('i',[0]);fcntl.ioctl(0,termios.FIONREAD,pending,True)
   if pending[0]>0:break
   time.sleep(.005)
  open(os.environ['CF_STALLED_PID'],'w').write(str(os.getpid()))
  open(os.environ['CF_STALLED_SEND'],'w').write(str(pending[0]))
  while not os.path.exists(os.environ['CF_STALLED_RELEASE']):time.sleep(.01)
  sys.exit(0)
`)
	command := filepath.Join(t.TempDir(), "reviewer")
	if err := os.WriteFile(command, []byte("#!/usr/bin/python3\nprint('{\"findings\":[]}')\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config, _ := a.Config(ctx)
	config.Reviewers = []prreview.Reviewer{{ID: "one", Kind: "command", Argv: []string{command, "{handoff}", strings.Repeat("x", 64<<10)}}, config.Reviewers[1]}
	if err := a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	run, err := a.Runs.PreparePRReview(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Profile.Sandbox = "danger-full-access"
	if err = a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	native := reviewModelWorkers{m: a.Runs, run: run}
	a.ReviewWorkers = prreview.CommandWorkers{Models: models, Execute: native.command, Root: filepath.Join(a.DataDir, "scratch", run.ID, "reviewers")}
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0600) })
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		if _, err := os.Stat(stalled); err == nil {
			return true
		}
		var saved domain.Run
		if err := a.Store.Get(ctx, "run", run.ID, &saved); err == nil && (saved.Status == "failed" || saved.Status == "idle") {
			t.Fatal("fixture failed before blocked transmission", saved.Error, saved.ReviewStage)
		}
		return false
	})
	interrupt := make(chan error, 1)
	started := time.Now()
	go func() { interrupt <- a.Runs.Interrupt(ctx, run.ID) }()
	select {
	case err = <-interrupt:
	case <-time.After(14 * time.Second):
		_ = os.WriteFile(release, nil, 0600)
		waitRetainedReviewValue(t, interrupt, "released blocked command fixture")
		t.Fatal("command cleanup exceeded its bounded budget while request transmission held the write lock")
	}
	if !errors.Is(err, prreview.ErrWorkerCleanup) || time.Since(started) < 9*time.Second {
		t.Fatal("blocked transmission did not retain timed cleanup uncertainty", time.Since(started), err)
	}
	var pid int
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Sscanf(string(data), "%d", &pid); err != nil || syscall.Kill(pid, 0) != syscall.ESRCH {
		t.Fatal("blocked provider was not reaped before Interrupt returned", pid, err)
	}
	assertReviewBarrierRejects(t, a, run.ID)
	if models.reviewCalls != 0 || models.completionCalls != 0 {
		t.Fatal("uncertain command admitted later reviewer/assessment", models)
	}
	var current domain.Run
	if err = a.Store.Get(ctx, "run", run.ID, &current); err != nil || !current.InterruptRequested || current.ResumePending {
		t.Fatal("bounded cleanup lost durable stop intent", current, err)
	}
}
