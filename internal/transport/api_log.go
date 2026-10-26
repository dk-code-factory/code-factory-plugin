package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const apiLogBodyLimit = 1 << 20

type apiCallRecord struct {
	StartedAt  time.Time `json:"startedAt"`
	API        string    `json:"api"`
	RPCMethod  string    `json:"rpcMethod,omitempty"`
	HTTPMethod string    `json:"httpMethod"`
	Parameters any       `json:"parameters"`
	HTTPStatus int       `json:"httpStatus"`
	DurationMS float64   `json:"durationMs"`
	Panicked   bool      `json:"panicked,omitempty"`
}

type apiCallLog struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	warn     sync.Once
}

func (s *Server) callLog() *apiCallLog {
	s.apiLogOnce.Do(func() {
		if s.App != nil && s.App.DataDir != "" {
			s.apiLog = &apiCallLog{path: filepath.Join(s.App.DataDir, "logs", "api-calls.jsonl"), maxBytes: 10 << 20}
		}
	})
	return s.apiLog
}

// LogAPI records incoming Code Factory requests, never outbound GitHub traffic.
// It observes consumed bytes without pre-reading, changing limits or retrying.
func (s *Server) LogAPI(boundary, token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		body := &apiBodyCapture{ReadCloser: r.Body}
		if r.Body != nil {
			r.Body = body
		}
		response := &apiStatusWriter{ResponseWriter: w}
		finished := false
		defer func() {
			record := apiCallRecord{StartedAt: started.UTC(), HTTPMethod: r.Method, HTTPStatus: response.status, DurationMS: float64(time.Since(started).Nanoseconds()) / 1e6, Panicked: !finished}
			if record.HTTPStatus == 0 && finished {
				record.HTTPStatus = http.StatusOK
			}
			if !finished && record.HTTPStatus == 0 {
				record.HTTPStatus = http.StatusInternalServerError
			}
			record.API, record.RPCMethod, record.Parameters = apiRequestParameters(boundary, r, body)
			secrets := []string{token}
			if s.App != nil && s.App.Vault != nil {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Second)
				secrets = s.App.Vault.Secrets(ctx, secrets)
				cancel()
			}
			clean := newAPIParameterSanitizer(secrets)
			record.API = clean.text(record.API)
			record.RPCMethod = clean.text(record.RPCMethod)
			record.HTTPMethod = clean.text(record.HTTPMethod)
			record.Parameters = clean.value("", record.Parameters, 0)
			if log := s.callLog(); log != nil {
				log.record(record)
			}
		}()
		next.ServeHTTP(response, r)
		finished = true
	})
}

type apiBodyCapture struct {
	io.ReadCloser
	data      bytes.Buffer
	truncated bool
}

func (b *apiBodyCapture) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	left := apiLogBodyLimit - b.data.Len()
	if n > left {
		b.truncated = true
	}
	if left > 0 {
		_, _ = b.data.Write(p[:min(n, left)])
	}
	return n, err
}

type apiStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *apiStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *apiStatusWriter) WriteHeader(code int) {
	if code >= 100 && code < 200 {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *apiStatusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func apiRequestParameters(boundary string, r *http.Request, b *apiBodyCapture) (string, string, any) {
	query := map[string]any{}
	for key, values := range r.URL.Query() {
		query[key] = values
	}
	var payload any
	if b.data.Len() > 0 {
		decoder := json.NewDecoder(bytes.NewReader(b.data.Bytes()))
		decoder.UseNumber()
		var extra any
		if b.truncated || decoder.Decode(&payload) != nil || decoder.Decode(&extra) != io.EOF {
			payload = map[string]any{"unparsed": true, "capturedBytes": b.data.Len()}
		}
		if _, ok := payload.(map[string]any); !ok {
			payload = map[string]any{"unparsed": true, "capturedBytes": b.data.Len()}
		}
	}
	if boundary == "mcp" {
		api, method := "mcp_http", ""
		params := any(map[string]any{})
		if message, ok := payload.(map[string]any); ok {
			if message["unparsed"] == true {
				return api, method, message
			}
			method, _ = message["method"].(string)
			if method != "" {
				api = method
			}
			if p, ok := message["params"].(map[string]any); ok {
				params = p
				if method == "tools/call" {
					if name, ok := p["name"].(string); ok {
						api = name
					}
					params = p["arguments"]
					if _, ok := params.(map[string]any); !ok && params != nil {
						params = map[string]any{"invalidArguments": true}
					}
				}
			} else if message["params"] != nil {
				params = map[string]any{"invalidParameters": true}
			}
		} else if payload != nil {
			params = payload
		}
		if len(query) > 0 {
			params = map[string]any{"arguments": params, "query": query}
		}
		return api, method, params
	}
	api := "owner_unknown"
	params := map[string]any{"arguments": payload}
	if len(query) > 0 {
		params["query"] = query
	}
	switch {
	case r.URL.Path == "/healthz":
		api = "healthz"
	case r.URL.Path == "/admin/dependencies":
		api = "dependencies"
	case r.URL.Path == "/admin/dependencies/operations":
		api = "dependency_operations"
	case strings.HasPrefix(r.URL.Path, "/admin/dependencies/operations/"):
		api = "dependency_operation"
		params["operationId"] = strings.TrimPrefix(r.URL.Path, "/admin/dependencies/operations/")
	case r.URL.Path == "/admin/dependencies/github-skills":
		api = "github_skill_source"
	}
	return api, "", params
}

func (l *apiCallLog) record(record apiCallRecord) {
	data, err := json.Marshal(record)
	if err == nil && len(data) > 8<<10 {
		record.Parameters = map[string]any{"truncated": true}
		data, err = json.Marshal(record)
	}
	if err == nil {
		err = l.append(append(data, '\n'))
	}
	if err != nil {
		l.warn.Do(func() { slog.Warn("Code Factory API call log unavailable; requests continue without retry") })
	}
}

func (l *apiCallLog) append(data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir := filepath.Dir(l.path)
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private log directory unavailable")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(l.path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("private log file unavailable")
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
			return fmt.Errorf("private log file is linked")
		}
		if err := os.Chmod(l.path, 0600); err != nil {
			return err
		}
		if info.Size()+int64(len(data)) > l.maxBytes {
			for n := 3; n >= 1; n-- {
				from := l.path
				if n > 1 {
					from += "." + strconv.Itoa(n-1)
				}
				if err := os.Rename(from, l.path+"."+strconv.Itoa(n)); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("private log file unavailable")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
		return fmt.Errorf("private log file is linked")
	}
	if err := f.Chmod(0600); err != nil {
		return err
	}
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}
