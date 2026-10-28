package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/prreview"
	"codefactory/internal/store"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestProductionReviewWorkersUseSelectedModelRetainedSkillAndSandboxedCommand(t *testing.T) {
	a, item, fake, _ := retainedFixture(t)
	a.ReviewWorkers = nil
	root := t.TempDir()
	record := filepath.Join(root, "protocol.jsonl")
	argsRecord := filepath.Join(root, "args.jsonl")
	t.Setenv("CF_FAKE_REVIEW_RESPONSES", root)
	t.Setenv("CF_FAKE_RECORD", record)
	t.Setenv("CF_FAKE_ARGS_RECORD", argsRecord)
	assessment, _ := fake.Completion(context.Background(), prreview.Preparation{}, prreview.Result{})
	for name, value := range map[string]any{"preparation": fake.analysis, "material-choice": prreview.MaterialCheck{Rationale: "Independent scope comparison", Evidence: []string{"pr/body"}}, "skill-review": prreview.Response{Findings: []prreview.Finding{}}, "completion": assessment} {
		if err := os.WriteFile(filepath.Join(root, name+".json"), raw(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := filepath.Join(t.TempDir(), "reviewer.py")
	if err := os.WriteFile(filepath.Join(filepath.Dir(command), "package.json"), []byte(`{"name":"review-fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/python3
import json,os,sys
handoff=json.load(open(sys.argv[1]))
assert handoff['contract']=='git-diff-v1'
assert handoff['head_sha'] and handoff['base_sha'] and handoff['decisions']
assert os.path.isfile(handoff['patch'])
assert handoff['reviewer_addendum']
instructions=sys.stdin.read()
assert 'git-diff-v1' in instructions
assert os.environ['TMPDIR'] and os.environ['XDG_DATA_HOME']
if os.environ['DK_PROVIDER_ATTEMPT']=='0':
 print('{"bad":true}')
else:
 assert 'Validator error' in instructions
 print('{"findings":[]}')
`
	if err := os.WriteFile(command, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	config, _ := a.Config(a.Runs.ctx)
	config.Reviewers[1] = prreview.Reviewer{ID: "two", Kind: "command", Argv: []string{command, "{handoff}"}}
	if err := a.SaveConfig(a.Runs.ctx, config); err != nil {
		t.Fatal(err)
	}
	run, err := a.Runs.PreparePRReview(a.Runs.ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Profile.Model = "fixture-selected-model"
	run.Profile.Effort = "high"
	if err = a.Store.Put(a.Runs.ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(a.Runs.ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitGuardReviewDone(t, a, run.ID)
	if err = a.Store.Get(a.Runs.ctx, "run", run.ID, &run); err != nil || run.Status != "idle" || !run.ReviewComplete {
		t.Fatalf("production integration: %+v %v", run, err)
	}
	var retained retainedReview
	if err = a.Store.Get(a.Runs.ctx, "review-workflow", run.ID, &retained); err != nil {
		t.Fatal(err)
	}
	if retained.Session.EngineRunID == "" || retained.Session.WorkerImplementation == "" || len(retained.Session.Reviews) != 2 {
		t.Fatal("engine/worker identities or checkpoints missing", retained.Session)
	}
	bytes, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	skillSeen := false
	for _, line := range strings.Split(strings.TrimSpace(string(bytes)), "\n") {
		var call struct {
			Method string
			Params map[string]json.RawMessage
		}
		if err = json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		if call.Method == "thread/start" {
			var model string
			_ = json.Unmarshal(call.Params["model"], &model)
			if model != run.Profile.Model {
				t.Fatal("selected model lost", model)
			}
			if _, ok := call.Params["sandbox"]; ok {
				t.Fatal("legacy sandbox overrides native bounded profile")
			}
		}
		if call.Method == "command/exec" {
			var env map[string]string
			_ = json.Unmarshal(call.Params["env"], &env)
			if _, ok := env["PATH"]; ok {
				t.Fatal("command overrides sandbox prepared child-provider PATH")
			}

			order = append(order, "command")
			continue
		}
		if call.Method != "turn/start" {
			continue
		}
		var effort string
		_ = json.Unmarshal(call.Params["effort"], &effort)
		if effort != "high" {
			t.Fatal("effort lost", effort)
		}
		var input []map[string]string
		_ = json.Unmarshal(call.Params["input"], &input)
		if strings.Contains(input[0]["text"], base64.StdEncoding.EncodeToString([]byte(script))) {
			t.Fatal("model context includes captured executable bytes")
		}
		stage, _, _ := strings.Cut(strings.TrimPrefix(input[0]["text"], "Code Factory retained review stage: "), "\n")
		order = append(order, stage)
		for _, entry := range input {
			if entry["type"] == "skill" {
				skillSeen = true
				if !strings.Contains(entry["path"], filepath.Join("snapshots", run.ID)) {
					t.Fatal("skill escaped retained snapshot", entry)
				}
			}
		}
	}
	if !skillSeen || !reflect.DeepEqual(order, []string{"preparation", "material-choice", "skill-review", "command", "command", "completion"}) {
		t.Fatal("ordered production dispatch or bounded correction changed", order, skillSeen)
	}
	args, err := os.ReadFile(argsRecord)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "default_permissions") || !strings.Contains(string(args), "shell_environment_policy.set") {
		t.Fatal("production workers omitted native sandbox/environment controls")
	}
}

func TestAcceptedReviewAnswerRecoversAppIntentBeforeEngineJournal(t *testing.T) {
	a, item, workers, _ := retainedFixture(t)
	workers.analysis.Questions = []prreview.Question{{ID: "scope", Status: "open", Material: true, Statements: "Scope conflict", AffectedWork: "Review boundary", Recommendation: "Narrow", ProposedChanges: "Choose scope", Options: []prreview.Option{{Choice: "Narrow", Benefits: "Bounded", Costs: "Deferred"}, {Choice: "Wide", Benefits: "Coverage", Costs: "Complexity"}}, Evidence: []string{"pr/body"}}}
	run, err := a.Runs.PreparePRReview(a.Runs.ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(a.Runs.ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitGuardReviewDone(t, a, run.ID)
	if a.Runs.busy() {
		t.Fatal("waiting retained review occupied execution capacity")
	}
	a.Runs.Close()
	// Emulate process loss after the app's answer/continuation transaction and
	// before acceptance in the SDK journal or continuation admission.
	err = a.Store.Write(context.Background(), func(tx *store.Tx) error {
		var retained retainedReview
		if err := tx.Get("review-workflow", run.ID, &retained); err != nil {
			return err
		}
		gate := retained.Session.Gate
		if gate == nil || gate.EngineGateID == "" || retained.Session.EngineRunID == "" {
			t.Fatal("wait has no durable SDK identity")
		}
		if err := prreview.AcceptAnswer(&retained.Session, gate.ID, gate.EvidenceID, json.RawMessage(`{"scope":"Narrow"}`)); err != nil {
			return err
		}
		var attention domain.Attention
		if err := tx.Get("attention", gate.ID, &attention); err != nil {
			return err
		}
		attention.Status = "answered"
		if err := tx.Put("attention", attention.ID, attention); err != nil {
			return err
		}
		if err := tx.Get("run", run.ID, &run); err != nil {
			return err
		}
		run.Status = "interrupted"
		run.ResumePending = true
		if err := tx.Put("run", run.ID, run); err != nil {
			return err
		}
		return tx.Put("review-workflow", run.ID, retained)
	})
	if err != nil {
		t.Fatal(err)
	}
	a.Runs = NewManager(context.Background(), a, "")
	if err = a.Runs.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitGuardReviewDone(t, a, run.ID)
	if err = a.Store.Get(context.Background(), "run", run.ID, &run); err != nil || run.Status != "idle" || !run.ReviewComplete {
		t.Fatal("accepted answer intent did not recover", run, err)
	}
	if workers.prepareCalls != 2 || workers.reviewCalls != 2 || workers.completionCalls != 1 {
		t.Fatal("answer replayed child work", workers)
	}
	// A second recovery cannot enqueue the consumed answer again.
	if err = a.Runs.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if workers.prepareCalls != 2 || workers.reviewCalls != 2 {
		t.Fatal("duplicate continuation")
	}
}

func TestReviewAnswerContinuationCannotUndoInterrupt(t *testing.T) {
	a, item, workers, _ := retainedFixture(t)
	workers.analysis.Questions = []prreview.Question{{ID: "scope", Status: "open", Material: true, Statements: "Choose scope", AffectedWork: "Review boundary", Recommendation: "Narrow", ProposedChanges: "Choose boundary", Options: []prreview.Option{{Choice: "Narrow", Benefits: "Bounded", Costs: "Deferred"}, {Choice: "Wide", Benefits: "Coverage", Costs: "Complexity"}}, Evidence: []string{"pr/body"}}}
	run, err := a.Runs.PreparePRReview(a.Runs.ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(a.Runs.ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitGuardReviewDone(t, a, run.ID)
	var retained retainedReview
	if err = a.Store.Get(a.Runs.ctx, "review-workflow", run.ID, &retained); err != nil {
		t.Fatal(err)
	}
	gate := retained.Session.Gate
	if err = prreview.AcceptAnswer(&retained.Session, gate.ID, gate.EvidenceID, json.RawMessage(`{"scope":"Narrow"}`)); err != nil {
		t.Fatal(err)
	}
	answerID := retained.Session.Gate.AnswerID
	if err = a.Store.Write(a.Runs.ctx, func(tx *store.Tx) error {
		if err := tx.Get("run", run.ID, &run); err != nil {
			return err
		}
		run.Status = "interrupted"
		run.ResumePending = true
		if err := tx.Put("review-workflow", run.ID, retained); err != nil {
			return err
		}
		return tx.Put("run", run.ID, run)
	}); err != nil {
		t.Fatal(err)
	}
	// This is the exact race window after accepted answer commit, before its
	// automatic start. A later explicit interrupt must win at admission.
	if err = a.Runs.Interrupt(a.Runs.ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.startReviewWorkflowIntent(a.Runs.ctx, run, true, answerID); err == nil {
		t.Fatal("answer continuation undid interrupt")
	}
	if err = a.Store.Get(a.Runs.ctx, "run", run.ID, &run); err != nil || !run.InterruptRequested || run.ResumePending || run.Status != "interrupted" {
		t.Fatal(run, err)
	}
	if workers.prepareCalls != 1 || workers.reviewCalls != 0 {
		t.Fatal("interrupted answer started child work")
	}
	if _, err = a.Runs.Start(a.Runs.ctx, run.ID, true); err != nil {
		t.Fatal(err)
	}
	waitGuardReviewDone(t, a, run.ID)
	if err = a.Store.Get(a.Runs.ctx, "run", run.ID, &run); err != nil || !run.ReviewComplete {
		t.Fatal("explicit Resume lost accepted answer", run, err)
	}
}

func TestProductionReviewInterruptJoinsProviderProcess(t *testing.T) {
	a, item, _, _ := retainedFixture(t)
	a.ReviewWorkers = nil
	root := t.TempDir()
	pids := filepath.Join(root, "pids")
	entered := filepath.Join(root, "entered")
	t.Setenv("CF_FAKE_PID_RECORD", pids)
	t.Setenv("CF_FAKE_BLOCK_RPC", "turn/start")
	t.Setenv("CF_FAKE_RPC_ENTERED", entered)
	t.Setenv("CF_FAKE_RPC_RELEASE", filepath.Join(root, "unreleased"))
	run, err := a.Runs.PreparePRReview(a.Runs.ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(a.Runs.ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(entered); return err == nil })
	if err = a.Runs.Interrupt(a.Runs.ctx, run.ID); !errors.Is(err, prreview.ErrWorkerCleanup) {
		t.Fatal("unsettled model admission was acknowledged as joined", err)
	}
	data, err := os.ReadFile(pids)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(line)
		if err != nil {
			t.Fatal(err)
		}
		if err = syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Fatal("interrupt returned before provider process was reaped", pid, err)
		}
	}
	if err = a.Store.Get(a.Runs.ctx, "run", run.ID, &run); err != nil || run.Status != "interrupted" || !run.InterruptRequested {
		t.Fatal(run, err)
	}
}

func TestProductionCommandInterruptTerminatesSeparateReviewerSession(t *testing.T) {
	a, item, models, _ := retainedFixture(t)
	ctx := a.Runs.ctx
	root := t.TempDir()
	pidPath := filepath.Join(root, "reviewer.pid")
	protocol := filepath.Join(root, "rpc.jsonl")
	t.Setenv("CF_FAKE_REVIEW_RESPONSES", root)
	t.Setenv("CF_FAKE_RECORD", protocol)
	script := filepath.Join(root, "review.py")
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"command-stop-fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	code := `#!/usr/bin/python3
import os,signal,sys,time
signal.signal(signal.SIGTERM,signal.SIG_IGN)
with open(sys.argv[2],'w') as f:
 f.write(str(os.getpid()))
while True:
 time.sleep(1)
`
	if err := os.WriteFile(script, []byte(code), 0700); err != nil {
		t.Fatal(err)
	}
	config, _ := a.Config(ctx)
	config.Reviewers[1] = prreview.Reviewer{ID: "two", Kind: "command", Argv: []string{script, "{handoff}", pidPath}}
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
	a.ReviewWorkers = prreview.CommandWorkers{Models: models, Root: filepath.Join(a.DataDir, "scratch", run.ID, "reviewers"), Execute: native.command}
	childPID := 0
	t.Cleanup(func() {
		if childPID != 0 {
			_ = syscall.Kill(-childPID, syscall.SIGKILL)
		}
	})
	if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		data, err := os.ReadFile(pidPath)
		if err != nil {
			return false
		}
		childPID, _ = strconv.Atoi(string(data))
		return childPID > 0
	})
	if err = a.Runs.Interrupt(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(childPID, 0); err != syscall.ESRCH {
		t.Fatal("separate TERM-ignoring command survived Interrupt", childPID, err)
	}
	calls, err := os.ReadFile(protocol)
	if err != nil || !strings.Contains(string(calls), "command/exec/terminate") {
		t.Fatal("command was stopped without native session termination", err)
	}
	if err = a.Store.Get(ctx, "run", run.ID, &run); err != nil || run.Status != "interrupted" || !run.InterruptRequested {
		t.Fatal(run, err)
	}
}

type cleanupFailureWorkers struct {
	prreview.Workers
	started chan struct{}
	calls   atomic.Int32
}

func (w *cleanupFailureWorkers) Review(ctx context.Context, b prreview.Binding, h prreview.Handoff, correction string) ([]byte, error) {
	if b.Reviewer.ID != "two" {
		return w.Workers.Review(ctx, b, h, correction)
	}
	if w.calls.Add(1) == 1 {
		close(w.started)
	}
	<-ctx.Done()
	return nil, fmt.Errorf("%w: no confirmed command termination", prreview.ErrWorkerCleanup)
}
func TestReviewInterruptReportsUnverifiedChildCleanup(t *testing.T) {
	a, item, workers, _ := retainedFixture(t)
	started := make(chan struct{})
	uncertain := &cleanupFailureWorkers{Workers: workers, started: started}
	a.ReviewWorkers = uncertain
	run, err := a.Runs.PreparePRReview(a.Runs.ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Runs.Start(a.Runs.ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitRetainedReviewValue(t, started, "child before cleanup failure")
	if err = a.Runs.Interrupt(a.Runs.ctx, run.ID); !errors.Is(err, prreview.ErrWorkerCleanup) {
		t.Fatal("interrupt acknowledged unverified cleanup", err)
	}
	if err = a.Store.Get(a.Runs.ctx, "run", run.ID, &run); err != nil || !strings.Contains(run.Error, "no confirmed command termination") || !run.InterruptRequested || run.ResumePending {
		t.Fatal("cleanup failure or stop intent lost", run, err)
	}
	var retained retainedReview
	if err = a.Store.Get(context.Background(), "review-workflow", run.ID, &retained); err != nil || retained.CleanupError != run.Error {
		t.Fatal("cleanup uncertainty was not retained as admission state", retained, err)
	}
	for _, phase := range []string{"same manager", "after restart"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			if phase == "after restart" {
				a.Runs.Close()
				a.Runs = NewManager(ctx, a, "")
				if err := a.Runs.Recover(ctx); err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := a.Runs.Start(ctx, run.ID, true); !errors.Is(err, prreview.ErrWorkerCleanup) {
					t.Fatal("Resume admitted unverified child replacement", err)
				}
				for _, text := range []string{"continue", "resume"} {
					if _, err := a.Runs.QueueMessage(ctx, run.ID, text, ""); !errors.Is(err, prreview.ErrWorkerCleanup) {
						t.Fatal("message admitted unverified child replacement", text, err)
					}
				}
				if _, err := a.Runs.startReviewWorkflowIntent(ctx, run, true, "stale-answer"); !errors.Is(err, prreview.ErrWorkerCleanup) {
					t.Fatal("automatic continuation bypassed cleanup barrier", err)
				}
			}
			var current domain.Run
			if err := a.Store.Get(ctx, "run", run.ID, &current); err != nil || !reflect.DeepEqual(current, run) {
				t.Fatal("rejected admission changed execution, error or stop intent", current, err)
			}
			var saved retainedReview
			if err := a.Store.Get(ctx, "review-workflow", run.ID, &saved); err != nil || !reflect.DeepEqual(saved, retained) {
				t.Fatal("rejected admission overwrote cleanup barrier or checkpoints", saved, err)
			}
			if workers.prepareCalls != 1 || workers.reviewCalls != 1 || workers.completionCalls != 0 || uncertain.calls.Load() != 1 {
				t.Fatal("cleanup uncertainty dispatched additional child work", workers, uncertain.calls.Load())
			}
			if delivery, err := a.MessageDelivery(ctx, run.ID); err != nil || delivery != nil {
				t.Fatal("rejected continuation was acknowledged as delivered", delivery, err)
			}
		})
	}
}

func TestReviewVerifiedChildCleanupAllowsResume(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart=%t", restart), func(t *testing.T) {
			a, item, workers, _ := retainedFixture(t)
			ctx := context.Background()
			workers.second = make(chan struct{})
			workers.release = make(chan struct{})
			run, err := a.Runs.PreparePRReview(ctx, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			waitRetainedReviewValue(t, workers.second, "reviewer before verified stop")
			if err = a.Runs.Interrupt(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			if restart {
				a.Runs.Close()
				a.Runs = NewManager(ctx, a, "")
				if err = a.Runs.Recover(ctx); err != nil {
					t.Fatal(err)
				}
			}
			close(workers.release)
			if _, err = a.Runs.Start(ctx, run.ID, true); err != nil {
				t.Fatal("verified cleanup blocked Resume", err)
			}
			waitGuardReviewDone(t, a, run.ID)
			if err = a.Store.Get(ctx, "run", run.ID, &run); err != nil || !run.ReviewComplete || run.Status != "idle" || run.InterruptRequested || run.Error != "" {
				t.Fatal("verified stop failed to resume normally", run, err)
			}
			var retained retainedReview
			if err = a.Store.Get(ctx, "review-workflow", run.ID, &retained); err != nil || retained.CleanupError != "" {
				t.Fatal("verified cleanup retained an uncertainty barrier", retained, err)
			}
			if workers.prepareCalls != 1 || workers.reviewCalls != 3 || workers.completionCalls != 1 || len(run.ReviewResult.Reused) != 1 || run.ReviewResult.Reused[0] != "one" {
				t.Fatal("verified Resume failed to reuse completed checkpoints", workers, run.ReviewResult)
			}
		})
	}
}

func TestProductionModelInterruptTerminatesSeparateToolSession(t *testing.T) {
	a, item, _, _ := retainedFixture(t)
	a.ReviewWorkers = nil
	root := t.TempDir()
	pidPath := filepath.Join(root, "tool.pid")
	protocol := filepath.Join(root, "rpc.jsonl")
	t.Setenv("CF_FAKE_MODE", "retained-tool")
	t.Setenv("CF_FAKE_TOOL_PID", pidPath)
	t.Setenv("CF_FAKE_RECORD", protocol)
	run, err := a.Runs.PreparePRReview(a.Runs.ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Profile.Sandbox = "danger-full-access"
	if err = a.Store.Put(a.Runs.ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	childPID := 0
	t.Cleanup(func() {
		if childPID > 0 {
			_ = syscall.Kill(-childPID, syscall.SIGKILL)
		}
	})
	if _, err = a.Runs.Start(a.Runs.ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		data, err := os.ReadFile(pidPath)
		if err != nil {
			return false
		}
		childPID, _ = strconv.Atoi(string(data))
		return childPID > 0
	})
	if err = a.Runs.Interrupt(a.Runs.ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(childPID, 0); err != syscall.ESRCH {
		t.Fatal("model tool session survived native interrupt", childPID, err)
	}
	data, err := os.ReadFile(protocol)
	if err != nil || !strings.Contains(string(data), "turn/interrupt") {
		t.Fatal("model stopped without a native interrupt", err)
	}
	if err = a.Store.Get(a.Runs.ctx, "run", run.ID, &run); err != nil || run.Status != "interrupted" || !run.InterruptRequested {
		t.Fatal(run, err)
	}
}

func TestCommandPackageRootIgnoresInvalidGitAncestor(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(root, "tools")
	if err := os.Mkdir(leaf, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(leaf, "reviewer")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '{\"findings\":[]}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "private.txt"), []byte("unrelated owner data"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := commandPackageRoot(script)
	if err != nil || got != script {
		t.Fatal("invalid ancestor Git marker established a package", got, err)
	}
	bindings, err := (&Manager{}).reviewBindings(context.Background(), []prreview.Reviewer{{ID: "standalone", Kind: "command", Argv: []string{script, "{handoff}"}}})
	if err != nil || len(bindings) != 1 || len(bindings[0].Resources) != 1 {
		t.Fatal("standalone reviewer captured unrelated files", bindings, err)
	}
}
