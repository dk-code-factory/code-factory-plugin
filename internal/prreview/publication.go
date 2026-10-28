package prreview

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

const ReviewMutation = "mutation($input:AddPullRequestReviewInput!){addPullRequestReview(input:$input){pullRequestReview{id fullDatabaseId url state body commit{oid}}}}"

type Thread struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Side      string `json:"side"`
	Body      string `json:"body"`
	StartLine int    `json:"startLine,omitempty"`
	StartSide string `json:"startSide,omitempty"`
}
type ReviewInput struct {
	PRNodeID string   `json:"pullRequestId"`
	Head     string   `json:"commitOID"`
	Event    string   `json:"event"`
	Threads  []Thread `json:"threads"`
	Body     string   `json:"body,omitempty"`
}
type Variables struct {
	Input ReviewInput `json:"input"`
}
type Payload struct {
	Query     string    `json:"query"`
	Variables Variables `json:"variables"`
}
type Placement struct {
	Finding   int    `json:"finding"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Side      string `json:"side"`
	StartLine int    `json:"startLine,omitempty"`
	StartSide string `json:"startSide,omitempty"`
}

// PostRequest is an explicit server operation. Automatic launches never consume
// this structure from model output; they use the fixed default COMMENT payload.
type PostRequest struct {
	Event      string      `json:"event,omitempty"`
	Summary    *string     `json:"summary,omitempty"`
	Placements []Placement `json:"placements,omitempty"`
}
type Omission struct {
	Finding int    `json:"finding"`
	Reason  string `json:"reason"`
}
type RemoteReview struct {
	ID      string   `json:"id"`
	URL     string   `json:"url"`
	Author  string   `json:"author"`
	Head    string   `json:"head"`
	State   string   `json:"state"`
	Body    string   `json:"body"`
	Threads []Thread `json:"threads"`
}
type Snapshot struct {
	Metadata Metadata       `json:"metadata"`
	Patch    string         `json:"patch"`
	Author   string         `json:"author"`
	Reviews  []RemoteReview `json:"reviews"`
}
type Receipt struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Head     string `json:"head"`
	Comments int    `json:"comments"`
}
type Operation struct {
	PriorAttempts []Reference `json:"prior_attempts,omitempty"`
	Version       int         `json:"version"`
	ID            string      `json:"id"`
	Target        string      `json:"target"`
	Artifact      Reference   `json:"artifact"`
	Expected      Snapshot    `json:"expected"`
	Approved      Payload     `json:"approved"`
	PayloadHash   string      `json:"payload_hash"`
	Status        string      `json:"status"`
	Omissions     []Omission  `json:"omissions"`
	Receipt       *Receipt    `json:"receipt,omitempty"`
	ResponseID    string      `json:"response_id,omitempty"`
	Error         string      `json:"error,omitempty"`
}
type PublicationRemote interface {
	Snapshot(context.Context, string) (Snapshot, error)
	Submit(context.Context, string, Payload) (string, error)
}
type OperationStore interface {
	LoadOperation(context.Context, string) (Operation, bool, error)
	SaveOperation(context.Context, Operation) error
}
type Publisher struct {
	Artifacts Artifacts
	Remote    PublicationRemote
	Store     OperationStore
}

func validThread(t Thread, patch Patch) error {
	if !SafePath(t.Path) || strings.TrimSpace(t.Body) == "" || !patch.Lines[Location{t.Path, t.Line, t.Side}] {
		return errors.New("inline finding is not anchored on the reviewed diff")
	}
	if t.StartLine != 0 || t.StartSide != "" {
		if t.StartLine < 1 || t.StartLine > t.Line || t.StartSide != t.Side {
			return errors.New("invalid inline finding range")
		}
		for n := t.StartLine; n <= t.Line; n++ {
			if !patch.Lines[Location{t.Path, n, t.Side}] {
				return errors.New("inline finding range crosses unreviewed lines")
			}
		}
	}
	return nil
}
func BuildPayload(a Artifact, e Evidence, request PostRequest) (Payload, []Omission, error) {
	if !a.Result.Complete {
		return Payload{}, nil, errors.New("incomplete review cannot be published")
	}
	event := request.Event
	if event == "" {
		event = "COMMENT"
	}
	if event != "COMMENT" && event != "APPROVE" && event != "REQUEST_CHANGES" {
		return Payload{}, nil, errors.New("invalid structured review event")
	}
	patch, err := ParsePatch(e.Patch)
	if err != nil {
		return Payload{}, nil, err
	}
	placements := map[int]Placement{}
	for _, p := range request.Placements {
		if _, ok := placements[p.Finding]; ok || p.Finding < 0 || p.Finding >= len(a.Result.Findings) {
			return Payload{}, nil, errors.New("invalid or duplicate finding placement")
		}
		placements[p.Finding] = p
	}
	input := ReviewInput{PRNodeID: e.Metadata.NodeID, Head: a.Head, Event: event, Threads: []Thread{}}
	if request.Summary != nil {
		input.Body = *request.Summary
	}
	omissions := []Omission{}
	for i, f := range a.Result.Findings {
		t := Thread{Path: f.Path, Line: f.Line, Side: "RIGHT", Body: f.Message + "\n\n" + f.Fix}
		if p, ok := placements[i]; ok {
			t.Path = p.Path
			t.Line = p.Line
			t.Side = p.Side
			t.StartLine = p.StartLine
			t.StartSide = p.StartSide
		}
		if err := validThread(t, patch); err != nil {
			omissions = append(omissions, Omission{i, err.Error()})
			continue
		}
		// Commit pages and same-PR commit prose are not reviewed code permalinks.
		lower := strings.ToLower(t.Body)
		if strings.Contains(lower, "/commit/") || strings.Contains(lower, "fixed in commit") {
			omissions = append(omissions, Omission{i, "finding contains an unsupported commit reference"})
			continue
		}
		input.Threads = append(input.Threads, t)
	}
	if event == "APPROVE" && len(omissions) > 0 {
		return Payload{}, omissions, errors.New("approval rejected: actionable findings remain unpublished")
	}
	if event == "REQUEST_CHANGES" && len(input.Threads) == 0 {
		return Payload{}, omissions, errors.New("REQUEST_CHANGES requires actionable inline findings")
	}
	return Payload{ReviewMutation, Variables{input}}, omissions, nil
}
func (p Publisher) ValidateArtifact(ref Reference, target string) (Artifact, Evidence, error) {
	var a Artifact
	var e Evidence
	if err := p.Artifacts.Load(ref, &a); err != nil {
		return a, e, err
	}
	if a.Version != 3 || a.PRURL != target || a.Identity.PRURL != target || a.Head != a.Identity.Head || a.Tree != a.Identity.Tree || a.EngineeringID != a.Identity.EngineeringID || a.Scope != a.Identity.Scope || !a.Result.Complete || a.Result.Outcome != "completed" || a.Result.GuardFailure || len(a.Result.Failed) > 0 || len(a.Result.Executed)+len(a.Result.Reused) == 0 {
		return a, e, errors.New("artifact is incomplete or does not bind the requested target")
	}
	if err := p.Artifacts.Load(a.Evidence, &e); err != nil {
		return a, e, err
	}
	if err := e.Validate(); err != nil {
		return a, e, err
	}
	var prep Preparation
	if err := p.Artifacts.Load(a.Preparation, &prep); err != nil {
		return a, e, err
	}
	if err := prep.Validate(e, prep.Bindings); err != nil {
		return a, e, err
	}
	if prep.ID != a.PreparationID || prep.EngineeringID != a.EngineeringID || e.Metadata.Head != a.Head || e.Metadata.Tree != a.Tree || e.Metadata.Base != a.Identity.Base || Digest(e.Scope) != a.Identity.ScopeText || Digest(e.Rules) != a.Identity.RulesText {
		return a, e, errors.New("artifact evidence identity changed")
	}
	accounted := map[string]bool{}
	for _, id := range append(append([]string{}, a.Result.Executed...), a.Result.Reused...) {
		if accounted[id] {
			return a, e, errors.New("duplicate reviewer accounting")
		}
		accounted[id] = true
	}
	for _, skip := range a.Result.Skipped {
		if accounted[skip.Reviewer] || skip.Reason == "" {
			return a, e, errors.New("invalid skipped reviewer")
		}
		accounted[skip.Reviewer] = true
	}
	if len(accounted) != len(prep.Bindings) {
		return a, e, errors.New("incomplete configured reviewer accounting")
	}
	for _, b := range prep.Bindings {
		if !accounted[b.Reviewer.ID] {
			return a, e, errors.New("missing configured reviewer result")
		}
	}
	return a, e, nil
}

// Post serializes by retained review identity across payload changes, records intent
// before submission and never repeats a submission after intent exists. Caller
// supplies live policy validation.
func (p Publisher) Post(ctx context.Context, ref Reference, target string, request PostRequest, authorize func(context.Context, Evidence) error) (Operation, error) {
	a, e, err := p.ValidateArtifact(ref, target)
	if err != nil {
		return Operation{}, err
	}
	payload, omissions, err := BuildPayload(a, e, request)
	if err != nil {
		return Operation{Omissions: omissions}, err
	}
	if p.Remote == nil || p.Store == nil || authorize == nil {
		return Operation{}, errors.New("publication dependencies unavailable")
	}
	live, err := p.Remote.Snapshot(ctx, target)
	if err != nil {
		return Operation{}, err
	}
	if err = validateSnapshot(live, e); err != nil {
		return Operation{}, err
	}
	if err = authorize(ctx, e); err != nil {
		return Operation{}, err
	}
	op := Operation{Version: 1, Target: target, Artifact: ref, Expected: live, Approved: payload, PayloadHash: Digest(payload), Status: "prepared", Omissions: omissions}
	op.ID = Digest([]any{target, e.Metadata.Head, a.SessionID})
	unlock, err := p.Artifacts.Lock(op.ID)
	if err != nil {
		return op, err
	}
	defer unlock()
	old, exists, err := p.Store.LoadOperation(ctx, op.ID)
	if err != nil {
		return op, err
	}
	if exists {
		if old.Version != 1 || old.Target != target || Digest(old.Approved) != old.PayloadHash {
			return old, errors.New("publication operation identity changed")
		}
		if old.Status == "submitting" || old.Status == "uncertain" {
			return p.reconcile(ctx, old)
		}
		if old.Status == "verified" {
			if old.PayloadHash != op.PayloadHash {
				return old, errors.New("this review was already published with a different payload; prepare a fresh review")
			}
			return old, nil
		}
		if old.Status != "prepared" && old.Status != "skipped" {
			return old, errors.New("unknown publication state")
		}
		if old.PayloadHash == op.PayloadHash && reflect.DeepEqual(old.Expected, live) {
			op = old
			if old.Status == "skipped" {
				return old, nil
			}
		} else {
			prior, err := p.Artifacts.Retain(old)
			if err != nil {
				return old, err
			}
			op.PriorAttempts = append(append([]Reference{}, old.PriorAttempts...), prior)
			if err = p.Store.SaveOperation(ctx, op); err != nil {
				return op, err
			}
		}
	}
	if len(payload.Variables.Input.Threads) == 0 && payload.Variables.Input.Event == "COMMENT" && payload.Variables.Input.Body == "" {
		op.Status = "skipped"
		return op, p.Store.SaveOperation(ctx, op)
	}
	if !exists {
		if err = p.Store.SaveOperation(ctx, op); err != nil {
			return op, err
		}
	}
	// Re-read immediately before mutation, including all reviews and pending drafts.
	before, err := p.Remote.Snapshot(ctx, target)
	if err != nil {
		return op, err
	}
	if err = validateSnapshot(before, e); err != nil {
		return op, err
	}
	if !reflect.DeepEqual(before, op.Expected) {
		return op, errors.New("pre-submit review snapshot or identity changed; inspect the prepared operation")
	}
	if err = authorize(ctx, e); err != nil {
		return op, err
	}
	op.Status = "submitting"
	if err = p.Store.SaveOperation(context.WithoutCancel(ctx), op); err != nil {
		return op, err
	}
	id, submitErr := p.Remote.Submit(ctx, target, op.Approved)
	op.ResponseID = id
	op.Status = "uncertain"
	if submitErr != nil {
		op.Error = "Review submission was not confirmed; reconcile this operation without another mutation."
	}
	if err = p.Store.SaveOperation(context.WithoutCancel(ctx), op); err != nil {
		return op, err
	}
	if submitErr != nil {
		return op, nil
	}
	return p.reconcile(ctx, op)
}
func validateSnapshot(s Snapshot, e Evidence) error {
	if s.Author == "" || s.Reviews == nil {
		return errors.New("incomplete pre-submit snapshot or authenticated identity")
	}
	if s.Metadata.URL != e.Metadata.URL || s.Metadata.NodeID != e.Metadata.NodeID || s.Metadata.Base != e.Metadata.Base || s.Metadata.Head != e.Metadata.Head || s.Metadata.Body != e.Metadata.Body || s.Patch != e.Patch {
		return errors.New("PR target, head, base, body or reviewed diff changed; review again")
	}
	if strings.EqualFold(s.Author, s.Metadata.Author) {
		return errors.New("cannot post a review of your own PR")
	}
	seen := map[string]bool{}
	for _, r := range s.Reviews {
		if r.ID == "" || seen[r.ID] || r.Threads == nil {
			return errors.New("incomplete review snapshot")
		}
		seen[r.ID] = true
	}
	return nil
}
func threadSignatures(ts []Thread) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, Digest(t))
	}
	sort.Strings(out)
	return out
}

func matchingSubmittedReview(op Operation, reviews []RemoteReview) (RemoteReview, bool) {
	existing := map[string]bool{}
	for _, r := range op.Expected.Reviews {
		existing[r.ID] = true
	}
	candidates := []RemoteReview{}
	for _, r := range reviews {
		if !existing[r.ID] && strings.EqualFold(r.Author, op.Expected.Author) && r.Head == op.Expected.Metadata.Head {
			candidates = append(candidates, r)
		}
	}
	state := map[string]string{"COMMENT": "COMMENTED", "APPROVE": "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED"}[op.Approved.Variables.Input.Event]
	input := op.Approved.Variables.Input
	if len(candidates) == 1 {
		r := candidates[0]
		if (op.ResponseID == "" || op.ResponseID == r.ID) && r.State == state && r.Body == input.Body && reflect.DeepEqual(threadSignatures(r.Threads), threadSignatures(input.Threads)) && r.URL != "" {
			if op.Receipt == nil || *op.Receipt == (Receipt{r.ID, r.URL, r.Head, len(r.Threads)}) {
				return r, true
			}
		}
	}
	return RemoteReview{}, false
}

func (p Publisher) reconcile(ctx context.Context, op Operation) (Operation, error) {
	if Digest(op.Approved) != op.PayloadHash {
		return op, errors.New("approved review payload changed")
	}
	snapshot, err := p.Remote.Snapshot(ctx, op.Target)
	if err != nil {
		return op, err
	}
	if snapshot.Metadata.Head != op.Expected.Metadata.Head || snapshot.Metadata.Base != op.Expected.Metadata.Base || snapshot.Metadata.NodeID != op.Expected.Metadata.NodeID || snapshot.Metadata.Body != op.Expected.Metadata.Body || snapshot.Patch != op.Expected.Patch || !strings.EqualFold(snapshot.Author, op.Expected.Author) {
		return op, errors.New("publication evidence or identity changed during reconciliation")
	}
	if r, ok := matchingSubmittedReview(op, snapshot.Reviews); ok {
		op.Status = "verified"
		op.Error = ""
		op.Receipt = &Receipt{r.ID, r.URL, r.Head, len(r.Threads)}
		return op, p.Store.SaveOperation(context.WithoutCancel(ctx), op)
	}
	op.Status = "uncertain"
	op.Error = "No unique complete submitted review match; inspect retained snapshot and reconcile without resubmitting."
	if err = p.Store.SaveOperation(context.WithoutCancel(ctx), op); err != nil {
		return op, err
	}
	return op, nil
}
func (p Publisher) Reconcile(ctx context.Context, id string) (Operation, error) {
	op, ok, err := p.Store.LoadOperation(ctx, id)
	if err != nil {
		return op, err
	}
	if !ok {
		return op, fmt.Errorf("unknown review publication operation")
	}
	unlock, err := p.Artifacts.Lock(op.ID)
	if err != nil {
		return op, err
	}
	defer unlock()
	if op.Status == "verified" || op.Status == "skipped" {
		return op, nil
	}
	if op.Status != "submitting" && op.Status != "uncertain" {
		return op, errors.New("operation has not been submitted")
	}
	return p.reconcile(ctx, op)
}
