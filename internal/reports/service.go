package reports

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"codefactory/internal/store"
)

type Engine interface {
	Prepare(context.Context, Report, *Runtime) error
	Start(context.Context, Runtime) (WorkflowState, error)
	Status(context.Context, Runtime, string) (WorkflowState, error)
	Resume(context.Context, Runtime, string) (WorkflowState, error)
	Complete(context.Context, Runtime) error
}
type GitHub interface {
	PrepareContext(context.Context, Submission) (investigationContext, error)
	Validate(context.Context, Findings) error
	FindMarker(context.Context, string, string) (*Issue, error)
	ReadIssue(context.Context, string, int) (Issue, error)
	Create(context.Context, Findings, string) (Issue, error)
	Track(context.Context, Issue, string) ([]Fix, []Release, error)
}
type Service struct {
	Store          *store.Store
	Vault          *store.Vault
	Dir            string
	Engine         Engine
	GitHub         GitHub
	PublishCommand func(context.Context, string) error
	mu             sync.Mutex // one investigation/publication owner, including manual resume and refresh
	captureMu      sync.Mutex
}

func newID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Service) Get(ctx context.Context, id string) (Report, error) {
	var r Report
	e := s.Store.Get(ctx, "issue-report", id, &r)
	return r, e
}
func validText(v string) bool { return strings.TrimSpace(v) != "" }
func (s *Service) Submit(ctx context.Context, in Submission) (Report, error) {
	if err := s.requirePrivateReportDir(ctx); err != nil {
		return Report{}, err
	}
	if !validText(in.IdempotencyKey) || len(in.IdempotencyKey) > 200 || in.ObservedAt.IsZero() || !validText(in.Surface) || !validText(in.Expected) || !validText(in.Actual) {
		return Report{}, fmt.Errorf("idempotencyKey, observedAt, surface, expected and actual are required")
	}
	if len(in.Steps) == 0 && !validText(in.ReproductionUnknown) {
		return Report{}, fmt.Errorf("steps or reproductionUnknown explanation required")
	}
	for _, step := range in.Steps {
		if !validText(step) {
			return Report{}, fmt.Errorf("reproduction steps must be nonempty")
		}
	}
	if in.EventAfter < 0 || in.EventThrough < 0 || (in.EventThrough > 0 && in.EventThrough < in.EventAfter) {
		return Report{}, fmt.Errorf("invalid event range")
	}
	b, _ := json.Marshal(in)
	if len(b) > 256<<10 || len(in.RunIDs) > 20 || len(in.ItemIDs) > 20 {
		return Report{}, fmt.Errorf("observation exceeds evidence limits")
	}
	digest, key := hash(in), hash(in.IdempotencyKey)
	// Redaction precedes persistence; hashing the original preserves conflict detection.
	in.IdempotencyKey = ""
	if e := s.sanitize(ctx, &in); e != nil {
		return Report{}, e
	}
	now := time.Now().UTC()
	r := Report{ID: newID(), Observation: in, Processing: "queued", Created: now, Updated: now, Evidence: []Evidence{}, Additions: []string{}, Versions: map[string]string{}, Fixes: []Fix{}, Releases: []Release{}, History: []Change{{"queued", now, "Observation retained; investigation queued"}}}
	created := false
	e := s.Store.Write(ctx, func(t *store.Tx) error {
		var old struct {
			ID     string
			Digest string
		}
		e := t.Get("report-key", key, &old)
		if e == nil {
			if old.Digest != digest {
				return fmt.Errorf("idempotency key conflicts with an earlier observation")
			}
			return t.Get("issue-report", old.ID, &r)
		}
		if !errors.Is(e, store.ErrNotFound) {
			return e
		}
		if e = t.Put("report-key", key, struct {
			ID     string
			Digest string
		}{r.ID, digest}); e != nil {
			return e
		}
		created = true
		return save(t, r)
	})
	if e == nil && created {
		// Snapshot local records before investigation capacity can delay this report.
		// The submission path never probes a provider or starts a model.
		if err := s.capture(ctx, &r); err != nil {
			_ = s.state(ctx, r.ID, "blocked", "Runtime evidence capture failed; restore the private data directory and resume")
		}
		if current, err := s.Get(ctx, r.ID); err == nil {
			r = current
		}
	}
	return r, e
}
func save(t *store.Tx, r Report) error {
	if e := t.Put("issue-report", r.ID, r); e != nil {
		return e
	}
	return t.Event("report.changed", r.ID, map[string]string{"id": r.ID, "processing": r.Processing})
}
func (s *Service) update(ctx context.Context, id string, f func(*Report) error) (Report, error) {
	var r Report
	e := s.Store.Write(ctx, func(t *store.Tx) error {
		if e := t.Get("issue-report", id, &r); e != nil {
			return e
		}
		if e := f(&r); e != nil {
			return e
		}
		r.Updated = time.Now().UTC()
		return save(t, r)
	})
	return r, e
}
func transition(r *Report, status, message string) {
	r.Processing = status
	r.Message = message
	r.History = append(r.History, Change{status, time.Now().UTC(), message})
}
func (s *Service) state(ctx context.Context, id, status, message string) error {
	_, e := s.update(ctx, id, func(r *Report) error { transition(r, status, message); return nil })
	return e
}
func (s *Service) runtime(ctx context.Context, id string) (Runtime, error) {
	var r Runtime
	e := s.Store.Get(ctx, "report-runtime", id, &r)
	if errors.Is(e, store.ErrNotFound) {
		e = nil
	}
	return r, e
}
func (s *Service) putRuntime(ctx context.Context, id string, r Runtime) error {
	return s.Store.Put(ctx, "report-runtime", id, r, "")
}
func (s *Service) List(ctx context.Context, in ListInput) (Page, error) {
	if in.Limit < 0 || in.Limit > 100 {
		return Page{}, fmt.Errorf("limit must be 1–100")
	}
	if in.Limit == 0 {
		in.Limit = 30
	}
	rows, e := store.List[Report](ctx, s.Store, "issue-report")
	if e != nil {
		return Page{}, e
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	p := Page{Items: []Report{}}
	for _, r := range rows {
		if r.ID <= in.Cursor || in.Status != "" && r.Processing != in.Status || in.Component != "" && r.Component != in.Component {
			continue
		}
		if in.Version != "" && r.Observation.PluginVersion != in.Version && r.InstalledVersion != in.Version {
			match := false
			for _, v := range r.Versions {
				match = match || v == in.Version
			}
			for _, v := range r.Releases {
				match = match || v.Version == in.Version || v.ComponentVersion == in.Version
			}
			if !match {
				continue
			}
		}
		text := r.ID + " " + r.Observation.Surface + " " + r.Observation.Actual + " " + r.Message
		if r.Findings != nil {
			text += " " + r.Findings.Summary
		}
		if in.Search != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(in.Search)) {
			continue
		}
		if len(p.Items) == in.Limit {
			p.NextCursor = p.Items[len(p.Items)-1].ID
			break
		}
		p.Items = append(p.Items, r)
	}
	return p, nil
}
func (s *Service) AddEvidence(ctx context.Context, in EvidenceInput) (Report, error) {
	if err := s.requirePrivateReportDir(ctx); err != nil {
		return Report{}, err
	}
	if !validText(in.Text) || !validText(in.IdempotencyKey) || len(in.Text) > 128<<10 {
		return Report{}, fmt.Errorf("nonempty text and idempotencyKey required; evidence limit 128 KiB")
	}
	digest := hash(in.Text)
	if e := s.sanitize(ctx, &in.Text); e != nil {
		return Report{}, e
	}
	var out Report
	e := s.Store.Write(ctx, func(t *store.Tx) error {
		if e := t.Get("issue-report", in.ID, &out); e != nil {
			return e
		}
		var prior string
		key := hash([]string{in.ID, in.IdempotencyKey})
		e := t.Get("report-evidence-key", key, &prior)
		if e == nil {
			if prior != digest {
				return fmt.Errorf("evidence idempotency key conflict")
			}
			return nil
		}
		if !errors.Is(e, store.ErrNotFound) {
			return e
		}
		if e = t.Put("report-evidence-key", key, digest); e != nil {
			return e
		}
		out.Additions = append(out.Additions, in.Text)
		out.Evidence = append(out.Evidence, Evidence{Origin: "chat:addition", At: time.Now().UTC(), SHA256: hash(in.Text), Bytes: len(in.Text)})
		var rt Runtime
		if e := t.Get("report-runtime", in.ID, &rt); e != nil && !errors.Is(e, store.ErrNotFound) {
			return e
		}
		if out.Processing == "needs_information" || out.Processing == "filing" && !rt.CreateAttempted {
			transition(&out, "queued", "Additional information received")
		}
		out.Updated = time.Now().UTC()
		return save(t, out)
	})
	return out, e
}
func (s *Service) ResumeReport(ctx context.Context, in ResumeInput) (Report, error) {
	if err := s.requirePrivateReportDir(ctx); err != nil {
		return Report{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, e := s.Get(ctx, in.ID)
	if e != nil {
		return r, e
	}
	rt, e := s.runtime(ctx, r.ID)
	if e != nil {
		return r, e
	}
	if r.Processing != "blocked" && r.Processing != "needs_attention" && r.Processing != "needs_information" {
		return r, fmt.Errorf("report is not awaiting recovery")
	}
	if rt.CreateAttempted { // An empty search is not proof of absence after a lost create response.
		if e = s.reconcilePublication(ctx, &r, rt); e != nil {
			return r, e
		}
		return s.Get(ctx, r.ID)
	}
	if rt.Preparing {
		if !in.Reconciled || !validText(in.Note) {
			return r, fmt.Errorf("inspect retained lifecycle records and complete stopped invocations first; reconciled=true and a note are required")
		}
		rt.Preparing = false
		if e = s.putRuntime(ctx, r.ID, rt); e != nil {
			return r, e
		}
	}
	if rt.Admitting || rt.ResumePending {
		recoveryNote := ""
		if rt.ResumeUncertain {
			if !in.Reconciled || !validText(in.Note) {
				return r, fmt.Errorf("inspect retained resume lifecycle records and complete stopped invocations first; reconciled=true and a note are required")
			}
			if e = s.sanitize(ctx, &in.Note); e != nil {
				return r, e
			}
			// The old entry intent remains immutable evidence. An acknowledged
			// recovery owns a new lifecycle attempt, never replaying that entry.
			rt.ResumeID = newID()
			rt.ResumeUncertain = false
			recoveryNote = in.Note
		}
		rt.ControllerErrors = 0
		if e = s.putRuntime(ctx, r.ID, rt); e != nil {
			return r, e
		}
		return s.update(ctx, r.ID, func(r *Report) error {
			transition(r, "investigating", "Retrying the retained idempotent controller request")
			if recoveryNote != "" {
				r.History = append(r.History, Change{"investigating", time.Now().UTC(), "Explicit lifecycle recovery: " + recoveryNote})
			}
			return nil
		})
	}
	if rt.UnusableResult {
		return s.update(ctx, r.ID, func(r *Report) error {
			transition(r, "queued", "Starting a fresh investigation after finalized unusable findings")
			return nil
		})
	}
	if r.WorkflowID != "" && r.Processing == "needs_attention" {
		if state, err := s.Engine.Status(ctx, rt, r.WorkflowID); err == nil && state.Status == "succeeded" {
			return s.update(ctx, r.ID, func(r *Report) error {
				transition(r, "investigating", "Retrying retained result and lifecycle finalization")
				return nil
			})
		}
		if !in.Reconciled || !validText(in.Note) {
			return r, fmt.Errorf("inspect workflow effects first; reconciled=true and a reconciliation note are required")
		}
		if e = s.sanitize(ctx, &in.Note); e != nil {
			return r, e
		}
		rt.ResumePending = true
		rt.ResumeID = newID()
		if e = s.putRuntime(ctx, r.ID, rt); e != nil {
			return r, e
		}
		_, e = s.update(ctx, r.ID, func(r *Report) error { transition(r, "investigating", "Explicit recovery: "+in.Note); return nil })
		if e != nil {
			return r, e
		}
		return s.Get(ctx, r.ID)
	}
	return s.update(ctx, r.ID, func(r *Report) error {
		transition(r, "queued", "Prerequisites or missing information ready for retry")
		return nil
	})
}
func (s *Service) Loop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	_ = s.Refresh(ctx, true)
	for {
		if ctx.Err() != nil {
			return
		}
		_ = s.Step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		_ = s.Refresh(ctx, false)
	}
}

// Step performs one bounded controller operation. All admission inputs are retained
// before the call, so loss of an acknowledgement retries the same engine request.
func (s *Service) Step(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := store.List[Report](ctx, s.Store, "issue-report")
	if e != nil {
		return e
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Created.Before(rows[j].Created) })
	var selected *Report
	for i := range rows {
		if rows[i].Processing == "investigating" || rows[i].Processing == "filing" {
			selected = &rows[i]
			break
		}
	}
	// Unresolved engine effects retain the sole investigation slot.
	if selected == nil {
		for _, r := range rows {
			rt, e := s.runtime(ctx, r.ID)
			if e != nil {
				return e
			}
			// Publication intent is retained only after this generation's workflow
			// succeeds and its lifecycle completes. An uncertain create therefore
			// needs reconciliation, but no longer occupies an investigator slot.
			publicationOnly := rt.CreateAttempted && rt.Intent != nil
			if r.Processing == "needs_attention" && r.WorkflowID != "" && !rt.UnusableResult && !publicationOnly {
				return nil
			}
			if rt.Admitting && r.Processing != "complete" {
				return nil
			}
			if rt.Preparing {
				if r.Processing == "needs_attention" {
					return nil
				}
				return s.state(ctx, r.ID, "needs_attention", "Investigation preparation interrupted; inspect and finalize retained dk-workflows lifecycle records before resuming")
			}
		}
	}
	if selected == nil {
		for i := range rows {
			if rows[i].Processing == "queued" {
				selected = &rows[i]
				break
			}
		}
	}
	if selected == nil {
		return nil
	}
	r := *selected
	rt, e := s.runtime(ctx, r.ID)
	if e != nil {
		return e
	}
	if r.Processing == "filing" {
		if s.PublishCommand != nil {
			if err := s.PublishCommand(ctx, r.ID); err != nil {
				recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				current, e := s.runtime(recoveryCtx, r.ID)
				if e != nil {
					return e
				}
				if current.AdmissionID != rt.AdmissionID {
					return nil
				}
				status := "blocked"
				if current.CreateAttempted {
					status = "needs_attention"
				}
				return s.publicationState(recoveryCtx, r.ID, current, status, "Publication command interrupted; resume to retry or reconcile any claimed issue creation", false)
			}
			return nil
		}
		return s.publish(ctx, r, rt)
	}
	if r.Processing == "queued" {
		if e = s.capture(ctx, &r); e != nil {
			return s.state(ctx, r.ID, "blocked", "Evidence capture failed: "+e.Error())
		}
		r, e = s.Get(ctx, r.ID)
		if e != nil {
			return e
		}
		if e = s.enrichVersions(ctx, &r); e != nil {
			return s.state(ctx, r.ID, "blocked", "Runtime version evidence could not be retained; inspect the private data directory")
		}
		if s.Engine == nil {
			return s.state(ctx, r.ID, "blocked", "Install agent-workflow, authenticated Codex and dk-workflows, then resume")
		}
		rt = Runtime{Generation: rt.Generation + 1, AdditionCount: len(r.Additions), OutputContract: structuredContract}
		if s.GitHub != nil {
			prepared, err := s.GitHub.PrepareContext(ctx, r.Observation)
			if err != nil {
				return s.state(ctx, r.ID, "blocked", "Repository context preparation interrupted; resume after connectivity recovers")
			}
			rt.Context = &prepared
		} else {
			rt.Context = &investigationContext{Version: 1}
		}
		if err := s.sanitize(ctx, rt.Context); err != nil {
			return err
		}
		limitContextBodies(rt.Context)
		refreshContextHashes(rt.Context)
		rt.AdmissionID = r.ID
		if rt.Generation > 1 {
			rt.AdmissionID = fmt.Sprintf("%s-%d", r.ID, rt.Generation)
		}
		rt.Preparing = true
		if e = s.Store.Write(ctx, func(t *store.Tx) error {
			var current Report
			if e := t.Get("issue-report", r.ID, &current); e != nil {
				return e
			}
			current.WorkflowID = ""
			if e := t.Put("report-runtime", r.ID, rt); e != nil {
				return e
			}
			return save(t, current)
		}); e != nil {
			return e
		}
		if e = s.Engine.Prepare(ctx, r, &rt); e != nil { // Prepare can retain a lifecycle token even on failure.
			var uncertain *preparationUncertain
			rt.Preparing = errors.As(e, &uncertain)
			if !rt.Preparing && rt.Token != "" && s.Engine.Complete(ctx, rt) != nil {
				rt.Preparing = true
			}
			if pe := s.putRuntime(ctx, r.ID, rt); pe != nil {
				return pe
			}
			status := "blocked"
			if rt.Preparing {
				status = "needs_attention"
			}
			return s.state(ctx, r.ID, status, e.Error())
		}
		rt.Preparing = false
		rt.Admitting = true
		if e = s.sanitize(ctx, &rt.Notice); e != nil {
			return e
		}
		if e = s.Store.Write(ctx, func(t *store.Tx) error {
			var current Report
			if e := t.Get("issue-report", r.ID, &current); e != nil {
				return e
			}
			transition(&current, "investigating", "Investigation admitted or awaiting admission reconciliation")
			if rt.Notice != "" {
				current.History = append(current.History, Change{"investigating", time.Now().UTC(), rt.Notice})
			}
			if rt.SkillVersion != "" {
				current.Versions["dk-workflows"] = rt.SkillVersion
			}
			if e := t.Put("report-runtime", r.ID, rt); e != nil {
				return e
			}
			return save(t, current)
		}); e != nil {
			return e
		}
	}
	var state WorkflowState
	if rt.ResumePending {
		state, e = s.Engine.Resume(ctx, rt, r.WorkflowID)
	} else if rt.Admitting {
		state, e = s.Engine.Start(ctx, rt)
	} else {
		state, e = s.Engine.Status(ctx, rt, r.WorkflowID)
	}
	if e != nil { // No raw provider output enters the public ledger.
		rt.ControllerErrors++
		var uncertain *preparationUncertain
		if rt.ResumePending && errors.As(e, &uncertain) {
			rt.ResumeUncertain = true
		}
		if err := s.putRuntime(ctx, r.ID, rt); err != nil {
			return err
		}
		if rt.ResumeUncertain {
			return s.state(ctx, r.ID, "needs_attention", "Resume lifecycle entry uncertain; inspect and complete retained invocations, then explicitly reconcile before resuming")
		}
		if rt.ControllerErrors >= 3 {
			return s.state(ctx, r.ID, "needs_attention", "Workflow controller unavailable; restore prerequisites then resume the retained request")
		}
		return nil
	}
	if rt.ControllerErrors != 0 {
		rt.ControllerErrors = 0
		if e = s.putRuntime(ctx, r.ID, rt); e != nil {
			return e
		}
	}
	if rt.Admitting || rt.ResumePending {
		rt.Admitting = false
		rt.ResumePending = false
		if e = s.Store.Write(ctx, func(t *store.Tx) error {
			var current Report
			if e := t.Get("issue-report", r.ID, &current); e != nil {
				return e
			}
			current.WorkflowID = state.ID
			if e := t.Put("report-runtime", r.ID, rt); e != nil {
				return e
			}
			return save(t, current)
		}); e != nil {
			return e
		}
	}
	switch state.Status {
	case "queued", "running":
		return nil
	case "succeeded":
		if e = s.Engine.Complete(ctx, rt); e != nil {
			return s.state(ctx, r.ID, "needs_attention", "Investigation ended; dk-workflows lifecycle completion needs recovery")
		}
		f := state.Outputs.Findings
		var resultErr error
		if rt.OutputContract == structuredContract {
			var draft structuredDraft
			draft, resultErr = decodeStructured(state.Outputs.Structured)
			if resultErr == nil {
				f, resultErr = renderDraft(draft, rt.Context, r.Observation, r.Additions[:min(len(r.Additions), rt.AdditionCount)])
			}
		} else if rt.OutputContract != 0 {
			resultErr = fmt.Errorf("unsupported retained contract")
		}
		if e = s.sanitize(ctx, &f); e != nil {
			return e
		}
		if resultErr == nil {
			if rt.OutputContract == structuredContract {
				resultErr = validateFindings(f)
			} else if !validText(f.Summary) || !slices.Contains([]string{"confirmed", "suspected", "duplicate", "insufficient_information", "explained"}, f.Classification) {
				resultErr = fmt.Errorf("invalid legacy findings")
			}
		}
		return s.Store.Write(ctx, func(t *store.Tx) error {
			var current Report
			if e := t.Get("issue-report", r.ID, &current); e != nil {
				return e
			}
			current.Updated = time.Now().UTC()
			if len(current.Additions) > rt.AdditionCount {
				transition(&current, "queued", "New evidence requires a fresh investigation after lifecycle completion")
				return save(t, current)
			}
			if resultErr != nil {
				rt.UnusableResult = true
				if e := t.Put("report-runtime", r.ID, rt); e != nil {
					return e
				}
				transition(&current, "needs_attention", "Investigation finalized with unusable findings; resume to start a fresh investigation")
				return save(t, current)
			}
			current.Findings = &f
			current.Component = f.Component
			switch f.Classification {
			case "insufficient_information":
				transition(&current, "needs_information", f.MissingInformation)
			case "explained":
				current.Outcome = "explained_behavior"
				transition(&current, "complete", f.Summary)
			case "confirmed", "suspected", "duplicate":
				transition(&current, "filing", "Verifying ownership and draft before publication")
			}
			return save(t, current)
		})
	default:
		// Engine uncertainty requires explicit reconciliation before retrying effects.
		if e = s.Engine.Complete(ctx, rt); e != nil {
			return s.state(ctx, r.ID, "needs_attention", "Workflow stopped; inspect and finalize retained dk-workflows lifecycle records before resuming")
		}
		if rt.OutputContract == structuredContract && state.Status == "failed" && strings.Contains(state.Error, "invalid structured output") {
			rt.UnusableResult = true
			if err := s.putRuntime(ctx, r.ID, rt); err != nil {
				return err
			}
			return s.state(ctx, r.ID, "needs_attention", "Investigation finalized with unusable findings; resume to start a fresh investigation")
		}
		return s.state(ctx, r.ID, "needs_attention", "Workflow stopped ("+state.Status+"); inspect retained engine attempts, reconcile effects and resume explicitly")
	}
}
func marker(id string) string { return "<!-- code-factory-report:" + id + " -->" }

// A publication failure owns only its retained generation and claim phase.
// Another process may have admitted fresh evidence or recovered the issue while
// this process was waiting for GitHub; neither result may be overwritten.
func (s *Service) publicationState(ctx context.Context, id string, expected Runtime, status, message string, rejected bool) error {
	return s.Store.Write(ctx, func(t *store.Tx) error {
		var r Report
		var rt Runtime
		if err := t.Get("issue-report", id, &r); err != nil {
			return err
		}
		if err := t.Get("report-runtime", id, &rt); err != nil {
			return err
		}
		if rt.AdmissionID != expected.AdmissionID || rt.CreateAttempted != expected.CreateAttempted || r.Issue != nil || (r.Processing != "filing" && r.Processing != "needs_attention") {
			return nil
		}
		if !rt.CreateAttempted && len(r.Additions) > rt.AdditionCount {
			return nil
		}
		if rejected {
			rt.CreateAttempted = false
			if err := t.Put("report-runtime", id, rt); err != nil {
				return err
			}
		}
		transition(&r, status, message)
		r.Updated = time.Now().UTC()
		return save(t, r)
	})
}

func (s *Service) reconcilePublication(ctx context.Context, r *Report, rt Runtime) error {
	current, err := s.Get(ctx, r.ID)
	if err != nil {
		return err
	}
	if current.Issue != nil {
		return nil
	}
	if s.GitHub == nil || rt.Intent == nil {
		return s.publicationState(ctx, r.ID, rt, "needs_attention", "Publication intent unavailable; inspect private ledger", false)
	}
	found, e := s.GitHub.FindMarker(ctx, rt.Intent.Repository, marker(r.ID))
	if e != nil {
		return s.publicationState(ctx, r.ID, rt, "needs_attention", "Cannot reconcile GitHub creation; retry reconciliation after connectivity recovers", false)
	}
	if found == nil {
		return s.publicationState(ctx, r.ID, rt, "needs_attention", "GitHub creation remains uncertain; no retry is allowed until the issue identity is recovered", false)
	}
	return s.finishIssue(ctx, r.ID, *found, "filed", rt.AdmissionID)
}
func (s *Service) finishIssue(ctx context.Context, id string, issue Issue, outcome, admissionID string) error {
	return s.Store.Write(ctx, func(t *store.Tx) error {
		var r Report
		var rt Runtime
		if e := t.Get("issue-report", id, &r); e != nil {
			return e
		}
		if e := t.Get("report-runtime", id, &rt); e != nil && !errors.Is(e, store.ErrNotFound) {
			return e
		}
		if rt.AdmissionID != admissionID && rt.CreateAttempted {
			return nil // A newer claimed effect owns issue reconciliation.
		}
		if r.Issue != nil && (rt.AdmissionID != admissionID || r.Processing != "filing" || *r.Issue != issue || r.Outcome != outcome) {
			return nil // An established identity and outcome belong to current work.
		}
		r.Issue = &issue
		if rt.AdmissionID == admissionID || r.Outcome == "" {
			r.Outcome = outcome
		}
		r.Updated = time.Now().UTC()
		if rt.AdmissionID != admissionID {
			return save(t, r) // A newer generation owns its processing state.
		}
		if !rt.CreateAttempted && len(r.Additions) > rt.AdditionCount {
			transition(&r, "queued", "Known issue retained; new evidence requires a fresh investigation")
		} else {
			transition(&r, "complete", "GitHub issue associated with this observation")
		}
		return save(t, r)
	})
}
func (s *Service) publish(ctx context.Context, r Report, rt Runtime) error {
	if batch, ok := s.GitHub.(interface {
		BeginOperation(context.Context) context.Context
	}); ok {
		ctx = batch.BeginOperation(ctx)
	}
	if rt.CreateAttempted {
		return s.reconcilePublication(ctx, &r, rt)
	}
	if r.Issue != nil {
		return s.finishIssue(ctx, r.ID, *r.Issue, r.Outcome, rt.AdmissionID)
	}
	if s.GitHub == nil || r.Findings == nil {
		return s.publicationState(ctx, r.ID, rt, "blocked", "GitHub adapter or investigation findings unavailable", false)
	}
	f := *r.Findings
	if e := s.sanitize(ctx, &f); e != nil {
		return e
	}
	if e := s.GitHub.Validate(ctx, f); e != nil {
		return s.publicationState(ctx, r.ID, rt, "blocked", e.Error(), false)
	}
	if f.Classification == "duplicate" {
		if f.DuplicateNumber < 1 {
			return s.publicationState(ctx, r.ID, rt, "blocked", "Duplicate investigation must identify the existing issue", false)
		}
		i, e := s.GitHub.ReadIssue(ctx, f.Repository, f.DuplicateNumber)
		if e != nil {
			return s.publicationState(ctx, r.ID, rt, "blocked", e.Error(), false)
		}
		return s.finishIssue(ctx, r.ID, i, "duplicate", rt.AdmissionID)
	}
	// Similar titles alone do not establish a duplicate. Reuse requires the
	// investigator's explicit duplicate disposition and verified issue identity.
	if !validText(f.Title) || !validText(f.Body) || len(f.Title) > 256 || len(f.Body) > 60000 {
		return s.publicationState(ctx, r.ID, rt, "blocked", "Repository-native verified title and body required", false)
	}
	existing, e := s.GitHub.FindMarker(ctx, f.Repository, marker(r.ID))
	if e != nil {
		return s.publicationState(ctx, r.ID, rt, "blocked", e.Error(), false)
	}
	if existing != nil {
		return s.finishIssue(ctx, r.ID, *existing, "filed", rt.AdmissionID)
	}
	// Durable intent AND attempted flag precede the network mutation. Any subsequent
	// interruption can only search/reconcile; it cannot repeat the POST.
	claimed := false
	stale := false
	var known *Issue
	knownOutcome := ""
	admissionID := rt.AdmissionID
	if e = s.Store.Write(ctx, func(t *store.Tx) error {
		if e := t.Get("report-runtime", r.ID, &rt); e != nil {
			return e
		}
		if rt.AdmissionID != admissionID {
			stale = true
			return nil
		}
		if rt.CreateAttempted {
			return nil
		}
		var current Report
		if e := t.Get("issue-report", r.ID, &current); e != nil {
			return e
		}
		if len(current.Additions) > rt.AdditionCount {
			stale = true
			transition(&current, "queued", "New evidence requires investigation before issue creation")
			current.Updated = time.Now().UTC()
			return save(t, current)
		}
		if current.Issue != nil {
			known, knownOutcome = current.Issue, current.Outcome
			return nil
		}
		rt.Intent = &f
		rt.CreateAttempted = true
		claimed = true
		return t.Put("report-runtime", r.ID, rt)
	}); e != nil {
		return e
	}
	if stale {
		return nil
	}
	if known != nil {
		return s.finishIssue(ctx, r.ID, *known, knownOutcome, admissionID)
	}
	if !claimed {
		return s.reconcilePublication(ctx, &r, rt)
	}
	issue, e := s.GitHub.Create(ctx, f, marker(r.ID))
	if e != nil {
		var rejected *publicationRejected
		if errors.As(e, &rejected) {
			return s.publicationState(ctx, r.ID, rt, "blocked", rejected.Error(), true)
		}
		return s.publicationState(ctx, r.ID, rt, "needs_attention", "GitHub creation result uncertain; reconcile before resuming", false)
	}
	return s.finishIssue(ctx, r.ID, issue, "filed", rt.AdmissionID)
}

// PublishReport is the deterministic post-workflow CLI command. It accepts no
// model-supplied mutation arguments, only a retained and verified ledger draft.
func (s *Service) PublishReport(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, e := s.Get(ctx, id)
	if e != nil {
		return e
	}
	if r.Processing == "complete" {
		return nil
	}
	rt, e := s.runtime(ctx, id)
	if e != nil {
		return e
	}
	if r.Processing != "filing" && !rt.CreateAttempted {
		return fmt.Errorf("report is not ready for publication")
	}
	return s.publish(ctx, r, rt)
}
func (s *Service) Refresh(ctx context.Context, force bool) error {
	if batch, ok := s.GitHub.(interface {
		BeginOperation(context.Context) context.Context
	}); ok {
		ctx = batch.BeginOperation(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := store.List[Report](ctx, s.Store, "issue-report")
	if e != nil {
		return e
	}
	if s.GitHub == nil {
		return nil
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.Issue == nil {
			continue
		}
		now := time.Now().UTC()
		if !now.Before(r.IssueSync.RetryAt) && (force || now.Sub(r.IssueSync.LastAttempt) >= 5*time.Minute) {
			issue, err := s.GitHub.ReadIssue(ctx, r.Issue.Repository, r.Issue.Number)
			_, e = s.update(ctx, r.ID, func(r *Report) error {
				r.IssueSync.LastAttempt = now
				if err != nil {
					syncError(&r.IssueSync, err, now)
				} else {
					r.Issue = &issue
					r.IssueSync.LastSuccess = now
					r.IssueSync.Error = ""
					r.IssueSync.RetryAt = time.Time{}
				}
				return nil
			})
			if e != nil {
				return e
			}
			var retry retryError
			if errors.As(err, &retry) {
				return nil
			}
		}
		if !now.Before(r.ReleaseSync.RetryAt) && (force || now.Sub(r.ReleaseSync.LastAttempt) >= 15*time.Minute) {
			fixes, releases, err := s.GitHub.Track(ctx, *r.Issue, r.Component)
			_, e = s.update(ctx, r.ID, func(r *Report) error {
				r.ReleaseSync.LastAttempt = now
				if err != nil {
					for _, fix := range fixes {
						found := false
						for i := range r.Fixes {
							retained := &r.Fixes[i]
							if retained.Repository == fix.Repository && retained.Component == fix.Component && retained.Commit == fix.Commit {
								if fix.PR != "" {
									retained.PR = fix.PR
								}
								if fix.Version != "" {
									retained.Version = fix.Version
								}
								found = true
								break
							}
						}
						if !found {
							r.Fixes = append(r.Fixes, fix)
						}
					}
					for _, release := range releases {
						found := false
						for _, retained := range r.Releases {
							if retained == release {
								found = true
								break
							}
						}
						if !found {
							r.Releases = append(r.Releases, release)
						}
					}
					syncError(&r.ReleaseSync, err, now)
				} else {
					r.Fixes = fixes
					r.Releases = releases
					r.ReleaseSync.LastSuccess = now
					r.ReleaseSync.Error = ""
					r.ReleaseSync.RetryAt = time.Time{}
				}
				return nil
			})
			if e != nil {
				return e
			}
			var retry retryError
			if errors.As(err, &retry) {
				return nil
			}
		}
	}
	return nil
}

type retryError interface{ RetryTime() time.Time }

func syncError(f *Freshness, err error, now time.Time) {
	f.Error = err.Error()
	f.RetryAt = now.Add(time.Minute)
	var retry retryError
	if errors.As(err, &retry) {
		f.RetryAt = retry.RetryTime()
	}
}
