package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/setup"
	"codefactory/internal/store"
	"codefactory/internal/web"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const ResourceURI = "ui://code-factory/inbox.html"

type Server struct {
	App         *app.App
	Events      *events.Service
	MCP         *mcp.Server
	lastRequest atomic.Int64
}
type Empty struct{}
type IDInput struct {
	ID string `json:"id"`
}
type Page struct {
	After int64 `json:"after,omitempty"`
	Limit int   `json:"limit,omitempty"`
}
type InboxInput struct {
	State string `json:"state,omitempty"`
}
type UserAction struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	Revision string `json:"revision,omitempty"`
	Priority *int   `json:"priority,omitempty"`
}
type StartInput struct {
	ID     string `json:"id"`
	Resume bool   `json:"resume,omitempty"`
}
type MessageInput struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	ExpectedTurn string `json:"expectedTurn,omitempty"`
}
type AnswerInput struct {
	ID     string         `json:"id"`
	Answer map[string]any `json:"answer"`
}
type LinkInput struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Evidence string `json:"evidence,omitempty"`
	Remove   bool   `json:"remove,omitempty"`
}
type CredentialInput struct {
	Token string `json:"token"`
}
type RepositoryInput struct {
	ID       string `json:"id"`
	Checkout string `json:"checkout,omitempty"`
	Base     string `json:"base,omitempty"`
}
type SaveSkillInput struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}
type SkillInput struct {
	CWD string `json:"cwd"`
}

func bind[I any](s *mcp.Server, name, desc string, appOnly, readOnly bool, f func(context.Context, I) (any, error)) {
	vis := []string{"model", "app"}
	if appOnly {
		vis = []string{"app"}
	}
	meta := mcp.Meta{"ui": map[string]any{"resourceUri": ResourceURI, "visibility": vis}}
	if name == "open_factory" {
		meta["openai/outputTemplate"] = ResourceURI
	}
	openWorld := name == "start_run" || name == "send_message" || name == "answer_request"
	destructive := name == "cleanup_run"
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc, Meta: meta, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, OpenWorldHint: ptr(openWorld), DestructiveHint: ptr(destructive)}}, func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, map[string]any, error) {
		v, e := f(ctx, in)
		if e != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: e.Error()}}}, nil, nil
		}
		return nil, map[string]any{"data": v}, nil
	})
}
func ptr[T any](v T) *T { return &v }
func New(a *app.App, ev *events.Service) *Server {
	s := &Server{App: a, Events: ev}
	m := mcp.NewServer(&mcp.Implementation{Name: "code-factory", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: "Code Factory manages a GitHub issue/PR inbox and local Codex runs, not email. Open the repository dashboard with open_factory; no mailbox address or webmail URL is needed. Opening or listing never launches an agent. Use prepare_launch, review the snapshot, then start_run only when requested. Never place credentials in chat.", Capabilities: &mcp.ServerCapabilities{}, SupportedProtocolVersions: []string{"2026-07-28", "2025-11-25", "2025-06-18"}})
	s.MCP = m
	m.AddResource(&mcp.Resource{URI: ResourceURI, Name: "Code Factory", MIMEType: "text/html;profile=mcp-app"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: ResourceURI, MIMEType: "text/html;profile=mcp-app", Text: web.HTML, Meta: mcp.Meta{"ui": map[string]any{"csp": map[string]any{"connectDomains": []string{}, "resourceDomains": []string{}}}}}}}, nil
	})
	bind(m, "open_factory", "Open Code Factory’s GitHub issue and pull-request dashboard, repository settings, and local Codex agents. No email account is needed.", false, true, func(ctx context.Context, _ Empty) (any, error) { return s.snapshot(ctx) })
	bind(m, "list_inbox", "List synchronized issues and PRs without marking anything read.", false, true, func(ctx context.Context, in InboxInput) (any, error) { return a.Inbox(ctx, in.State) })
	bind(m, "read_item", "Read item detail. Does not mark read; the UI marks the displayed revision separately.", false, true, func(ctx context.Context, in IDInput) (any, error) { return s.detail(ctx, in.ID) })
	bind(m, "item_action", "Mark displayed revision read, unread, acknowledge, record action, or override priority.", false, false, func(ctx context.Context, in UserAction) (any, error) {
		return nil, a.UserAction(ctx, in.ID, in.Action, in.Revision, in.Priority)
	})
	bind(m, "get_settings", "Read configuration without credentials.", false, true, func(ctx context.Context, _ Empty) (any, error) { return a.Config(ctx) })
	bind(m, "save_settings", "Save priority, assessment, Jira tenant and notification settings.", false, false, func(ctx context.Context, in domain.Config) (any, error) { return nil, a.SaveConfig(ctx, in) })
	bind(m, "save_repository", "Configure a GitHub owner/repository and optional existing local checkout.", false, false, func(ctx context.Context, in RepositoryInput) (any, error) {
		return nil, a.SaveRepository(ctx, domain.Repository{ID: in.ID, Checkout: in.Checkout, Base: in.Base})
	})
	bind(m, "save_profile", "Create or edit a launch profile, prompt, permissions, skills and publishing authority.", false, false, func(ctx context.Context, in domain.Profile) (any, error) { return nil, a.SaveProfile(ctx, in) })
	bind(m, "set_jira_credential", "Store a Jira API token from the secure UI. Never return the credential.", true, false, func(ctx context.Context, in CredentialInput) (any, error) {
		if strings.TrimSpace(in.Token) == "" {
			return nil, fmt.Errorf("token required")
		}
		return nil, a.Vault.Set(ctx, "jira", in.Token)
	})
	bind(m, "set_relationship", "Add or remove a local item relationship; never changes GitHub or Jira.", false, false, func(ctx context.Context, in LinkInput) (any, error) {
		return nil, a.Link(ctx, domain.Relationship{Source: in.Source, Target: in.Target, Evidence: in.Evidence}, in.Remove)
	})
	bind(m, "prepare_launch", "Prepare an editable, durable launch snapshot. Does not start an agent.", false, false, func(ctx context.Context, in app.LaunchInput) (any, error) {
		r, e := a.Runs.Prepare(ctx, in)
		return publicRun(r), e
	})
	bind(m, "start_run", "Start a prepared launch or explicitly resume an interrupted run. Returns immediately.", false, false, func(ctx context.Context, in StartInput) (any, error) {
		r, e := a.Runs.Start(ctx, in.ID, in.Resume)
		return publicRun(r), e
	})
	bind(m, "list_runs", "List agent status and retained workspaces.", false, true, func(ctx context.Context, _ Empty) (any, error) {
		runs, e := store.List[domain.Run](ctx, a.Store, "run")
		for i := range runs {
			runs[i] = publicRun(runs[i])
		}
		return runs, e
	})
	bind(m, "read_run", "Read a run snapshot and pending attention requests.", false, true, func(ctx context.Context, in IDInput) (any, error) {
		var run domain.Run
		if e := a.Store.Get(ctx, "run", in.ID, &run); e != nil {
			return nil, e
		}
		atts, e := store.List[domain.Attention](ctx, a.Store, "attention")
		out := []domain.Attention{}
		for _, at := range atts {
			if at.RunID == in.ID && at.Status == "pending" {
				out = append(out, at)
			}
		}
		return map[string]any{"run": publicRun(run), "attention": out}, e
	})
	bind(m, "send_message", "Send follow-up chat; provide expectedTurn for an active turn to steer safely.", false, false, func(ctx context.Context, in MessageInput) (any, error) {
		return nil, a.Runs.Message(ctx, in.ID, in.Text, in.ExpectedTurn)
	})
	bind(m, "answer_request", "Answer a matching live question or approval once. Stale answers are rejected.", false, false, func(ctx context.Context, in AnswerInput) (any, error) {
		b, e := json.Marshal(in.Answer)
		if e != nil {
			return nil, e
		}
		return nil, a.Runs.Answer(ctx, in.ID, b)
	})
	bind(m, "interrupt_run", "Interrupt the active turn, preserving worktree and history.", false, false, func(ctx context.Context, in IDInput) (any, error) { return nil, a.Runs.Interrupt(ctx, in.ID) })
	bind(m, "cleanup_run", "Explicitly remove a stopped run's clean worktree. Refuses uncommitted changes.", false, false, func(ctx context.Context, in IDInput) (any, error) { return nil, a.Runs.Cleanup(ctx, in.ID) })
	bind(m, "discover_skills", "Discover local Codex skills for a checkout.", false, true, func(ctx context.Context, in SkillInput) (any, error) { return a.Runs.DiscoverSkills(ctx, in.CWD) })
	bind(m, "save_skill", "Create or edit a service-managed skill with YAML frontmatter.", false, false, func(ctx context.Context, in SaveSkillInput) (any, error) {
		return nil, a.Runs.SaveSkill(ctx, domain.Skill{Name: in.Name, Content: in.Content})
	})
	bind(m, "read_changes", "Read up to 200 incremental journal events after a cursor. App-only polling.", true, true, func(ctx context.Context, in Page) (any, error) {
		ev, e := a.Store.Events(ctx, in.After, in.Limit)
		next := in.After
		for _, v := range ev {
			next = v.Seq
		}
		return map[string]any{"events": ev, "cursor": next, "hasMore": next < a.Store.Latest(ctx)}, e
	})
	bind(m, "read_snapshot", "Read current UI state and a cursor for incremental updates.", true, true, func(ctx context.Context, _ Empty) (any, error) { return s.snapshot(ctx) })
	type TranscriptInput struct {
		ID    string `json:"id"`
		After int64  `json:"after,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	bind(m, "read_transcript", "Page durable agent output and diffs without loading the entire transcript.", true, true, func(ctx context.Context, in TranscriptInput) (any, error) {
		return a.Store.RunEvents(ctx, in.ID, in.After, in.Limit)
	})
	bind(m, "diagnostics", "Check daemon, providers, Codex compatibility and outstanding host setup blockers.", false, true, func(ctx context.Context, _ Empty) (any, error) { return s.diagnostics(ctx), nil })
	registerEvents(m, ev)
	return s
}
func (s *Server) snapshot(ctx context.Context) (any, error) {
	cursor := s.App.Store.Latest(ctx)
	inbox, e := s.App.Inbox(ctx, "all")
	if e != nil {
		return nil, e
	}
	repos, e := store.List[domain.Repository](ctx, s.App.Store, "repository")
	if e != nil {
		return nil, e
	}
	profiles, e := store.List[domain.Profile](ctx, s.App.Store, "profile")
	if e != nil {
		return nil, e
	}
	runs, e := store.List[domain.Run](ctx, s.App.Store, "run")
	if e != nil {
		return nil, e
	}
	for i := range runs {
		runs[i] = publicRun(runs[i])
	}
	atts, e := store.List[domain.Attention](ctx, s.App.Store, "attention")
	if e != nil {
		return nil, e
	}
	skills, e := store.List[domain.Skill](ctx, s.App.Store, "skill")
	if e != nil {
		return nil, e
	}
	config, e := s.App.Config(ctx)
	return map[string]any{"inbox": inbox, "repositories": repos, "profiles": profiles, "runs": runs, "attention": atts, "skills": skills, "config": config, "cursor": cursor}, e
}
func (s *Server) detail(ctx context.Context, id string) (any, error) {
	var item domain.Item
	if e := s.App.Store.Get(ctx, "item", id, &item); e != nil {
		return nil, e
	}
	links, e := store.List[domain.Relationship](ctx, s.App.Store, "relationship")
	if e != nil {
		return nil, e
	}
	out := []domain.Relationship{}
	tickets := []domain.Ticket{}
	for _, l := range links {
		if l.Source == id {
			out = append(out, l)
			var ticket domain.Ticket
			if s.App.Store.Get(ctx, "ticket", l.Target, &ticket) == nil {
				tickets = append(tickets, ticket)
			}
		}
	}
	var state domain.UserState
	if e = s.App.Store.Get(ctx, "user", id, &state); e != nil {
		return nil, e
	}
	return map[string]any{"item": item, "user": state, "relationships": out, "tickets": tickets}, nil
}
func (s *Server) diagnostics(ctx context.Context) setup.Diagnostics {
	return setup.Diagnose(ctx, s.App.DataDir, s.App.Runs.Binary, "", s.App.Store)
}

func (s *Server) Handler(token string) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.MCP }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || len(token) < 32 || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "owner authentication required", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		s.lastRequest.Store(time.Now().Unix())
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

type eventParams struct {
	mcp.ParamsBase
	events.SubscribeInput
}
type eventResult struct {
	mcp.ResultBase
	Fields map[string]any `json:"-"`
}

func (r *eventResult) MarshalJSON() ([]byte, error) {
	v := map[string]any{"resultType": "complete"}
	for k, val := range r.Fields {
		v[k] = val
	}
	if r.Meta != nil {
		v["_meta"] = r.Meta
	}
	return json.Marshal(v)
}

type discovery struct{ *mcp.DiscoverResult }

func (d *discovery) MarshalJSON() ([]byte, error) {
	b, e := json.Marshal(d.DiscoverResult)
	if e != nil {
		return nil, e
	}
	var v map[string]any
	if e = json.Unmarshal(b, &v); e != nil {
		return nil, e
	}
	caps, ok := v["capabilities"].(map[string]any)
	if !ok {
		caps = map[string]any{}
		v["capabilities"] = caps
	}
	caps["events"] = map[string]any{}
	return json.Marshal(v)
}
func registerEvents(m *mcp.Server, ev *events.Service) {
	m.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			r, e := next(ctx, method, req)
			if e == nil && method == "server/discover" {
				if d, ok := r.(*mcp.DiscoverResult); ok {
					return &discovery{d}, nil
				}
			}
			return r, e
		}
	})
	_ = mcp.AddReceivingCustomMethod(m, "events/list", func(context.Context, *mcp.ServerSession, *eventParams) (*eventResult, error) {
		return &eventResult{Fields: map[string]any{"events": events.Definitions()}}, nil
	})
	_ = mcp.AddReceivingCustomMethod(m, "events/subscribe", func(ctx context.Context, _ *mcp.ServerSession, in *eventParams) (*eventResult, error) {
		v, e := ev.Subscribe(ctx, in.SubscribeInput)
		if e != nil {
			var callback *events.CallbackError
			if errors.As(e, &callback) {
				data, _ := json.Marshal(map[string]string{"reason": callback.Reason})
				return nil, &jsonrpc.Error{Code: -32015, Message: e.Error(), Data: data}
			}
			return nil, &jsonrpc.Error{Code: -32602, Message: e.Error()}
		}
		return &eventResult{Fields: v}, nil
	})
	_ = mcp.AddReceivingCustomMethod(m, "events/unsubscribe", func(ctx context.Context, _ *mcp.ServerSession, in *eventParams) (*eventResult, error) {
		if e := ev.Unsubscribe(ctx, in.SubscribeInput); e != nil {
			return nil, e
		}
		return &eventResult{Fields: map[string]any{}}, nil
	})
}

func publicRun(r domain.Run) domain.Run {
	r.Skills = append([]domain.Skill{}, r.Skills...)
	for i := range r.Skills {
		r.Skills[i].Files = nil
		r.Skills[i].Content = ""
	}
	return r
}
