package transport

import (
	"bytes"
	"codefactory/internal/httpbudget"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const apiLogBodyLimit = 1 << 20
const apiLogQueueBytes = 8 << 20

type apiCallRecord struct {
	RequestID       uint64     `json:"requestId"`
	StartedAt       time.Time  `json:"startedAt"`
	API             string     `json:"api"`
	RPCMethod       string     `json:"rpcMethod,omitempty"`
	HTTPMethod      string     `json:"httpMethod"`
	Parameters      any        `json:"parameters"`
	HTTPStatus      int        `json:"httpStatus"`
	DurationMS      float64    `json:"durationMs"`
	Panicked        bool       `json:"panicked,omitempty"`
	HTTPStatusKnown bool       `json:"httpStatusKnown"`
	HandlerStatus   int        `json:"handlerStatus,omitempty"`
	RejectionReason string     `json:"rejectionReason,omitempty"`
	RejectedCount   uint64     `json:"rejectedCount,omitempty"`
	Coalesced       bool       `json:"coalesced,omitempty"`
	LastStartedAt   *time.Time `json:"lastStartedAt,omitempty"`
	APIVersion      int        `json:"apiVersion"`
	Outcome         string     `json:"outcome"`
	ResponseBytes   int        `json:"responseBytes"`
	BudgetBreached  bool       `json:"budgetBreached"`
	DroppedLogs     uint64     `json:"droppedLogs"`
}

type apiCallLog struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	warn     sync.Once
	retained atomic.Int64
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
type apiLogJob struct {
	record          apiCallRecord
	boundary, token string
	request         *http.Request
	body            *apiBodyCapture
	done            chan struct{}
	response        []byte
	retainedBytes   int64
}

func (s *Server) LogAPI(boundary, token string, next http.Handler) http.Handler {
	lifetime := context.Background()
	if s.operations != nil {
		lifetime = s.operations.ctx
	}
	log := s.callLog()
	jobs := make(chan *apiLogJob, 256)
	var dropped atomic.Uint64
	if log != nil {
		s.apiLogWorkers.Add(1)
		go func() {
			defer s.apiLogWorkers.Done()
			for {
				var job *apiLogJob
				select {
				case job = <-jobs:
				case <-lifetime.Done():
					return
				}
				func() {
					defer log.retained.Add(-job.retainedBytes)
					<-job.done
					if lifetime.Err() != nil {
						return
					}
					record := job.record
					var reply struct {
						Result struct {
							IsError           bool `json:"isError"`
							StructuredContent struct {
								Data struct {
									Response struct {
										State string `json:"state"`
									}
								}
							}
						}
					}
					if json.Unmarshal(job.response, &reply) == nil {
						if reply.Result.IsError {
							record.Outcome = "failed"
						} else if state := reply.Result.StructuredContent.Data.Response.State; state != "" {
							record.Outcome = state
						}
					}
					record.API, record.RPCMethod, record.Parameters = apiRequestParameters(job.boundary, job.request, job.body)
					secrets := []string{job.token}
					if s.App != nil && s.App.Vault != nil {
						ctx, cancel := context.WithTimeout(lifetime, time.Second)
						secrets = s.App.Vault.Secrets(ctx, secrets)
						interrupted := ctx.Err() != nil
						cancel()
						if interrupted {
							dropped.Add(1)
							if lifetime.Err() != nil {
								return
							}
							return
						}
					}
					clean := newAPIParameterSanitizer(secrets)
					record.API = clean.text(record.API)
					record.RPCMethod = clean.text(record.RPCMethod)
					record.HTTPMethod = clean.text(record.HTTPMethod)
					// Tool command payloads never enter the log, even after redaction.
					if job.boundary == "mcp" {
						record.Parameters = map[string]any{}
					} else {
						record.Parameters = clean.value("", record.Parameters, 0)
					}
					if lifetime.Err() != nil {
						return
					}
					log.record(record)
				}()
			}
		}()
	}
	return s.BoundHTTP(lifetime, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		body := &apiBodyCapture{ReadCloser: r.Body}
		if r.Body != nil {
			r.Body = body
		}
		response := &apiStatusWriter{ResponseWriter: w}
		finished := false
		defer func() {
			if log == nil {
				return
			}
			record := apiCallRecord{RequestID: httpbudget.RequestID(r.Context()), StartedAt: started.UTC(), HTTPMethod: r.Method, HTTPStatus: response.status, Panicked: !finished, APIVersion: APIVersion, ResponseBytes: response.bytes, Outcome: "returned"}
			if record.HTTPStatus == 0 {
				if finished {
					record.HTTPStatus = http.StatusOK
				} else {
					record.HTTPStatus = http.StatusInternalServerError
				}
			}
			if !finished {
				record.Outcome = "panicked"
			} else if record.HTTPStatus >= 400 {
				record.Outcome = "rejected"
			}
			record.HTTPStatus = httpbudget.Outcome(r.Context(), 0)
			record.HTTPStatusKnown = record.HTTPStatus != 0
			if !finished {
				record.HandlerStatus = http.StatusInternalServerError
			}
			// Keep one consumed-byte buffer, never the replay reader, headers or
			// request context. Byte admission covers both waiting and active jobs.
			retained := int64(body.data.Cap() + cap(response.sample) + len(r.Method) + len(r.URL.Path) + len(r.URL.RawQuery))
			if lifetime.Err() != nil || !log.reserve(retained) {
				dropped.Add(1)
				return
			}
			request := &http.Request{Method: strings.Clone(r.Method), URL: &url.URL{Path: strings.Clone(r.URL.Path), RawQuery: strings.Clone(r.URL.RawQuery)}}
			capture := &apiBodyCapture{data: body.data, truncated: body.truncated}
			job := &apiLogJob{record: record, boundary: boundary, token: token, request: request, body: capture, done: make(chan struct{}), response: response.sample, retainedBytes: retained}
			select {
			case jobs <- job:
			default:
				log.retained.Add(-retained)
				dropped.Add(1)
			}
			elapsed := httpbudget.ResponseDuration(r.Context())
			job.record.DurationMS = float64(elapsed.Nanoseconds()) / 1e6
			job.record.BudgetBreached = elapsed > httpbudget.Duration
			job.record.DroppedLogs = dropped.Load()
			close(job.done)
		}()
		next.ServeHTTP(response, r)
		finished = true
		httpbudget.Complete(r.Context())
	}))
}

// BoundHTTP covers the daemon mux as well as directly exported API handlers.
// Rejections never read bodies, queries, credentials or the secret vault.
func (s *Server) BoundHTTP(lifetime context.Context, next http.Handler) http.Handler {
	return httpbudget.WrapContext(lifetime, httpbudget.AuditRejections(next, func(r *http.Request) string {
		if r.URL.Path == "/mcp" {
			return "mcp_http"
		}
		switch r.URL.Path {
		case "/healthz":
			return "healthz"
		case "/admin/dependencies":
			return "dependencies"
		case "/admin/dependencies/operations":
			return "dependency_operations"
		case "/admin/dependencies/github-skills":
			return "github_skill_source"
		}
		if strings.HasPrefix(r.URL.Path, "/admin/dependencies/operations/") {
			return "dependency_operation"
		}
		return "owner_unknown"
	}, func(ctx context.Context, rejection httpbudget.Rejection) {
		if ctx.Err() != nil {
			return
		}
		if log := s.callLog(); log != nil {
			log.record(apiCallRecord{StartedAt: rejection.StartedAt, LastStartedAt: &rejection.LastAt,
				API: rejection.API, HTTPMethod: rejection.HTTPMethod, Parameters: map[string]any{"unread": true},
				HTTPStatus: http.StatusServiceUnavailable, HTTPStatusKnown: true,
				DurationMS:      float64(rejection.Duration.Nanoseconds()) / 1e6,
				RejectionReason: "request_not_started", RejectedCount: rejection.Count, Coalesced: rejection.Coalesced})
		}
	}))
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
	bytes  int
	sample []byte
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
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	if len(w.sample)+n <= responseLimit {
		w.sample = append(w.sample, p[:n]...)
	}
	return n, err
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

func (l *apiCallLog) reserve(size int64) bool {
	if size < 0 || size > apiLogQueueBytes {
		return false
	}
	for attempt := 0; attempt < 8; attempt++ {
		retained := l.retained.Load()
		if retained > apiLogQueueBytes-size {
			return false
		}
		if l.retained.CompareAndSwap(retained, retained+size) {
			return true
		}
	}
	return false
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
