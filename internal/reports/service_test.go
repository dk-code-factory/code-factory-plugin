package reports

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/store"
)

func fixture(t *testing.T) (*Service, context.Context) {
	t.Helper()
	dir := t.TempDir()
	db, e := store.Open(filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	v, e := store.NewVault(db, dir)
	if e != nil {
		t.Fatal(e)
	}
	return &Service{Store: db, Vault: v, Dir: dir}, context.Background()
}
func observation() Submission {
	return Submission{IdempotencyKey: "host-observation-1", ObservedAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), Surface: "Agents", Expected: "Answer reaches the task", Actual: "Answer returned an error", ReproductionUnknown: "Observed once; exact sequence unknown"}
}
func submit(t *testing.T, s *Service, ctx context.Context) Report {
	t.Helper()
	r, e := s.Submit(ctx, observation())
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func step(t *testing.T, s *Service, ctx context.Context) {
	t.Helper()
	if e := s.Step(ctx); e != nil {
		t.Fatal(e)
	}
}
func get(t *testing.T, s *Service, ctx context.Context, id string) Report {
	t.Helper()
	r, e := s.Get(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

type fakeEngine struct {
	prepareErr                   error
	resumeErr                    error
	completeErr                  error
	startErr                     bool
	status                       string
	findings                     Findings
	starts, resumes, completions int
	requests                     []Runtime
	resumeRequests               []Runtime
	preparedReports              []Report
}

func (e *fakeEngine) Prepare(_ context.Context, report Report, r *Runtime) error {
	e.preparedReports = append(e.preparedReports, report)
	r.Workspace = "private"
	r.Prompt = "immutable input"
	r.Token = "lifecycle"
	return e.prepareErr
}
func (e *fakeEngine) Start(_ context.Context, r Runtime) (WorkflowState, error) {
	e.starts++
	e.requests = append(e.requests, r)
	if e.startErr {
		e.startErr = false
		return WorkflowState{}, errors.New("lost admission acknowledgement")
	}
	return e.result(), nil
}
func (e *fakeEngine) result() WorkflowState {
	s := WorkflowState{ID: "workflow-1", Status: e.status}
	s.Outputs.Findings = e.findings
	return s
}
func (e *fakeEngine) Status(context.Context, Runtime, string) (WorkflowState, error) {
	return e.result(), nil
}
func (e *fakeEngine) Resume(_ context.Context, rt Runtime, _ string) (WorkflowState, error) {
	e.resumes++
	e.resumeRequests = append(e.resumeRequests, rt)
	if e.resumeErr != nil {
		return WorkflowState{}, e.resumeErr
	}
	return e.result(), nil
}
func (e *fakeEngine) Complete(context.Context, Runtime) error {
	e.completions++
	return e.completeErr
}

type fakeGitHub struct {
	createErr                          error
	creates, refreshes, tracks         int
	uncertain                          bool
	found                              *Issue
	validateErr, errorRead, errorTrack error
	issue                              Issue
	fixes                              []Fix
	releases                           []Release
	onValidate, onMarker, onCreate     func()
	created                            []Findings
}

func (g *fakeGitHub) Validate(context.Context, Findings) error {
	if g.onValidate != nil {
		g.onValidate()
	}
	return g.validateErr
}
func (g *fakeGitHub) FindMarker(context.Context, string, string) (*Issue, error) {
	if g.onMarker != nil {
		g.onMarker()
	}
	return g.found, nil
}
func (g *fakeGitHub) ReadIssue(context.Context, string, int) (Issue, error) {
	g.refreshes++
	return g.issue, g.errorRead
}
func (g *fakeGitHub) Create(_ context.Context, f Findings, _ string) (Issue, error) {
	g.creates++
	g.created = append(g.created, f)
	if g.onCreate != nil {
		g.onCreate()
	}
	if g.createErr != nil {
		return Issue{}, g.createErr
	}
	if g.uncertain {
		return Issue{}, errors.New("lost response")
	}
	return g.issue, nil
}
func (g *fakeGitHub) Track(context.Context, Issue, string) ([]Fix, []Release, error) {
	g.tracks++
	return g.fixes, g.releases, g.errorTrack
}
func findings() Findings {
	return Findings{Classification: "suspected", Summary: "Runtime evidence supports a suspected delivery regression", Repository: DefaultRepository, Component: "code-factory", Labels: []string{}, Title: "Answer delivery fails after reconnect", Body: "Expected the answer to reach the task; actual response was an error. Reproduction is unknown."}
}
func setupEngine(s *Service) (*fakeEngine, *fakeGitHub) {
	e := &fakeEngine{status: "succeeded", findings: findings()}
	g := &fakeGitHub{issue: Issue{Repository: DefaultRepository, Number: 17, URL: "https://github.com/" + DefaultRepository + "/issues/17", State: "open"}}
	s.Engine = e
	s.GitHub = g
	return e, g
}
func TestSubmissionValidationConcurrentIdempotencyAndConflicts(t *testing.T) {
	s, ctx := fixture(t)
	for _, change := range []func(*Submission){func(o *Submission) { o.IdempotencyKey = "" }, func(o *Submission) { o.ObservedAt = time.Time{} }, func(o *Submission) { o.Surface = " " }, func(o *Submission) { o.Expected = "" }, func(o *Submission) { o.Actual = "" }, func(o *Submission) { o.ReproductionUnknown = "" }, func(o *Submission) { o.EventAfter = -1 }, func(o *Submission) { o.Steps = []string{" "} }} {
		o := observation()
		change(&o)
		if _, e := s.Submit(ctx, o); e == nil {
			t.Fatal("invalid submission accepted", o)
		}
	}
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.Submit(ctx, observation())
			if e != nil {
				t.Error(e)
				return
			}
			ids <- r.ID
		}()
	}
	wg.Wait()
	close(ids)
	id := ""
	for v := range ids {
		if id != "" && id != v {
			t.Fatal("duplicate report", id, v)
		}
		id = v
	}
	o := observation()
	o.Actual = "different observation"
	if _, e := s.Submit(ctx, o); e == nil {
		t.Fatal("conflicting key accepted")
	}
	p, e := s.List(ctx, ListInput{})
	if e != nil || len(p.Items) != 1 || p.Items[0].Processing != "queued" {
		t.Fatal(p, e)
	}
	events, _ := s.Store.Events(ctx, 0, 100)
	if len(events) != 2 {
		t.Fatal("repeat submissions added journal events", events)
	}
}
func TestEvidenceIsPrivateScopedHashedAndRedacted(t *testing.T) {
	s, ctx := fixture(t)
	if e := s.Vault.Set(ctx, "jira", "private-jira-secret"); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(s.Dir, "owner.token"), []byte("private-owner-token"), 0600); e != nil {
		t.Fatal(e)
	}
	o := observation()
	o.Errors = "private-jira-secret bearer cloud-token ghp_abcdefghijk password=hunter2 private-owner-token"
	o.Errors += "\nCookie: session=unknown-request-cookie; other=unknown-second-cookie\r\nSet-Cookie: auth=unknown-response-cookie; Secure; HttpOnly\r\nretained-line-evidence"
	o.Errors += "\n> Cookie: session=unknown-prefixed-request\n< Set-Cookie: session=unknown-prefixed-response; Secure\n2026-10-25T12:00:00Z [http] Cookie: session=unknown-logged-cookie\nretained-prefixed-line"
	o.ToolEvidence = `{"Authorization": "Basic ZXhhbXBsZTpwdGVzdA==", "Cookie": "session=unknown-json-cookie; other=unknown-json-second", "Set-Cookie": "session=unknown-json-response; Secure", "id_token": "unknown-json-id-token", "headers": {"Cookie": [
"unknown-json-array-cookie", "unknown-json-array-second"], "Set-Cookie": ["unknown-json-array-response"]}, "status": 401}`
	o.ToolEvidence += "\n{'id_token': 'unknown-single-id', 'Cookie': 'session=unknown-single-cookie', 'Set-Cookie': ['unknown-single-response', 'unknown-single-array']}\nretained-single-quote-line"
	o.Errors += "\n{'password': 'unknown-single-password secret-single-suffix', \"password\": \"unknown-double-password secret-double-suffix\"}\nretained-quoted-value-line"
	o.RunIDs = []string{"run-1", "missing-run"}
	if err := s.Store.Put(ctx, "item", "repo#1", domain.Item{ID: "repo#1", Revision: "captured-associated-item-revision"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, run := range []domain.Run{{ID: "run-1", ItemID: "repo#1", Prompt: "relevant private-jira-secret", Base: "immutable-base"}, {ID: "unrelated", Prompt: "unrelated-private-work-must-not-appear"}} {
		if e := s.Store.Put(ctx, "run", run.ID, run, ""); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.Store.Write(ctx, func(t *store.Tx) error {
		return t.Event("run.output", "run-1", map[string]any{"text": "private-jira-secret\nCookie: session=unknown-event-cookie\nretained-event-line", "headers": map[string]any{"Cookie": []string{"unknown-map-cookie", "unknown-map-second"}, "Set-Cookie": "unknown-map-response"}, "id_token": "unknown-map-id-token"})
	}); e != nil {
		t.Fatal(e)
	}
	if err := s.Store.Put(ctx, "repository", "unrelated/private", domain.Repository{ID: "unrelated/private", Error: "unrelated-private-sync-must-not-appear"}, ""); err != nil {
		t.Fatal(err)
	}
	r, e := s.Submit(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	if e := s.Store.Put(ctx, "run", "run-1", domain.Run{ID: "run-1", Base: "later-state"}, ""); e != nil {
		t.Fatal(e)
	}
	if e = s.capture(ctx, &r); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(s.Dir, "issue-reports", r.ID, "evidence.json"))
	if e != nil {
		t.Fatal(e)
	}
	secrets := []string{"private-jira-secret", "cloud-token", "ghp_abcdefghijk", "hunter2", "private-owner-token", "ZXhhbXBsZTpwdGVzdA==", "unknown-request-cookie", "unknown-second-cookie", "unknown-response-cookie", "unknown-json-cookie", "unknown-json-second", "unknown-json-response", "unknown-json-id-token", "unknown-json-array-cookie", "unknown-json-array-second", "unknown-json-array-response", "unknown-event-cookie", "unknown-map-cookie", "unknown-map-second", "unknown-map-response", "unknown-map-id-token", "unrelated-private-work-must-not-appear", "unrelated-private-sync-must-not-appear"}
	secrets = append(secrets, "unknown-prefixed-request", "unknown-prefixed-response", "unknown-logged-cookie")
	secrets = append(secrets, "unknown-single-id", "unknown-single-cookie", "unknown-single-response", "unknown-single-array")
	secrets = append(secrets, "unknown-single-password", "secret-single-suffix", "unknown-double-password", "secret-double-suffix")
	for _, secret := range secrets {
		if strings.Contains(string(b), secret) {
			t.Fatal("private evidence leaked", secret)
		}
	}
	if !strings.Contains(string(b), "retained-line-evidence") || !strings.Contains(string(b), "retained-event-line") || !strings.Contains(string(b), "retained-prefixed-line") || !strings.Contains(string(b), "retained-single-quote-line") || !strings.Contains(string(b), "retained-quoted-value-line") {
		t.Fatal("header redaction swallowed unrelated evidence", string(b))
	}
	if !strings.Contains(string(b), "immutable-base") || !strings.Contains(string(b), "Referenced run unavailable") || !strings.Contains(string(b), "captured-associated-item-revision") {
		t.Fatal("missing scoped evidence or omissions", string(b))
	}
	info, _ := os.Stat(filepath.Join(s.Dir, "issue-reports", r.ID, "evidence.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	r = get(t, s, ctx, r.ID)
	if len(r.Evidence) == 0 || r.Evidence[len(r.Evidence)-1].SHA256 == "" {
		t.Fatal("manifest missing", r)
	}
	events, _ := s.Store.Events(ctx, 0, 100)
	for _, event := range events {
		if event.Kind == "report.changed" && strings.Contains(string(event.Data), "relevant") {
			t.Fatal("full evidence journaled")
		}
	}
	engine, github := setupEngine(s)
	engine.findings.Body = "Cookie: session=unknown-draft-cookie\nSet-Cookie: session=unknown-draft-response; Secure\nid_token=unknown-draft-id-token\nretained-draft-line"
	engine.findings.Body += "\n{'id_token': 'unknown-single-draft-id', 'Cookie': 'session=unknown-single-draft-cookie', 'Set-Cookie': ['unknown-single-draft-response']}"
	engine.findings.Body += "\n{'password': 'unknown-draft-password secret-draft-suffix'}"
	if _, err := s.AddEvidence(ctx, EvidenceInput{ID: r.ID, IdempotencyKey: "redacted-addition", Text: "Cookie: session=unknown-added-cookie\nretained-addition-line"}); err != nil {
		t.Fatal(err)
	}
	step(t, s, ctx)
	// Re-sanitize a retained draft at publication as well as fresh worker output.
	if _, err := s.update(ctx, r.ID, func(r *Report) error { r.Findings.Body = engine.findings.Body; return nil }); err != nil {
		t.Fatal(err)
	}
	step(t, s, ctx)
	if len(github.created) != 1 || strings.Contains(github.created[0].Body, "unknown-") || strings.Contains(github.created[0].Body, "secret-draft-suffix") || !strings.Contains(github.created[0].Body, "retained-draft-line") {
		t.Fatal("unknown draft credentials reached publication", github.created)
	}
	prepared, _ := json.Marshal(engine.preparedReports[0])
	for _, secret := range append(secrets, "unknown-added-cookie") {
		if strings.Contains(string(prepared), secret) {
			t.Fatal("unknown credentials reached investigator input", secret)
		}
	}
}

func TestEvidenceAfterWorkerCompletionRequiresFreshGeneration(t *testing.T) {
	for _, classification := range []string{"suspected", "insufficient_information", "explained"} {
		t.Run(classification, func(t *testing.T) {
			s, ctx := fixture(t)
			engine, github := setupEngine(s)
			engine.status = "running"
			r := submit(t, s, ctx)
			step(t, s, ctx)
			engine.status = "succeeded"
			engine.findings.Classification = classification
			addition := EvidenceInput{ID: r.ID, IdempotencyKey: "worker-missed-this", Text: "Host response supplied after worker completion"}
			if _, err := s.AddEvidence(ctx, addition); err != nil {
				t.Fatal(err)
			}
			step(t, s, ctx)
			if get(t, s, ctx, r.ID).Processing != "queued" || engine.completions != 1 || engine.starts != 1 || github.creates != 0 {
				t.Fatal("stale findings settled before new evidence or lifecycle was not finalized")
			}
			if _, err := s.AddEvidence(ctx, addition); err != nil {
				t.Fatal(err)
			}
			engine.findings.Classification = "explained"
			step(t, s, ctx)
			rt, err := s.runtime(ctx, r.ID)
			if err != nil || rt.Generation != 2 || rt.AdditionCount != 1 || len(engine.preparedReports[1].Additions) != 1 || engine.preparedReports[1].Additions[0] != addition.Text || engine.requests[0].AdmissionID == engine.requests[1].AdmissionID || get(t, s, ctx, r.ID).Processing != "complete" {
				t.Fatal("fresh generation did not investigate idempotent evidence", rt, err)
			}
		})
	}
}

func TestEvidencePublicationClaimBoundary(t *testing.T) {
	for _, phase := range []string{"validate", "marker", "known-marker", "new-generation", "known-marker-new-generation", "claimed", "uncertain-claim"} {
		t.Run(phase, func(t *testing.T) {
			s, ctx := fixture(t)
			engine, github := setupEngine(s)
			r := submit(t, s, ctx)
			step(t, s, ctx)
			inject := func() {
				if _, err := s.AddEvidence(ctx, EvidenceInput{ID: r.ID, IdempotencyKey: "publication-race", Text: "New host evidence while publication was pending"}); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(phase, "new-generation") {
					engine.status = "running"
					other := &Service{Store: s.Store, Vault: s.Vault, Dir: s.Dir, Engine: engine, GitHub: github}
					step(t, other, ctx)
				}
			}
			switch phase {
			case "validate", "new-generation":
				github.onValidate = inject
			case "marker", "known-marker", "known-marker-new-generation":
				github.onMarker = inject
				if strings.HasPrefix(phase, "known-marker") {
					github.found = &github.issue
				}
			case "claimed", "uncertain-claim":
				github.onCreate = inject
				github.uncertain = phase == "uncertain-claim"
			}
			step(t, s, ctx)
			github.onValidate, github.onMarker, github.onCreate = nil, nil, nil
			current := get(t, s, ctx, r.ID)
			if phase == "claimed" || phase == "uncertain-claim" {
				if github.creates != 1 || len(current.Additions) != 1 || engine.starts != 1 {
					t.Fatal("accepted post-claim evidence repeated an external effect", current)
				}
				if phase == "uncertain-claim" {
					if current.Processing != "needs_attention" {
						t.Fatal("uncertain publication lost reconciliation state", current)
					}
					for range 2 {
						if _, err := s.ResumeReport(ctx, ResumeInput{ID: r.ID}); err != nil {
							t.Fatal(err)
						}
					}
					github.found = &github.issue
					if _, err := s.ResumeReport(ctx, ResumeInput{ID: r.ID}); err != nil {
						t.Fatal(err)
					}
				}
				if get(t, s, ctx, r.ID).Processing != "complete" || github.creates != 1 {
					t.Fatal("claimed publication did not settle once")
				}
				return
			}
			wantState := "queued"
			if strings.Contains(phase, "new-generation") {
				wantState = "investigating"
			}
			if current.Processing != wantState || github.creates != 0 || engine.completions != 1 {
				t.Fatal("unclaimed stale draft settled or created an issue", current)
			}
			if strings.HasPrefix(phase, "known-marker") && (current.Issue == nil || current.Issue.Number != 17) {
				t.Fatal("known issue identity was lost while fresh investigation remained pending", current)
			}
			engine.status = "succeeded"
			step(t, s, ctx)
			step(t, s, ctx)
			if get(t, s, ctx, r.ID).Processing != "complete" || len(engine.preparedReports[1].Additions) != 1 {
				t.Fatal("fresh context did not finish investigation")
			}
			wantCreates := 1
			if strings.HasPrefix(phase, "known-marker") {
				wantCreates = 0
			}
			if github.creates != wantCreates {
				t.Fatal("publication did not respect known issue/claim identity", github.creates)
			}
		})
	}
}
func TestWorkflowLostAdmissionDaemonRestartAndUncertainCreation(t *testing.T) {
	s, ctx := fixture(t)
	engine, github := setupEngine(s)
	engine.startErr = true
	r := submit(t, s, ctx)
	step(t, s, ctx)
	if get(t, s, ctx, r.ID).Processing != "investigating" {
		t.Fatal("lost admission discarded")
	}
	// A fresh service instance reads retained admission inputs from the same SQLite ledger.
	restarted := &Service{Store: s.Store, Vault: s.Vault, Dir: s.Dir, Engine: engine, GitHub: github}
	step(t, restarted, ctx)
	if engine.starts != 2 || engine.requests[0].AdmissionID != r.ID || engine.requests[0].Prompt != engine.requests[1].Prompt {
		t.Fatal("lost admission changed request", engine.requests)
	}
	github.uncertain = true
	step(t, restarted, ctx)
	if get(t, s, ctx, r.ID).Processing != "needs_attention" || github.creates != 1 {
		t.Fatal("creation uncertainty lost")
	}
	engine.status = "running"
	o := observation()
	o.IdempotencyKey = "investigate-after-uncertain-publication"
	other, e := restarted.Submit(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	step(t, restarted, ctx)
	if engine.starts != 3 || engine.completions != 1 || get(t, s, ctx, other.ID).Processing != "investigating" || github.creates != 1 {
		t.Fatal("finalized publication uncertainty blocked unrelated investigation or repeated create")
	}
	o.IdempotencyKey = "one-investigator-at-a-time"
	waiting, e := restarted.Submit(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	step(t, restarted, ctx)
	if engine.starts != 3 || get(t, s, ctx, waiting.ID).Processing != "queued" {
		t.Fatal("publication recovery released an active investigator slot")
	}
	for range 3 {
		if _, e := restarted.ResumeReport(ctx, ResumeInput{ID: r.ID}); e != nil {
			t.Fatal(e)
		}
	}
	if github.creates != 1 {
		t.Fatal("ambiguous creation repeated")
	}
	github.found = &github.issue
	if _, e := restarted.ResumeReport(ctx, ResumeInput{ID: r.ID}); e != nil {
		t.Fatal(e)
	}
	result := get(t, s, ctx, r.ID)
	if result.Outcome != "filed" || result.Issue.Number != 17 || result.Processing != "complete" {
		t.Fatal(result)
	}
}
func TestMissingPrerequisitesInformationAndExplicitRecovery(t *testing.T) {
	s, ctx := fixture(t)
	engine, _ := setupEngine(s)
	engine.prepareErr = errors.New("Install agent-workflow explicitly, then resume")
	r := submit(t, s, ctx)
	step(t, s, ctx)
	if get(t, s, ctx, r.ID).Processing != "blocked" {
		t.Fatal("missing prerequisites dropped")
	}
	engine.prepareErr = nil
	engine.findings.Classification = "insufficient_information"
	engine.findings.MissingInformation = "What exact response did Work display?"
	if _, e := s.ResumeReport(ctx, ResumeInput{ID: r.ID}); e != nil {
		t.Fatal(e)
	}
	step(t, s, ctx)
	if get(t, s, ctx, r.ID).Processing != "needs_information" {
		t.Fatal("information request lost")
	}
	in := EvidenceInput{ID: r.ID, IdempotencyKey: "addition-1", Text: "Work showed a stale answer error"}
	r, e := s.AddEvidence(ctx, in)
	if e != nil || r.Processing != "queued" {
		t.Fatal(r, e)
	}
	r, e = s.AddEvidence(ctx, in)
	if e != nil || len(r.Additions) != 1 {
		t.Fatal("evidence retry appended twice", r, e)
	}
	in.Text = "different"
	if _, e = s.AddEvidence(ctx, in); e == nil {
		t.Fatal("evidence key conflict accepted")
	}
	engine.status = "needs_attention"
	step(t, s, ctx)
	if _, e = s.ResumeReport(ctx, ResumeInput{ID: r.ID}); e == nil {
		t.Fatal("uncertain workflow resumed without acknowledgement")
	}
	engine.status = "succeeded"
	engine.findings.Classification = "explained"
	if _, e = s.ResumeReport(ctx, ResumeInput{ID: r.ID, Reconciled: true, Note: "Inspected attempts; no external effects occurred"}); e != nil {
		t.Fatal(e)
	}
	step(t, s, ctx)
	r = get(t, s, ctx, r.ID)
	if r.Outcome != "explained_behavior" || r.Issue != nil {
		t.Fatal(r)
	}
}
func TestDuplicateReuseRoutingBlockAndSingleInvestigation(t *testing.T) {
	s, ctx := fixture(t)
	engine, github := setupEngine(s)
	engine.status = "running"
	r := submit(t, s, ctx)
	o := observation()
	o.IdempotencyKey = "second"
	other, e := s.Submit(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	step(t, s, ctx)
	step(t, s, ctx)
	if engine.starts != 1 || get(t, s, ctx, other.ID).Processing != "queued" {
		t.Fatal("multiple concurrent investigations")
	}
	engine.status = "succeeded"
	engine.findings.Classification = "duplicate"
	engine.findings.DuplicateNumber = 17
	step(t, s, ctx)
	step(t, s, ctx)
	if get(t, s, ctx, r.ID).Outcome != "duplicate" || github.creates != 0 {
		t.Fatal("duplicate created another issue")
	}
	engine.findings.Classification = "suspected"
	github.validateErr = errors.New("destination outside dk-code-factory")
	step(t, s, ctx)
	step(t, s, ctx)
	if get(t, s, ctx, other.ID).Processing != "blocked" || github.creates != 0 {
		t.Fatal("invalid routing published")
	}
}
func TestRefreshSeparatesIssueFixDeliveryAndPreservesSuccessfulState(t *testing.T) {
	s, ctx := fixture(t)
	engine, github := setupEngine(s)
	r := submit(t, s, ctx)
	step(t, s, ctx)
	step(t, s, ctx)
	starts := engine.starts
	github.issue.State = "closed"
	github.issue.ClosureReason = "completed"
	if e := s.Refresh(ctx, true); e != nil {
		t.Fatal(e)
	}
	r = get(t, s, ctx, r.ID)
	if r.Issue.State != "closed" || len(r.Fixes) != 0 || len(r.Releases) != 0 || r.InstalledVersion != "" {
		t.Fatal("closure inferred release", r)
	}
	github.fixes = []Fix{{Repository: DefaultRepository, Commit: strings.Repeat("a", 40)}}
	github.issue.State = "open"
	if e := s.Refresh(ctx, true); e != nil {
		t.Fatal(e)
	}
	r = get(t, s, ctx, r.ID)
	if r.Issue.State != "open" || len(r.Fixes) != 1 || len(r.Releases) != 0 {
		t.Fatal("merge inferred release or reopen lost", r)
	}
	github.releases = []Release{{Repository: DefaultRepository, Version: "1.0.0"}}
	if e := s.Refresh(ctx, true); e != nil {
		t.Fatal(e)
	}
	lastSuccess := get(t, s, ctx, r.ID).ReleaseSync.LastSuccess
	github.fixes = []Fix{{Repository: DefaultRepository, Commit: strings.Repeat("a", 40), Version: "1.0.0"}, {Repository: DefaultRepository, Commit: strings.Repeat("b", 40)}}
	github.releases = []Release{{Repository: DefaultRepository, Version: "1.1.0"}}
	github.errorRead = errors.New("GitHub issue read unavailable")
	github.errorTrack = errors.New("GitHub unavailable")
	if e := s.Refresh(ctx, true); e != nil {
		t.Fatal(e)
	}
	r = get(t, s, ctx, r.ID)
	if r.Issue.State != "open" || len(r.Fixes) != 2 || r.Fixes[0].Version != "1.0.0" || len(r.Releases) != 2 || r.Releases[0].Version != "1.0.0" || r.IssueSync.Error == "" || r.ReleaseSync.Error == "" || !r.ReleaseSync.LastSuccess.Equal(lastSuccess) {
		t.Fatal("verified partial fixes/releases or last successful availability discarded", r)
	}
	before := github.refreshes
	_ = s.Refresh(ctx, true)
	if github.refreshes != before {
		t.Fatal("manual refresh ignored retry deadline")
	}
	if engine.starts != starts {
		t.Fatal("refresh started an agent")
	}
}
func TestRefreshRateLimitStopsOtherOperationsAndReports(t *testing.T) {
	for _, operation := range []string{"issue", "release"} {
		t.Run(operation, func(t *testing.T) {
			s, ctx := fixture(t)
			engine, github := setupEngine(s)
			for _, key := range []string{"first", "second"} {
				o := observation()
				o.IdempotencyKey = key
				if _, err := s.Submit(ctx, o); err != nil {
					t.Fatal(err)
				}
				step(t, s, ctx)
				step(t, s, ctx)
			}
			github.fixes = []Fix{{Repository: DefaultRepository, Commit: strings.Repeat("a", 40)}}
			github.releases = []Release{{Repository: DefaultRepository, Version: "1.0.0"}}
			if err := s.Refresh(ctx, true); err != nil {
				t.Fatal(err)
			}
			page, err := s.List(ctx, ListInput{})
			if err != nil || len(page.Items) != 2 {
				t.Fatal(page, err)
			}
			before := map[string]Report{}
			for _, r := range page.Items {
				before[r.ID] = r
			}
			reads, tracks, starts := github.refreshes, github.tracks, engine.starts
			until := time.Now().Add(time.Hour).UTC()
			if operation == "issue" {
				github.errorRead = &rateLimit{until}
			} else {
				github.errorTrack = &rateLimit{until}
			}
			if err := s.Refresh(ctx, true); err != nil {
				t.Fatal(err)
			}
			wantTracks := tracks
			if operation == "release" {
				wantTracks++
			}
			if github.refreshes != reads+1 || github.tracks != wantTracks || engine.starts != starts {
				t.Fatal("rate limit did not stop remaining operations/reports", github.refreshes, github.tracks)
			}
			limited := 0
			for id, old := range before {
				r := get(t, s, ctx, id)
				freshness := r.IssueSync
				if operation == "release" {
					freshness = r.ReleaseSync
				}
				if freshness.Error != "" {
					limited++
					if !freshness.RetryAt.Equal(until) {
						t.Fatal("triggering operation lost rate-limit deadline", freshness)
					}
				} else if !r.IssueSync.LastAttempt.Equal(old.IssueSync.LastAttempt) || !r.ReleaseSync.LastAttempt.Equal(old.ReleaseSync.LastAttempt) {
					t.Fatal("skipped report freshness changed", r)
				}
				if len(r.Fixes) != 1 || len(r.Releases) != 1 || r.Releases[0].Version != "1.0.0" || !r.ReleaseSync.LastSuccess.Equal(old.ReleaseSync.LastSuccess) {
					t.Fatal("rate limit discarded successful delivery evidence", r)
				}
				if operation == "issue" && (!r.IssueSync.LastSuccess.Equal(old.IssueSync.LastSuccess) || !r.ReleaseSync.LastAttempt.Equal(old.ReleaseSync.LastAttempt)) {
					t.Fatal("issue rate limit changed skipped release freshness", r)
				}
			}
			if limited != 1 {
				t.Fatal("triggering operation freshness was not retained exactly once", limited)
			}
		})
	}
}

func TestLedgerPaginationSearchAndVersionFilters(t *testing.T) {
	s, ctx := fixture(t)
	for i, key := range []string{"a", "b", "c"} {
		o := observation()
		o.IdempotencyKey = key
		o.PluginVersion = key
		if i == 2 {
			o.Surface = "Settings"
		}
		if _, e := s.Submit(ctx, o); e != nil {
			t.Fatal(e)
		}
	}
	page, e := s.List(ctx, ListInput{Limit: 2})
	if e != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatal(page, e)
	}
	next, e := s.List(ctx, ListInput{Limit: 2, Cursor: page.NextCursor})
	if e != nil || len(next.Items) != 1 || next.NextCursor != "" {
		t.Fatal(next, e)
	}
	p, e := s.List(ctx, ListInput{Search: "settings", Version: "c", Status: "queued"})
	if e != nil || len(p.Items) != 1 {
		t.Fatal(p, e)
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "lifecycle") {
		t.Fatal("private runtime exposed")
	}
}

func TestConcurrentPublicationCommandsClaimOneExternalEffect(t *testing.T) {
	s, ctx := fixture(t)
	_, github := setupEngine(s)
	r := submit(t, s, ctx)
	step(t, s, ctx)
	other := &Service{Store: s.Store, Vault: s.Vault, Dir: s.Dir, GitHub: github}
	var wg sync.WaitGroup
	for _, service := range []*Service{s, other} {
		wg.Add(1)
		go func(service *Service) {
			defer wg.Done()
			if e := service.PublishReport(ctx, r.ID); e != nil {
				t.Error(e)
			}
		}(service)
	}
	wg.Wait()
	if github.creates != 1 {
		t.Fatal("publication was repeated", github.creates)
	}
	result := get(t, s, ctx, r.ID)
	if result.Processing != "complete" || result.Issue == nil {
		t.Fatal("concurrent reconciliation overwrote known identity", result)
	}
}
func TestInterruptedPreparationNeedsLifecycleReconciliation(t *testing.T) {
	s, ctx := fixture(t)
	engine, _ := setupEngine(s)
	r := submit(t, s, ctx)
	rt := Runtime{Generation: 1, Preparing: true}
	if e := s.putRuntime(ctx, r.ID, rt); e != nil {
		t.Fatal(e)
	}
	step(t, s, ctx)
	if get(t, s, ctx, r.ID).Processing != "needs_attention" || engine.starts != 0 {
		t.Fatal("uncertain entry was replayed")
	}
	if _, e := s.ResumeReport(ctx, ResumeInput{ID: r.ID}); e == nil {
		t.Fatal("lifecycle uncertainty accepted without reconciliation")
	}
	if _, e := s.ResumeReport(ctx, ResumeInput{ID: r.ID, Reconciled: true, Note: "Inspected retained lifecycle records and completed the stopped invocation"}); e != nil {
		t.Fatal(e)
	}
	step(t, s, ctx)
	if engine.starts != 1 {
		t.Fatal("explicit preparation recovery did not run")
	}
}

func TestPriorGenerationFindingsDoNotReleaseUncertainPreparation(t *testing.T) {
	s, ctx := fixture(t)
	engine, _ := setupEngine(s)
	engine.findings.Classification = "insufficient_information"
	engine.findings.MissingInformation = "What response did the host display?"
	r := submit(t, s, ctx)
	step(t, s, ctx)
	if _, err := s.AddEvidence(ctx, EvidenceInput{ID: r.ID, IdempotencyKey: "next-generation", Text: "The host displayed a connection error"}); err != nil {
		t.Fatal(err)
	}
	engine.prepareErr = &preparationUncertain{}
	step(t, s, ctx)
	rt, err := s.runtime(ctx, r.ID)
	if err != nil || rt.Generation != 2 || !rt.Preparing || rt.Intent != nil || rt.CreateAttempted || get(t, s, ctx, r.ID).Findings == nil {
		t.Fatal("prior findings did not survive into the interrupted generation as expected", rt, err)
	}
	o := observation()
	o.IdempotencyKey = "waiting-after-prior-findings"
	other, err := s.Submit(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	step(t, s, ctx)
	if engine.starts != 1 || get(t, s, ctx, other.ID).Processing != "queued" {
		t.Fatal("stale findings released a generation with uncertain lifecycle entry")
	}
}

func TestUncertainResumeLifecycleNeedsAcknowledgedRecovery(t *testing.T) {
	s, ctx := fixture(t)
	engine, _ := setupEngine(s)
	engine.status = "needs_attention"
	r := submit(t, s, ctx)
	step(t, s, ctx)
	in := ResumeInput{ID: r.ID, Reconciled: true, Note: "Inspected effects and completed the stopped lifecycle invocation"}
	if _, err := s.ResumeReport(ctx, in); err != nil {
		t.Fatal(err)
	}
	engine.resumeErr = &preparationUncertain{}
	step(t, s, ctx)
	rt, err := s.runtime(ctx, r.ID)
	if err != nil || !rt.ResumePending || !rt.ResumeUncertain || get(t, s, ctx, r.ID).Processing != "needs_attention" {
		t.Fatal("uncertain lifecycle entry not retained", rt, err)
	}
	otherObservation := observation()
	otherObservation.IdempotencyKey = "waiting-for-recovery"
	other, err := s.Submit(ctx, otherObservation)
	if err != nil {
		t.Fatal(err)
	}
	// A restarted service must neither replay the lifecycle entry nor launch the
	// queued report while the earlier invocation remains unreconciled.
	restarted := &Service{Store: s.Store, Vault: s.Vault, Dir: s.Dir, Engine: engine, GitHub: s.GitHub}
	step(t, restarted, ctx)
	if engine.resumes != 1 || get(t, s, ctx, other.ID).Processing != "queued" {
		t.Fatal("uncertain entry replayed or sole slot released")
	}
	if _, err := restarted.ResumeReport(ctx, ResumeInput{ID: r.ID}); err == nil {
		t.Fatal("uncertain resume accepted without reconciliation")
	}
	if _, err := restarted.ResumeReport(ctx, in); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.runtime(ctx, r.ID)
	if err != nil || recovered.ResumeID == rt.ResumeID || recovered.ResumeUncertain || recovered.AdmissionID != rt.AdmissionID {
		t.Fatal("acknowledged recovery did not preserve admission and rotate lifecycle attempt", recovered, err)
	}
	engine.resumeErr = nil
	engine.status = "succeeded"
	engine.findings.Classification = "explained"
	step(t, restarted, ctx)
	if get(t, s, ctx, r.ID).Processing != "complete" || engine.resumes != 2 {
		t.Fatal("explicit recovery failed")
	}
	step(t, restarted, ctx)
	if engine.starts != 2 {
		t.Fatal("queued report did not progress after recovery")
	}
}

func TestFinalizedUnusableFindingsReleaseSlotAndAllowFreshInvestigation(t *testing.T) {
	for _, invalid := range []string{"empty summary", "invalid classification"} {
		t.Run(invalid, func(t *testing.T) {
			s, ctx := fixture(t)
			engine, github := setupEngine(s)
			if invalid == "empty summary" {
				engine.findings.Summary = " "
			} else {
				engine.findings.Classification = "unusable"
			}
			r := submit(t, s, ctx)
			engine.completeErr = errors.New("lifecycle finalization needs recovery")
			step(t, s, ctx)
			rt, err := s.runtime(ctx, r.ID)
			if err != nil || rt.UnusableResult || engine.completions != 1 || get(t, s, ctx, r.ID).Processing != "needs_attention" {
				t.Fatal("unfinalized invocation was incorrectly released", rt, err)
			}
			o := observation()
			o.IdempotencyKey = "next-report"
			other, err := s.Submit(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			step(t, s, ctx)
			if engine.starts != 1 || get(t, s, ctx, other.ID).Processing != "queued" {
				t.Fatal("queued report started before lifecycle finalization")
			}
			engine.completeErr = nil
			if _, err := s.ResumeReport(ctx, ResumeInput{ID: r.ID}); err != nil {
				t.Fatal(err)
			}
			step(t, s, ctx)
			rt, err = s.runtime(ctx, r.ID)
			if err != nil || !rt.UnusableResult || engine.completions != 2 {
				t.Fatal("unusable result did not finalize after recovery", rt, err)
			}
			engine.findings = findings()
			engine.findings.Classification = "explained"
			step(t, s, ctx)
			if get(t, s, ctx, other.ID).Processing != "complete" || engine.starts != 2 || github.creates != 0 {
				t.Fatal("finalized unusable report blocked queued work")
			}
			if _, err := s.ResumeReport(ctx, ResumeInput{ID: r.ID}); err != nil {
				t.Fatal(err)
			}
			step(t, s, ctx)
			recovered, err := s.runtime(ctx, r.ID)
			if err != nil || recovered.Generation != rt.Generation+1 || recovered.AdmissionID == rt.AdmissionID || recovered.UnusableResult || engine.resumes != 0 || engine.starts != 3 || get(t, s, ctx, r.ID).Processing != "complete" {
				t.Fatal("fresh investigation replayed immutable unusable result", recovered, err)
			}
		})
	}
}

func TestSimilarTitlesDoNotSilentlyMergeDistinctObservations(t *testing.T) {
	s, ctx := fixture(t)
	_, github := setupEngine(s)
	first := submit(t, s, ctx)
	step(t, s, ctx)
	step(t, s, ctx)
	in := observation()
	in.IdempotencyKey = "distinct-observation"
	in.Actual = "A different failure with a similar title"
	second, e := s.Submit(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	step(t, s, ctx)
	step(t, s, ctx)
	if github.creates != 2 || get(t, s, ctx, first.ID).Outcome != "filed" || get(t, s, ctx, second.ID).Outcome != "filed" {
		t.Fatal("title similarity incorrectly established a duplicate")
	}
}

func TestRejectedCreationCanResumeWithoutTreatingItAsAnUnknownEffect(t *testing.T) {
	s, ctx := fixture(t)
	_, github := setupEngine(s)
	github.createErr = &publicationRejected{Reason: "GitHub rejected issue creation (HTTP 403)"}
	r := submit(t, s, ctx)
	step(t, s, ctx)
	step(t, s, ctx)
	if get(t, s, ctx, r.ID).Processing != "blocked" {
		t.Fatal("known rejection treated as an unknown effect")
	}
	rt, e := s.runtime(ctx, r.ID)
	if e != nil || rt.CreateAttempted {
		t.Fatal("known rejected effect retained an uncertain claim", rt, e)
	}
	github.createErr = nil
	if _, e = s.ResumeReport(ctx, ResumeInput{ID: r.ID}); e != nil {
		t.Fatal(e)
	}
	step(t, s, ctx)
	step(t, s, ctx)
	if get(t, s, ctx, r.ID).Processing != "complete" || github.creates != 2 {
		t.Fatal("rejected creation did not recover")
	}
}
