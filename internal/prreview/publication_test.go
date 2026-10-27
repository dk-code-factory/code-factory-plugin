package prreview

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type memoryOperations struct {
	ops        map[string]Operation
	failStatus string
}

func (m *memoryOperations) LoadOperation(_ context.Context, id string) (Operation, bool, error) {
	op, ok := m.ops[id]
	return cloneJSON(op), ok, nil
}
func (m *memoryOperations) SaveOperation(_ context.Context, op Operation) error {
	if op.Status == m.failStatus {
		return errors.New("durability failure")
	}
	if m.ops == nil {
		m.ops = map[string]Operation{}
	}
	m.ops[op.ID] = cloneJSON(op)
	return nil
}

type publicationRemote struct {
	snapshot         Snapshot
	calls, mutations int
	ambiguous        bool
	lost             bool
	change           func(int, *Snapshot)
}

func (r *publicationRemote) Snapshot(context.Context, string) (Snapshot, error) {
	r.calls++
	if r.change != nil {
		r.change(r.calls, &r.snapshot)
	}
	return cloneJSON(r.snapshot), nil
}
func (r *publicationRemote) Submit(_ context.Context, _ string, p Payload) (string, error) {
	r.mutations++
	input := p.Variables.Input
	review := RemoteReview{ID: "new", URL: "https://github.com/o/r/pull/2#pullrequestreview-new", Author: r.snapshot.Author, Head: input.Head, State: map[string]string{"COMMENT": "COMMENTED", "APPROVE": "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED"}[input.Event], Body: input.Body, Threads: cloneJSON(input.Threads)}
	r.snapshot.Reviews = append(r.snapshot.Reviews, review)
	if r.ambiguous {
		review.ID = "unrelated"
		r.snapshot.Reviews = append(r.snapshot.Reviews, review)
	}
	if r.lost {
		return "", errors.New("connection lost")
	}
	return "new", nil
}
func publicationFixture(t *testing.T, findings bool) (Publisher, Reference, Artifact, Evidence, *publicationRemote, *memoryOperations) {
	t.Helper()
	w, s, workers, c := workflowFixture(t)
	if findings {
		workers.results["one"] = [][]byte{[]byte(`{"findings":[{"severity":"Major","path":"a.go","line":2,"message":"State is lost","fix":"Preserve the state"}]}`)}
	}
	result, err := w.Run(context.Background(), s)
	if err != nil || !result.Complete {
		t.Fatal(err, result)
	}
	ref := s.Results[len(s.Results)-1]
	var a Artifact
	if err = w.Artifacts.Load(ref, &a); err != nil {
		t.Fatal(err)
	}
	remote := &publicationRemote{snapshot: Snapshot{Metadata: c.e.Metadata, Patch: c.e.Patch, Author: "reviewer", Reviews: []RemoteReview{{ID: "draft", Author: "reviewer", Head: c.e.Metadata.Head, State: "PENDING", Threads: []Thread{}}}}}
	store := &memoryOperations{}
	return Publisher{w.Artifacts, remote, store}, ref, a, c.e, remote, store
}
func authorized(context.Context, Evidence) error { return nil }

func TestCurrentEvidenceAllowsOnlyExactDurablePublicationDelta(t *testing.T) {
	for _, change := range []string{"none", "external-review", "body", "head", "own-thread", "ambiguous", "payload", "response-id", "receipt", "unsubmitted"} {
		t.Run(change, func(t *testing.T) {
			p, ref, a, evidence, remote, _ := publicationFixture(t, true)
			evidence.Scope["pr-reviews"] = rawJSON(remote.snapshot.Reviews)
			op, err := p.Post(context.Background(), ref, a.PRURL, PostRequest{}, authorized)
			if err != nil || op.Status != "verified" {
				t.Fatal(op, err)
			}
			live := cloneJSON(evidence)
			reviews := cloneJSON(remote.snapshot.Reviews)
			switch change {
			case "external-review":
				reviews = append(reviews, RemoteReview{ID: "external", Author: "another", Threads: []Thread{}})
			case "body":
				live.Metadata.Body += "Changed governing scope"
			case "head":
				live.Metadata.Head = live.Metadata.Base
			case "own-thread":
				reviews[1].Threads[0].Body += "Edited after posting"
			case "ambiguous":
				duplicate := reviews[1]
				duplicate.ID = "duplicate"
				reviews = append(reviews, duplicate)
			case "payload":
				op.Approved.Variables.Input.Body = "unapproved"
			case "response-id":
				op.ResponseID = "another"
			case "receipt":
				op.Receipt.ID = "another"
			case "unsubmitted":
				op.Status = "prepared"
			}
			live.Scope["pr-reviews"] = rawJSON(reviews)
			collector := GitCollector{Remote: pinnedGitSource{live}}
			if err := collector.ValidateCurrent(context.Background(), evidence); err == nil {
				t.Fatal("ordinary evidence validation ignored a review mutation")
			}
			err = collector.ValidateCurrentAfterPublication(context.Background(), evidence, op)
			if (err == nil) != (change == "none") {
				t.Fatal("publication delta validation", change, err)
			}
		})
	}
}

func TestPublicationVerifiedReceiptAndDuplicatePrevention(t *testing.T) {
	p, ref, a, _, remote, _ := publicationFixture(t, true)
	pending := remote.snapshot.Reviews[0]
	op, err := p.Post(context.Background(), ref, a.PRURL, PostRequest{}, authorized)
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != "verified" || op.Receipt == nil || op.Receipt.Comments != 1 || op.Approved.Variables.Input.Event != "COMMENT" || op.Approved.Variables.Input.Body != "" {
		t.Fatalf("bad receipt or authority %+v", op)
	}
	if !reflect.DeepEqual(pending, remote.snapshot.Reviews[0]) {
		t.Fatal("unrelated pending draft altered")
	}
	again, err := p.Post(context.Background(), ref, a.PRURL, PostRequest{}, authorized)
	if err != nil || again.Status != "verified" || remote.mutations != 1 {
		t.Fatalf("duplicate publication: %+v %v, mutations=%d", again, err, remote.mutations)
	}
}
func TestUncertainPublicationOnlyReconciles(t *testing.T) {
	for _, mode := range []string{"lost-response", "ambiguous", "lost-intent-save"} {
		t.Run(mode, func(t *testing.T) {
			p, ref, a, _, remote, store := publicationFixture(t, true)
			remote.lost = mode == "lost-response"
			remote.ambiguous = mode == "ambiguous"
			if mode == "lost-intent-save" {
				store.failStatus = "uncertain"
			}
			op, err := p.Post(context.Background(), ref, a.PRURL, PostRequest{}, authorized)
			if mode == "lost-intent-save" {
				if err == nil {
					t.Fatal("lost save reported success")
				}
				store.failStatus = ""
			} else if err != nil {
				t.Fatal(err)
			}
			if op.Status != "uncertain" {
				t.Fatal(op.Status)
			}
			op, err = p.Reconcile(context.Background(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := "verified"
			if mode == "ambiguous" {
				expected = "uncertain"
			}
			if op.Status != expected {
				t.Fatalf("%s: %+v", mode, op)
			}
			_, err = p.Post(context.Background(), ref, a.PRURL, PostRequest{}, authorized)
			if err != nil || remote.mutations != 1 {
				t.Fatalf("uncertain operation replayed: %v, %d", err, remote.mutations)
			}
		})
	}
}
func TestPublicationPolicyAndSnapshotChangesBlockMutation(t *testing.T) {
	for _, mode := range []string{"policy", "head", "base", "identity", "reviews", "body", "storage"} {
		t.Run(mode, func(t *testing.T) {
			p, ref, a, _, remote, store := publicationFixture(t, true)
			enabled := true
			remote.change = func(n int, s *Snapshot) {
				if n != 2 {
					return
				}
				switch mode {
				case "policy":
					enabled = false
				case "head":
					s.Metadata.Head = "changed"
				case "base":
					s.Metadata.Base = "changed"
				case "identity":
					s.Author = "other"
				case "reviews":
					s.Reviews = append(s.Reviews, RemoteReview{ID: "other", Threads: []Thread{}})
				case "body":
					s.Metadata.Body = "changed"
				}
			}
			if mode == "storage" {
				store.failStatus = "submitting"
			}
			_, err := p.Post(context.Background(), ref, a.PRURL, PostRequest{}, func(context.Context, Evidence) error {
				if !enabled {
					return errors.New("disabled")
				}
				return nil
			})
			if err == nil || remote.mutations != 0 {
				t.Fatalf("%s: %v, mutations %d", mode, err, remote.mutations)
			}
		})
	}
}
func TestPostingEmptyCommentAndExplicitApproval(t *testing.T) {
	p, ref, a, _, remote, _ := publicationFixture(t, false)
	op, err := p.Post(context.Background(), ref, a.PRURL, PostRequest{}, authorized)
	if err != nil || op.Status != "skipped" || remote.mutations != 0 {
		t.Fatalf("empty COMMENT: %+v %v", op, err)
	}
	op, err = p.Post(context.Background(), ref, a.PRURL, PostRequest{Event: "APPROVE"}, authorized)
	if err != nil || op.Status != "verified" || remote.mutations != 1 {
		t.Fatalf("explicit approval: %+v %v", op, err)
	}
}
func TestPostingTargetMismatchAndIncompleteReview(t *testing.T) {
	p, ref, a, _, remote, _ := publicationFixture(t, true)
	if _, err := p.Post(context.Background(), ref, "https://github.com/o/r/pull/3", PostRequest{}, authorized); err == nil {
		t.Fatal("target mismatch accepted")
	}
	a.Result.Complete = false
	bad, err := p.Artifacts.Retain(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Post(context.Background(), bad, a.PRURL, PostRequest{}, authorized); err == nil {
		t.Fatal("incomplete review accepted")
	}
	if remote.calls != 0 || remote.mutations != 0 {
		t.Fatal("invalid authority touched remote")
	}
}
func TestPostingSidesRangesOmissionsAndApprovalGuard(t *testing.T) {
	_, _, a, e, _, _ := publicationFixture(t, true)
	for _, placement := range []Placement{{0, "a.go", 2, "LEFT", 1, "LEFT"}, {0, "a.go", 3, "RIGHT", 2, "RIGHT"}} {
		payload, omissions, err := BuildPayload(a, e, PostRequest{Placements: []Placement{placement}})
		if err != nil || len(omissions) != 0 || len(payload.Variables.Input.Threads) != 1 {
			t.Fatalf("valid side/range rejected: %+v %v", omissions, err)
		}
	}
	for _, placement := range []Placement{{0, "a.go", 9, "RIGHT", 0, ""}, {0, "a.go", 3, "RIGHT", 1, "LEFT"}, {0, "../a.go", 2, "RIGHT", 0, ""}, {0, "a.go", 4, "LEFT", 2, "LEFT"}} {
		_, omissions, err := BuildPayload(a, e, PostRequest{Placements: []Placement{placement}})
		if err != nil || len(omissions) != 1 {
			t.Fatal("invalid inline finding not reported as omitted", omissions, err)
		}
		if _, _, err = BuildPayload(a, e, PostRequest{Event: "APPROVE", Placements: []Placement{placement}}); err == nil {
			t.Fatal("approval with unpublished finding accepted")
		}
	}
	summary := "Explicitly requested summary"
	payload, _, err := BuildPayload(a, e, PostRequest{Summary: &summary})
	if err != nil || payload.Variables.Input.Body != summary {
		t.Fatal("structured summary lost")
	}
}

func TestUncertainReviewCannotChangePayloadAndSubmitAgain(t *testing.T) {
	p, ref, a, _, remote, _ := publicationFixture(t, true)
	remote.lost = true
	first, err := p.Post(context.Background(), ref, a.PRURL, PostRequest{}, authorized)
	if err != nil || first.Status != "uncertain" {
		t.Fatal(first, err)
	}
	summary := "New explicit summary"
	next, err := p.Post(context.Background(), ref, a.PRURL, PostRequest{Event: "APPROVE", Summary: &summary}, authorized)
	if err != nil || next.ID != first.ID || remote.mutations != 1 || next.Approved.Variables.Input.Event != "COMMENT" {
		t.Fatal("changed uncertain payload submitted again", next, err, remote.mutations)
	}
}
func TestPreparedReviewSnapshotCanRefreshWithoutReplayingMutation(t *testing.T) {
	p, ref, a, _, remote, _ := publicationFixture(t, true)
	remote.change = func(n int, s *Snapshot) {
		if n == 2 {
			s.Reviews = append(s.Reviews, RemoteReview{ID: "other", Author: "someone", Head: a.Head, Threads: []Thread{}})
		}
	}
	first, err := p.Post(context.Background(), ref, a.PRURL, PostRequest{}, authorized)
	if err == nil || first.Status != "prepared" || remote.mutations != 0 {
		t.Fatal(first, err)
	}
	remote.change = nil
	next, err := p.Post(context.Background(), ref, a.PRURL, PostRequest{}, authorized)
	if err != nil || next.Status != "verified" || remote.mutations != 1 || len(next.PriorAttempts) != 1 {
		t.Fatal("unsubmitted snapshot could not refresh", next, err)
	}
	var prior Operation
	if err = p.Artifacts.Load(next.PriorAttempts[0], &prior); err != nil || Digest(prior) != Digest(first) {
		t.Fatal("previous approved intent was not preserved", err)
	}
}
