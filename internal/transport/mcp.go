package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/providers"
	"codefactory/internal/setup"
	"codefactory/internal/store"
	"codefactory/internal/web"
	"context"
	"crypto/sha256"
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

const legacyResourceURI = "ui://code-factory/inbox.html"

// ResourceURI identifies the embedded HTML bytes, including its bundled JS/CSS.
var ResourceURI = resourceURI(web.HTML)
var SettingsResourceURI = settingsResourceURI(web.SettingsHTML())

func resourceURI(html string) string {
	return fmt.Sprintf("ui://code-factory/inbox-%x.html", sha256.Sum256([]byte(html)))
}

func settingsResourceURI(html string) string {
	return fmt.Sprintf("ui://code-factory/settings-%x.html", sha256.Sum256([]byte(html)))
}

func uiResourceContents(uri, html string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/html;profile=mcp-app", Text: html, Meta: mcp.Meta{"ui": map[string]any{"csp": map[string]any{"connectDomains": []string{}, "resourceDomains": []string{}}}}}}}
}
func registerSettingsUIResource(m *mcp.Server, html string) string {
	uri := settingsResourceURI(html)
	m.AddResource(&mcp.Resource{URI: uri, Name: "Code Factory settings", MIMEType: "text/html;profile=mcp-app"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return uiResourceContents(uri, html), nil
	})
	return uri
}
func registerUIResource(m *mcp.Server, html string) string {
	uri := resourceURI(html)
	m.AddResource(&mcp.Resource{URI: uri, Name: "Code Factory", MIMEType: "text/html;profile=mcp-app"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return uiResourceContents(uri, html), nil
	})
	// Only the original inbox URI has a compatibility alias. Settings cannot alias it.
	m.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "resources/read" {
				if params, ok := req.GetParams().(*mcp.ReadResourceParams); ok && params != nil && params.URI == legacyResourceURI {
					return uiResourceContents(legacyResourceURI, html), nil
				}
			}
			return next(ctx, method, req)
		}
	})
	return uri
}

type Server struct {
	App         *app.App
	Events      *events.Service
	MCP         *mcp.Server
	lastRequest atomic.Int64
}
type Empty struct {
	Freshness string `json:"freshness,omitempty" jsonschema:"Unique read request marker to avoid reusing a host-cached response; does not change server state."`
}
type IDInput struct {
	ID        string `json:"id"`
	Freshness string `json:"freshness,omitempty" jsonschema:"Use a new marker when reading current state after waiting or a mutation."`
}
type Page struct {
	After     int64  `json:"after,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Freshness string `json:"freshness,omitempty" jsonschema:"Unique read request marker; ignored by journal pagination."`
}
type InboxInput struct {
	State string `json:"state,omitempty"`
	Queue string `json:"queue,omitempty"`
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
type PRReviewInput struct {
	ItemID string `json:"itemId,omitempty"`
	Target string `json:"target,omitempty"`
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
	fields                  map[string]json.RawMessage
	AutoReview              *bool  `json:"autoReview,omitempty"`
	ReviewPrompt            string `json:"reviewPrompt,omitempty"`
	Paused                  bool   `json:"paused,omitempty"`
	ReviewProfileID         string `json:"reviewProfileId,omitempty"`
	ImplementationProfileID string `json:"implementationProfileId,omitempty"`
	ID                      string `json:"id"`
	Checkout                string `json:"checkout,omitempty"`
	Base                    string `json:"base,omitempty"`
}

// Presence-aware updates preserve settings from older cached widgets. Null is an
// explicit repository inheritance choice, while an omitted field is unchanged.
func (in *RepositoryInput) UnmarshalJSON(b []byte) error {
	type plain RepositoryInput
	if err := json.Unmarshal(b, (*plain)(in)); err != nil {
		return err
	}
	return json.Unmarshal(b, &in.fields)
}

type SettingsInput struct {
	domain.Config
	fields map[string]json.RawMessage
}

func (in *SettingsInput) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &in.Config); err != nil {
		return err
	}
	return json.Unmarshal(b, &in.fields)
}

type SaveSkillInput struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}
type SkillInput struct {
	CWD string `json:"cwd"`
}

func bind[I any](s *mcp.Server, resourceURI, name, desc string, appOnly, readOnly bool, f func(context.Context, I) (any, error)) {
	vis := []string{"model", "app"}
	if appOnly {
		vis = []string{"app"}
	}
	// A resource URI asks MCP Apps hosts to render a widget. Only explicit
	// openers carry it; status/launch/answer calls stay conversational while
	// remaining callable from the embedded app through visibility metadata.
	meta := mcp.Meta{"ui": map[string]any{"visibility": vis}}
	if name == "open_factory" || name == "open_settings" {
		meta["ui"].(map[string]any)["resourceUri"] = resourceURI
		meta["openai/outputTemplate"] = resourceURI
	}
	openWorld := name == "start_run" || name == "send_message" || name == "answer_request" || name == "manage_codex_plugins"
	destructive := name == "cleanup_run" || name == "manage_codex_plugins"
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc, Meta: meta, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, OpenWorldHint: ptr(openWorld), DestructiveHint: ptr(destructive)}}, func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, map[string]any, error) {
		v, e := f(ctx, in)
		if e != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: e.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name + " returned saved state or operation result; inspect structuredContent.data."}}}, map[string]any{"data": v}, nil
	})
}
func ptr[T any](v T) *T { return &v }
func New(a *app.App, ev *events.Service) *Server {
	s := &Server{App: a, Events: ev}
	m := mcp.NewServer(&mcp.Implementation{Name: "code-factory", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: "Code Factory manages a GitHub issue/PR inbox and local Codex runs, not email. Open the repository dashboard with open_factory and the separate configuration view with open_settings; no mailbox address or webmail URL is needed. Opening or listing never launches an agent. For checking reviews/agents and publication use track_work; omit target and filter queue for triage. Use track_work then send_message for follow-up. Open the dashboard only when explicitly requested. For agents waiting for human input, call list_attention with a fresh marker, show the exact questions and options, and use answer_request only with the user’s answer and the request ID. Never invent an answer, replay uncertain delivery or collect secrets in chat. Use returned delivery state and pending attention; a queued message is saved, not yet delivered, and accepted does not mean complete. On uncertainty use track_work, never replay. To run a PR review pipeline: call prepare_pr_review with target URL or repository shorthand, inspect the returned profile, skills, prompt and authority, then call start_run with the returned run id when authorized. prepare_pr_review resolves repository then system review defaults automatically. Use prepare_launch for other or customized launches. An explicit request to run a review authorizes starting the matching prepared review; ask only if authority or scope needs clarification. Successful start_run establishes launch acceptance; answer_request establishes local answer delivery. send_message saves the message and establishes queue acceptance only; its messageDelivery state tracks later agent delivery. These responses include pending attention. Report that returned state without adding a verification read or polling in the same launch/answer/message workflow; acceptance/delivery does not establish task completion. Use track_work for later user-requested status or uncertain responses. Never place credentials in chat.", Capabilities: &mcp.ServerCapabilities{}, SupportedProtocolVersions: []string{"2026-07-28", "2025-11-25", "2025-06-18"}})
	s.MCP = m
	uri := registerUIResource(m, web.HTML)
	settingsURI := registerSettingsUIResource(m, web.SettingsHTML())
	bind(m, settingsURI, "open_settings", "Open Code Factory settings in its own dedicated view. Configure repositories, priorities, profiles and skills here; settings are not part of the inbox or agent UI.", false, true, func(ctx context.Context, _ Empty) (any, error) { return s.settingsSnapshot(ctx) })
	bind(m, uri, "open_factory", "Open Code Factory’s GitHub issue and pull-request dashboard and local Codex agents. No email account is needed.", false, true, func(ctx context.Context, _ Empty) (any, error) { return s.dashboard(ctx) })
	bind(m, uri, "track_work", "Check an agent or review, triage a queue, or verify GitHub publication in one local saved-state read. Target accepts run ID, canonical item ID, GitHub URL or repository shorthand (python-driver#725); ambiguous identifiers return candidates without selecting or starting. Returns current/retained runs, exact pending questions, bounded latest output, continuation IDs and current/earlier-head receipts. Omit target for paginated queue triage. Follow nextCursor, attention.nextCursor or nextReceiptCursor independently. freshness bypasses host memoization, never GitHub policy. Stale/missing observations refresh in background; unknown stays unknown. Dashboard opening is optional.", false, true, func(ctx context.Context, in app.TrackInput) (any, error) { return a.TrackWork(ctx, in) })
	bind(m, uri, "list_inbox", "List synchronized issues and PRs, optionally filtering queue (working, issues, mine, review), without marking read.", false, true, func(ctx context.Context, in InboxInput) (any, error) {
		if in.Queue != "" && in.Queue != "all" && in.Queue != "working" && in.Queue != "issues" && in.Queue != "mine" && in.Queue != "review" {
			return nil, fmt.Errorf("unknown queue")
		}
		entries, err := a.InboxSummaries(ctx, in.State)
		if err != nil {
			return nil, err
		}
		out := []domain.InboxEntry{}
		for _, entry := range entries {
			if app.ContainsQueue(entry, in.Queue) {
				out = append(out, entry)
			}
		}
		return inboxSummaries(out), nil
	})
	bind(m, uri, "read_item", "Read saved item detail immediately with observation time and background refresh state. freshness never forces GitHub. Does not mark read; the UI marks the displayed revision separately.", false, true, func(ctx context.Context, in IDInput) (any, error) { return s.detail(ctx, in.ID) })
	bind(m, uri, "read_pr_reviews", "Read GitHub-verified posted reviews for a synchronized PR item id: receipt URL, author, verdict, reviewed commit, submitted time, body and inline comments. Includes all authors; does not attribute them to a run. syncedAt, refresh, error and retryAt describe saved observations and background refresh. Pending drafts and agent publication claims are excluded. Returns immediately and schedules stale/missing observations in the background; never posts, retries publication or starts work.", false, true, func(ctx context.Context, in IDInput) (any, error) { return a.ReadPRReviews(ctx, in.ID) })
	bind(m, uri, "item_action", "Mark displayed revision read, unread, acknowledge, record action, or override priority.", false, false, func(ctx context.Context, in UserAction) (any, error) {
		return nil, a.UserAction(ctx, in.ID, in.Action, in.Revision, in.Priority)
	})
	bind(m, settingsURI, "get_settings", "Read configuration without credentials.", false, true, func(ctx context.Context, _ Empty) (any, error) { return a.Config(ctx) })
	bind(m, settingsURI, "save_settings", "Save GitHub request budget (60–5000/hour), reserve (0–5000), notification interval (60–3600s), review/pipeline defaults, plugin check cadence, priorities and notifications. Omitted new admin fields are preserved.", false, false, func(ctx context.Context, in SettingsInput) (any, error) {
		old, err := a.Config(ctx)
		if err != nil {
			return nil, err
		}
		c := in.Config
		if _, ok := in.fields["autoReview"]; !ok {
			c.AutoReview = old.AutoReview
		}
		if _, ok := in.fields["reviewProfileId"]; !ok {
			c.ReviewProfileID = old.ReviewProfileID
		}
		if _, ok := in.fields["implementationProfileId"]; !ok {
			c.ImplementationProfileID = old.ImplementationProfileID
		}
		if _, ok := in.fields["githubRequestsPerHour"]; !ok {
			c.GitHubRequestsPerHour = old.GitHubRequestsPerHour
		}
		if _, ok := in.fields["githubReserve"]; !ok {
			c.GitHubReserve = old.GitHubReserve
		}
		if _, ok := in.fields["githubPollSeconds"]; !ok {
			c.GitHubPollSeconds = old.GitHubPollSeconds
		}
		if _, ok := in.fields["pluginCheckMinutes"]; !ok {
			c.PluginCheckMinutes = old.PluginCheckMinutes
		}
		return nil, a.SaveConfig(ctx, c)
	})
	bind(m, settingsURI, "read_plugin_admin", "Read cached Codex plugin, marketplace, version and operation status without installing anything.", false, true, func(ctx context.Context, in app.PluginAdminInput) (any, error) { return a.Runs.PluginState(ctx, in) })
	bind(m, settingsURI, "manage_codex_plugins", "Manage Codex plugins: refresh, check-updates, discover-skills, add-marketplace, install, update or remove. Mutations require explicit user choice. Returns a durable operation; poll read_plugin_admin and never replay uncertain operations.", false, false, func(ctx context.Context, in app.PluginAction) (any, error) { return a.Runs.PluginAction(ctx, in) })
	bind(m, settingsURI, "save_repository", "Configure repository monitoring, role pipelines and additional review instructions. Omitted fields are preserved; null autoReview explicitly inherits the system default.", false, false, func(ctx context.Context, in RepositoryInput) (any, error) {
		r := domain.Repository{ID: in.ID}
		if err := a.Store.Get(ctx, "repository", in.ID, &r); err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if _, ok := in.fields["checkout"]; ok {
			r.Checkout = in.Checkout
		}
		if _, ok := in.fields["base"]; ok {
			r.Base = in.Base
		}
		if _, ok := in.fields["paused"]; ok {
			r.Paused = in.Paused
		}
		if _, ok := in.fields["reviewProfileId"]; ok {
			r.ReviewProfileID = in.ReviewProfileID
		}
		if _, ok := in.fields["implementationProfileId"]; ok {
			r.ImplementationProfileID = in.ImplementationProfileID
		}
		if _, ok := in.fields["autoReview"]; ok {
			r.AutoReview = in.AutoReview
		}
		if _, ok := in.fields["reviewPrompt"]; ok {
			r.ReviewPrompt = in.ReviewPrompt
		}
		return nil, a.SaveRepository(ctx, r)
	})
	bind(m, settingsURI, "sync_repositories", "Poll configured repositories now. Repositories opted into automatic review may launch their selected review pipeline.", false, false, func(ctx context.Context, _ Empty) (any, error) {
		return nil, a.Sync(providers.WithGitHubRead(ctx, providers.RequireLive))
	})
	bind(m, settingsURI, "save_profile", "Create or edit a launch profile, prompt, permissions, skills and publishing authority.", false, false, func(ctx context.Context, in domain.Profile) (any, error) { return nil, a.SaveProfile(ctx, in) })
	bind(m, settingsURI, "set_jira_credential", "Store a Jira API token from the secure UI. Never return the credential.", true, false, func(ctx context.Context, in CredentialInput) (any, error) {
		if strings.TrimSpace(in.Token) == "" {
			return nil, fmt.Errorf("token required")
		}
		return nil, a.Vault.Set(ctx, "jira", in.Token)
	})
	bind(m, uri, "set_relationship", "Add or remove a local item relationship; never changes GitHub or Jira.", false, false, func(ctx context.Context, in LinkInput) (any, error) {
		return nil, a.Link(ctx, domain.Relationship{Source: in.Source, Target: in.Target, Evidence: in.Evidence}, in.Remove)
	})
	bind(m, uri, "prepare_pr_review", "Prepare the configured PR review pipeline for a synchronized PR. Pass target or itemId as a run ID, canonical item ID, GitHub URL or repository shorthand. Ambiguity returns candidates without preparing. Resolves repository reviewProfileId, then system default, then review; retains selected skills, model, pinned PR head and repository review prompt. Accepts read-only/ask authority or an explicitly configured full-access/never/authorized profile. Does not start work or call GitHub. Inspect the returned run, then use start_run with its id when the user requested a review. Use read_run/list_runs to check uncertain starts; never automatically replay them.", false, false, func(ctx context.Context, in PRReviewInput) (any, error) {
		target := in.ItemID
		if in.Target != "" {
			target = in.Target
		}
		resolved, e := a.ResolveTarget(ctx, target)
		if e != nil {
			return nil, e
		}
		if resolved.Status != "resolved" {
			return resolved, nil
		}
		r, e := a.Runs.PreparePRReview(ctx, resolved.ItemID)
		return publicRun(r), e
	})
	bind(m, uri, "prepare_launch", "Prepare an editable durable launch snapshot using target (run/item ID, GitHub URL or repository shorthand) or legacy itemId, and profileId. Ambiguity returns candidates without preparing. For a PR's configured review pipeline prefer prepare_pr_review; use this tool for investigation, implementation or explicit custom choices. Does not start an agent; inspect the result then call start_run with its id when authorized.", false, false, func(ctx context.Context, in app.LaunchInput) (any, error) {
		target := in.ItemID
		if in.Target != "" {
			target = in.Target
		}
		resolved, e := a.ResolveTarget(ctx, target)
		if e != nil {
			return nil, e
		}
		if resolved.Status != "resolved" {
			return resolved, nil
		}
		in.ItemID = resolved.ItemID
		in.Target = ""
		r, e := a.Runs.Prepare(ctx, in)
		return publicRun(r), e
	})
	bind(m, uri, "start_run", "Start the prepared PR review pipeline or other launch using the id returned by prepare_pr_review/prepare_launch. An explicit user request to run that review authorizes Start after inspecting its snapshot. Returns acceptance, run/turn IDs and pending attention. A successful response confirms launch acceptance: report it without adding a verification read or polling. Use track_work for a later user-requested status check or an uncertain response. On an uncertain result inspect that same run before any explicit retry. Also supports explicit resume of an interrupted run.", false, false, func(ctx context.Context, in StartInput) (any, error) {
		r, e := a.Runs.Start(ctx, in.ID, in.Resume)
		if e != nil {
			return nil, e
		}
		out := s.operationState(ctx, r.ID, "accepted")
		snapshot, _ := json.Marshal(publicRun(r))
		var legacy map[string]any
		_ = json.Unmarshal(snapshot, &legacy)
		for key, value := range legacy {
			out[key] = value
		}
		return out, nil
	})
	bind(m, uri, "list_runs", "List agent status and retained workspaces.", false, true, func(ctx context.Context, _ Empty) (any, error) {
		runs, e := store.ListWithout[domain.Run](ctx, a.Store, "run", "$.skills", "$.prompt", "$.profile.prompt", "$.sourceSnapshot", "$.reviewPromptSnapshot")
		for i := range runs {
			runs[i] = publicRun(runs[i])
		}
		return runs, e
	})
	bind(m, uri, "list_attention", "List the Messages queue of unanswered agent questions and approvals, oldest first, with request id, exact question IDs/options and owning run/item/profile. Includes retained textual asks; excludes answered, stale and orphaned requests. Blocking requests pause the agent; nonblocking requests do not. Use runId to filter, nextCursor for more pages, and a new freshness marker on fresh reads. Read only: never answers or starts work. Ask the user before answering; send the request.id, not run.id, to answer_request.", false, true, func(ctx context.Context, in app.AttentionInput) (any, error) { return a.ListAttention(ctx, in) })
	bind(m, uri, "read_run", "Read a run snapshot, pending attention and saved postedReviews for its PR. Run completion is not publication proof. Use read_pr_reviews with run.itemId to refresh verified receipts, verdicts and findings; reviews include all authors and are not attributed to this run.", false, true, func(ctx context.Context, in IDInput) (any, error) {
		runs, e := store.Runs(ctx, a.Store, "", in.ID)
		if e != nil {
			return nil, e
		}
		if len(runs) == 0 {
			return nil, store.ErrNotFound
		}
		run := runs[0]
		atts, e := store.Select[domain.Attention](ctx, a.Store, "attention", "runId", in.ID)
		out := []domain.Attention{}
		for _, at := range atts {
			if at.RunID == in.ID && at.Status == "pending" {
				out = append(out, at)
			}
		}
		recoveries, err := store.Select[domain.QuestionRecovery](ctx, a.Store, "question-recovery", "runId", in.ID)
		if err != nil {
			return nil, err
		}
		runRecoveries := []domain.QuestionRecovery{}
		for _, r := range recoveries {
			if r.RunID == in.ID {
				runRecoveries = append(runRecoveries, r)
			}
		}
		reviews, err := a.SavedPRReviews(ctx, run.ItemID)
		if err != nil {
			return nil, err
		}
		delivery, e := a.MessageDelivery(ctx, in.ID)
		return map[string]any{"run": publicRun(run), "attention": out, "questionRecoveries": runRecoveries, "postedReviews": reviews, "messageDelivery": delivery}, e
	})
	bind(m, uri, "send_message", "Save a message durably for background delivery to the same agent, restoring its retained session when needed. No GitHub or agent request is required for acceptance. Provide expectedTurn from track_work for an active turn. Returns delivery=accepted and messageDelivery.state=queued, not proof of agent delivery or completion. Inspect track_work later for delivered or uncertain status; never replay an uncertain message.", false, false, func(ctx context.Context, in MessageInput) (any, error) {
		delivery, e := a.Runs.QueueMessage(ctx, in.ID, in.Text, in.ExpectedTurn)
		if e != nil {
			return nil, e
		}
		out := s.operationState(ctx, in.ID, "accepted")
		out["messageId"] = delivery.ID
		out["messageDelivery"] = delivery
		return out, nil
	})
	bind(m, uri, "answer_request", "Answer one pending request from list_attention/read_run, using its request id (not run id). For questions send answer: {answers: {questionId: {answers: [userAnswer]}}}, including each exact question ID and selected option label or permitted free text. Ask the user; never invent their answer or approval. Secret answers belong in the embedded form, not chat. Stale/duplicate answers reject; after uncertain delivery use track_work instead of replaying. A successful result includes delivery and pending attention for local verification. Valid answers continue the originating session.", false, false, func(ctx context.Context, in AnswerInput) (any, error) {
		b, e := json.Marshal(in.Answer)
		if e != nil {
			return nil, e
		}
		var request domain.Attention
		if e = a.Store.Get(ctx, "attention", in.ID, &request); e != nil {
			return nil, e
		}
		if e = a.Runs.Answer(ctx, in.ID, b); e != nil {
			return nil, e
		}
		out := s.operationState(ctx, request.RunID, "delivered")
		out["requestId"] = in.ID
		return out, nil
	})
	bind(m, uri, "interrupt_run", "Interrupt the active turn, preserving worktree and history.", false, false, func(ctx context.Context, in IDInput) (any, error) { return nil, a.Runs.Interrupt(ctx, in.ID) })
	bind(m, uri, "dismiss_question", "Dismiss a textual fallback that needs no reply. Does not answer, authorize or advance the agent.", true, false, func(ctx context.Context, in IDInput) (any, error) { return nil, a.Runs.DismissQuestion(ctx, in.ID) })
	bind(m, uri, "cleanup_run", "Explicitly remove a stopped run's clean worktree. Refuses uncommitted changes.", false, false, func(ctx context.Context, in IDInput) (any, error) { return nil, a.Runs.Cleanup(ctx, in.ID) })
	bind(m, settingsURI, "discover_skills", "Discover local Codex skills for a checkout.", false, true, func(ctx context.Context, in SkillInput) (any, error) { return a.Runs.DiscoverSkills(ctx, in.CWD) })
	bind(m, settingsURI, "save_skill", "Create or edit a service-managed skill with YAML frontmatter.", false, false, func(ctx context.Context, in SaveSkillInput) (any, error) {
		return nil, a.Runs.SaveSkill(ctx, domain.Skill{Name: in.Name, Content: in.Content})
	})
	bind(m, uri, "read_changes", "Read up to 200 incremental journal events after a cursor. App-only polling.", true, true, func(ctx context.Context, in Page) (any, error) {
		ev, e := a.Store.Events(ctx, in.After, in.Limit)
		next := in.After
		for _, v := range ev {
			next = v.Seq
		}
		return map[string]any{"events": ev, "cursor": next, "hasMore": next < a.Store.Latest(ctx)}, e
	})
	bind(m, uri, "read_snapshot", "Read current UI state and a cursor for incremental updates.", true, true, func(ctx context.Context, _ Empty) (any, error) { return s.snapshot(ctx) })
	type TranscriptInput struct {
		ID        string `json:"id"`
		After     int64  `json:"after,omitempty"`
		Limit     int    `json:"limit,omitempty"`
		Freshness string `json:"freshness,omitempty" jsonschema:"Unique read request marker; ignored by transcript pagination."`
	}
	bind(m, uri, "read_transcript", "Page durable redacted agent output and diffs without loading the entire transcript; use a track_work continuation run ID. Follow cursor and hasMore for full retained output.", false, true, func(ctx context.Context, in TranscriptInput) (any, error) {
		return a.Store.RunEvents(ctx, in.ID, in.After, in.Limit)
	})
	bind(m, settingsURI, "diagnostics", "Check daemon, providers, Codex compatibility and outstanding host setup blockers.", false, true, func(ctx context.Context, _ Empty) (any, error) { return s.diagnostics(ctx), nil })
	registerEvents(m, ev)
	return s
}

// Full activity and raw check payloads are available through read_item. They
// can be many megabytes across a repository and do not belong in every poll.
func inboxSummaries(entries []domain.InboxEntry) []domain.InboxEntry {
	for i := range entries {
		entries[i].Item.Activity = []domain.Activity{}
		entries[i].Item.Checks = nil
		if pr := entries[i].Item.PR; pr != nil {
			copy := *pr
			copy.Checks = []domain.Check{}
			entries[i].Item.PR = &copy
		}
		linked := append([]domain.PullRequest(nil), entries[i].LinkedPullRequests...)
		for n := range linked {
			linked[n].Checks = []domain.Check{}
		}
		entries[i].LinkedPullRequests = linked
	}
	return entries
}

func (s *Server) snapshot(ctx context.Context) (map[string]any, error) {
	cursor := s.App.Store.Latest(ctx)
	inbox, e := s.App.InboxSummaries(ctx, "all")
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
	runs, e := store.Runs(ctx, s.App.Store, "", "")
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
	recoveries, e := store.List[domain.QuestionRecovery](ctx, s.App.Store, "question-recovery")
	if e != nil {
		return nil, e
	}
	skills, e := store.ListWithout[domain.Skill](ctx, s.App.Store, "skill", "$.files", "$.content")
	if e != nil {
		return nil, e
	}
	var viewer domain.GitHubViewer
	if err := s.App.Store.Get(ctx, "identity", "github", &viewer); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	config, e := s.App.Config(ctx)
	if e != nil {
		return nil, e
	}
	reviews, e := store.ListWithout[domain.PRReviews](ctx, s.App.Store, "pr-reviews", "$.reviews")
	versions := map[string]string{}
	for _, r := range reviews {
		versions[r.ItemID] = r.LastAttempt.Format(time.RFC3339Nano)
	}
	return map[string]any{"viewer": viewer, "inbox": inboxSummaries(inbox), "repositories": repos, "profiles": profiles, "runs": runs, "attention": atts, "questionRecoveries": recoveries, "skills": skills, "config": config, "reviewVersions": versions, "cursor": cursor}, e
}

// The model-visible settings entry does not disclose unrelated task/run contents.
func (s *Server) settingsSnapshot(ctx context.Context) (map[string]any, error) {
	cursor := s.App.Store.Latest(ctx)
	repos, err := store.List[domain.Repository](ctx, s.App.Store, "repository")
	if err != nil {
		return nil, err
	}
	profiles, err := store.List[domain.Profile](ctx, s.App.Store, "profile")
	if err != nil {
		return nil, err
	}
	skills, err := store.ListWithout[domain.Skill](ctx, s.App.Store, "skill", "$.files")
	if err != nil {
		return nil, err
	}
	config, err := s.App.Config(ctx)
	if err != nil {
		return nil, err
	}
	usage, err := s.App.GitHubStatus(ctx)
	return map[string]any{"githubUsage": usage, "repositories": repos, "profiles": profiles, "skills": skills, "config": config, "cursor": cursor, "inbox": []any{}, "runs": []any{}, "attention": []any{}}, err
}

func (s *Server) detail(ctx context.Context, id string) (any, error) {
	return s.App.ItemDetail(ctx, id)
}
func (s *Server) diagnostics(ctx context.Context) setup.Diagnostics {
	return setup.Diagnose(ctx, s.App.DataDir, s.App.Runs.CodexBinary(), "", s.App.Store)
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
	r.SourceSnapshot = nil
	r.Skills = append([]domain.Skill{}, r.Skills...)
	for i := range r.Skills {
		r.Skills[i].Files = nil
		r.Skills[i].Content = ""
	}
	return r
}
