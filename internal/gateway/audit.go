package gateway

import (
	"log/slog"
	"net/http"
	"time"
)

// Only fixed endpoint labels, response status and elapsed time are logged. URLs, queries,
// headers, request bodies, client IDs, codes and tokens never enter diagnostics.
func audit(surface string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		started := time.Now()
		response := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(response, r)
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		slog.Info("Code Factory gateway request", "surface", surface, "route", route, "status", status, "duration_ms", time.Since(started).Milliseconds())
	})
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
