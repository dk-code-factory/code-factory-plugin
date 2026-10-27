package reports

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	rawResult                    json.RawMessage
	workflowError                string
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
	s := WorkflowState{ID: "workflow-1", Status: e.status, Error: e.workflowError}
	s.Outputs.Findings = e.findings
	s.Outputs.Structured = mustJSON(draftFixture(e.findings))
	if e.rawResult != nil {
		s.Outputs.Structured = e.rawResult
	}
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
	preparations                       int
	preparedContext                    *investigationContext
	createErr                          error
	creates, refreshes, tracks         int
	uncertain                          bool
	found                              *Issue
	validateErr, errorRead, errorTrack error
	issue                              Issue
	fixes                              []Fix
	releases                           []Release
	onValidate, onMarker, onCreate     func()
	onRead                             func()
	markerErr                          error
	created                            []Findings
}

func (g *fakeGitHub) PrepareContext(context.Context, Submission) (investigationContext, error) {
	g.preparations++
	if g.preparedContext != nil {
		return *g.preparedContext, nil
	}
	return investigationContext{Version: 1, Repositories: []repositoryContext{{Repository: DefaultRepository, Candidates: []duplicateCandidate{{Number: 17}, {Number: 31}}}, {Repository: routes["agent-workflow"]}, {Repository: routes["dk-workflows"]}}}, nil
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
	return g.found, g.markerErr
}
func (g *fakeGitHub) ReadIssue(context.Context, string, int) (Issue, error) {
	g.refreshes++
	if g.onRead != nil {
		g.onRead()
	}
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
	return Findings{Classification: "suspected", Summary: "Runtime evidence supports a suspected delivery regression", Repository: DefaultRepository, Component: "code-factory", SourcePath: "internal/reports/service.go", SourceCommit: strings.Repeat("a", 40), OwnershipReason: "The captured implementation owns answer delivery", Labels: []string{}, Title: "Answer delivery fails after reconnect", Body: "Expected the answer to reach the task; actual response was an error. Reproduction is unknown."}
}
func setupEngine(s *Service) (*fakeEngine, *fakeGitHub) {
	e := &fakeEngine{status: "succeeded", findings: findings()}
	g := &fakeGitHub{issue: Issue{Repository: DefaultRepository, Number: 17, URL: "https://github.com/" + DefaultRepository + "/issues/17", State: "open"}}
	s.Engine = e
	s.GitHub = g
	return e, g
}
func TestPrivateInputsAreRejectedBeforeCheckoutPersistence(t *testing.T) {
	s, ctx := fixture(t)
	if output, err := exec.Command("git", "init", "--quiet", s.Dir).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	repoRoot := s.Dir
	s.Dir = filepath.Join(repoRoot, "nested-data")
	if err := os.Mkdir(s.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", repoRoot)
	if _, err := s.Submit(ctx, observation()); err == nil {
		t.Fatal("observation accepted inside Git")
	}
	rows, err := store.List[Report](ctx, s.Store, "issue-report")
	if err != nil || len(rows) != 0 {
		t.Fatal("rejected observation was persisted", rows, err)
	}
	var key any
	if err := s.Store.Get(ctx, "report-key", hash(observation().IdempotencyKey), &key); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("rejected observation consumed its idempotency key", err)
	}
	events, err := s.Store.Events(ctx, 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatal("rejected observation wrote report events", events, err)
	}
	// A previously valid private ledger can later become part of a checkout.
	// Recheck its location before any additional host input or recovery note.
	other, ctx := fixture(t)
	t.Setenv("GIT_DIR", filepath.Join(repoRoot, ".git"))
	t.Setenv("GIT_WORK_TREE", repoRoot)
	t.Setenv("GIT_COMMON_DIR", filepath.Join(repoRoot, ".git"))
	if err := other.requirePrivateReportDir(ctx); err != nil {
		t.Fatal("ambient Git overrides changed actual private-location detection", err)
	}
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_CEILING_DIRECTORIES"} {
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	engine, _ := setupEngine(other)
	engine.status = "needs_attention"
	r := submit(t, other, ctx)
	step(t, other, ctx)
	before := get(t, other, ctx, r.ID)
	runtimeBefore, _ := other.runtime(ctx, r.ID)
	if output, err := exec.Command("git", "init", "--quiet", other.Dir).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	addition := EvidenceInput{ID: r.ID, IdempotencyKey: "private-extra", Text: "Private host observation"}
	if _, err := other.AddEvidence(ctx, addition); err == nil {
		t.Fatal("additional evidence accepted inside Git")
	}
	if _, err := other.ResumeReport(ctx, ResumeInput{ID: r.ID, Reconciled: true, Note: "Private reconciliation details"}); err == nil {
		t.Fatal("recovery note accepted inside Git")
	}
	afterRuntime, _ := other.runtime(ctx, r.ID)
	if hash(before) != hash(get(t, other, ctx, r.ID)) || hash(runtimeBefore) != hash(afterRuntime) {
		t.Fatal("rejected private inputs changed the ledger")
	}
	if err := other.Store.Get(ctx, "report-evidence-key", hash([]string{r.ID, addition.IdempotencyKey}), &key); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("rejected evidence consumed its idempotency key", err)
	}
}

// Store.Write acquires an uncancellable mutex. Isolate these concurrent workers
// so a regression cannot outlive the test and race fixture database cleanup.
func runConcurrentReportChild(t *testing.T) bool {
	t.Helper()
	if os.Getenv("CODE_FACTORY_CONCURRENT_REPORT_TEST") == t.Name() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.timeout=15s", "-test.v")
	cmd.Env = append(os.Environ(), "CODE_FACTORY_CONCURRENT_REPORT_TEST="+t.Name(), "TMPDIR="+t.TempDir())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput() // Wait reaps the owned child before TempDir cleanup.
	if ctx.Err() != nil || err != nil {
		t.Fatalf("concurrent report child failed (deadline=%v, reaped=%v): %v\n%s", ctx.Err(), cmd.ProcessState != nil, err, output)
	}
	t.Logf("%s", output)
	return true
}

func TestSubmissionValidationConcurrentIdempotencyAndConflicts(t *testing.T) {
	if runConcurrentReportChild(t) {
		return
	}
	s, ctx := fixture(t)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
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

func TestRedactCredentialCandidateStormOmitsPrivateTail(t *testing.T) {
	const prefix = "Inspected diagnostic prefix\n"
	text := prefix + strings.Repeat("{invalid ", 10_000) + "uninspected-private-tail"
	got := redactCredentialText(text)
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, "[redacted]") || strings.Contains(got, "uninspected-private-tail") {
		t.Fatal("candidate exhaustion did not preserve inspected evidence and omit the unresolved tail", got)
	}
}

func TestRedactCredentialExhaustedDepthOmitsPrivateText(t *testing.T) {
	// Enter an already exhausted state without building exponentially escaped JSON.
	if got := redactCredentialValue("uninspected-private-text", 1<<20); got != "[redacted]" {
		t.Fatal("exhausted decoding returned unresolved private text", got)
	}
}

func TestRedactCredentialPreservesLargeDiagnosticInteger(t *testing.T) {
	const prefix, suffix = "Diagnostic prefix ", " diagnostic suffix"
	got := redactCredentialText(prefix + `{"sequence":9007199254740993,"Authorization":"Negotiate unknown-integer-proof","adjacent":"kept diagnostic field"}` + suffix)
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) || strings.Contains(got, "unknown-integer-proof") {
		t.Fatal("credential redaction lost neighboring text or retained its proof", got)
	}
	var diagnostic struct {
		Sequence json.Number `json:"sequence"`
		Adjacent string      `json:"adjacent"`
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix)
	if err := json.Unmarshal([]byte(encoded), &diagnostic); err != nil || diagnostic.Sequence.String() != "9007199254740993" || diagnostic.Adjacent != "kept diagnostic field" {
		t.Fatal("decoded diagnostic values changed during redaction", diagnostic, err)
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
	o.Errors += "\nAuthorization: Negotiate unknown-observation-proof\nProxy-Authorization: Digest username=unknown-proxy-user, response=unknown-proxy-proof\nretained-authorization-line"
	o.ToolEvidence += `
{"Proxy-Authorization": "Negotiate unknown-tool-proxy-proof"}`
	o.ToolEvidence += `
{"Authorization": ["Negotiate unknown-text-array-proof",
"Custom unknown-text-array-second"], "adjacent": "retained-auth-array-neighbor"}
{'Proxy-Authorization': ['Custom unknown-text-proxy-array-proof', 'Negotiate unknown-text-proxy-array-second'], 'adjacent': 'retained-proxy-array-neighbor'}`
	inlineHeaders := "Cookie: state={\"opaque\":\"unknown-inline-cookie-json\"}\nAuthorization: Negotiate {\"opaque\":\"unknown-inline-auth-json\"}\nretained-inline-header-neighbor"
	o.Errors += "\n" + inlineHeaders
	nestedText, _ := json.Marshal(map[string]any{"headers": map[string]string{"Authorization": "Negotiate unknown-escaped-proof", "Cookie": "session=unknown-escaped-cookie"}, "raw": inlineHeaders, "adjacent": "retained-nested-tool-neighbor"})
	nestedTool, _ := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "text", "text": string(nestedText)}}, "isError": true})
	o.ToolEvidence += "\nMCP response: " + string(nestedTool) + `
[["Authorization", "Negotiate unknown-text-tuple-proof"], ["X-Diagnostic", "retained-text-tuple-neighbor"]]`
	headerEvidence := `{"headers":{"X-Api-Key":"unknown-text-api-proof"},"pairs":[["X-Api-Key","unknown-text-api-tuple-proof"]],"records":[{"name":"Authorization","value":"Negotiate unknown-text-record-proof","adjacent":"retained-text-record-neighbor"},{"key":"Cookie","value":"session=unknown-text-record-cookie"},{"name":"X-Api-Key","value":"unknown-text-api-record-proof"}],"oauth":{"client_secret":"unknown-text-client-secret","adjacent":"retained-oauth-diagnostic"},"sequence":9007199254740993}`
	o.ToolEvidence += "\n" + headerEvidence
	o.Errors += "\nX-Api-Key: unknown-raw-api-proof\nretained-api-header-neighbor"
	o.RunIDs = []string{"run-1", "missing-run"}
	if err := s.Store.Put(ctx, "item", "repo#1", domain.Item{ID: "repo#1", Revision: "captured-associated-item-revision"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, run := range []domain.Run{{ID: "run-1", ItemID: "repo#1", Prompt: "relevant private-jira-secret", Base: "immutable-base"}, {ID: "unrelated", Prompt: "unrelated-private-work-must-not-appear"}} {
		if e := s.Store.Put(ctx, "run", run.ID, run, ""); e != nil {
			t.Fatal(e)
		}
	}
	if err := s.Store.Put(ctx, "attention", "attention-1", domain.Attention{ID: "attention-1", RunID: "run-1", Params: json.RawMessage(`{"sequence":9007199254740993,"client_secret":"unknown-attention-client-secret","headers":{"X-Api-Key":"unknown-attention-api-proof"},"records":[{"name":"Authorization","value":"Negotiate unknown-attention-record-proof","adjacent":"retained-attention-record-neighbor"}]}`)}, ""); err != nil {
		t.Fatal(err)
	}
	if e := s.Store.Write(ctx, func(t *store.Tx) error {
		return t.Event("run.output", "run-1", map[string]any{
			"text":                "private-jira-secret\nCookie: session=unknown-event-cookie\nAuthorization: Custom unknown-event-proof\nretained-event-line",
			"headers":             map[string]any{"Cookie": []string{"unknown-map-cookie", "unknown-map-second"}, "Set-Cookie": "unknown-map-response", "Proxy-Authorization": []string{"Negotiate unknown-map-proxy-proof", "Custom unknown-map-proxy-second"}, "X-Api-Key": "unknown-structured-api-proof"},
			"headerPairs":         [][]string{{"Authorization", "Negotiate unknown-structured-tuple-proof"}, {"Proxy-Authorization", "Custom unknown-structured-proxy-tuple-proof"}, {"X-Api-Key", "unknown-structured-api-tuple-proof"}, {"X-Diagnostic", "retained-structured-tuple-neighbor"}},
			"headerRecords":       []map[string]string{{"name": "Authorization", "value": "Negotiate unknown-structured-record-proof", "adjacent": "retained-header-record-neighbor"}, {"key": "Cookie", "value": "session=unknown-structured-record-cookie"}},
			"Proxy-Authorization": "Negotiate unknown-structured-proxy-proof",
			"id_token":            "unknown-map-id-token",
			"sequence":            json.Number("9007199254740993"),
			"oauth":               map[string]string{"client_secret": "unknown-event-client-secret", "adjacent": "retained-event-oauth-diagnostic"},
		})
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
	var captured struct {
		Evidence map[string]json.RawMessage `json:"evidence"`
	}
	if err := json.Unmarshal(b, &captured); err != nil {
		t.Fatal(err)
	}
	var capturedEvents []struct {
		Data struct {
			Sequence json.RawMessage `json:"sequence"`
		} `json:"data"`
	}
	if err := json.Unmarshal(captured.Evidence["referenced-events"], &capturedEvents); err != nil || len(capturedEvents) != 1 || string(capturedEvents[0].Data.Sequence) != "9007199254740993" {
		t.Error("captured journal diagnostic integer changed", capturedEvents, err)
	}
	var capturedAttention []struct {
		Params struct {
			Sequence json.RawMessage `json:"sequence"`
		} `json:"params"`
	}
	if err := json.Unmarshal(captured.Evidence["attention:run-1"], &capturedAttention); err != nil || len(capturedAttention) != 1 || string(capturedAttention[0].Params.Sequence) != "9007199254740993" {
		t.Error("captured attention diagnostic integer changed", capturedAttention, err)
	}
	secrets := []string{"private-jira-secret", "cloud-token", "ghp_abcdefghijk", "hunter2", "private-owner-token", "ZXhhbXBsZTpwdGVzdA==", "unknown-request-cookie", "unknown-second-cookie", "unknown-response-cookie", "unknown-json-cookie", "unknown-json-second", "unknown-json-response", "unknown-json-id-token", "unknown-json-array-cookie", "unknown-json-array-second", "unknown-json-array-response", "unknown-event-cookie", "unknown-map-cookie", "unknown-map-second", "unknown-map-response", "unknown-map-id-token", "unrelated-private-work-must-not-appear", "unrelated-private-sync-must-not-appear"}
	secrets = append(secrets, "unknown-prefixed-request", "unknown-prefixed-response", "unknown-logged-cookie")
	secrets = append(secrets, "unknown-single-id", "unknown-single-cookie", "unknown-single-response", "unknown-single-array")
	secrets = append(secrets, "unknown-single-password", "secret-single-suffix", "unknown-double-password", "secret-double-suffix")
	secrets = append(secrets, "unknown-observation-proof", "unknown-proxy-user", "unknown-proxy-proof", "unknown-tool-proxy-proof", "unknown-event-proof", "unknown-map-proxy-proof", "unknown-map-proxy-second", "unknown-structured-proxy-proof")
	secrets = append(secrets, "unknown-text-array-proof", "unknown-text-array-second", "unknown-text-proxy-array-proof", "unknown-text-proxy-array-second")
	secrets = append(secrets, "unknown-escaped-proof", "unknown-escaped-cookie", "unknown-text-tuple-proof", "unknown-structured-tuple-proof", "unknown-structured-proxy-tuple-proof")
	secrets = append(secrets, "unknown-inline-cookie-json", "unknown-inline-auth-json")
	secrets = append(secrets, "unknown-text-api-proof", "unknown-text-api-tuple-proof", "unknown-text-api-record-proof", "unknown-text-record-proof", "unknown-text-record-cookie", "unknown-raw-api-proof", "unknown-attention-api-proof", "unknown-attention-record-proof", "unknown-structured-api-proof", "unknown-structured-api-tuple-proof", "unknown-structured-record-proof", "unknown-structured-record-cookie")
	secrets = append(secrets, "unknown-text-client-secret", "unknown-attention-client-secret", "unknown-event-client-secret")
	stored, _ := json.Marshal(get(t, s, ctx, r.ID))
	for _, secret := range secrets {
		if strings.Contains(string(b), secret) || strings.Contains(string(stored), secret) {
			t.Error("private evidence or stored report leaked", secret)
		}
	}
	if !strings.Contains(string(b), "retained-line-evidence") || !strings.Contains(string(b), "retained-event-line") || !strings.Contains(string(b), "retained-prefixed-line") || !strings.Contains(string(b), "retained-single-quote-line") || !strings.Contains(string(b), "retained-quoted-value-line") || !strings.Contains(string(b), "retained-authorization-line") {
		t.Fatal("header redaction swallowed unrelated evidence", string(b))
	}
	if !strings.Contains(string(b), "immutable-base") || !strings.Contains(string(b), "Referenced run unavailable") || !strings.Contains(string(b), "captured-associated-item-revision") {
		t.Fatal("missing scoped evidence or omissions", string(b))
	}
	if !strings.Contains(string(b), "retained-auth-array-neighbor") || !strings.Contains(string(b), "retained-proxy-array-neighbor") {
		t.Error("authorization array redaction swallowed adjacent evidence", string(b))
	}
	for _, adjacent := range []string{"retained-nested-tool-neighbor", "retained-text-tuple-neighbor", "retained-structured-tuple-neighbor", "retained-inline-header-neighbor", "retained-api-header-neighbor", "retained-text-record-neighbor", "retained-header-record-neighbor", "retained-attention-record-neighbor", "retained-oauth-diagnostic", "retained-event-oauth-diagnostic"} {
		if !strings.Contains(string(b), adjacent) {
			t.Error("serialized evidence redaction swallowed adjacent data", adjacent)
		}
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
	engine.findings.Body += "\nAuthorization: Negotiate unknown-draft-proof\nProxy-Authorization: Custom unknown-draft-proxy-proof\nretained-draft-authorization-line"
	engine.findings.Body += `
{"Authorization": ["Custom unknown-draft-array-proof"], "adjacent": "retained-draft-array-neighbor"}
{'Proxy-Authorization': ['Negotiate unknown-draft-proxy-array-proof'], 'adjacent': 'retained-draft-proxy-array-neighbor'}`
	engine.findings.Body += "\nHost tool response: " + string(nestedTool) + `
[["Proxy-Authorization", "Negotiate unknown-draft-tuple-proof"], ["X-Diagnostic", "retained-draft-tuple-neighbor"]]`
	engine.findings.Body += "\n" + inlineHeaders
	engine.findings.Body += "\n" + headerEvidence
	if _, err := s.AddEvidence(ctx, EvidenceInput{ID: r.ID, IdempotencyKey: "redacted-addition", Text: "Cookie: session=unknown-added-cookie\nAuthorization: Custom unknown-added-proof\nretained-addition-line\n" + `{"Proxy-Authorization": ["Custom unknown-added-array-proof"], "adjacent": "retained-added-array-neighbor"}` + "\n" + string(nestedTool) + "\n" + headerEvidence}); err != nil {
		t.Fatal(err)
	}
	step(t, s, ctx)
	stored, _ = json.Marshal(get(t, s, ctx, r.ID))
	if strings.Contains(string(stored), "unknown-") || !strings.Contains(string(stored), "retained-addition-line") || !strings.Contains(string(stored), "retained-draft-authorization-line") {
		t.Error("unknown credentials persisted after investigation", string(stored))
	}
	// Re-sanitize a retained draft at publication as well as fresh worker output.
	if _, err := s.update(ctx, r.ID, func(r *Report) error { r.Findings.Body = engine.findings.Body; return nil }); err != nil {
		t.Fatal(err)
	}
	step(t, s, ctx)
	if len(github.created) != 1 || strings.Contains(github.created[0].Body, "unknown-") || strings.Contains(github.created[0].Body, "secret-draft-suffix") || !strings.Contains(github.created[0].Body, "retained-draft-line") {
		t.Error("unknown draft credentials reached publication", github.created)
	}
	if len(github.created) == 1 && (!strings.Contains(github.created[0].Body, "retained-draft-array-neighbor") || !strings.Contains(github.created[0].Body, "retained-draft-proxy-array-neighbor")) {
		t.Error("authorization array redaction swallowed adjacent publication evidence", github.created)
	}
	if len(github.created) == 1 && (!strings.Contains(github.created[0].Body, "retained-nested-tool-neighbor") || !strings.Contains(github.created[0].Body, "retained-draft-tuple-neighbor") || !strings.Contains(github.created[0].Body, "retained-inline-header-neighbor")) {
		t.Error("serialized evidence redaction swallowed adjacent publication data", github.created)
	}
	if len(github.created) == 1 && (!strings.Contains(github.created[0].Body, `"sequence":9007199254740993`) || !strings.Contains(github.created[0].Body, "retained-text-record-neighbor") || !strings.Contains(github.created[0].Body, "retained-oauth-diagnostic")) {
		t.Error("header record redaction changed neighboring publication diagnostics", github.created)
	}
	prepared, _ := json.Marshal(engine.preparedReports[0])
	for _, secret := range append(secrets, "unknown-added-cookie", "unknown-added-proof", "unknown-added-array-proof") {
		if strings.Contains(string(prepared), secret) {
			t.Error("unknown credentials reached investigator input", secret)
		}
	}
	if !strings.Contains(string(prepared), "retained-added-array-neighbor") {
		t.Error("authorization array redaction swallowed adjacent investigator evidence", string(prepared))
	}
	if !strings.Contains(string(prepared), "retained-inline-header-neighbor") {
		t.Error("raw header redaction swallowed adjacent investigator evidence", string(prepared))
	}
	if !strings.Contains(string(prepared), "retained-oauth-diagnostic") {
		t.Error("OAuth credential redaction swallowed adjacent investigator evidence", string(prepared))
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
			engine.findings.MissingInformation = ""
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
	engine.findings.MissingInformation = ""
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
	engine.findings.DuplicateNumber = 0
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

func TestPublicationCommandInterruptionRecovery(t *testing.T) {
	if dir := os.Getenv("CODE_FACTORY_PUBLISH_INTERRUPTION_FIXTURE"); dir != "" {
		if os.Getenv("CODE_FACTORY_PUBLISH_INTERRUPTION_PHASE") == "hung-child" {
			if err := os.WriteFile(filepath.Join(dir, "child-started"), []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Minute)
			os.Exit(23)
		}
		if os.Getenv("CODE_FACTORY_PUBLISH_INTERRUPTION_PHASE") == "before-claim" {
			os.Exit(23)
		}
		db, err := store.Open(filepath.Join(dir, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		github := &fakeGitHub{onCreate: func() {
			if err := os.WriteFile(filepath.Join(dir, "creation-effect"), []byte("one external attempt"), 0600); err != nil {
				t.Fatal(err)
			}
			os.Exit(23) // Lose the subprocess after its durable claim, before acknowledgement.
		}}
		publisher := &Service{Store: db, Dir: dir, GitHub: github}
		if err := publisher.PublishReport(context.Background(), os.Getenv("CODE_FACTORY_PUBLISH_INTERRUPTION_REPORT")); err != nil {
			t.Fatal(err)
		}
		t.Fatal("fixture publication did not reach its interruption")
	}
	for _, phase := range []string{"before-claim", "after-claim", "hung-child"} {
		t.Run(phase, func(t *testing.T) {
			s, ctx := fixture(t)
			engine, github := setupEngine(s)
			r := submit(t, s, ctx)
			step(t, s, ctx)
			o := observation()
			o.IdempotencyKey = "queued-after-publication-interruption"
			other, err := s.Submit(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			stepCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			s.PublishCommand = func(ctx context.Context, id string) error {
				childLimit := 5 * time.Second
				if phase == "hung-child" {
					childLimit = 2 * time.Second
				}
				childCtx, stopChild := context.WithTimeout(ctx, childLimit)
				defer stopChild()
				cmd := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestPublicationCommandInterruptionRecovery$", "-test.timeout=10s")
				cmd.WaitDelay = time.Second
				cmd.Env = append(os.Environ(), "CODE_FACTORY_PUBLISH_INTERRUPTION_FIXTURE="+s.Dir, "CODE_FACTORY_PUBLISH_INTERRUPTION_PHASE="+phase, "CODE_FACTORY_PUBLISH_INTERRUPTION_REPORT="+id)
				err := cmd.Run()
				if phase == "hung-child" {
					if !errors.Is(childCtx.Err(), context.DeadlineExceeded) || err == nil || cmd.ProcessState == nil {
						t.Fatal("owned hung child was not killed and reaped at its local deadline", err, cmd.ProcessState)
					}
					if data, err := os.ReadFile(filepath.Join(s.Dir, "child-started")); err != nil || string(data) != "ready" {
						t.Fatal("hung-child regression did not establish child execution", string(data), err)
					}
				} else {
					var exited *exec.ExitError
					if childCtx.Err() != nil || !errors.As(err, &exited) || exited.ExitCode() != 23 {
						t.Fatal("fixture did not reach its intended publication interruption", err)
					}
				}
				cancel() // Shutdown cancellation must still retain the recovery state.
				return err
			}
			step(t, s, stepCtx)
			current := get(t, s, ctx, r.ID)
			rt, err := s.runtime(ctx, r.ID)
			claimed := phase == "after-claim"
			wantState := "blocked"
			if claimed {
				wantState = "needs_attention"
			}
			if err != nil || current.Processing != wantState || rt.CreateAttempted != claimed || claimed && rt.Intent == nil {
				t.Fatal("subprocess interruption did not retain actionable claim state", current, rt, err)
			}
			engine.status = "running"
			step(t, s, ctx)
			if get(t, s, ctx, other.ID).Processing != "investigating" || engine.starts != 2 {
				t.Fatal("interrupted publication starved queued investigation")
			}
			attempts := 1
			if claimed {
				attempts = 2
			}
			for range attempts {
				if _, err := s.ResumeReport(ctx, ResumeInput{ID: r.ID}); err != nil {
					t.Fatal(err)
				}
			}
			if github.creates != 0 {
				t.Fatal("resume repeated a subprocess creation")
			}
			if claimed {
				github.found = &github.issue
				if _, err := s.ResumeReport(ctx, ResumeInput{ID: r.ID}); err != nil {
					t.Fatal(err)
				}
				if get(t, s, ctx, r.ID).Issue == nil {
					t.Fatal("claimed subprocess issue was not reconciled")
				}
				if data, err := os.ReadFile(filepath.Join(s.Dir, "creation-effect")); err != nil || string(data) != "one external attempt" {
					t.Fatal("fixture did not reach exactly one creation attempt", string(data), err)
				}
			}
		})
	}
}

func TestObsoletePublicationFailuresPreserveActiveGeneration(t *testing.T) {
	for _, phase := range []string{"validation", "queued-evidence", "duplicate-read", "marker-read", "subprocess"} {
		t.Run(phase, func(t *testing.T) {
			s, ctx := fixture(t)
			engine, github := setupEngine(s)
			if phase == "duplicate-read" {
				engine.findings.Classification = "duplicate"
				engine.findings.DuplicateNumber = 17
			}
			r := submit(t, s, ctx)
			step(t, s, ctx)
			var newer Report
			var newerRuntime Runtime
			inject := func() {
				if _, err := s.AddEvidence(ctx, EvidenceInput{ID: r.ID, IdempotencyKey: "fresh-failure-race", Text: "Newer host evidence"}); err != nil {
					t.Fatal(err)
				}
				engine.status = "running"
				if phase != "queued-evidence" {
					other := &Service{Store: s.Store, Vault: s.Vault, Dir: s.Dir, Engine: engine, GitHub: github}
					step(t, other, ctx)
				}
				newer = get(t, s, ctx, r.ID)
				newerRuntime, _ = s.runtime(ctx, r.ID)
			}
			failure := errors.New("obsolete remote operation failed")
			switch phase {
			case "validation", "queued-evidence":
				github.onValidate, github.validateErr = inject, failure
			case "duplicate-read":
				github.onRead, github.errorRead = inject, failure
			case "marker-read":
				github.onMarker, github.markerErr = inject, failure
			case "subprocess":
				s.PublishCommand = func(context.Context, string) error { inject(); return failure }
			}
			step(t, s, ctx)
			currentRuntime, _ := s.runtime(ctx, r.ID)
			wantGeneration := 2
			if phase == "queued-evidence" {
				wantGeneration = 1
			}
			if newerRuntime.Generation != wantGeneration || hash(newerRuntime) != hash(currentRuntime) || hash(newer) != hash(get(t, s, ctx, r.ID)) {
				t.Fatal("obsolete publisher failure overwrote newer generation")
			}
			o := observation()
			o.IdempotencyKey = "must-remain-queued"
			waiting, err := s.Submit(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			step(t, s, ctx)
			if engine.starts != 2 || get(t, s, ctx, waiting.ID).Processing != "queued" || github.creates != 0 {
				t.Fatal("obsolete failure released an active investigation slot")
			}
		})
	}
	t.Run("obsolete-success-cannot-adopt-newer-claim", func(t *testing.T) {
		s, ctx := fixture(t)
		engine, github := setupEngine(s)
		r := submit(t, s, ctx)
		step(t, s, ctx)
		var newer Report
		var newerRuntime Runtime
		oldMarkerReads := 0
		github.onValidate = func() {
			github.onValidate = nil
			if _, err := s.AddEvidence(ctx, EvidenceInput{ID: r.ID, IdempotencyKey: "newer-claim", Text: "Fresh host observation"}); err != nil {
				t.Fatal(err)
			}
			other := &Service{Store: s.Store, Vault: s.Vault, Dir: s.Dir, Engine: engine, GitHub: github}
			step(t, other, ctx)
			github.uncertain = true
			if err := other.PublishReport(ctx, r.ID); err != nil {
				t.Fatal(err)
			}
			newer = get(t, s, ctx, r.ID)
			newerRuntime, _ = s.runtime(ctx, r.ID)
			github.onMarker = func() {
				oldMarkerReads++
				if oldMarkerReads > 1 {
					github.markerErr = errors.New("an obsolete publisher must not reconcile this claim")
				}
			}
		}
		step(t, s, ctx)
		currentRuntime, _ := s.runtime(ctx, r.ID)
		if newerRuntime.Generation != 2 || !newerRuntime.CreateAttempted || github.creates != 1 || oldMarkerReads != 1 || hash(newer) != hash(get(t, s, ctx, r.ID)) || hash(newerRuntime) != hash(currentRuntime) {
			t.Fatal("obsolete publisher adopted a newer generation's publication claim")
		}
	})
}

func TestDelayedPublicationSuccessPreservesEstablishedIssue(t *testing.T) {
	for _, phase := range []string{"newer-generation", "newer-claimed", "same-generation", "same-identity-outcome"} {
		t.Run(phase, func(t *testing.T) {
			s, ctx := fixture(t)
			engine, github := setupEngine(s)
			engine.findings.Classification = "duplicate"
			engine.findings.DuplicateNumber = github.issue.Number
			r := submit(t, s, ctx)
			step(t, s, ctx)
			originalRuntime, _ := s.runtime(ctx, r.ID)
			var established Report
			var establishedRuntime Runtime
			github.onRead = func() {
				if strings.HasPrefix(phase, "newer") {
					if _, err := s.AddEvidence(ctx, EvidenceInput{ID: r.ID, IdempotencyKey: "newer-verified-issue", Text: "Updated evidence changes the investigated issue"}); err != nil {
						t.Fatal(err)
					}
					engine.findings = findings()
					newGitHub := &fakeGitHub{issue: Issue{Repository: DefaultRepository, Number: 31, URL: "https://github.com/" + DefaultRepository + "/issues/31", State: "open"}}
					if phase == "newer-claimed" {
						newGitHub.onCreate = func() {
							claimed := get(t, s, ctx, r.ID)
							claimedRuntime, _ := s.runtime(ctx, r.ID)
							if err := s.finishIssue(ctx, r.ID, github.issue, "duplicate", originalRuntime.AdmissionID); err != nil {
								t.Fatal(err)
							}
							afterRuntime, _ := s.runtime(ctx, r.ID)
							if !claimedRuntime.CreateAttempted || hash(claimed) != hash(get(t, s, ctx, r.ID)) || hash(claimedRuntime) != hash(afterRuntime) {
								t.Fatal("older success replaced a newer effect still awaiting its issue identity")
							}
						}
					}
					other := &Service{Store: s.Store, Vault: s.Vault, Dir: s.Dir, Engine: engine, GitHub: newGitHub}
					step(t, other, ctx)
					step(t, other, ctx)
					if newGitHub.creates != 1 {
						t.Fatal("newer generation did not establish its own verified issue")
					}
				} else {
					rt, err := s.runtime(ctx, r.ID)
					if err != nil {
						t.Fatal(err)
					}
					issue := github.issue
					if phase == "same-generation" {
						issue.Number, issue.URL = 31, "https://github.com/"+DefaultRepository+"/issues/31"
					}
					other := &Service{Store: s.Store, Dir: s.Dir}
					if err := other.finishIssue(ctx, r.ID, issue, "filed", rt.AdmissionID); err != nil {
						t.Fatal(err)
					}
				}
				established = get(t, s, ctx, r.ID)
				establishedRuntime, _ = s.runtime(ctx, r.ID)
			}
			step(t, s, ctx) // The older duplicate read now returns its original issue.
			currentRuntime, _ := s.runtime(ctx, r.ID)
			if established.Processing != "complete" || established.Outcome != "filed" || hash(established) != hash(get(t, s, ctx, r.ID)) || hash(establishedRuntime) != hash(currentRuntime) || github.creates != 0 {
				t.Fatal("delayed success overwrote an established identity, outcome, timestamp or runtime")
			}
		})
	}
	t.Run("unrecorded-effect-retains-newer-outcome", func(t *testing.T) {
		s, ctx := fixture(t)
		engine, github := setupEngine(s)
		engine.findings.Classification = "insufficient_information"
		engine.findings.MissingInformation = "Which contract detail did the host observe?"
		r := submit(t, s, ctx)
		step(t, s, ctx)
		oldRuntime, _ := s.runtime(ctx, r.ID)
		if _, err := s.AddEvidence(ctx, EvidenceInput{ID: r.ID, IdempotencyKey: "explanation", Text: "The host supplied the missing contract detail"}); err != nil {
			t.Fatal(err)
		}
		engine.findings.Classification = "explained"
		engine.findings.MissingInformation = ""
		step(t, s, ctx)
		newerRuntime, _ := s.runtime(ctx, r.ID)
		before := get(t, s, ctx, r.ID)
		if err := s.finishIssue(ctx, r.ID, github.issue, "filed", oldRuntime.AdmissionID); err != nil {
			t.Fatal(err)
		}
		after := get(t, s, ctx, r.ID)
		currentRuntime, _ := s.runtime(ctx, r.ID)
		if after.Issue == nil || *after.Issue != github.issue || after.Outcome != before.Outcome || after.Processing != before.Processing || hash(currentRuntime) != hash(newerRuntime) {
			t.Fatal("safe recovery lost an unrecorded issue or overwrote newer processing/outcome", after)
		}
	})
}

func TestPublicationFailuresPreserveRecoveredIssueAndClaim(t *testing.T) {
	for _, phase := range []string{"creation-rejected", "creation-uncertain", "reconcile-error", "reconcile-empty"} {
		t.Run(phase, func(t *testing.T) {
			s, ctx := fixture(t)
			_, github := setupEngine(s)
			r := submit(t, s, ctx)
			step(t, s, ctx)
			recoverIssue := func() {
				rt, err := s.runtime(ctx, r.ID)
				if err != nil {
					t.Fatal(err)
				}
				other := &Service{Store: s.Store, Dir: s.Dir}
				if err := other.finishIssue(ctx, r.ID, github.issue, "filed", rt.AdmissionID); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "creation-rejected" {
				github.createErr = &publicationRejected{Reason: "known rejection"}
				github.onCreate = recoverIssue
			} else {
				github.uncertain = true
				if phase == "creation-uncertain" {
					github.onCreate = recoverIssue
				}
			}
			step(t, s, ctx)
			if strings.HasPrefix(phase, "reconcile") {
				github.onMarker = recoverIssue
				if phase == "reconcile-error" {
					github.markerErr = errors.New("obsolete marker failure")
				}
				if _, err := s.ResumeReport(ctx, ResumeInput{ID: r.ID}); err != nil {
					t.Fatal(err)
				}
			}
			rt, err := s.runtime(ctx, r.ID)
			current := get(t, s, ctx, r.ID)
			if err != nil || current.Processing != "complete" || current.Issue == nil || !rt.CreateAttempted || rt.Intent == nil || github.creates != 1 {
				t.Fatal("late failure overwrote recovered identity or durable claim", current, rt, err)
			}
		})
	}
}

func TestConcurrentPublicationCommandsClaimOneExternalEffect(t *testing.T) {
	if runConcurrentReportChild(t) {
		return
	}
	s, ctx := fixture(t)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
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
	engine.findings.MissingInformation = ""
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
			engine.findings.MissingInformation = ""
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

func draftFixture(f Findings) structuredDraft {
	return structuredDraft{Version: structuredContract, Classification: f.Classification, Summary: f.Summary, MissingInformation: f.MissingInformation, Component: f.Component, SourcePath: f.SourcePath, SourceCommit: f.SourceCommit, OwnershipReason: f.OwnershipReason, DuplicateNumber: f.DuplicateNumber, Title: f.Title, Labels: f.Labels, Restriction: "none", GuidanceReviewed: []string{}, Answers: []fieldAnswer{}, Content: reportContent{Environment: "Fixture host", Reproduction: "Reproduction unknown; reconnect timing unavailable", Expected: "Answer delivered", Actual: "Answer failed", Evidence: f.Body, Findings: f.Summary, Uncertainty: "Suspected, not proven"}}
}
