package transport

import (
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/events"
	"codefactory/internal/httpbudget"
	"codefactory/internal/web"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ResourceURI = resourceURI(web.HTML)
var SettingsResourceURI = settingsResourceURI(web.SettingsHTML())

type Server struct {
	apiLogOnce    sync.Once
	apiLogWorkers sync.WaitGroup
	apiLog        *apiCallLog
	App           *app.App
	Events        *events.Service
	MCP           *mcp.Server
	lastRequest   atomic.Int64
	operations    *operationQueue
	projections   *projectionCache
	documents     *documentCache
}
type Empty struct {
	Freshness string `json:"freshness,omitempty"`
}
type IDInput struct {
	ID        string `json:"id"`
	Freshness string `json:"freshness,omitempty"`
}
type UserAction struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	Revision string `json:"revision,omitempty"`
	Priority *int   `json:"priority,omitempty"`
}
type LinkInput struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Evidence string `json:"evidence,omitempty"`
	Remove   bool   `json:"remove,omitempty"`
}
type eventParams struct {
	mcp.ParamsBase
	events.SubscribeInput
}
type eventResult struct {
	mcp.ResultBase
	Fields map[string]any `json:"-"`
}

type discovery struct{ *mcp.DiscoverResult }

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
	return uri
}

func bind[I any](server *Server, resourceURI, name, desc string, appOnly, readOnly bool, f func(context.Context, I) (any, error)) {
	s := server.MCP
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
	if name == "open_factory" {
		meta["openai/ui"] = map[string]any{"entrypoints": []any{map[string]any{"type": "global"}}}
	}
	openWorld := name == "session_chat" || name == "configuration_chat" || name == "session_control" || name == "answer_session_question" || name == "confirm_configuration"
	destructive := name == "session_control"
	operationTool := isOperationTool(name)
	schema, err := jsonschema.For[I](nil)
	if err != nil {
		panic(err)
	}
	if field := projectionIdentifierField(name); field != "" {
		schema.Properties[field].MaxLength = ptr(projectionIdentifierBytes)
		desc += " Read identifiers are limited to 1024 UTF-8 bytes; oversized selectors are rejected before projection admission."
	}
	if operationTool || name == "operation_status" {
		schema.Properties["requestId"] = &jsonschema.Schema{Type: "string", MinLength: ptr(1), MaxLength: ptr(128)}
		schema.Properties["instanceId"] = &jsonschema.Schema{Type: "string", MinLength: ptr(1), MaxLength: ptr(128)}
		if operationTool {
			schema.Required = append(schema.Required, "requestId", "instanceId")
		}
	}
	if operationTool {
		desc += " API v2: submit once with a new client requestId and bootstrap instanceId. The receipt is acceptance only; poll operation_status to completion. Never resubmit with a new identity after a lost response."
	} else if name != "open_factory" && name != "open_settings" && name != "operation_status" && name != "document_chunk" {
		desc += " API v2: pending/busy means data is not current; poll after 250 ms. Ready data is in value. Resolve document descriptors with document_chunk without truncation."
	}
	if name == "document_chunk" {
		schema.Properties["version"] = &jsonschema.Schema{Type: "string", MinLength: ptr(64), MaxLength: ptr(64), Pattern: "^[0-9a-f]{64}$"}
	}
	if name == "factory_state" {
		desc += " An unchanged check requires sinceVersion and sinceInstanceId from the same ready response; another or missing instance returns current data."
	}
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc, InputSchema: schema, Meta: meta, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly && !operationTool, OpenWorldHint: ptr(openWorld), DestructiveHint: ptr(destructive)}}, func(ctx context.Context, req *mcp.CallToolRequest, in I) (*mcp.CallToolResult, map[string]any, error) {
		var v any
		var e error
		switch {
		case name == "open_factory" || name == "open_settings":
			v = map[string]any{"state": "ready", "resourceUri": resourceURI, "pollMs": 250, "idlePollMs": 1000, "hiddenPollMs": 10000}
		case name == "operation_status" || name == "document_chunk":
			v, e = f(ctx, in)
		case operationTool:
			var identity struct {
				RequestID  string `json:"requestId"`
				InstanceID string `json:"instanceId"`
				ID         string `json:"id"`
			}
			_ = json.Unmarshal(req.Params.Arguments, &identity)
			scope, control := operationScope(name, req.Params.Arguments)
			if identity.ID == "" {
				identity.ID = scope
			}
			v, e = server.operations.submit(identity.InstanceID, identity.RequestID, name, scope, control, len(req.Params.Arguments), func(ctx context.Context) (any, error) { return f(ctx, in) }, identity.ID)
		default:
			e = validateProjectionIdentifier(name, req.Params.Arguments)
			if e == nil {
				v = server.projections.read(projectionKey(name, req.Params.Arguments), projectionDependencies(name, req.Params.Arguments), func(ctx context.Context) (any, error) { return f(ctx, in) })
			}
		}
		if e != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: e.Error()}}}, nil, nil
		}

		if name == "factory_state" {
			// Numeric revisions belong to one daemon instance.
			var requested struct {
				SinceVersion    *uint64 `json:"sinceVersion"`
				SinceInstanceID string  `json:"sinceInstanceId"`
			}
			_ = json.Unmarshal(req.Params.Arguments, &requested)
			if projection, ok := v.(map[string]any); ok && projection["state"] == "ready" && requested.SinceVersion != nil && requested.SinceInstanceID == server.operations.instance && *requested.SinceVersion == projection["revision"] {
				projection["value"] = map[string]any{"unchanged": true, "version": *requested.SinceVersion}
			}
		}
		// Values are immutable, serialized and size-checked by background workers.
		data := map[string]any{"apiVersion": APIVersion, "instanceId": server.operations.instance, "response": v}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name + " API v2; inspect structuredContent.data.response. Acceptance is not completion."}}}, map[string]any{"data": data}, nil
	})

}

const projectionIdentifierBytes = 1024

func projectionIdentifierField(name string) string {
	switch name {
	case "factory_item", "session_transcript":
		return "id"
	case "factory_state":
		return "itemId"
	}
	return ""
}

func validateProjectionIdentifier(name string, raw json.RawMessage) error {
	field := projectionIdentifierField(name)
	if field == "" {
		return nil
	}
	var in struct {
		ID     string `json:"id"`
		ItemID string `json:"itemId"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	id := in.ID
	if field == "itemId" {
		id = in.ItemID
	}
	if len(id) > projectionIdentifierBytes {
		return fmt.Errorf("%s must not exceed %d UTF-8 bytes; read was not admitted", field, projectionIdentifierBytes)
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

const rpcIDLimit = 512 // Canonical encoded ID bytes; leaves room in the tool envelope.
const rpcRequestLimit = 1 << 20

// Validate the transport identifier before any tool can admit an operation.
// Logical requestId/instanceId validation remains owned by operation admission.
func admitRPCEnvelope(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		return true
	}
	original := r.Body
	defer original.Close()
	body, err := io.ReadAll(io.LimitReader(original, rpcRequestLimit+1))
	if err != nil || len(body) > rpcRequestLimit {
		http.Error(w, "JSON-RPC request exceeds its read bound", http.StatusRequestEntityTooLarge)
		return false
	}
	message, err := jsonrpc.DecodeMessage(body)
	if err != nil {
		http.Error(w, "Invalid JSON-RPC envelope", http.StatusBadRequest)
		return false
	}
	var id jsonrpc.ID
	switch message := message.(type) {
	case *jsonrpc.Request:
		if message.Method == "tools/call" {
			httpbudget.LimitResponse(r.Context(), responseLimit)
		}
		id = message.ID
	case *jsonrpc.Response:
		id = message.ID
	}
	encoded, err := json.Marshal(id.Raw())
	if err != nil || len(encoded) > rpcIDLimit {
		http.Error(w, "JSON-RPC identifier exceeds 512 encoded bytes; request was not admitted", http.StatusRequestEntityTooLarge)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return true
}

func (s *Server) Handler(token string) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.MCP }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: rpcRequestLimit})
	return s.LogAPI("mcp", token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || len(token) < 32 || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "owner authentication required", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		if !admitRPCEnvelope(w, r) {
			return
		}
		s.lastRequest.Store(time.Now().Unix())
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r.WithContext(events.WithRequestContext(r.Context())))
	}))
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

func isOperationTool(name string) bool {
	switch name {
	case "session_chat", "configuration_chat", "session_control", "answer_session_question", "resolve_session_delivery", "inspect_configuration", "propose_configuration", "confirm_configuration", "user_action", "link_items", "sync_repositories", "diagnostics":
		return true
	}
	return false
}

// This backstop also covers schema-error responses. Functional results are
// chunked by background workers, before dispatch can serialize them.
func registerToolBudget(server *mcp.Server) {
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			result, err := next(ctx, method, req)
			if err != nil {
				encoded, encodeErr := jsonrpc.EncodeMessage(&jsonrpc.Response{Error: err})
				if encodeErr != nil || len(encoded) > responseLimit-1024 {
					return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Tool validation error exceeded its bound; simplify the request."}
				}
				return result, err
			}
			if tool, ok := result.(*mcp.CallToolResult); ok {
				encoded, err := json.Marshal(tool)
				if err != nil || len(encoded) > responseLimit-1024 {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Tool response exceeded its bound; inspect saved state and refresh the owning read."}}}, nil
				}
			}
			return result, nil
		}
	})
}
