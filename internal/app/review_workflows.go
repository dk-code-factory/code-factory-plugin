package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/prreview"
	"codefactory/internal/store"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type PRReviewPolicy struct {
	ItemID         string          `json:"itemId"`
	AutoPostReview *bool           `json:"autoPostReview"`
	Effective      prreview.Policy `json:"effective"`
}

func (a *App) ReviewPostingPolicy(ctx context.Context, id string, launch *bool) (prreview.Policy, error) {
	var item domain.Item
	if err := a.Store.Get(ctx, "item", id, &item); err != nil {
		return prreview.Policy{}, err
	}
	if item.Kind != "pr" {
		return prreview.Policy{}, errors.New("review policy requires a PR")
	}
	var repo domain.Repository
	if err := a.Store.Get(ctx, "repository", item.Repository, &repo); err != nil {
		return prreview.Policy{}, err
	}
	config, err := a.Config(ctx)
	if err != nil {
		return prreview.Policy{}, err
	}
	var override PRReviewPolicy
	if err = a.Store.Get(ctx, "pr-review-policy", id, &override); err != nil && !errors.Is(err, store.ErrNotFound) {
		return prreview.Policy{}, err
	}
	return prreview.ResolvePolicy(config.AutoPostReview, repo.AutoPostReview, override.AutoPostReview, launch), nil
}
func (a *App) ReadReviewPolicy(ctx context.Context, id string) (PRReviewPolicy, error) {
	out := PRReviewPolicy{ItemID: id}
	if err := a.Store.Get(ctx, "pr-review-policy", id, &out); err != nil && !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	policy, err := a.ReviewPostingPolicy(ctx, id, nil)
	out.Effective = policy
	return out, err
}
func (a *App) SaveReviewPolicy(ctx context.Context, id string, value *bool) (PRReviewPolicy, error) {
	if _, err := a.ReviewPostingPolicy(ctx, id, nil); err != nil {
		return PRReviewPolicy{}, err
	}
	out := PRReviewPolicy{ItemID: id, AutoPostReview: value}
	if err := a.Store.Put(ctx, "pr-review-policy", id, out, "review.policy"); err != nil {
		return out, err
	}
	return a.ReadReviewPolicy(ctx, id)
}
func reviewSettings(c domain.Config, r domain.Repository) ([]prreview.Reviewer, string) {
	reviewers := c.Reviewers
	if r.Reviewers != nil {
		reviewers = *r.Reviewers
	}
	rules := c.ReviewRules
	if r.ReviewRules != nil {
		rules = *r.ReviewRules
	}
	if r.ReviewPrompt != "" {
		rules += "\n" + r.ReviewPrompt
	}
	return reviewers, rules
}
func (a *App) reviewArtifacts() prreview.Artifacts {
	return prreview.Artifacts{Root: filepath.Join(a.DataDir, "pr-review-artifacts")}
}
func (a *App) reviewAdapter() prreview.EngineAdapter {
	if a.ReviewEngine != nil {
		return a.ReviewEngine
	}
	return prreview.NativeAdapter{}
}
func (m *Manager) reviewBindings(ctx context.Context, reviewers []prreview.Reviewer) ([]prreview.Binding, error) {
	bindings := []prreview.Binding{}
	for _, r := range reviewers {
		b := prreview.Binding{Reviewer: r, Resources: map[string]string{}, Modes: map[string]uint32{}}
		if r.Kind == "skill" {
			var skill domain.Skill
			if err := m.app.Store.Get(ctx, "skill", r.SkillID, &skill); err != nil {
				return nil, fmt.Errorf("reviewer %s skill unavailable", r.ID)
			}
			if strings.Contains(skill.Name, "dk-pr-review") || strings.Contains(skill.Name, "dk-pr-iter") {
				return nil, errors.New("review orchestration skills cannot be reviewer bindings")
			}
			if skill.Disabled {
				return nil, fmt.Errorf("reviewer %s skill disabled", r.ID)
			}
			captured, err := snapshotSkill(skill)
			if err != nil {
				return nil, err
			}
			b.Resources = captured.Files
			b.Modes = captured.Modes
			b.Entrypoint = captured.EntryPath
			if b.Entrypoint == "" {
				b.Entrypoint = "SKILL.md"
			}
			if len(b.Resources) == 0 {
				b.Entrypoint = "SKILL.md"
				b.Resources = map[string]string{"SKILL.md": base64.StdEncoding.EncodeToString([]byte(captured.Content))}
			}
		} else {
			// Pin the executable and every local argument file; never re-resolve PATH on resume.
			command, err := exec.LookPath(r.Argv[0])
			if err != nil {
				return nil, fmt.Errorf("reviewer %s command unavailable", r.ID)
			}
			command, err = filepath.EvalSymlinks(command)
			if err != nil {
				return nil, err
			}
			command, err = filepath.Abs(command)
			if err != nil {
				return nil, err
			}
			root, err := commandPackageRoot(command)
			if err != nil {
				return nil, err
			}
			b.Entrypoint = "bin/" + filepath.Base(command)
			if root != command {
				b.Entrypoint = filepath.Join("bin", strings.TrimPrefix(command, root+string(filepath.Separator)))
			}
			if err = captureCommandPackage(root, "bin", command, &b); err != nil {
				return nil, err
			}
			b.Reviewer.Argv = append([]string{}, r.Argv...)
			b.Reviewer.Argv[0] = "{package}/" + b.Entrypoint
			for i, arg := range r.Argv[1:] {
				if strings.Contains(arg, "{handoff}") || strings.HasPrefix(arg, "-") {
					continue
				}
				if info, err := os.Stat(arg); err == nil && info.Mode().IsRegular() {
					root, err := commandPackageRoot(arg)
					if err != nil {
						return nil, err
					}
					prefix := fmt.Sprintf("args/%d", i)
					absolute, err := filepath.Abs(arg)
					if err != nil {
						return nil, err
					}
					relative, err := filepath.Rel(root, absolute)
					if info, statErr := os.Stat(root); statErr == nil && info.Mode().IsRegular() {
						relative = filepath.Base(root)
					}
					if err != nil {
						return nil, err
					}
					if err = captureCommandPackage(root, prefix, absolute, &b); err != nil {
						return nil, err
					}
					b.Reviewer.Argv[i+1] = "{package}/" + filepath.Join(prefix, relative)
				}
			}
		}
		b = prreview.PinBinding(b)
		if err := b.Validate(); err != nil {
			return nil, err
		}
		bindings = append(bindings, b)
	}
	return bindings, nil
}

type retainedReview struct {
	Session       prreview.Session       `json:"session"`
	Bindings      []prreview.Binding     `json:"bindings"`
	Rules         string                 `json:"rules"`
	Configuration string                 `json:"configuration"`
	CleanupError  string                 `json:"cleanupError,omitempty"`
	PendingWorker *reviewWorkerAdmission `json:"pendingWorker,omitempty"`
}

// A waiter keeps its execution's result even after the session starts another.
// err is written before done closes and remains immutable afterwards.
type reviewExecutionResult struct {
	done chan struct{}
	err  error
}

func (m *Manager) startReviewWorkflow(ctx context.Context, launch domain.Run, resume bool) (domain.Run, error) {
	return m.startReviewWorkflowIntent(ctx, launch, resume, "")
}

// expectedAnswer fences automatic continuation against a later explicit interrupt.
// An explicit user Resume has no answer fence and may clear interruption intent.
func (m *Manager) startReviewWorkflowIntent(ctx context.Context, launch domain.Run, resume bool, expectedAnswer string) (domain.Run, error) {
	if launch.ReviewWorkflow != prreview.Contract {
		return launch, errors.New("unknown retained review workflow contract")
	}
	var item domain.Item
	if err := m.app.Store.Get(ctx, "item", launch.ItemID, &item); err != nil {
		return launch, err
	}
	var repo domain.Repository
	if err := m.app.Store.Get(ctx, "repository", item.Repository, &repo); err != nil {
		return launch, err
	}
	if err := (prreview.Evidence{Repository: repo.Checkout}).ValidateStorage(m.app.reviewArtifacts().Root); err != nil {
		return launch, err
	}
	config, err := m.app.Config(ctx)
	if err != nil {
		return launch, err
	}
	if err := validatePRReviewRun(func(kind, id string, out any) error { return m.app.Store.Get(ctx, kind, id, out) }, launch, item); err != nil {
		return launch, err
	}
	reviewers, rules := reviewSettings(config, repo)
	if len(reviewers) == 0 {
		return launch, errors.New("PR review requires at least one ordered reviewer; configure reviewers in Code Factory settings before fetching the diff")
	}
	if err = prreview.ValidateReviewers(reviewers); err != nil {
		return launch, err
	}
	if err = m.app.reviewAdapter().Check(ctx); err != nil {
		return launch, err
	}
	if err = m.reviewWorkerDependencies(launch); err != nil {
		return launch, err
	}
	owner, unlock, err := m.lockRun(ctx, launch.ID, false)
	if err != nil {
		return launch, err
	}
	defer unlock()
	if owner.reviewResult != nil {
		select {
		case <-owner.reviewResult.done:
		default:
			return launch, errors.New("previous review execution is still stopping; resume after its retained status settles")
		}
	}
	var retained retainedReview
	err = m.app.Store.Get(ctx, "review-workflow", launch.ID, &retained)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return launch, err
	}
	configuration := prreview.Digest([]any{reviewers, rules, launch.Profile, launch.Skills})
	if retained.Session.ID != "" {
		if retained.Configuration != configuration {
			return launch, errors.New("review configuration changed; start a fresh review")
		}
		if !resume {
			return launch, errors.New("existing review workflow requires explicit resume")
		}
	} else {
		bindings, err := m.reviewBindings(ctx, reviewers)
		if err != nil {
			return launch, err
		}
		retained = retainedReview{Session: prreview.Session{ID: launch.ID, Target: item.URL}, Bindings: bindings, Rules: rules, Configuration: configuration}
	}
	lifetime, cancel := context.WithCancel(m.ctx)
	err = m.app.Store.Write(ctx, func(t *store.Tx) error {
		if err := t.Get("run", launch.ID, &launch); err != nil {
			return err
		}
		// Closing the execution result proves the orchestrator exited, not that every
		// native child stopped. Keep that uncertainty across explicit resumes
		// and daemon restarts; no admission path can clear it implicitly.
		var current retainedReview
		if err := t.Get("review-workflow", launch.ID, &current); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := reviewCleanupError(current); err != nil {
			return err
		}
		if expectedAnswer != "" {
			var accepted retainedReview
			if err := t.Get("review-workflow", launch.ID, &accepted); err != nil {
				return err
			}
			if launch.InterruptRequested || !launch.ResumePending || accepted.Session.Gate == nil || accepted.Session.Gate.AnswerID != expectedAnswer {
				return errors.New("review answer remains accepted; explicit interruption stopped its continuation")
			}
		}
		if launch.Status != "prepared" && !(resume && (launch.Status == "interrupted" || launch.Status == "failed")) {
			return fmt.Errorf("review cannot start from %s", launch.Status)
		}
		if err := validatePRReviewRun(t.Get, launch, item); err != nil {
			return err
		}
		if launch.AutomaticReview {
			if err := validateAutomaticStart(t, launch); err != nil {
				return err
			}
		}
		launch.ReviewExecution = ID()
		launch.Status = "running"
		launch.Error = ""
		launch.ResumePending = true
		launch.InterruptRequested = false
		launch.TurnID = ""
		if err := t.Put("review-workflow", launch.ID, retained); err != nil {
			return err
		}
		if err := t.Put("run", launch.ID, launch); err != nil {
			return err
		}
		return t.Event("run.state", launch.ID, map[string]string{"id": launch.ID, "status": launch.Status})
	})
	if err != nil {
		cancel()
		return launch, err
	}
	owner.ctx = lifetime
	owner.cancelMu.Lock()
	owner.cancel = cancel
	owner.cancelMu.Unlock()
	owner.run = launch
	owner.retired = false
	owner.clientless = false
	owner.pending = map[string]domain.Attention{}
	// All provider calls are children of this retained workflow. Native completion
	// callbacks cannot reach the parent run's one-turn lifecycle.
	execution := &reviewExecutionResult{done: make(chan struct{})}
	owner.reviewResult = execution
	if !m.background(func() {
		defer close(execution.done)
		m.executeReviewWorkflow(lifetime, owner, retained, launch, execution)
	}) {
		cancel()
		execution.err = errors.New("daemon is stopping")
		close(execution.done)
		return launch, execution.err
	}
	return launch, nil
}

type launchReviewCollector struct {
	prreview.Collector
	head           string
	publication    *prreview.Operation
	publicationErr error
}

func (c launchReviewCollector) ValidateCurrent(ctx context.Context, e prreview.Evidence) error {
	if e.Metadata.Head != c.head {
		return errors.New("captured PR head changed from the accepted launch; prepare a fresh review")
	}
	if c.publicationErr != nil {
		return c.publicationErr
	}
	if c.publication != nil {
		if validator, ok := c.Collector.(interface {
			ValidateCurrentAfterPublication(context.Context, prreview.Evidence, prreview.Operation) error
		}); ok {
			return validator.ValidateCurrentAfterPublication(ctx, e, *c.publication)
		}
	}
	return c.Collector.ValidateCurrent(ctx, e)
}

func (m *Manager) executeReviewWorkflow(ctx context.Context, owner *session, retained retainedReview, launch domain.Run, execution *reviewExecutionResult) {
	id := retained.Session.ID
	collector := m.app.ReviewCollector
	if collector == nil {
		var item domain.Item
		var repo domain.Repository
		if m.app.Store.Get(ctx, "item", launch.ItemID, &item) == nil && m.app.Store.Get(ctx, "repository", item.Repository, &repo) == nil {
			collector = prreview.GitCollector{Remote: providers.ReviewGitHub{Client: providers.LiveGitHub(m.app.GitHub)}, Repository: repo.Checkout}
		}
	}
	workers := m.reviewWorkflowWorkers(launch, owner)
	w := prreview.Workflow{Adapter: m.app.reviewAdapter(), Collector: collector, Workers: workers, Artifacts: m.app.reviewArtifacts(), Bindings: retained.Bindings, Rules: retained.Rules, Constraints: launch.Prompt}
	head := launch.Base
	if launch.SourceSnapshot != nil && launch.SourceSnapshot.Head != "" {
		head = launch.SourceSnapshot.Head
	}
	if collector != nil {
		guard := launchReviewCollector{Collector: collector, head: head}
		if retained.Session.Status == "completed" && launch.PostingOperation != "" {
			op, exists, err := (reviewOperationStore{a: m.app}).LoadOperation(ctx, launch.PostingOperation)
			if err != nil {
				guard.publicationErr = err
			} else if !exists || len(retained.Session.Results) == 0 || op.Artifact != retained.Session.Results[len(retained.Session.Results)-1] || op.Target != retained.Session.Target || op.ID != prreview.Digest([]any{op.Target, head, id}) {
				guard.publicationErr = errors.New("retained publication does not belong to this completed review")
			} else if op.Status == "submitting" || op.Status == "uncertain" || op.Status == "verified" {
				guard.publication = &op
			}
		}
		w.Collector = guard
	}
	w.Save = func(ctx context.Context, state *prreview.Session) error {
		owner.mu.Lock()
		defer owner.mu.Unlock()
		retained.Session = *state
		var savedRun domain.Run
		err := m.app.Store.Write(ctx, func(t *store.Tx) error {
			var run domain.Run
			if err := t.Get("run", id, &run); err != nil {
				return err
			}
			if run.ReviewExecution != launch.ReviewExecution || run.InterruptRequested || run.Status == "terminated" || run.Status == "cleaned" {
				return context.Canceled
			}
			run.ReviewStage = state.Stage
			if state.Status == "waiting" && state.Gate != nil {
				questions := []map[string]any{}
				for _, q := range state.Gate.Questions {
					options := []map[string]string{}
					for _, o := range q.Options {
						options = append(options, map[string]string{"label": o.Choice, "description": o.Benefits + "; " + o.Costs})
					}
					questions = append(questions, map[string]any{"id": q.ID, "header": "Review scope", "question": q.Statements + "\n" + q.AffectedWork, "options": options})
				}
				at := domain.Attention{ID: state.Gate.ID, RunID: id, Method: "item/tool/requestUserInput", Params: raw(map[string]any{"questions": questions, "gateId": state.Gate.ID, "evidenceId": state.Gate.EvidenceID}), Origin: "workflow", Status: "pending", Blocking: true, Created: time.Now().UTC()}
				if err := putAttention(t, at, "attention.required"); err != nil {
					return err
				}
				run.Status = "waiting"
			}
			if len(state.Results) > 0 {
				ref := state.Results[len(state.Results)-1]
				run.ReviewArtifact = &ref
				run.ReviewComplete = state.Status == "completed"
				var artifact prreview.Artifact
				if err := m.app.reviewArtifacts().Load(ref, &artifact); err != nil {
					return err
				}
				summary := artifact.Result.Summary()
				run.ReviewResult = &summary
			}
			var current retainedReview
			if err := t.Get("review-workflow", id, &current); err != nil {
				return err
			}
			current.Session = *state
			if err := t.Put("review-workflow", id, current); err != nil {
				return err
			}
			if err := t.Put("run", id, run); err != nil {
				return err
			}
			savedRun = run
			return t.Event("run.state", id, map[string]string{"id": id, "status": run.Status, "reviewStage": run.ReviewStage})
		})
		if err == nil {
			owner.run = savedRun
		}
		return err
	}
	w.OnComplete = func(ctx context.Context, ref prreview.Reference) error {
		var current domain.Run
		if err := m.app.Store.Get(ctx, "run", id, &current); err != nil {
			return err
		}
		if current.PostingOperation != "" && (current.PostingState == "submitting" || current.PostingState == "uncertain") {
			_, err := m.app.ReconcileRetainedReview(ctx, current.PostingOperation)
			return err
		}
		if current.PostingState == "verified" || current.PostingState == "skipped" {
			return nil
		}
		var artifact prreview.Artifact
		var evidence prreview.Evidence
		if err := m.app.reviewArtifacts().Load(ref, &artifact); err != nil {
			return err
		}
		if err := m.app.reviewArtifacts().Load(artifact.Evidence, &evidence); err != nil {
			return err
		}
		if err := w.Collector.ValidateCurrent(ctx, evidence); err != nil {
			return err
		}
		policy, err := m.app.ReviewPostingPolicy(ctx, launch.ItemID, launch.AutoPostReview)
		if err != nil {
			return err
		}
		if !policy.Enabled {
			return nil
		}
		_, err = m.app.postRetainedReview(ctx, PostRetainedReviewInput{RunID: id, Artifact: ref}, true)
		return err
	}
	var result prreview.Result
	source, _, runErr := m.continuationContext(ctx, launch)
	if runErr == nil && closedWork(*source) {
		terminal, finish := context.WithTimeout(m.ctx, 5*time.Second)
		runErr = m.stopClosedItem(terminal, *source)
		finish()
		if runErr == nil {
			return
		}
	}
	if runErr == nil {
		result, runErr = w.Run(ctx, &retained.Session)
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.run.ReviewExecution != launch.ReviewExecution {
		return
	}
	owner.retired = true
	runErr = errors.Join(runErr, workers.failure)
	var settled domain.Run
	finalErr := m.app.Store.Write(context.WithoutCancel(ctx), func(t *store.Tx) error {
		var run domain.Run
		if err := t.Get("run", id, &run); err != nil {
			return err
		}
		if run.ReviewExecution != launch.ReviewExecution || run.Status == "terminated" || run.Status == "cleaned" {
			return nil
		}
		var current retainedReview
		if err := t.Get("review-workflow", id, &current); err != nil {
			return err
		}
		// An adapter's return cannot certify a worker whose matching durable
		// return proof is still missing, even if it returned no cleanup error.
		runErr = errors.Join(runErr, reviewCleanupError(current))
		if errors.Is(runErr, prreview.ErrWaiting) || (errors.Is(runErr, context.Canceled) && !run.InterruptRequested && retained.Session.Gate != nil && len(retained.Session.Gate.Answer) == 0) {
			run.Status = "waiting"
		} else if run.InterruptRequested || errors.Is(runErr, context.Canceled) {
			run.Status = "interrupted"
		} else if runErr != nil || !result.Complete {
			run.Status = "failed"
			run.Error = result.Reason
			if runErr != nil {
				run.Error = runErr.Error()
			}
		} else {
			run.Status = "idle"
		}
		if errors.Is(runErr, prreview.ErrWorkerCleanup) {
			run.Error = runErr.Error()
			run.InterruptRequested = true
			current.CleanupError = run.Error
			if err := t.Put("review-workflow", id, current); err != nil {
				return err
			}
		}
		run.ReviewComplete = result.Complete
		run.ResumePending = run.Status == "interrupted" && !run.InterruptRequested
		if err := t.Put("run", id, run); err != nil {
			return err
		}
		settled = run
		return t.Event("run.state", id, map[string]string{"id": id, "status": run.Status})
	})
	if errors.Is(runErr, prreview.ErrWorkerCleanup) {
		execution.err = runErr
	}
	if finalErr != nil {
		execution.err = errors.Join(execution.err, fmt.Errorf("review final state could not be retained: %w", finalErr))
		owner.run.Error = errors.Join(runErr, execution.err).Error()
	} else if settled.ID != "" {
		owner.run = settled
	}
}
func (m *Manager) interruptReviewWorkflow(ctx context.Context, id string) error {
	owner, unlock, err := m.lockRun(ctx, id, false)
	if err != nil {
		return err
	}
	if err = m.app.Store.Write(ctx, func(t *store.Tx) error {
		var run domain.Run
		if err := t.Get("run", id, &run); err != nil {
			return err
		}
		if run.Status == "cleaned" || run.Status == "terminated" {
			return nil
		}
		run.Status = "interrupted"
		run.InterruptRequested = true
		run.ResumePending = false
		owner.run = run
		if err := t.Put("run", id, run); err != nil {
			return err
		}
		return t.Event("run.state", id, map[string]string{"id": id, "status": run.Status})
	}); err != nil {
		unlock()
		return err
	}
	owner.cancelLifetime()
	execution := owner.reviewResult
	stopReason := owner.run.Error
	unlock()
	return waitReviewInterrupt(ctx, execution, stopReason)
}

func waitReviewInterrupt(ctx context.Context, execution *reviewExecutionResult, stopReason string) error {
	if execution != nil {
		select {
		case <-execution.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		if execution.err != nil {
			return execution.err
		}
	}
	if strings.Contains(stopReason, prreview.ErrWorkerCleanup.Error()) {
		return fmt.Errorf("%w: %s", prreview.ErrWorkerCleanup, stopReason)
	}
	return nil
}
func (m *Manager) recoverReviewWorkflow(ctx context.Context, run domain.Run) error {
	var saved retainedReview
	if err := m.app.Store.Get(ctx, "review-workflow", run.ID, &saved); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := reviewCleanupError(saved); err != nil && run.Status != "cleaned" && run.Status != "terminated" {
		run.Status = "interrupted"
		run.ResumePending = false
		run.InterruptRequested = true
		if run.Error == "" {
			run.Error = err.Error()
		}
		return m.app.Store.Put(ctx, "run", run.ID, run, "run.state")
	}
	if run.ResumePending && !run.InterruptRequested {
		if saved.Session.Gate != nil && saved.Session.Gate.AnswerID != "" && len(saved.Session.Gate.Answer) > 0 {
			run.Status = "interrupted"
			if err := m.app.Store.Put(ctx, "run", run.ID, run, "run.state"); err != nil {
				return err
			}
			_, err := m.startReviewWorkflowIntent(ctx, run, true, saved.Session.Gate.AnswerID)
			return err
		}
	}

	if run.Status != "running" && run.Status != "queued" && run.Status != "starting" {
		return nil
	}
	run.Status = "interrupted"
	run.ResumePending = false
	run.Error = "Review workflow interrupted by restart; explicitly resume to validate retained evidence and checkpoints."
	return m.app.Store.Put(ctx, "run", run.ID, run, "run.state")
}
func (m *Manager) answerReviewWorkflow(ctx context.Context, at domain.Attention, answer json.RawMessage) error {
	// Validate current governing evidence before accepting an answer from an older
	// view. Recheck the gate under the transaction after this bounded read.
	var saved retainedReview
	var run domain.Run
	if err := m.app.Store.Get(ctx, "review-workflow", at.RunID, &saved); err != nil {
		return err
	}
	if err := m.app.Store.Get(ctx, "run", at.RunID, &run); err != nil {
		return err
	}
	workers := m.reviewWorkflowWorkers(run, nil)
	if saved.Session.Implementation != prreview.ImplementationIdentity() || saved.Session.WorkerImplementation != prreview.WorkerIdentity(workers) {
		return errors.New("review implementation changed; prepare a fresh review")
	}
	var evidence prreview.Evidence
	if saved.Session.Evidence == nil {
		return errors.New("review question has no retained evidence")
	}
	if err := m.app.reviewArtifacts().Load(*saved.Session.Evidence, &evidence); err != nil {
		return err
	}
	var item domain.Item
	var repo domain.Repository
	if err := m.app.Store.Get(ctx, "item", run.ItemID, &item); err != nil {
		return err
	}
	if err := m.app.Store.Get(ctx, "repository", item.Repository, &repo); err != nil {
		return err
	}
	config, err := m.app.Config(ctx)
	if err != nil {
		return err
	}
	reviewers, rules := reviewSettings(config, repo)
	if saved.Configuration != prreview.Digest([]any{reviewers, rules, run.Profile, run.Skills}) {
		return errors.New("review question governing configuration changed; prepare a fresh review")
	}
	collector := m.app.ReviewCollector
	if collector == nil {
		collector = prreview.GitCollector{Remote: providers.ReviewGitHub{Client: providers.LiveGitHub(m.app.GitHub)}, Repository: repo.Checkout}
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = collector.ValidateCurrent(bounded, evidence); err != nil {
		return fmt.Errorf("review question evidence is stale: %w", err)
	}
	if saved.Session.Preparation == nil {
		return errors.New("review question has no retained preparation")
	}
	var preparation prreview.Preparation
	if err = m.app.reviewArtifacts().Load(*saved.Session.Preparation, &preparation); err != nil {
		return err
	}
	if err = preparation.Validate(evidence, saved.Bindings); err != nil {
		return err
	}
	if err = evidence.ValidateCheckout(bounded); err != nil {
		return err
	}
	owner, unlock, err := m.lockRun(ctx, at.RunID, false)
	if err != nil {
		return err
	}
	if owner.reviewResult != nil {
		done := owner.reviewResult.done
		unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
		owner, unlock, err = m.lockRun(ctx, at.RunID, false)
		if err != nil {
			return err
		}
	}

	acceptedAnswerID := ""
	err = m.app.Store.Write(ctx, func(t *store.Tx) error {
		var current domain.Attention
		if err := t.Get("attention", at.ID, &current); err != nil {
			return err
		}
		if current.Status != "pending" {
			return errors.New("stale or duplicate review answer")
		}
		var run domain.Run
		if err := t.Get("run", at.RunID, &run); err != nil {
			return err
		}
		if run.InterruptRequested || run.Status != "waiting" {
			return errors.New("review wait is no longer active")
		}
		var currentItem domain.Item
		var currentRepo domain.Repository
		var currentConfig domain.Config
		if err := t.Get("item", run.ItemID, &currentItem); err != nil {
			return err
		}
		if err := t.Get("repository", currentItem.Repository, &currentRepo); err != nil {
			return err
		}
		if err := t.Get("config", "settings", &currentConfig); err != nil {
			return err
		}
		currentReviewers, currentRules := reviewSettings(currentConfig, currentRepo)
		if saved.Configuration != prreview.Digest([]any{currentReviewers, currentRules, run.Profile, run.Skills}) {
			return errors.New("review question configuration changed during validation; prepare a fresh review")
		}
		if err := validatePRReviewRun(t.Get, run, currentItem); err != nil {
			return err
		}
		if run.AutomaticReview {
			if err := validateAutomaticPolicy(t, run, currentItem, false); err != nil {
				return err
			}
		}

		if err := validateAnswer(current, answer); err != nil {
			return err
		}
		var payload struct {
			Answers map[string]struct {
				Answers []string `json:"answers"`
			} `json:"answers"`
		}
		if err := json.Unmarshal(answer, &payload); err != nil {
			return err
		}
		values := map[string]string{}
		for k, v := range payload.Answers {
			values[k] = strings.Join(v.Answers, "\n")
		}
		var retained retainedReview
		if err := t.Get("review-workflow", at.RunID, &retained); err != nil {
			return err
		}
		if retained.Session.Gate == nil {
			return errors.New("missing review gate")
		}
		if saved.Session.Gate == nil || retained.Configuration != saved.Configuration || retained.Session.Gate.EvidenceID != saved.Session.Gate.EvidenceID {
			return errors.New("review question evidence changed during answer validation")
		}

		if err := prreview.AcceptAnswer(&retained.Session, at.ID, retained.Session.Gate.EvidenceID, raw(values)); err != nil {
			return err
		}
		acceptedAnswerID = retained.Session.Gate.AnswerID
		current.Status = "answered"
		run.Status = "interrupted"
		run.ResumePending = true
		if err := t.Put("review-workflow", at.RunID, retained); err != nil {
			return err
		}
		if err := t.Put("run", run.ID, run); err != nil {
			return err
		}
		owner.run = run
		return putAttention(t, current, "attention.resolved")
	})
	unlock()
	if err != nil {
		return err
	}
	if err = m.app.Store.Get(ctx, "run", at.RunID, &run); err != nil {
		return err
	}
	_, err = m.startReviewWorkflowIntent(ctx, run, true, acceptedAnswerID)
	return err
}

// Existing send_message controls can resume an interrupted workflow without
// inventing an answer or changing its engineering inputs. New scope needs a fresh
// prepared review; material answers use the correlated attention request.
func (m *Manager) queueReviewContinuation(ctx context.Context, run domain.Run, text, expected string) (MessageDelivery, error) {
	operation := strings.ToLower(strings.TrimSpace(text))
	if expected != "" || (operation != "continue" && operation != "resume" && operation != "continue review") {
		return MessageDelivery{}, errors.New("retained reviews accept continue or resume here; answer pending questions with answer_request, or prepare a fresh review for changed engineering instructions")
	}
	if run.Status != "interrupted" && run.Status != "failed" {
		return MessageDelivery{}, fmt.Errorf("review cannot continue from %s; inspect its pending questions or retained result", run.Status)
	}
	if _, err := m.startReviewWorkflow(ctx, run, true); err != nil {
		return MessageDelivery{}, err
	}
	delivery := MessageDelivery{ID: ID(), RunID: run.ID, State: "delivered", Created: time.Now().UTC(), Updated: time.Now().UTC()}
	err := m.app.Store.Write(ctx, func(t *store.Tx) error { return saveMessage(t, delivery) })
	return delivery, err
}

func commandPackageRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	leaf := filepath.Dir(absolute)
	for root := leaf; filepath.Dir(root) != root; root = filepath.Dir(root) {
		for _, marker := range []string{".codex-plugin/plugin.json", "pyproject.toml", "package.json", "go.mod", ".git"} {
			if _, err := os.Stat(filepath.Join(root, marker)); err == nil {
				if marker == ".git" {
					// A stray or incomplete ancestor marker is not a package boundary. In
					// particular, never sweep an unrelated shared temporary directory.
					gitRoot, gitErr := command(context.Background(), root, "rev-parse", "--show-toplevel")
					if gitErr != nil || filepath.Clean(gitRoot) != root {
						continue
					}
				}
				return root, nil
			}
		}
	}
	return absolute, nil
}
func captureCommandPackage(root, prefix, selected string, b *prreview.Binding) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		_, err := captureCommandResource(root, filepath.Join(prefix, filepath.Base(root)), 32<<20, b)
		return err
	}
	relative, err := filepath.Rel(root, selected)
	if err != nil {
		return err
	}
	total, err := captureCommandResource(selected, filepath.Join(prefix, relative), 32<<20, b)
	if err != nil {
		return err
	}
	tracked := map[string]bool{}
	gitRoot, gitErr := command(context.Background(), root, "rev-parse", "--show-toplevel")
	_ = gitRoot
	if gitErr == nil {
		cmd := exec.CommandContext(context.Background(), "git", "ls-files", "-z")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		paths, err := cmd.Output()
		if err != nil {
			return errors.New("git ls-files failed; check repository access and base reference")
		}
		for _, path := range strings.Split(string(paths), "\x00") {
			if path != "" {
				tracked[filepath.Join(root, path)] = true
			}
		}
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("command reviewer packages cannot contain symlinks")
		}
		if entry.IsDir() {
			if path != root {
				switch entry.Name() {
				case ".git", "node_modules", ".venv", "__pycache__", ".cache":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if path == selected || gitErr == nil && !tracked[path] {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		size, err := captureCommandResource(path, filepath.Join(prefix, relative), (32<<20)-total, b)
		total += size
		return err
	})
}

// Stat before allocating, then bound the read as well: files can grow after Stat.
func captureCommandResource(path, name string, remaining int64, b *prreview.Binding) (int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("invalid command reviewer package resource")
	}
	// A replacement after Lstat must not block on a FIFO or follow a symlink.
	// Recheck the opened descriptor before reading any resource bytes.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("invalid command reviewer package resource")
	}
	if info.Size() > remaining {
		return 0, errors.New("command reviewer package exceeds 32 MiB")
	}
	data, err := readCommandBytes(file, remaining)
	if err != nil {
		return 0, err
	}
	b.Resources[name] = base64.StdEncoding.EncodeToString(data)
	b.Modes[name] = uint32(info.Mode().Perm())
	return int64(len(data)), nil
}

func readCommandBytes(reader io.Reader, remaining int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, remaining+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > remaining {
		return nil, errors.New("command reviewer package exceeds 32 MiB")
	}
	return data, nil
}
