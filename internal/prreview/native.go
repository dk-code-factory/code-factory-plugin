package prreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dk-code-factory/agent-workflow/pkg/workflows"
)

// Engine journals are private and scoped to one app-owned session lock. Recovery
// never sweeps a journal belonging to another active review.
func (w *Workflow) engineStore(s *Session) (*workflows.SQLiteStore, error) {
	if err := w.Artifacts.check(); err != nil {
		return nil, err
	}
	return workflows.OpenSQLite(filepath.Join(w.Artifacts.Root, "engine", s.ID))
}

type reviewActionInput struct {
	Reviewer string `json:"reviewer,omitempty"`
}
type reviewActionOutput struct {
	Artifact Reference `json:"artifact"`
	Ready    bool      `json:"ready"`
	Waiting  bool      `json:"waiting"`
	Evidence string    `json:"evidence"`
	Gate     string    `json:"gate"`
}

// admissionBridge binds the engine origin before any application action starts.
// If the app write is interrupted, the private journal's sole origin recovers it.
type admissionBridge struct {
	*workflows.SQLiteStore
	w *Workflow
	s *Session
}

func (b admissionBridge) Admit(ctx context.Context, run *workflows.Run, snap workflows.Snapshot) error {
	if err := b.SQLiteStore.Admit(ctx, run, snap); err != nil {
		return err
	}
	if run.ID == run.OriginID {
		b.s.EngineRunID, b.s.EngineGeneration = run.ID, run.Generation
		return b.w.Save(ctx, b.s)
	}
	return nil
}
func (b admissionBridge) EnsureInputGate(ctx context.Context, gate workflows.ExternalInputGate) (workflows.ExternalInputGate, error) {
	saved, err := b.SQLiteStore.EnsureInputGate(ctx, gate)
	if err != nil {
		return saved, err
	}
	var payload struct {
		Attention string `json:"attention"`
	}
	if err = json.Unmarshal(saved.Payload, &payload); err != nil {
		return saved, err
	}
	var g *Gate
	if b.s.Gate != nil && b.s.Gate.ID == payload.Attention {
		g = b.s.Gate
	}
	for i := range b.s.Answers {
		if b.s.Answers[i].ID == payload.Attention {
			g = &b.s.Answers[i]
			break
		}
	}
	if g == nil || saved.EvidenceID != g.EvidenceID {
		return saved, errors.New("engine wait does not match the retained attention gate")
	}
	if g.EngineGateID != "" && g.EngineGateID != saved.ID {
		return saved, errors.New("engine gate identity changed")
	}
	if g.EngineGateID == "" || (len(g.Answer) == 0 && b.s.Status != "waiting") {
		if len(g.Answer) == 0 {
			b.s.Status = "waiting"
		}
		g.EngineGateID = saved.ID
		if b.s.Gate != nil && b.s.Gate.ID == g.ID {
			b.s.Gate.EngineGateID = saved.ID
		}
		if err = b.w.Save(ctx, b.s); err != nil {
			return saved, err
		}
	}
	if len(g.Answer) > 0 && saved.Answer == nil {
		r := newReviewExecution(b.w, b.s)
		if err = r.hydrate(); err != nil {
			return saved, err
		}
		if err = r.guard(ctx); err != nil {
			return saved, err
		}
		saved, err = b.SQLiteStore.AcceptInput(ctx, saved.OriginID, saved.ID, workflows.ExternalInputAnswer{AnswerID: g.AnswerID, EvidenceID: g.EvidenceID, Value: g.Answer})
	}
	return saved, err
}

func (a NativeAdapter) Execute(ctx context.Context, w *Workflow, s *Session) (result Result, err error) {
	if err = a.Check(ctx); err != nil {
		return
	}
	r := newReviewExecution(w, s)
	defer func() {
		if errors.Is(r.failure, ErrWorkerCleanup) {
			err = errors.Join(err, r.failure)
		}
	}()
	if err = r.hydrate(); err != nil {
		return
	}
	if s.Evidence != nil {
		if r.e.Rules != w.Rules || r.e.Constraints != w.Constraints {
			return result, errors.New("review governing instructions changed; prepare a fresh review")
		}
		if err = r.guard(ctx); err != nil {
			return
		}
	}
	journal, err := w.engineStore(s)
	if err != nil {
		return result, err
	}
	defer journal.Close()
	if err = journal.Recover(ctx); err != nil {
		return result, err
	}
	// Reconcile an interrupted origin-ID write using its immutable configuration.
	binding := Digest([]any{s.ID, s.Target, w.Bindings, w.Rules, w.Constraints, s.Implementation})
	if s.EngineRunID == "" {
		runs, e := journal.List(ctx)
		if e != nil {
			return result, e
		}
		for _, run := range runs {
			if run.ID == run.OriginID {
				_, snap, e := journal.Load(ctx, run.ID)
				if e != nil {
					return result, e
				}
				if snap.Metadata["review_identity"] != binding || s.EngineRunID != "" {
					return result, errors.New("retained engine admission identity differs; prepare a fresh review")
				}
				s.EngineRunID, s.EngineGeneration = run.ID, run.Generation
			}
		}
		if s.EngineRunID != "" {
			if err = w.Save(ctx, s); err != nil {
				return
			}
		}
	}
	if s.EngineRunID != "" {
		run, snap, e := journal.Load(ctx, s.EngineRunID)
		if e != nil {
			return result, e
		}
		if run.ID != run.OriginID || snap.Metadata["review_identity"] != binding {
			return result, errors.New("engine origin or configuration identity changed")
		}
		if err = r.bridgeAnswers(ctx, journal); err != nil {
			return
		}
		s.EngineGeneration = run.Generation
	}
	assessed := false
	if s.Status == "incomplete" {
		s.Status = "active"
		if err = w.Save(ctx, s); err != nil {
			return
		}
	}
	registry := &workflows.ActionRegistry{}
	for _, name := range []string{"capture", "prepare", "reviewer", "assess", "publish"} {
		action := name
		spec := workflows.ActionSpec[reviewActionInput, reviewActionOutput]{
			Implementation: ImplementationIdentity(), Effect: workflows.ActionReadOnly,
			Bindings: func(context.Context, workflows.ActionContext, reviewActionInput) (json.RawMessage, error) {
				return rawJSON(map[string]string{"identity": binding}), nil
			},
			ValidateCheckpoint: func(ctx context.Context, _ workflows.ActionContext, cp workflows.ActionCheckpoint[reviewActionInput, reviewActionOutput]) error {
				if err := r.hydrate(); err != nil {
					return err
				}
				if err := r.guard(ctx); err != nil {
					return err
				}
				// Every retained output is content addressed, including historical preparations.
				var value json.RawMessage
				if err := w.Artifacts.Load(cp.Result.Output.Artifact, &value); err != nil {
					return err
				}
				if action == "reviewer" {
					var checkpoint Checkpoint
					if err := StrictDecode(value, &checkpoint); err != nil {
						return err
					}
					if s.Identity == nil || checkpoint.Reviewer != cp.Input.Reviewer || checkpoint.SessionID != s.ID || Digest(checkpoint.Identity) != Digest(*s.Identity) {
						return errors.New("engine reviewer checkpoint identity changed")
					}
				}
				return nil
			},
			Handler: func(ctx context.Context, _ workflows.ActionContext, in reviewActionInput) (workflows.ActionResult[reviewActionOutput], error) {
				out := reviewActionOutput{}
				if err := r.hydrate(); err != nil {
					return workflows.ActionResult[reviewActionOutput]{}, err
				}
				var callErr error
				switch action {
				case "capture":
					callErr = r.capture(ctx)
					if s.Evidence != nil {
						out.Artifact = *s.Evidence
					}
				case "prepare":
					if s.Gate != nil && len(s.Gate.Answer) == 0 {
						callErr = ErrWaiting
					} else {
						callErr = r.prepare(ctx)
					}
					if errors.Is(callErr, ErrWaiting) {
						callErr = nil
						out.Waiting = true
						out.Evidence = s.Gate.EvidenceID
						out.Gate = s.Gate.ID
					} else if callErr == nil {
						out.Ready = true
					}
					if s.Preparation != nil {
						out.Artifact = *s.Preparation
					}
				case "reviewer":
					found := false
					for _, b := range w.Bindings {
						if b.Reviewer.ID == in.Reviewer {
							found = true
							callErr = r.review(ctx, b)
							if errors.Is(callErr, ErrWorkerCleanup) {
								r.failure = callErr
							}
							out.Artifact = s.Reviews[in.Reviewer]
							if callErr == nil {
								var cp Checkpoint
								callErr = w.Artifacts.Load(out.Artifact, &cp)
								if callErr == nil {
									switch cp.Status {
									case "exhausted":
										callErr = workflows.ErrUsageExhausted
									case "failed", "incomplete":
										callErr = fmt.Errorf("%w: %s", workflows.ErrProviderFailure, cp.Reason)
									}
								}
							}
							break
						}
					}
					if !found {
						callErr = errors.New("reviewer is absent from the pinned order")
					}
				case "assess":
					assessed = true
					if s.Status == "completed" && len(s.Results) > 0 {
						var saved Artifact
						callErr = w.Artifacts.Load(s.Results[len(s.Results)-1], &saved)
						result = saved.Result
					} else {
						result, callErr = r.complete(ctx)
					}
					out.Ready = result.Complete
					if len(s.Results) > 0 {
						out.Artifact = s.Results[len(s.Results)-1]
					}
				case "publish":
					result, callErr = w.resumePublication(ctx, s)
					if len(s.Results) > 0 {
						out.Artifact = s.Results[len(s.Results)-1]
					}
				}
				if errors.Is(callErr, ErrWorkerCleanup) {
					r.failure = callErr
				}
				return workflows.ActionResult[reviewActionOutput]{Output: out}, callErr
			},
		}
		if action == "publish" {
			spec.Effect = workflows.ActionExternalMutation
			spec.Reconcile = func(ctx context.Context, _ workflows.ActionContext, cp workflows.ActionCheckpoint[reviewActionInput, reviewActionOutput]) (workflows.ActionReconciliation[reviewActionOutput], error) {
				if s.Status != "completed" || s.Stage != "finished" || len(s.Results) == 0 {
					return workflows.ActionReconciliation[reviewActionOutput]{Outcome: "unknown"}, nil
				}
				out := reviewActionOutput{Artifact: s.Results[len(s.Results)-1]}
				return workflows.ActionReconciliation[reviewActionOutput]{Outcome: "completed", Proof: rawJSON(out.Artifact), Result: workflows.ActionResult[reviewActionOutput]{Output: out}}, nil
			}
		}
		if err = workflows.RegisterAction(registry, action, spec); err != nil {
			return
		}
	}
	dir, err := os.MkdirTemp(journal.Dir, "definitions-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	if err = os.Mkdir(filepath.Join(dir, "workflows"), 0700); err != nil {
		return
	}
	for _, name := range []string{"review", "preparation", "reviewer"} {
		data, e := implementation.ReadFile("workflows/" + name + ".yml")
		if e != nil {
			return result, e
		}
		if err = os.WriteFile(filepath.Join(dir, "workflows", name+".yml"), data, 0600); err != nil {
			return
		}
	}
	engine := workflows.Executor{WorkspaceDir: dir, ApplicationActions: registry, Store: admissionBridge{journal, w, s}, MaxParallel: 1}
	var run *workflows.Run
	if s.EngineRunID == "" {
		items := []any{}
		for _, b := range w.Bindings {
			items = append(items, map[string]any{"id": b.Reviewer.ID})
		}
		run, err = engine.Run(ctx, workflows.RunRequest{Ref: "workflows/review.yml", Inputs: map[string]any{"reviewers": items}, Metadata: map[string]any{"review_identity": binding}})
	} else {
		// A validated completed application artifact fences all reviewer dispatch,
		// including exhausted skips. Publication recovery uses its own intent/receipt
		// ledger; resuming the SDK root would make exhausted children retryable.
		if s.Status == "completed" {
			return w.resumePublication(ctx, s)
		}
		run, err = engine.Resume(ctx, s.EngineRunID, nil, true)
	}
	if run != nil {
		if s.EngineRunID != run.ID || s.EngineGeneration != run.Generation {
			s.EngineRunID, s.EngineGeneration = run.ID, run.Generation
			if saveErr := w.Save(context.WithoutCancel(ctx), s); saveErr != nil {
				return result, errors.Join(err, saveErr)
			}
		}
		if run.Status == workflows.RunStatusWaiting {
			return result, ErrWaiting
		}
	}
	if err != nil && s.Identity != nil && s.Status != "completed" && !assessed {
		// SDK guard failures stop iteration. Preserve validated partial findings even
		// when a dependent aggregation job could not start.
		if r.failure == nil && !errors.Is(err, workflows.ErrProviderFailure) && !errors.Is(err, workflows.ErrUsageExhausted) {
			r.failure = err
		}
		result, finishErr := r.complete(ctx)
		if (errors.Is(err, workflows.ErrProviderFailure) || errors.Is(err, workflows.ErrUsageExhausted)) && r.failure == nil {
			return result, finishErr
		}
		return result, errors.Join(err, finishErr)
	}
	if len(s.Results) > 0 {
		var artifact Artifact
		if loadErr := w.Artifacts.Load(s.Results[len(s.Results)-1], &artifact); loadErr != nil {
			return result, errors.Join(err, loadErr)
		}
		result = artifact.Result
	}
	return result, err
}

// The app transaction is the answer write-ahead log. Only that accepted identity
// can enter the SDK journal, so a crash between stores is safe to reconcile.
func (r *reviewExecution) bridgeAnswers(ctx context.Context, j *workflows.SQLiteStore) error {
	gates, err := j.Inputs(ctx, r.s.EngineRunID)
	if err != nil {
		return err
	}
	for _, gate := range gates {
		if gate.State == "canceled" || gate.State == "replaced" {
			return errors.New("retained engine question was canceled")
		}
		var payload struct {
			Attention string `json:"attention"`
		}
		if err = json.Unmarshal(gate.Payload, &payload); err != nil {
			return err
		}
		var answer *Gate
		for i := range r.s.Answers {
			if r.s.Answers[i].ID == payload.Attention {
				answer = &r.s.Answers[i]
				break
			}
		}
		if answer == nil {
			if gate.Answer != nil {
				return errors.New("engine answer has no accepted application identity")
			}
			continue
		}
		if answer.EngineGateID == "" && answer.EvidenceID == gate.EvidenceID {
			answer.EngineGateID = gate.ID
			if r.s.Gate != nil && r.s.Gate.ID == answer.ID {
				r.s.Gate.EngineGateID = gate.ID
			}
			if err = r.w.Save(ctx, r.s); err != nil {
				return err
			}
		}
		if answer.EngineGateID != gate.ID || answer.EvidenceID != gate.EvidenceID || answer.AnswerID == "" {
			return errors.New("retained answer and engine gate identity differ")
		}
		a := workflows.ExternalInputAnswer{AnswerID: answer.AnswerID, EvidenceID: answer.EvidenceID, Value: answer.Answer}
		if gate.Answer != nil {
			if Digest(*gate.Answer) != Digest(a) {
				return errors.New("engine answer identity differs")
			}
			continue
		}
		if err = r.guard(ctx); err != nil {
			return err
		}
		if _, err = j.AcceptInput(ctx, gate.OriginID, gate.ID, a); err != nil {
			return err
		}
	}
	return nil
}
