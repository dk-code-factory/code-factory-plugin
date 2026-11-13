package gateway

import (
	"codefactory/internal/httpbudget"
	"context"
	"log/slog"
	"net/http"
)

// Only fixed endpoint labels, response status and elapsed time are logged. URLs, queries,
// headers, request bodies, client IDs, codes and tokens never enter diagnostics.
func audit(surface string, next http.Handler) http.Handler {
	return httpbudget.AuditRejections(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := auditRoute(r)
		response := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(response, r)
		actual := httpbudget.Complete(r.Context())
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		if actual != 0 {
			status = actual
		}
		slog.Info("Code Factory gateway request", "surface", surface, "route", route, "request_id", httpbudget.RequestID(r.Context()), "budget_breached", httpbudget.ResponseDuration(r.Context()) > httpbudget.Duration, "status", status, "duration_ms", httpbudget.ResponseDuration(r.Context()).Milliseconds())
	}), auditRoute, func(ctx context.Context, rejection httpbudget.Rejection) {
		slog.InfoContext(ctx, "Code Factory gateway request", "surface", surface, "route", rejection.API,
			"status", http.StatusServiceUnavailable, "rejection_reason", "request_not_started",
			"rejected_count", rejection.Count, "coalesced", rejection.Coalesced,
			"duration_ms", rejection.Duration.Milliseconds(),
			"started_at", rejection.StartedAt, "last_started_at", rejection.LastAt)
	})
}

func auditRoute(r *http.Request) string {
	route := "other"
	switch r.URL.Path {
	case "/mcp":
		route = "mcp"
	case "/register":
		route = "oauth_registration"
	case "/authorize":
		route = "oauth_authorization"
	case "/approve":
		route = "owner_approval"
	case "/token":
		route = "oauth_token"
	case "/revoke":
		route = "oauth_revocation"
	case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
		route = "oauth_resource_metadata"
	case "/.well-known/oauth-authorization-server":
		route = "oauth_server_metadata"
	}
	return route
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
