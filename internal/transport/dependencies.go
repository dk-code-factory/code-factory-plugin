package transport

import (
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/store"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
)

// DependencyHandler is mounted only on the loopback owner's admin route. It is
// intentionally absent from MCP discovery and the public OAuth gateway routes.
func (s *Server) DependencyHandler(token string) http.Handler {
	return s.LogAPI("owner", token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		auth := r.Header.Get("Authorization")
		if len(token) < 32 || !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(token)) != 1 {
			adminError(w, http.StatusUnauthorized, "owner authentication required")
			return
		}
		if r.Header.Get("Origin") != "" {
			adminError(w, http.StatusForbidden, "dependency administration uses the private owner API")
			return
		}
		base := "/admin/dependencies"
		path := r.URL.Path
		var value any
		var err error
		status := http.StatusOK
		switch {
		case path == base && r.Method == http.MethodGet:
			value, err = s.App.Runs.DependencyState(r.Context())
		case strings.HasPrefix(path, base+"/operations/") && r.Method == http.MethodGet:
			value, err = s.App.Runs.DependencyOperation(r.Context(), strings.TrimPrefix(path, base+"/operations/"))
		case path == base+"/operations" && r.Method == http.MethodPost:
			var in app.DependencyRequest
			if !decodeAdmin(w, r, &in) {
				return
			}
			value, err = s.App.Runs.UpdateDependencies(r.Context(), in)
			status = http.StatusAccepted
		case path == base+"/github-skills" && r.Method == http.MethodPost:
			var in app.GitHubSkillSource
			if !decodeAdmin(w, r, &in) {
				return
			}
			value, err = s.App.Runs.RegisterGitHubSkill(r.Context(), in)
		default:
			if path == base || path == base+"/operations" || path == base+"/github-skills" {
				adminError(w, http.StatusMethodNotAllowed, "method not allowed")
			} else {
				adminError(w, http.StatusNotFound, "admin route not found")
			}
			return
		}
		if err != nil {
			status = http.StatusBadRequest
			if errors.Is(err, app.ErrMaintenanceBusy) {
				status = http.StatusConflict
			}
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			// Adapter errors carry no command output or URL/request bodies.
			adminError(w, status, err.Error())
			return
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}))
}

func adminError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func decodeAdmin(w http.ResponseWriter, r *http.Request, out any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		adminError(w, http.StatusUnsupportedMediaType, "application/json required")
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		adminError(w, http.StatusBadRequest, "invalid or oversized admin request")
		return false
	}
	// Inputs are flat objects with strings and string arrays. Reject duplicate
	// members before struct decoding rather than accepting the last occurrence.
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		adminError(w, 400, "one JSON object required")
		return false
	}
	seen := map[string]bool{}
	allowed := map[string]bool{}
	typ := reflect.TypeOf(out).Elem()
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			allowed[name] = true
		}
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			adminError(w, 400, "invalid admin request")
			return false
		}
		name, ok := key.(string)
		if !ok || !allowed[name] || seen[name] {
			adminError(w, 400, "duplicate admin request field")
			return false
		}
		seen[name] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			adminError(w, 400, "invalid admin request")
			return false
		}
	}
	if _, err = d.Token(); err != nil {
		adminError(w, 400, "invalid admin request")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		adminError(w, 400, "one JSON object required")
		return false
	}
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		adminError(w, 400, "invalid admin request fields")
		return false
	}
	return true
}
