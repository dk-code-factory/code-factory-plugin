package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/events"
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
	"sync"
	"sync/atomic"
	"time"
)

var ResourceURI = resourceURI(web.HTML)
var SettingsResourceURI = settingsResourceURI(web.SettingsHTML())

type Server struct {
	apiLogOnce  sync.Once
	apiLog      *apiCallLog
	App         *app.App
	Events      *events.Service
	MCP         *mcp.Server
	lastRequest atomic.Int64
	operations  *operations
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

func bind[I any](s *Server, resourceURI, name, desc string, appOnly, readOnly bool, f func(context.Context, I) (any, error)) {
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
	mcp.AddTool(s.MCP, &mcp.Tool{Name: name, Description: desc, Meta: meta, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, OpenWorldHint: ptr(openWorld), DestructiveHint: ptr(destructive)}}, func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, map[string]any, error) {
		return s.operations.run(ctx, name, func(workCtx context.Context) (any, error) { return f(workCtx, in) })
	})
}

func ptr[T any](v T) *T { return &v }

func (s *Server) Handler(token string) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.MCP }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20})
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
