// Package httpbudget bounds finite local HTTP responses, including dependencies
// that ignore cancellation. Admission lasts until the actual handler exits.
package httpbudget

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const Duration = 75 * time.Millisecond
const Capacity = 32
const MaxResponseBytes = 8 << 20

type key struct{}
type requestState struct {
	complete chan chan int
	outcome  atomic.Int32
}
type boundary struct {
	slots    chan struct{}
	duration time.Duration
	maxBytes int
}

var rejectionWorkers atomic.Int64
var requests atomic.Uint64
var shared = boundary{make(chan struct{}, Capacity), Duration, MaxResponseBytes}

// Outcome reports the actual committed HTTP status, falling back before commit.
func Outcome(ctx context.Context, fallback int) int {
	if s, _ := ctx.Value(key{}).(*requestState); s != nil {
		if code := s.outcome.Load(); code != 0 {
			return int(code)
		}
	}
	return fallback
}

// Complete may only be called after the business handler has returned. It lets
// the response finish before synchronous audit work, without releasing admission.
// The acknowledgement is the actual wire status, including a prior rejection.
func Complete(ctx context.Context) int {
	s, _ := ctx.Value(key{}).(*requestState)
	if s == nil {
		return 0
	}
	if code := s.outcome.Load(); code != 0 {
		return int(code)
	}
	ack := make(chan int, 1)
	select {
	case s.complete <- ack:
		return <-ack
	case <-ctx.Done():
		return Outcome(ctx, http.StatusServiceUnavailable)
	}
}

func Wrap(next http.Handler) http.Handler { return WrapContext(context.Background(), next) }
func WrapContext(lifetime context.Context, next http.Handler) http.Handler {
	if lifetime == nil {
		lifetime = context.Background()
	}
	return shared.wrap(lifetime, next)
}

// Drain waits for actual admitted handlers after callers stop new requests.
// It is intended for service teardown; canceled or stuck work keeps its slot.
func Drain(ctx context.Context) error {
	timer := time.NewTicker(time.Millisecond)
	defer timer.Stop()
	for len(shared.slots) > 0 || rejectionWorkers.Load() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
func unavailable(w http.ResponseWriter, started bool) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	if started {
		_, _ = io.WriteString(w, `{"error":"outcome_unknown","message":"Request outcome may be unknown; inspect saved state before retrying. Reconnect OAuth instead of replaying a consumed grant."}`+"\n")
	} else {
		_, _ = io.WriteString(w, `{"error":"request_not_started","message":"Server capacity unavailable; no request handler was started."}`+"\n")
	}
}
func (b *boundary) wrap(lifetime context.Context, next http.Handler) http.Handler {
	var audit *rejectionAudit
	if h, ok := next.(*auditedHandler); ok {
		audit = h.audit
	}
	return &auditedHandler{audit: audit, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rejectUnstarted := func() {
			unavailable(w, false)
			if audit != nil {
				audit.enqueue(lifetime, r, started)
			}
		}
		if r.Context().Value(key{}) != nil {
			next.ServeHTTP(w, r)
			return
		}
		if r.Context().Err() != nil || lifetime.Err() != nil {
			rejectUnstarted()
			return
		}
		select {
		case b.slots <- struct{}{}:
		default:
			rejectUnstarted()
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
		defer cancel()
		s := &requestState{complete: make(chan chan int)}
		ctx = context.WithValue(ctx, key{}, s)
		rw := &response{header: make(http.Header), maxBytes: b.maxBytes, stream: make(chan chan bool), ctx: ctx, wire: w, overflow: make(chan struct{}, 1)}
		done := make(chan struct{})
		var panicValue any
		requestID := requests.Add(1)
		go func() {
			defer func() {
				panicValue = recover()
				if panicValue != nil {
					slog.Error("Code Factory HTTP handler panicked", "request_id", requestID, "panic_type", fmt.Sprintf("%T", panicValue), "stack", string(debug.Stack()))
				}
				<-b.slots
				close(done)
			}()
			next.ServeHTTP(rw, r.WithContext(ctx))
		}()
		deadline := time.Now().Add(b.duration)
		timer := time.NewTimer(b.duration)
		defer timer.Stop()
		reject := func() { s.outcome.Store(http.StatusServiceUnavailable); rw.close(); cancel(); unavailable(w, true) }
		limitExceeded := func() {
			s.outcome.Store(http.StatusInternalServerError)
			rw.close()
			cancel()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"response_limit_exceeded","message":"Response exceeds the supported 8MiB limit; inspect saved state and use a narrower read before retrying."}`+"\n")
		}
		commit := func() int {
			if time.Now().After(deadline) || ctx.Err() != nil {
				reject()
				return http.StatusServiceUnavailable
			}
			rw.mu.Lock()
			if rw.failed {
				rw.mu.Unlock()
				limitExceeded()
				return http.StatusInternalServerError
			}
			status := rw.status
			if status == 0 {
				status = http.StatusOK
			}
			for k, v := range rw.header {
				w.Header()[k] = append([]string(nil), v...)
			}
			s.outcome.Store(int32(status))
			w.WriteHeader(status)
			_, _ = w.Write(rw.body.Bytes())
			rw.body.Reset()
			rw.mu.Unlock()
			return status
		}
		select {
		case <-done:
			if panicValue != nil {
				panic(http.ErrAbortHandler)
			}
			commit()
		case ack := <-s.complete:
			ack <- commit()
			// Keep ordinary audit completion synchronous when it fits the original
			// budget; a slow sink retains its admission after the response returns.
			select {
			case <-done:
			case <-timer.C:
			case <-ctx.Done():
			}
		case ack := <-rw.stream:
			commit()
			if ctx.Err() != nil {
				ack <- false
				return
			}
			rw.mu.Lock()
			rw.live = true
			rw.mu.Unlock()
			_ = http.NewResponseController(w).Flush()
			ack <- true
			// An admitted stream owns the real writer until its handler actually exits.
			<-done
			if panicValue != nil {
				panic(http.ErrAbortHandler)
			}
		case <-rw.overflow:
			limitExceeded()
		case <-timer.C:
			reject()
		case <-ctx.Done():
			reject()
		}
	})}
}

// Rejection is safe metadata for a request whose business handler never ran.
// A coalesced record accounts for Count calls in [StartedAt, LastAt]; their
// individual route/method identities are deliberately not retained.
type Rejection struct {
	StartedAt, LastAt time.Time
	API, HTTPMethod   string
	Count             uint64
	Duration          time.Duration
	Coalesced         bool
}

// AuditRejections attaches a separate bounded diagnostic lane. classify must
// return a fixed endpoint label, never user-controlled paths or parameters.
// There is at most one in-flight sink call, Capacity queued records, and one
// overflow aggregate per configured observer. No request/body is retained.
// Sink receives the service lifetime (not the canceled request context).
func AuditRejections(next http.Handler, classify func(*http.Request) string, sink func(context.Context, Rejection)) http.Handler {
	return &auditedHandler{Handler: next, audit: &rejectionAudit{classify: classify, sink: sink}}
}

type auditedHandler struct {
	http.Handler
	audit *rejectionAudit
}
type rejectionJob struct {
	ctx    context.Context
	record Rejection
}
type rejectionAudit struct {
	mu         sync.Mutex
	classify   func(*http.Request) string
	sink       func(context.Context, Rejection)
	queue      [Capacity]rejectionJob
	head, size int
	overflow   rejectionJob
	running    bool
}

func (a *rejectionAudit) enqueue(lifetime context.Context, r *http.Request, started time.Time) {
	method := "other"
	switch r.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		method = r.Method
	}
	record := Rejection{StartedAt: started.UTC(), LastAt: started.UTC(), API: a.classify(r), HTTPMethod: method, Count: 1, Duration: time.Since(started)}
	a.mu.Lock()
	if a.size < len(a.queue) && a.overflow.record.Count == 0 {
		a.queue[(a.head+a.size)%len(a.queue)] = rejectionJob{lifetime, record}
		a.size++
	} else {
		if a.overflow.record.Count == 0 {
			a.overflow = rejectionJob{lifetime, Rejection{StartedAt: record.StartedAt, API: "other", HTTPMethod: "other", Coalesced: true}}
		}
		if record.StartedAt.Before(a.overflow.record.StartedAt) {
			a.overflow.record.StartedAt = record.StartedAt
		}
		if record.LastAt.After(a.overflow.record.LastAt) {
			a.overflow.record.LastAt = record.LastAt
		}
		a.overflow.record.Count++
	}
	if !a.running {
		a.running = true
		rejectionWorkers.Add(1)
		go a.run()
	}
	a.mu.Unlock()
}
func (a *rejectionAudit) run() {
	for {
		a.mu.Lock()
		var job rejectionJob
		if a.size > 0 {
			job = a.queue[a.head]
			a.queue[a.head] = rejectionJob{}
			a.head = (a.head + 1) % len(a.queue)
			a.size--
		} else if a.overflow.record.Count > 0 {
			job = a.overflow
			a.overflow = rejectionJob{}
		} else {
			a.running = false
			rejectionWorkers.Add(-1)
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
		func() {
			defer func() {
				if v := recover(); v != nil {
					slog.Error("Code Factory rejection audit panicked", "panic_type", fmt.Sprintf("%T", v), "stack", string(debug.Stack()))
				}
			}()
			a.sink(job.ctx, job.record)
		}()
	}
}

type response struct {
	mu                   sync.Mutex
	header               http.Header
	status               int
	body                 bytes.Buffer
	maxBytes             int
	closed, failed, live bool
	stream               chan chan bool
	overflow             chan struct{}
	ctx                  context.Context
	wire                 http.ResponseWriter
}

func (w *response) Header() http.Header { return w.header }
func (w *response) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.status != 0 || code < 200 {
		return
	}
	w.status = code
}
func (w *response) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.failed {
		return 0, http.ErrHandlerTimeout
	}
	if w.live {
		return w.wire.Write(p)
	}
	if len(p) > w.maxBytes-w.body.Len() {
		w.failed = true
		select {
		case w.overflow <- struct{}{}:
		default:
		}
		return 0, http.ErrHandlerTimeout
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}
func (w *response) close() { w.mu.Lock(); w.closed = true; w.body.Reset(); w.mu.Unlock() }
func (w *response) FlushError() error {
	w.mu.Lock()
	if w.closed || w.failed {
		w.mu.Unlock()
		return http.ErrHandlerTimeout
	}
	if w.live {
		w.mu.Unlock()
		return http.NewResponseController(w.wire).Flush()
	}
	if !strings.HasPrefix(w.header.Get("Content-Type"), "text/event-stream") || !completeSSEEvent(w.body.Bytes()) {
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()
	ack := make(chan bool, 1)
	select {
	case w.stream <- ack:
		if <-ack {
			return nil
		}
		return http.ErrHandlerTimeout
	case <-w.ctx.Done():
		return http.ErrHandlerTimeout
	}
}
func (w *response) Flush() { _ = w.FlushError() }

// Header flushes and priming comments do not acknowledge an active stream.
// Require a complete data event before transferring ownership to streaming.
func completeSSEEvent(data []byte) bool {
	frames := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n\n")
	for _, frame := range frames[:len(frames)-1] {
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "data:") && strings.TrimSpace(strings.TrimPrefix(line, "data:")) != "" {
				return true
			}
		}
	}
	return false
}
