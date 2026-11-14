package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/factory"
	"codefactory/internal/setup"
	"codefactory/internal/store"
	"codefactory/internal/web"
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewFactory exposes only session/configuration and retained triage interfaces.
// Pipeline launch, retained review publication and report orchestration are absent.
func NewFactory(a *app.App, f *factory.Service, ev *events.Service) *Server {
	s := &Server{App: a, Events: ev}
	m := mcp.NewServer(&mcp.Implementation{Name: "code-factory", Version: "0.4.0"}, &mcp.ServerOptions{Instructions: "API version 2. First call open_factory or open_settings for bootstrap instanceId. Reads return ready/value or pending; poll pending reads every 250 ms. Mutations, diagnostics and native inspection return operation receipts: use a new client-generated requestId with bootstrap instanceId, poll operation_status, and never describe queued/running/waiting acceptance as delivery. A waiting synchronization reports retryReason and retryAt and resumes its safe reads automatically; keep saved data visible. Repeated IDs retrieve the original operation; lost or uncertain work requires inspection, never automatic replay. Resolve large JSON document descriptors using versioned document_chunk calls. Code Factory routes configured GitHub events to one retained Codex conversation per PR. Listing and opening do not launch agents. Use session_chat to start or steer authorized work, session_control to run/resume/interrupt/clean up, and session_questions plus answer_session_question for exact human answers and approvals. Inspect uncertain delivery before retrying. Skills own execution and publishing. Use configuration_chat for settings changes, inspect concrete proposals, and obtain explicit user confirmation before confirm_configuration. Never invent answers or confirmation. Native Codex configuration and authentication are reused.", SupportedProtocolVersions: []string{"2026-07-28", "2025-11-25", "2025-06-18"}})
	s.MCP = m
	registerToolBudget(m)
	uri := registerUIResource(m, web.HTML)
	settingsURI := registerSettingsUIResource(m, web.SettingsHTML())
	s.documents = &documentCache{}
	s.operations = newOperationQueue(a.Store, s.documents)
	s.projections = newProjectionCache(a.Store, s.documents)
	cache := &factoryInboxCache{}
	bind(s, uri, "operation_status", "Read an operation by its original instance and request identity; never replay lost or uncertain work.", false, true, func(_ context.Context, in struct {
		Empty
		InstanceID string `json:"instanceId"`
		RequestID  string `json:"requestId"`
	}) (any, error) {
		return s.operations.status(in.InstanceID, in.RequestID), nil
	})
	bind(s, uri, "document_chunk", "Read one immutable JSON document chunk (32 KiB base64); reassemble all chunks of the exact version before using functional data.", false, true, func(_ context.Context, in struct {
		Empty
		Version string `json:"version"`
		Index   int    `json:"index"`
	}) (any, error) {
		return s.documents.chunk(in.Version, in.Index)
	})
	state := func(ctx context.Context, in factoryStateInput) (any, error) {
		return factoryState(ctx, a, in, cache)
	}
	bind(s, uri, "open_factory", "Open the GitHub inbox and retained sessions.", false, true, state)
	bind(s, settingsURI, "open_settings", "Open persistent configuration chat and effective settings.", false, true, func(ctx context.Context, _ Empty) (any, error) {
		return state(ctx, factoryStateInput{View: "configuration"})
	})
	bind(s, uri, "factory_state", "Read one bounded bodyless inbox page and compact session/question status for the requested view. Full source is available through factory_item; configuration payloads are returned only for that view. Viewing never launches work.", false, true, state)
	bind(s, uri, "factory_item", "Read a source item and its local relationships without marking it read or flushing events.", false, true, func(ctx context.Context, in IDInput) (any, error) {
		var item domain.Item
		if err := projectionStore(ctx, a.Store).Get(ctx, "item", in.ID, &item); err != nil {
			return nil, err
		}
		links, err := store.Select[domain.Relationship](ctx, projectionStore(ctx, a.Store), "relationship", "related", item.ID)
		if err != nil {
			return nil, err
		}
		visible := []domain.Relationship{}
		for _, l := range links {
			if l.Source == item.ID || l.Target == item.ID {
				visible = append(visible, l)
			}
		}
		return map[string]any{"item": item, "relationships": visible, "detailVersion": factoryDetailVersion(item.Revision, visible)}, nil
	})
	type Chat struct {
		ItemID string         `json:"itemId"`
		Text   string         `json:"text"`
		Agent  *factory.Agent `json:"agent,omitempty"`
	}
	bind(s, uri, "session_chat", "Send an authorized message to an item's canonical conversation. Bypasses quiet time and attaches queued events; native blocking requests must be answered first. Agent override is allowed only on first creation.", false, false, func(ctx context.Context, in Chat) (any, error) { return f.Chat(ctx, in.ItemID, in.Text, in.Agent) })
	bind(s, settingsURI, "configuration_chat", "Chat with the persistent read/propose configuration agent. It cannot confirm proposals.", false, false, func(ctx context.Context, in struct {
		Text string `json:"text"`
	}) (any, error) {
		return f.Chat(ctx, "configuration", in.Text, nil)
	})
	bind(s, uri, "session_control", "Explicit run, resume, interrupt or cleanup. Done and GitHub closure retain history. Cleanup refuses dirty worktrees.", false, false, func(ctx context.Context, in struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}) (any, error) {
		return f.Control(ctx, in.ID, in.Action)
	})
	bind(s, uri, "session_questions", "Read a bounded page of exact native questions/approvals or explicit status requests. Filter by sessionId and status (pending by default); page through total without truncating payloads.", false, true, func(ctx context.Context, in questionPageInput) (any, error) {
		return questionPage(ctx, projectionStore(ctx, a.Store), in)
	})

	bind(s, uri, "answer_session_question", "Deliver the human's answer once. Stale, duplicate and uncertain answers are never replayed.", false, false, func(ctx context.Context, in struct {
		ID     string         `json:"id"`
		Answer map[string]any `json:"answer"`
	}) (any, error) {
		answer, err := json.Marshal(in.Answer)
		if err != nil {
			return nil, err
		}
		return map[string]bool{"delivered": true}, f.Answer(ctx, in.ID, answer)
	})
	bind(s, uri, "resolve_session_delivery", "Explicitly retry or discard an uncertain batch only after retained history reconciliation and a human decision.", true, false, func(ctx context.Context, in struct {
		ID       string `json:"id"`
		Decision string `json:"decision"`
	}) (any, error) {
		return nil, f.ResolveDelivery(ctx, in.ID, in.Decision)
	})
	bind(s, uri, "factory_events", "Inspect one bounded page of compact events, deliveries, batches or ordered rule metadata. Raw webhook bodies, batch context and rule instructions are excluded; use conversation or configuration for current detail. Viewing never launches work.", false, true, func(ctx context.Context, in factoryEventsInput) (any, error) {
		return factoryEvents(ctx, a, in)
	})
	bind(s, uri, "session_transcript", "Read a bounded page of retained session output and batch delivery messages.", false, true, func(ctx context.Context, in struct {
		Empty
		ID    string `json:"id"`
		After int64  `json:"after,omitempty"`
	}) (any, error) {
		return projectionStore(ctx, a.Store).SessionEvents(ctx, in.ID, in.After, 100)
	})
	bind(s, settingsURI, "inspect_configuration", "Read actual native Codex configuration layers for a checkout. No model turn or file write.", true, true, func(ctx context.Context, in struct {
		Empty
		CWD string `json:"cwd"`
	}) (any, error) {
		return f.NativeConfig(ctx, in.CWD)
	})
	bind(s, settingsURI, "propose_configuration", "Prepare a concrete application settings diff or native key edits; requires separate explicit user confirmation.", true, false, func(ctx context.Context, in struct {
		Settings *factory.Settings   `json:"settings,omitempty"`
		Native   *factory.NativeEdit `json:"native,omitempty"`
	}) (any, error) {
		return f.Propose(ctx, in.Settings, in.Native)
	})
	bind(s, settingsURI, "confirm_configuration", "Apply or reject the displayed proposal only with explicit user confirmation. Confirmed edits wait for safe turn boundaries and stale proposals fail.", true, false, func(ctx context.Context, in struct {
		ID      string `json:"id"`
		Confirm bool   `json:"confirm"`
	}) (any, error) {
		return f.Confirm(ctx, in.ID, in.Confirm)
	})
	bind(s, uri, "user_action", "Change local priority override or unread/new/action state.", true, false, func(ctx context.Context, in UserAction) (any, error) {
		return nil, a.UserAction(ctx, in.ID, in.Action, in.Revision, in.Priority)
	})
	bind(s, uri, "link_items", "Add or remove a manual issue/PR relationship without changing GitHub.", true, false, func(ctx context.Context, in LinkInput) (any, error) {
		return nil, a.Link(ctx, domain.Relationship{ID: in.Source + ":" + in.Target, Source: in.Source, Target: in.Target, Kind: "implementation", Evidence: in.Evidence, Manual: true}, in.Remove)
	})
	bind(s, uri, "sync_repositories", "Reconcile configured repositories through the existing authenticated, budgeted provider. Concurrent scans share one operation; throttling reports waiting with retryReason and retryAt and resumes safe reads automatically.", true, false, func(ctx context.Context, _ Empty) (any, error) { return nil, a.Sync(ctx) })
	bind(s, settingsURI, "diagnostics", "Inspect local connection, authentication and synchronization health without a model turn.", false, true, func(ctx context.Context, _ Empty) (any, error) {
		return setup.Diagnose(ctx, a.DataDir, f.Binary, "", a.Store), nil
	})
	if ev != nil {
		registerEvents(m, ev)
	}
	return s
}
