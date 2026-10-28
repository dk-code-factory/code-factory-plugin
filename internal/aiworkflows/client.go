// Package aiworkflows runs model turns through embedded YAML workflows. The
// provider adapter retains Code Factory's interactive Codex protocol and policy;
// the application ledger remains responsible for session recovery and delivery.
package aiworkflows

import (
	"codefactory/internal/codex"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dk-code-factory/agent-workflow/pkg/workflows"
	"github.com/google/jsonschema-go/jsonschema"
)

//go:embed workflows/*.yml
var definitions embed.FS

type Kind string

const (
	Task           Kind = "task"
	Review         Kind = "review"
	Assessment     Kind = "assessment"
	QuestionCheck  Kind = "question-check"
	AttentionCheck Kind = "attention-check"
	QuestionRepair Kind = "question-repair"
)

var roles = map[Kind]string{Task: "task", Review: "reviewer", Assessment: "assessor", QuestionCheck: "question-checker", AttentionCheck: "attention-checker", QuestionRepair: "question-repairer"}

type Client struct {
	*codex.Client
	ctx    context.Context
	cancel context.CancelFunc
	dir    string
	kind   Kind
	store  workflows.RunStore
	mu     sync.Mutex
	active *turn
	wg     sync.WaitGroup
	closed bool

	native    bool
	onMessage func(codex.Message)
}

type turn struct {
	kind      Kind
	params    map[string]any
	ack       chan startResult
	end       chan turnResult
	finished  chan error
	finalized chan struct{}
	delivered chan struct{}
	text      string
	id        string
	ended     bool
	status    string
	cancel    context.CancelFunc
	err       error // Published by closing finalized before structured terminal delivery.
}

type startResult struct {
	response json.RawMessage
	err      error
}
type turnResult struct{ text, status string }

// Start prepares immutable private definitions before starting the transport.
// Only model-producing turn/start calls enter workflows; discovery, steering,
// interrupts and replies continue to target the existing provider session.
func Start(ctx context.Context, binary string, extra []string, dataDir string, kind Kind, onMessage func(codex.Message)) (*Client, error) {
	if _, ok := roles[kind]; !ok {
		return nil, fmt.Errorf("unknown AI workflow %q", kind)
	}
	root := filepath.Join(dataDir, "ai-workflows")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(root, "definitions-")
	if err != nil {
		return nil, err
	}
	if err = os.Mkdir(filepath.Join(dir, "workflows"), 0700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	for k := range roles {
		name := "workflows/" + string(k) + ".yml"
		data, readErr := definitions.ReadFile(name)
		if readErr != nil {
			_ = os.RemoveAll(dir)
			return nil, readErr
		}
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &Client{ctx: ctx, cancel: cancel, dir: dir, kind: kind, store: statusStore{dir: filepath.Join(root, "runs")}, onMessage: onMessage}
	native, err := codex.Start(ctx, binary, extra, func(msg codex.Message) {
		if completed := c.observe(msg); completed != nil {
			defer close(completed.delivered)
			// Structured helpers must validate before their caller consumes a
			// successful result. Interactive tasks deliver completion immediately
			// so an answer cannot steer an already ended native turn while an
			// observational checkpoint is being flushed.
			structured := completed.kind == Assessment || completed.kind == QuestionCheck || completed.kind == AttentionCheck
			if structured {
				<-completed.finalized
			}
			if structured && completed.err != nil {
				// Native completion is insufficient when the YAML output contract
				// or workflow checkpoint failed. Existing callers consume native
				// failed-turn handling; never turn engine failure into task success.
				var params map[string]json.RawMessage
				var terminal map[string]any
				if json.Unmarshal(msg.Params, &params) == nil && json.Unmarshal(params["turn"], &terminal) == nil && terminal["status"] == "completed" {
					terminal["status"] = "failed"
					terminal["error"] = map[string]string{"message": "AI workflow failed; inspect its private execution status."}
					params["turn"], _ = json.Marshal(terminal)
					msg.Params, _ = json.Marshal(params)
				}
			}
		}
		if onMessage != nil {
			onMessage(msg)
		}
	})
	if err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	c.Client = native
	return c, nil
}

// StartNative uses one retained app-server session without the YAML executor.
// Explicit mode selection is frozen on the application run, never a fallback.
func StartNative(ctx context.Context, binary string, extra []string, dataDir string, kind Kind, onMessage func(codex.Message)) (*Client, error) {
	ctx, cancel := context.WithCancel(ctx)
	client, err := codex.Start(ctx, binary, extra, onMessage)
	if err != nil {
		cancel()
		return nil, err
	}
	return &Client{Client: client, ctx: ctx, cancel: cancel, native: true}, nil
}

func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if c.native || method != "turn/start" {
		return c.Client.Call(ctx, method, params, out)
	}
	return c.CallWorkflow(ctx, c.kind, params, out)
}

// CallWorkflow admits exactly one model turn. It returns the provider's start
// acknowledgement while the workflow keeps running until that turn terminates.
// Failure before acknowledgement is never retried; delivery may be uncertain.
func (c *Client) CallWorkflow(ctx context.Context, kind Kind, params any, out any) error {
	if c.native {
		return c.Client.Call(ctx, "turn/start", params, out)
	}
	if _, ok := roles[kind]; !ok {
		return fmt.Errorf("unknown AI workflow %q", kind)
	}
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var native map[string]any
	if err = json.Unmarshal(b, &native); err != nil {
		return err
	}
	input, _ := native["input"].([]any)
	var texts []string
	for _, v := range input {
		m, _ := v.(map[string]any)
		if m["type"] == "text" {
			text, _ := m["text"].(string)
			texts = append(texts, text)
		}
	}
	prompt := strings.Join(texts, "\n")
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("AI workflow requires a text prompt")
	}
	workflowCtx, cancel := context.WithCancel(c.ctx)
	t := &turn{kind: kind, params: native, ack: make(chan startResult, 1), end: make(chan turnResult, 1), finished: make(chan error, 1), finalized: make(chan struct{}), delivered: make(chan struct{}), cancel: cancel}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		return errors.New("AI workflow client closed")
	}
	if c.active != nil {
		c.mu.Unlock()
		cancel()
		return errors.New("AI workflow turn already active")
	}
	c.active = t
	c.wg.Add(1)
	c.mu.Unlock()
	stop := context.AfterFunc(ctx, cancel)
	go func() {
		defer cancel()
		e := workflows.Executor{WorkspaceDir: c.dir, Agents: &providerTurn{client: c, turn: t}, Store: c.store, MaxParallel: 1}
		_, runErr := e.Run(workflowCtx, workflows.RunRequest{Ref: "workflows/" + string(kind) + ".yml", Inputs: map[string]any{"prompt": prompt}})
		c.mu.Lock()
		if c.active == t {
			c.active = nil
		}
		status, id := t.status, t.id
		c.mu.Unlock()
		t.finished <- runErr
		t.err = runErr
		close(t.finalized)
		// Application shutdown joins execution while holding its session lock.
		// Like native notifications, diagnostics must not be part of that join.
		c.wg.Done()
		if runErr != nil && status == "completed" && (kind == Task || kind == Review || kind == QuestionRepair) && c.onMessage != nil {
			<-t.delivered
			params, _ := json.Marshal(map[string]any{"threadId": t.params["threadId"], "turnId": id, "message": "AI workflow failed; inspect its private execution status."})
			c.onMessage(codex.Message{Method: "workflow/error", Params: params})
		}
	}()
	defer stop()
	select {
	case ack := <-t.ack:
		return c.acceptStart(ctx, t, stop, ack, out)
	case err := <-t.finished:
		// A fast provider may complete before the caller receives its ack.
		select {
		case ack := <-t.ack:
			return c.acceptStart(ctx, t, stop, ack, out)
		default:
			if err == nil {
				err = errors.New("AI workflow ended without starting a turn")
			}
			return err
		}
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	}
}

func (c *Client) acceptStart(ctx context.Context, t *turn, stop func() bool, ack startResult, out any) error {
	if ack.err != nil {
		t.cancel()
		return ack.err
	}
	// Stopping admission cancellation transfers observation to the client. A
	// callback that already started may still cancel even after stop returns.
	if !stop() || ctx.Err() != nil {
		t.cancel()
		return ctx.Err()
	}
	if err := c.ctx.Err(); err != nil {
		t.cancel()
		return err
	}
	if out != nil {
		return json.Unmarshal(ack.response, out)
	}
	return nil
}

type providerTurn struct {
	client *Client
	turn   *turn
}

func (p *providerTurn) HasAgent(role string) bool { return role == roles[p.turn.kind] }

func (p *providerTurn) RunAgent(ctx context.Context, req workflows.AgentRequest) (map[string]any, error) {
	if req.RepairOnly || !p.HasAgent(req.AgentID) {
		return nil, errors.New("unsupported AI workflow agent invocation")
	}
	t := p.turn
	// Preserve skill inputs and all selected native mode, model, sandbox,
	// approval, effort and same-session delivery fields.
	input, _ := t.params["input"].([]any)
	prompt := req.Prompt
	if req.Output != nil {
		// Native outputSchema carries this contract. Avoid appending duplicate
		// formatting prose to the captured JSON evidence in helper prompts.
		prompt = strings.TrimSuffix(prompt, "\n\n"+req.Output.Instruction())
	}
	next := []any{map[string]any{"type": "text", "text": prompt}}
	for _, v := range input {
		m, _ := v.(map[string]any)
		if m["type"] != "text" {
			next = append(next, v)
		}
	}
	t.params["input"] = next
	if req.Output != nil {
		t.params["outputSchema"] = req.Output.Schema
	}
	var response json.RawMessage
	err := p.client.Client.Call(ctx, "turn/start", t.params, &response)
	if err == nil {
		var ack struct{ Turn struct{ ID string } }
		if json.Unmarshal(response, &ack) != nil || ack.Turn.ID == "" {
			err = errors.New("provider returned no turn identity")
		}
		p.client.mu.Lock()
		t.id = ack.Turn.ID
		p.client.mu.Unlock()
	}
	t.ack <- startResult{response: response, err: err}
	if err != nil {
		return nil, err
	}
	select {
	case result := <-t.end:
		if result.status != "completed" {
			return nil, fmt.Errorf("provider turn ended: %s", result.status)
		}
		if req.Output != nil && req.Output.Enabled() {
			// Validate the YAML-owned contract fully before executor success. The
			// pinned executor's validator omits some numeric constraints.
			data, err := json.Marshal(req.Output.Schema)
			var schema jsonschema.Schema
			if err != nil || json.Unmarshal(data, &schema) != nil {
				return nil, errors.New("invalid AI workflow output schema")
			}
			resolved, err := schema.Resolve(&jsonschema.ResolveOptions{Loader: func(*url.URL) (*jsonschema.Schema, error) {
				return nil, errors.New("external AI workflow schema references are unsupported")
			}})
			raw, _, parseErr := workflows.ExtractJSONValue(result.text)
			var value any
			if err != nil || parseErr != nil || json.Unmarshal([]byte(raw), &value) != nil || resolved.Validate(value) != nil {
				return nil, errors.New("AI workflow response does not match its output schema")
			}
		}
		return map[string]any{"text": result.text}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) observe(msg codex.Message) *turn {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.active
	if t == nil || t.ended {
		return nil
	}
	var data struct {
		TurnID string `json:"turnId"`
		Item   struct{ Type, Text string }
		Turn   struct{ ID, Status string }
	}
	_ = json.Unmarshal(msg.Params, &data)
	id := data.TurnID
	if data.Turn.ID != "" {
		id = data.Turn.ID
	}
	if t.id != "" && id != "" && id != t.id {
		return nil
	}
	if msg.Method == "item/completed" && data.Item.Type == "agentMessage" {
		t.text = data.Item.Text
	}
	if msg.Method == "turn/completed" {
		t.ended = true
		t.status = data.Turn.Status
		t.end <- turnResult{text: t.text, status: data.Turn.Status}
		// Release the transport for a follow-up, even if saving the workflow's
		// terminal checkpoint is still pending. Old execution owns only t.
		c.active = nil
		return t
	}
	if msg.Method == "process/closed" {
		t.cancel()
	}
	return nil
}

func (c *Client) Close() error {
	if c.native {
		c.cancel()
		return c.Client.Close()
	}
	c.mu.Lock()
	c.closed = true
	active := c.active != nil
	c.mu.Unlock()
	joined := make(chan struct{})
	go func() { c.wg.Wait(); close(joined) }()
	if !active {
		// Completed native turns only have their final checkpoint left. Drain
		// that work before cancellation can reclassify a successful workflow.
		select {
		case <-joined:
		case <-time.After(2 * time.Second):
		}
	}
	c.cancel()
	err := c.Client.Close()
	<-joined
	_ = os.RemoveAll(c.dir)
	return err
}

// Observational checkpoints contain execution status only. Prompts, answers,
// output and provider errors may contain secrets; the application's redacted
// ledger owns those. These records never authorize replay after a restart.
type statusStore struct{ dir string }

func (s statusStore) Save(ctx context.Context, run *workflows.Run) error {
	clean := *run
	clean.Outputs = nil
	if clean.Error != "" {
		clean.Error = "AI workflow did not complete"
	}
	clean.Jobs = make(map[string]workflows.JobExecution, len(run.Jobs))
	for key, job := range run.Jobs {
		job.Outputs, job.Iterations = nil, nil
		if job.Error != "" {
			job.Error = "AI workflow job did not complete"
		}
		steps := make(map[string]workflows.StepExecution, len(job.Steps))
		for id, step := range job.Steps {
			step.Outputs = nil
			if step.Error != "" {
				step.Error = "AI workflow step did not complete"
			}
			steps[id] = step
		}
		job.Steps = steps
		clean.Jobs[key] = job
	}
	return (workflows.FileStore{Dir: s.dir}).Save(context.WithoutCancel(ctx), &clean)
}
