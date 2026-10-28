package prreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var ErrWaiting = errors.New("review workflow suspended for a material decision")
var ErrWorkerCleanup = errors.New("review worker cleanup could not be verified")

type Gate struct {
	EngineGateID string          `json:"engine_gate_id,omitempty"`
	AnswerID     string          `json:"answer_id,omitempty"`
	ID           string          `json:"id"`
	EvidenceID   string          `json:"evidence_id"`
	Questions    []Question      `json:"questions"`
	Answer       json.RawMessage `json:"answer,omitempty"`
}
type SessionIdentity struct {
	PRURL         string   `json:"pr_url"`
	Head          string   `json:"head"`
	Base          string   `json:"base"`
	EngineeringID string   `json:"engineering_id"`
	Tree          string   `json:"tree"`
	Scope         string   `json:"scope"`
	ScopeText     string   `json:"scope_text"`
	RulesText     string   `json:"rules_text"`
	Reviewers     []string `json:"reviewers"`
}
type Checkpoint struct {
	Version   int             `json:"checkpoint_version"`
	SessionID string          `json:"session_id"`
	Identity  SessionIdentity `json:"session_identity"`
	Reviewer  string          `json:"reviewer"`
	Status    string          `json:"status"`
	Result    Response        `json:"result"`
	Reason    string          `json:"reason,omitempty"`
}
type Session struct {
	WorkerImplementation string               `json:"worker_implementation,omitempty"`
	EngineRunID          string               `json:"engine_run_id,omitempty"`
	EngineGeneration     int64                `json:"engine_generation,omitempty"`
	MaterialCheck        *Reference           `json:"material_check,omitempty"`
	MaterialCorrections  int                  `json:"material_corrections"`
	Implementation       string               `json:"implementation_identity"`
	Version              int                  `json:"session_version"`
	ID                   string               `json:"id"`
	Target               string               `json:"target"`
	Stage                string               `json:"stage"`
	Status               string               `json:"status"`
	Identity             *SessionIdentity     `json:"identity,omitempty"`
	Evidence             *Reference           `json:"evidence,omitempty"`
	Preparation          *Reference           `json:"preparation,omitempty"`
	Preparations         []Reference          `json:"preparations"`
	Reviews              map[string]Reference `json:"reviews"`
	History              []Reference          `json:"review_history"`
	Results              []Reference          `json:"results"`
	Gate                 *Gate                `json:"gate,omitempty"`
	Answers              []Gate               `json:"answers"`
}
type ReviewerReason struct {
	Reviewer string `json:"reviewer"`
	Reason   string `json:"reason"`
}
type AggregateFinding struct {
	Finding
	Reviewers []string `json:"reviewers"`
}
type ReviewerFailure struct {
	Reviewer string `json:"reviewer"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
}
type Result struct {
	Complete     bool               `json:"complete"`
	Outcome      string             `json:"outcome"`
	Findings     []AggregateFinding `json:"findings"`
	Executed     []string           `json:"executed_reviewers"`
	Reused       []string           `json:"reused_reviewers"`
	Skipped      []ReviewerReason   `json:"skipped_reviewers"`
	Failed       []ReviewerFailure  `json:"failed_reviewers,omitempty"`
	GuardFailure bool               `json:"guard_failure,omitempty"`
	Reason       string             `json:"reason,omitempty"`
}

// ResultSummary is the public run projection. Finding prose remains exclusively
// in the retained artifact; a missing historical count never establishes clean.
type ResultSummary struct {
	Complete     bool              `json:"complete"`
	Outcome      string            `json:"outcome"`
	FindingCount *int              `json:"finding_count,omitempty"`
	Executed     []string          `json:"executed_reviewers"`
	Reused       []string          `json:"reused_reviewers"`
	Skipped      []ReviewerReason  `json:"skipped_reviewers"`
	Failed       []ReviewerFailure `json:"failed_reviewers,omitempty"`
	GuardFailure bool              `json:"guard_failure,omitempty"`
	Reason       string            `json:"reason,omitempty"`
}

func (r Result) Summary() ResultSummary {
	count := len(r.Findings)
	return ResultSummary{r.Complete, r.Outcome, &count, r.Executed, r.Reused, r.Skipped, r.Failed, r.GuardFailure, r.Reason}
}

type Artifact struct {
	Version       int             `json:"version"`
	SessionID     string          `json:"session_id"`
	Identity      SessionIdentity `json:"session_identity"`
	PRURL         string          `json:"pr_url"`
	Head          string          `json:"head"`
	Tree          string          `json:"tree"`
	Scope         string          `json:"scope"`
	PreparationID string          `json:"preparation_id"`
	EngineeringID string          `json:"engineering_id"`
	Result        Result          `json:"result"`
	Evidence      Reference       `json:"evidence"`
	Preparation   Reference       `json:"preparation"`
}
type PreparationInput struct {
	MaterialCheck *MaterialCheck `json:"material_check,omitempty"`
	Evidence      Evidence       `json:"evidence"`
	Bindings      []Binding      `json:"reviewers"`
	Previous      *Preparation   `json:"previous,omitempty"`
	Answers       []Gate         `json:"answers"`
}

const DecisionInstructions = "Use active entries in the unified decisions array when present. Superseded entries are history, not current constraints. For legacy contexts check all live, discussion, and local decisions."

type Handoff struct {
	Contract             string       `json:"contract"`
	Checkout             string       `json:"checkout"`
	BaseSHA              string       `json:"base_sha"`
	HeadSHA              string       `json:"head_sha"`
	Patch                string       `json:"patch"`
	Scope                string       `json:"scope"`
	Decisions            DecisionData `json:"decisions"`
	DecisionInstructions string       `json:"decision_instructions"`
	Rules                string       `json:"rules"`
	PRURL                string       `json:"pr_url"`
	Preparation          Preparation  `json:"preparation"`
	ReviewerAddendum     string       `json:"reviewer_addendum"`
	Evidence             Evidence     `json:"-"`
}

func NewHandoff(e Evidence, p Preparation, addendum string) Handoff {
	return Handoff{Contract: "git-diff-v1", Checkout: e.Checkout, BaseSHA: e.Metadata.Base, HeadSHA: e.Metadata.Head, Patch: p.Sources["patch"].Snapshot, Scope: p.Analysis.WorkerInstructions + "\nPinned scope evidence:\n" + string(rawJSON(e.Scope)), Decisions: e.Decisions.Data, DecisionInstructions: DecisionInstructions, Rules: e.Rules, PRURL: e.Metadata.URL, Preparation: p, ReviewerAddendum: addendum, Evidence: e}
}

type MaterialCheck struct {
	Required  bool     `json:"required"`
	Rationale string   `json:"rationale"`
	Evidence  []string `json:"evidence"`
}
type CompletionCheck struct {
	Verified  bool     `json:"verified"`
	Rationale string   `json:"rationale"`
	Evidence  []string `json:"evidence"`
}
type CompletionAssessment struct {
	Criteria map[string]CompletionCheck `json:"criteria"`
}

// Workers is the model boundary. Implementations must apply retained native skill
// instructions or execute a retained command binding with a git-diff-v1 handoff.
// Calls are child work only; parent status is owned by Workflow.
// A return certifies joined child work or no admission. Implementations must
// wrap ErrWorkerCleanup whenever admitted work may remain after the return.
type Workers interface {
	Prepare(context.Context, PreparationInput, string) ([]byte, error)
	MaterialChoice(context.Context, PreparationInput, Analysis) (MaterialCheck, error)
	Review(context.Context, Binding, Handoff, string) ([]byte, error)
	Completion(context.Context, Preparation, Result) (CompletionAssessment, error)
}

func WorkerIdentity(workers Workers) string {
	if source, ok := workers.(interface{ ExecutionIdentity() string }); ok {
		return source.ExecutionIdentity()
	}
	return ""
}

type Workflow struct {
	Adapter     EngineAdapter
	Collector   Collector
	Workers     Workers
	Artifacts   Artifacts
	Bindings    []Binding
	Rules       string
	Constraints string
	Save        func(context.Context, *Session) error
	Progress    func(context.Context, string) error
	OnComplete  func(context.Context, Reference) error
}

func (w *Workflow) step(ctx context.Context, s *Session, stage string) error {
	s.Stage = stage
	if err := w.Save(ctx, s); err != nil {
		return err
	}
	if w.Progress != nil {
		return w.Progress(ctx, stage)
	}
	return ctx.Err()
}
func (w *Workflow) Run(ctx context.Context, s *Session) (result Result, err error) {
	defer func() {
		if err != nil && !errors.Is(err, ErrWaiting) && !result.Complete && s.Status != "completed" && !(s.Gate != nil && len(s.Gate.Answer) == 0) {
			s.Status = "incomplete"
			if w.Save != nil {
				_ = w.Save(context.WithoutCancel(ctx), s)
			}
		}
	}()
	if len(w.Bindings) == 0 {
		return Result{}, errors.New("PR review requires at least one ordered reviewer; configure reviewers in Code Factory settings before fetching the diff")
	}
	if w.Adapter == nil {
		w.Adapter = NativeAdapter{}
	}
	if err := w.Adapter.Check(ctx); err != nil {
		return Result{}, err
	}
	for _, b := range w.Bindings {
		if err := b.Validate(); err != nil {
			return Result{}, err
		}
	}
	if w.Save == nil || w.Collector == nil || w.Workers == nil {
		return Result{}, errors.New("incomplete review workflow dependencies")
	}
	unlock, err := w.Artifacts.Lock(s.ID)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	if s.Version == 0 {
		s.Version = 2
		s.Implementation = ImplementationIdentity()
		s.WorkerImplementation = WorkerIdentity(w.Workers)
		s.Status = "active"
		s.Reviews = map[string]Reference{}
		s.Preparations = []Reference{}
		s.History = []Reference{}
		s.Results = []Reference{}
		s.Answers = []Gate{}
	}
	if s.Implementation != ImplementationIdentity() || s.WorkerImplementation != WorkerIdentity(w.Workers) {
		return Result{}, errors.New("review action implementation changed; start a fresh review")
	}
	if s.Version != 2 {
		return Result{}, errors.New("legacy review session must use its retained runtime")
	}
	return w.Adapter.Execute(ctx, w, s)
}

// reviewExecution is shared by fixture orchestration and registered SDK actions.
// Durable artifacts remain the authority when an engine checkpoint is reused.
type reviewExecution struct {
	w        *Workflow
	s        *Session
	e        Evidence
	p        Preparation
	result   Result
	failure  error
	executed map[string]bool
}

func newReviewExecution(w *Workflow, s *Session) *reviewExecution {
	return &reviewExecution{w: w, s: s, executed: map[string]bool{}}
}
func (r *reviewExecution) hydrate() error {
	if r.s.Evidence != nil {
		if err := r.w.Artifacts.Load(*r.s.Evidence, &r.e); err != nil {
			return err
		}
	}
	if r.s.Preparation != nil {
		if err := r.w.Artifacts.Load(*r.s.Preparation, &r.p); err != nil {
			return err
		}
	}
	return nil
}
func (w *Workflow) execute(ctx context.Context, s *Session) (Result, error) {
	if s.Status == "completed" {
		return w.resumePublication(ctx, s)
	}
	if s.Gate != nil && len(s.Gate.Answer) == 0 {
		return Result{}, ErrWaiting
	}
	r := newReviewExecution(w, s)
	if err := r.hydrate(); err != nil {
		return Result{}, err
	}
	if err := r.capture(ctx); err != nil {
		return Result{}, err
	}
	if err := r.prepare(ctx); err != nil {
		return Result{}, err
	}
	for _, binding := range w.Bindings {
		if err := r.review(ctx, binding); err != nil {
			r.failure = err
			break
		}
	}
	result, err := r.complete(ctx)
	if err == nil && result.Complete {
		return w.resumePublication(ctx, s)
	}
	return result, err
}
func (r *reviewExecution) capture(ctx context.Context) (err error) {
	w, s := r.w, r.s
	if err = w.step(ctx, s, "capture-evidence"); err != nil {
		return
	}
	e := r.e
	defer func() { r.e = e }()
	if s.Evidence == nil {
		e, err = w.Collector.Capture(ctx, s.Target, w.Artifacts.Root)
		if err != nil {
			return
		}
		e.Rules = w.Rules
		e.Constraints = w.Constraints
		if e.Sources == nil {
			e.Sources = map[string]string{}
		}
		e.Sources["rules/private"] = w.Rules
		e.Sources["user_instructions/launch"] = w.Constraints
		if err = e.ValidateStorage(w.Artifacts.Root); err != nil {
			return
		}
		if err = e.Validate(); err != nil {
			return
		}
		var ref Reference
		ref, err = w.Artifacts.Retain(e)
		if err != nil {
			return
		}
		s.Evidence = &ref
	} else {
		if err = w.Artifacts.Load(*s.Evidence, &e); err != nil {
			return
		}
		if e.Rules != w.Rules || e.Constraints != w.Constraints {
			err = errors.New("review rules or constraints changed; start a fresh review")
			return
		}
	}
	return w.Save(ctx, s)
}
func (r *reviewExecution) guard(ctx context.Context) error {
	w, s, e := r.w, r.s, r.e
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.Validate(); err != nil {
		return err
	}
	if err := w.Collector.ValidateCurrent(ctx, e); err != nil {
		return err
	}
	for _, b := range w.Bindings {
		if err := b.Validate(); err != nil {
			return err
		}
	}
	if s.Preparation != nil {
		if err := r.p.Validate(e, w.Bindings); err != nil {
			return err
		}
	}
	return e.ValidateCheckout(ctx)
}
func (r *reviewExecution) prepare(ctx context.Context) (err error) {
	w, s, e := r.w, r.s, r.e
	defer func() { r.e = e }()
	if err = r.guard(ctx); err != nil {
		return
	}
	if err = w.step(ctx, s, "prepare"); err != nil {
		return
	}
	var prior *Preparation
	if s.Preparation != nil {
		var saved Preparation
		if err = w.Artifacts.Load(*s.Preparation, &saved); err != nil {
			return
		}
		if err = saved.Validate(e, w.Bindings); err != nil {
			return
		}
		prior = &saved
	}
	p := Preparation{}
	defer func() { r.p = p }()
	if prior != nil && prior.Readiness == "ready" && s.Gate == nil {
		p = *prior
	} else {
		input := PreparationInput{Evidence: e, Bindings: w.Bindings, Previous: prior, Answers: s.Answers}
		if s.MaterialCheck != nil {
			var check MaterialCheck
			if err = w.Artifacts.Load(*s.MaterialCheck, &check); err != nil {
				return
			}
			input.MaterialCheck = &check
		}
		correction := ""
		for attempt := 0; attempt < 2; attempt++ {
			var b []byte
			b, err = w.Workers.Prepare(ctx, input, correction)
			if err != nil {
				return
			}
			var analysis Analysis
			err = DecodeAnalysis(b, &analysis)
			if err == nil {
				p, err = NewPreparation(e, *s.Evidence, w.Bindings, analysis, prior, s.Preparation, s.Answers)
			}
			if err == nil {
				break
			}
			correction = err.Error()
		}
		if err != nil {
			return
		}
		if err = r.guard(ctx); err != nil {
			return
		}
		var check MaterialCheck
		check, err = w.Workers.MaterialChoice(ctx, input, p.Analysis)
		if err != nil {
			return
		}
		if strings.TrimSpace(check.Rationale) == "" {
			err = errors.New("independent material-choice check requires rationale")
			return
		}
		if err = evidenceReferences(check.Evidence, e); err != nil {
			return
		}
		checkRef, retainErr := w.Artifacts.Retain(check)
		if retainErr != nil {
			err = retainErr
			return
		}
		s.MaterialCheck = &checkRef
		needQuestion := p.Readiness == "needs_decision" || check.Required
		hasQuestion := func(a Analysis) bool {
			for _, q := range a.Questions {
				if q.Material && q.Status == "open" {
					return true
				}
			}
			return false
		}
		if needQuestion && !hasQuestion(p.Analysis) {
			if s.MaterialCorrections >= 1 {
				err = errors.New("material preparation correction exhausted; inspect the retained independent check and prepare fresh inputs")
				return
			}
			s.MaterialCorrections++
			input.MaterialCheck = &check
			e.Sources["questions/independent-check"] = string(rawJSON(check))
			input.Evidence = e
			var evidenceRef Reference
			evidenceRef, err = w.Artifacts.Retain(e)
			if err != nil {
				return
			}
			s.Evidence = &evidenceRef
			if err = w.step(ctx, s, "prepare-material-correction"); err != nil {
				return
			}
			correction := "Independent material-choice check requires a structured, answerable material question. Preserve its exact rationale/evidence and all prior questions: " + string(rawJSON(check))
			var corrected []byte
			corrected, err = w.Workers.Prepare(ctx, input, correction)
			if err != nil {
				return
			}
			var analysis Analysis
			if err = DecodeAnalysis(corrected, &analysis); err != nil {
				return
			}
			p, err = NewPreparation(e, *s.Evidence, w.Bindings, analysis, prior, s.Preparation, s.Answers)
			if err != nil {
				return
			}
			if p.Readiness != "needs_decision" || !hasQuestion(p.Analysis) {
				err = errors.New("independent material decision still lacks a validated question after its bounded correction")
				return
			}
			if err = r.guard(ctx); err != nil {
				return
			}
		}

		var ref Reference
		ref, err = w.Artifacts.Retain(p)
		if err != nil {
			return
		}
		s.Preparation = &ref
		s.Preparations = append(s.Preparations, ref)
		if p.Readiness != "ready" {
			if p.Readiness == "sync_pending" {
				err = errors.New("preparation requires Decisions synchronization before review")
				return
			}
			questions := []Question{}
			for _, q := range p.Analysis.Questions {
				if q.Status == "open" && q.Material {
					questions = append(questions, q)
				}
			}
			if len(questions) == 0 {
				err = errors.New("material proposal needs an explicit decision question")
				return
			}
			s.Gate = &Gate{ID: Digest([]any{s.ID, p.ID, len(s.Answers)}), EvidenceID: p.EngineeringID, Questions: questions}
			s.Status = "waiting"
			if err = w.step(ctx, s, "material-decision"); err != nil {
				return
			}
			err = ErrWaiting
			return
		}
		s.Gate = nil
	}
	identity := SessionIdentity{e.Metadata.URL, e.Metadata.Head, e.Metadata.Base, p.EngineeringID, e.Metadata.Tree, p.EngineeringID, Digest(e.Scope), Digest(e.Rules), p.ReviewerOrder}
	if s.Identity != nil && Digest(*s.Identity) != Digest(identity) {
		err = errors.New("engineering inputs or reviewer order changed; start a fresh review")
		return
	}
	s.Identity = &identity
	r.p = p
	s.Status = "active"
	if err = w.step(ctx, s, "ordered-reviewers"); err != nil {
		return
	}
	return nil
}
func (r *reviewExecution) review(ctx context.Context, binding Binding) (err error) {
	w, s, e, p := r.w, r.s, r.e, r.p
	identity := *s.Identity
	result := &r.result
	record := func(b Binding, status string, response Response, reason string) error {
		cp := Checkpoint{1, s.ID, identity, b.Reviewer.ID, status, response, reason}
		ref, er := w.Artifacts.Retain(cp)
		if er != nil {
			return er
		}
		s.Reviews[b.Reviewer.ID] = ref
		s.History = append(s.History, ref)
		return w.Save(context.WithoutCancel(ctx), s)
	}
	name := binding.Reviewer.ID
	if err = r.guard(ctx); err != nil {
		result.GuardFailure = true
		return err
	}
	if ref, ok := s.Reviews[name]; ok {
		var cp Checkpoint
		if err = w.Artifacts.Load(ref, &cp); err != nil {
			result.GuardFailure = true
			return err
		}
		if cp.Version != 1 || cp.SessionID != s.ID || cp.Reviewer != name || Digest(cp.Identity) != Digest(identity) {
			err = errors.New("reviewer checkpoint identity changed")
			result.GuardFailure = true
			return err
		}
		if cp.Status == "completed" {
			var validated Response
			validated, err = ValidateResponse(rawJSON(cp.Result), e.Patch)
			if err != nil || validated.Status != "" {
				err = errors.New("invalid completed reviewer checkpoint")
				result.GuardFailure = true
				return err
			}
			return nil
		}
	}
	if err = record(binding, "running", Response{}, ""); err != nil {
		result.GuardFailure = true
		return err
	}
	var response Response
	var reviewErr error
	correction := ""
	for attempt := 0; attempt < 2; attempt++ {
		var b []byte
		reviewEvidence := cloneJSON(e)
		reviewEvidence.Decisions = preparedDecisions(e, p.Answers)
		b, reviewErr = w.Workers.Review(ctx, binding, NewHandoff(reviewEvidence, p, p.Analysis.ReviewerAddenda[name]), correction)
		if errors.Is(reviewErr, ErrWorkerCleanup) {
			return reviewErr
		}
		if reviewErr != nil {
			break
		}
		response, reviewErr = ValidateResponse(b, e.Patch)
		if reviewErr == nil {
			break
		}
		previous := b
		if len(previous) > 12000 {
			previous = previous[:12000]
		}
		correction = "Validator error: " + reviewErr.Error() + "\nPrevious response: " + string(previous)
	}
	if err = r.guard(ctx); err != nil {
		result.GuardFailure = true
		return err
	}
	if reviewErr != nil {
		if err = record(binding, "failed", Response{}, reviewErr.Error()); err != nil {
			result.GuardFailure = true
			return err
		}
		return nil
	}
	switch response.Status {
	case "tokens_exhausted":
		err = record(binding, "exhausted", response, response.Message)
	case "incomplete":
		err = record(binding, "incomplete", response, response.Message)
	default:
		r.executed[name] = true
		err = record(binding, "completed", response, "")
	}
	if err != nil {
		result.GuardFailure = true
		return err
	}
	return nil
}
func (r *reviewExecution) aggregate() (Result, error) {
	w, s, e := r.w, r.s, r.e
	result := Result{Findings: []AggregateFinding{}, Executed: []string{}, Reused: []string{}, Skipped: []ReviewerReason{}}
	appendFindings := func(response Response, name string) {
		for _, f := range response.Findings {
			found := false
			for i := range result.Findings {
				if result.Findings[i].Finding == f {
					result.Findings[i].Reviewers = append(result.Findings[i].Reviewers, name)
					found = true
					break
				}
			}
			if !found {
				result.Findings = append(result.Findings, AggregateFinding{f, []string{name}})
			}
		}
	}

	for _, b := range w.Bindings {
		ref, ok := s.Reviews[b.Reviewer.ID]
		if !ok {
			continue
		}
		var cp Checkpoint
		if err := w.Artifacts.Load(ref, &cp); err != nil {
			return result, err
		}
		if cp.Version != 1 || cp.SessionID != s.ID || cp.Reviewer != b.Reviewer.ID || s.Identity == nil || Digest(cp.Identity) != Digest(*s.Identity) {
			return result, errors.New("reviewer checkpoint identity changed")
		}
		switch cp.Status {
		case "completed":
			v, err := ValidateResponse(rawJSON(cp.Result), e.Patch)
			if err != nil || v.Status != "" {
				return result, errors.New("invalid completed reviewer checkpoint")
			}
			appendFindings(cp.Result, b.Reviewer.ID)
			if r.executed[b.Reviewer.ID] {
				result.Executed = append(result.Executed, b.Reviewer.ID)
			} else {
				result.Reused = append(result.Reused, b.Reviewer.ID)
			}
		case "exhausted":
			result.Skipped = append(result.Skipped, ReviewerReason{b.Reviewer.ID, cp.Reason})
		default:
			result.Failed = append(result.Failed, ReviewerFailure{b.Reviewer.ID, cp.Status, cp.Reason})
		}
	}
	if len(result.Executed)+len(result.Reused)+len(result.Skipped)+len(result.Failed) != len(w.Bindings) && r.failure == nil {
		return result, errors.New("configured reviewer accounting is incomplete")
	}
	return result, nil
}
func (r *reviewExecution) complete(ctx context.Context) (result Result, err error) {
	w, s, e, p := r.w, r.s, r.e, r.p
	identity := *s.Identity
	result, err = r.aggregate()
	if err == nil {
		err = r.failure
	}
	if err != nil {
		result.GuardFailure = true
	}
	if err == nil {
		err = r.guard(ctx)
		if err != nil {
			result.GuardFailure = true
		}
	}
	assessmentReady := false
	if err != nil {
		result.Reason = err.Error()
		result.Outcome = "guard_failed"
	} else if len(result.Failed) > 0 {
		result.Outcome = "provider_failed"
		result.Reason = result.Failed[0].Reason
	} else if len(result.Executed)+len(result.Reused) == 0 {
		result.Outcome = "usage_exhausted"
		result.Reason = "all configured reviewers exhausted their usage limits"
	} else {
		assessmentReady = true
	}
	sort.SliceStable(result.Findings, func(i, j int) bool {
		a, b := result.Findings[i], result.Findings[j]
		rank := map[string]int{"Major": 0, "Minor": 1, "Nit": 2}
		if a.Severity != b.Severity {
			return rank[a.Severity] < rank[b.Severity]
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	if assessmentReady {
		if err = w.step(ctx, s, "validate-and-aggregate"); err != nil {
			return
		}
		var assessment CompletionAssessment
		assessment, err = w.Workers.Completion(ctx, p, result)
		if err == nil {
			for _, key := range []string{"scope", "remediation_depth", "placement", "test_sufficiency", "cumulative_complexity"} {
				c, ok := assessment.Criteria[key]
				if !ok || !c.Verified || strings.TrimSpace(c.Rationale) == "" {
					err = fmt.Errorf("engineering completion criterion %s remains unverified", key)
					break
				}
				if err = evidenceReferences(c.Evidence, e); err != nil {
					break
				}
			}
		}
		if err == nil {
			err = r.guard(ctx)
			if err != nil {
				result.GuardFailure = true
			}
		}
		if err == nil {
			_, err = w.Artifacts.Retain(assessment)
		}
		if err != nil {
			assessmentReady = false
			result.Outcome = "provider_failed"
			if result.GuardFailure {
				result.Outcome = "guard_failed"
			}
			result.Reason = err.Error()
		}
	}
	retainedResult := result
	if assessmentReady {
		retainedResult.Complete = true
		retainedResult.Outcome = "completed"
	}
	artifact := Artifact{3, s.ID, identity, e.Metadata.URL, e.Metadata.Head, e.Metadata.Tree, p.EngineeringID, p.ID, p.EngineeringID, retainedResult, *s.Evidence, *s.Preparation}
	ref, retainErr := w.Artifacts.Retain(artifact)
	if retainErr != nil {
		return result, retainErr
	}
	previousResults := s.Results
	s.Results = append(s.Results, ref)
	s.Status = "incomplete"
	if retainedResult.Complete {
		s.Status = "completed"
	}
	s.Stage = "evaluate-posting-policy"
	if saveErr := w.Save(context.WithoutCancel(ctx), s); saveErr != nil {
		s.Results = previousResults
		s.Status = "incomplete"
		return result, saveErr
	}
	// Completion belongs to the durably bound artifact, not reviewer readiness.
	result = retainedResult
	if w.Progress != nil {
		if progressErr := w.Progress(context.WithoutCancel(ctx), s.Stage); progressErr != nil {
			return result, progressErr
		}
	}
	return result, err
}
func (w *Workflow) resumePublication(ctx context.Context, s *Session) (Result, error) {
	if len(s.Results) == 0 || s.Identity == nil {
		return Result{}, errors.New("completed session has no retained result")
	}
	ref := s.Results[len(s.Results)-1]
	a, e, err := (Publisher{Artifacts: w.Artifacts}).ValidateArtifact(ref, s.Target)
	if err != nil {
		return Result{}, err
	}
	if a.SessionID != s.ID || Digest(a.Identity) != Digest(*s.Identity) || !a.Result.Complete {
		return a.Result, errors.New("completed session result identity changed")
	}
	var p Preparation
	if err := w.Artifacts.Load(a.Preparation, &p); err != nil {
		return a.Result, err
	}
	if err := p.Validate(e, w.Bindings); err != nil {
		return a.Result, err
	}
	if e.Rules != w.Rules || e.Constraints != w.Constraints {
		return a.Result, errors.New("governing review instructions changed")
	}
	if err := w.Collector.ValidateCurrent(ctx, e); err != nil {
		return a.Result, err
	}
	if err := e.ValidateCheckout(ctx); err != nil {
		return a.Result, err
	}
	// A finished session can outlive a lost parent finalization. Recover its
	// validated result without reopening posting policy or publication.
	if s.Stage == "finished" {
		return a.Result, nil
	}
	if err := w.step(ctx, s, "evaluate-posting-policy"); err != nil {
		return a.Result, err
	}
	if w.OnComplete != nil {
		if err := w.step(ctx, s, "publish-and-reconcile"); err != nil {
			return a.Result, err
		}
		if err := w.OnComplete(ctx, ref); err != nil {
			return a.Result, err
		}
	}
	return a.Result, w.step(ctx, s, "finished")
}

// AcceptAnswer is called under the application's durable run lock. The caller
// saves this session and attention resolution in one transaction before waking it.
func AcceptAnswer(s *Session, gateID, evidenceID string, answer json.RawMessage) error {
	if s.Status != "waiting" || s.Gate == nil || s.Gate.ID != gateID || s.Gate.EvidenceID != evidenceID || len(s.Gate.Answer) != 0 {
		return errors.New("stale or duplicate review decision answer")
	}
	var values map[string]string
	if err := StrictDecode(answer, &values); err != nil {
		return err
	}
	if len(values) != len(s.Gate.Questions) {
		return errors.New("answer every pending material decision")
	}
	for _, q := range s.Gate.Questions {
		if strings.TrimSpace(values[q.ID]) == "" {
			return errors.New("missing material decision answer")
		}
	}
	s.Gate.AnswerID = Digest([]any{s.ID, gateID, evidenceID, json.RawMessage(answer)})
	s.Gate.Answer = append(json.RawMessage{}, answer...)
	s.Answers = append(s.Answers, *s.Gate)
	s.Status = "active"
	s.Stage = "prepare"
	return nil
}
