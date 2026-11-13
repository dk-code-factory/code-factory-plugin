package httpbudget

import (
	"bytes"
	"context"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlowResponseRetainsAdmissionAndReturnsSuccess(t *testing.T) {
	b := boundary{make(chan struct{}, 1), Duration, 32}
	release, entered, returned := make(chan struct{}), make(chan context.Context, 1), make(chan struct{})
	releaseWork := releaseBoundaryOnExit(t, &b, release)
	var calls atomic.Int32
	h := b.wrap(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- r.Context()
		<-release
		if r.Context().Err() != nil {
			t.Error("latency canceled business work")
		}
		w.Header().Set("Result", "preserved")
		io.WriteString(w, "slow success")
	}))
	w := httptest.NewRecorder()
	go func() { defer close(returned); h.ServeHTTP(w, httptest.NewRequest("POST", "/", nil)) }()
	var ctx context.Context
	select {
	case ctx = <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler readiness timed out")
	}
	time.Sleep(125 * time.Millisecond)
	select {
	case <-returned:
		t.Fatal("latency ended request")
	default:
	}
	if ctx.Err() != nil {
		t.Fatal("latency canceled request")
	}
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest("POST", "/", nil))
	if second.Code != 503 || !strings.Contains(second.Body.String(), "request_not_started") || calls.Load() != 1 {
		t.Fatal("slow worker admission was released")
	}
	releaseWork()
	awaitBoundary(t, returned, "slow response completion")
	if w.Code != 200 || w.Body.String() != "slow success" || w.Header().Get("Result") != "preserved" {
		t.Fatal("slow response was changed", w)
	}
}
func TestCompletedResponseKeepsWorkerThroughAuditAndReportsWireOutcome(t *testing.T) {
	b := boundary{make(chan struct{}, 1), Duration, 32}
	release := make(chan struct{})
	releaseWork := releaseBoundaryOnExit(t, &b, release)
	exited := make(chan int, 1)
	h := b.wrap(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		io.WriteString(w, "accepted-id")
		status := Complete(r.Context())
		<-release
		exited <- status
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 202 || w.Body.String() != "accepted-id" {
		t.Fatal("completed response lost", w)
	}
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest("POST", "/", nil))
	if second.Code != 503 {
		t.Fatal("audit orphan was no longer counted")
	}
	releaseWork()
	select {
	case status := <-exited:
		if status != 202 {
			t.Fatal("wrong audit wire status")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("audit completion timed out")
	}
}
func TestResponseBufferLimitAndCancellation(t *testing.T) {
	for _, scenario := range []string{"overflow", "lifetime", "request"} {
		t.Run(scenario, func(t *testing.T) {
			life, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, requestCancel := context.WithCancel(context.Background())
			defer requestCancel()
			exited := make(chan struct{})
			b := boundary{make(chan struct{}, 1), Duration, 4}
			h := b.wrap(life, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(exited)
				if scenario == "overflow" {
					io.WriteString(w, "12345")
					return
				}
				if scenario == "lifetime" {
					cancel()
				} else {
					requestCancel()
				}
				<-r.Context().Done()
				io.WriteString(w, "late")
			}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil).WithContext(req))
			awaitBoundary(t, exited, "handler exit")
			want := 503
			if scenario == "overflow" {
				want = 500
			}
			if w.Code != want || strings.Contains(w.Body.String(), "12345") {
				t.Fatal("invalid bounded rejection", w)
			}
		})
	}
}
func TestSSEHandshakeRetainsStreamAndCancelsOnDisconnect(t *testing.T) {
	b := boundary{make(chan struct{}, 1), Duration, 128}
	exited := make(chan struct{})
	h := b.wrap(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(exited)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: acknowledged\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		<-r.Context().Done()
	}))
	server := httptest.NewServer(h)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	started := time.Now()
	res, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 100*time.Millisecond || res.StatusCode != 200 {
		t.Fatal("slow stream handshake")
	}
	buf := make([]byte, len("data: acknowledged\n\n"))
	if _, err = io.ReadFull(res.Body, buf); err != nil || !strings.Contains(string(buf), "acknowledged") {
		t.Fatal("lost stream ack", err)
	}
	time.Sleep(125 * time.Millisecond)
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest("GET", "/", nil))
	if second.Code != 503 {
		t.Fatal("stream admission released early")
	}
	res.Body.Close()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("stream ignored disconnect")
	}
}
func TestNonSSEFlushPreservesCallerCancellationAndLivePanicPropagates(t *testing.T) {
	b := boundary{make(chan struct{}, 1), Duration, 32}
	h := b.wrap(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "premature")
		http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	w := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	if w.Code != 503 || strings.Contains(w.Body.String(), "premature") {
		t.Fatal("nonstream Flush bypassed budget")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("live panic swallowed")
			}
		}()
		(&boundary{make(chan struct{}, 1), Duration, 32}).wrap(context.Background(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private") })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
}

func TestEmptyOrPrimingSSEFlushPreservesDelayedStream(t *testing.T) {
	for _, priming := range []string{"", ": ok\n\n"} {
		t.Run(priming, func(t *testing.T) {
			b := boundary{make(chan struct{}, 1), Duration, 128}
			exited := make(chan struct{})
			h := b.wrap(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(exited)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, priming)
				http.NewResponseController(w).Flush()
				time.Sleep(125 * time.Millisecond)
				io.WriteString(w, "data: late-ack\n\n")
				http.NewResponseController(w).Flush()
			}))
			w := httptest.NewRecorder()
			started := time.Now()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			if w.Code != 200 || time.Since(started) < 125*time.Millisecond || !strings.Contains(w.Body.String(), "late-ack") {
				t.Fatal("delayed stream acknowledgement was lost", w.Code, time.Since(started))
			}
			awaitBoundary(t, exited, "handler exit")
		})
	}
}

func TestLateHandlerPanicRetainsSanitizedOwnerDiagnostic(t *testing.T) {
	var output lockedBuffer
	old := slog.Default()
	oldWriter, oldFlags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer func() { slog.SetDefault(old); log.SetOutput(oldWriter); log.SetFlags(oldFlags) }()
	b := boundary{make(chan struct{}, 1), Duration, 32}
	release := make(chan struct{})
	releaseWork := releaseBoundaryOnExit(t, &b, release)
	h := b.wrap(context.Background(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release; panic("private-panic-value") }))
	w := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/private-route?secret=private-query", nil).WithContext(ctx))
	if w.Code != 503 {
		t.Fatal("late panic did not time out")
	}
	releaseWork()
	deadline := time.After(time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for len(b.slots) > 0 {
		select {
		case <-deadline:
			t.Fatal("late panic worker did not exit")
		case <-tick.C:
		}
	}
	diagnostic := output.String()
	if !strings.Contains(diagnostic, "panic_type") || !strings.Contains(diagnostic, "request_id") || !strings.Contains(diagnostic, "stack") || strings.Contains(diagnostic, "private-panic-value") || strings.Contains(diagnostic, "private-query") {
		t.Fatal("missing or unsafe late panic diagnostic", diagnostic)
	}
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }

func releaseBoundaryOnExit(t *testing.T, b *boundary, release chan struct{}) func() {
	t.Helper()
	var once sync.Once
	releaseWork := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		releaseWork()
		deadline := time.After(2 * time.Second)
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for len(b.slots) > 0 {
			select {
			case <-deadline:
				t.Error("private HTTP worker cleanup timed out")
				return
			case <-tick.C:
			}
		}
	})
	return releaseWork
}

func TestCapacityRejectionAuditBoundsOverflowAndDrainsActualWorker(t *testing.T) {
	b := boundary{make(chan struct{}, 1), Duration, 32}
	// Occupy admission without starting another handler or audit.
	b.slots <- struct{}{}
	defer func() { <-b.slots }()
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release, records := make(chan struct{}), make(chan struct{}), make(chan Rejection, Capacity+2)
	var once sync.Once
	releaseSink := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		releaseSink()
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := Drain(ctx); err != nil {
			t.Error("rejection worker cleanup", err)
		}
	})
	var calls atomic.Int32
	h := b.wrap(life, AuditRejections(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }),
		func(*http.Request) string { return "owner_api" }, func(ctx context.Context, record Rejection) {
			if !record.Coalesced && record.Count != 1 {
				t.Error("invalid individual record", record)
			}
			select {
			case <-entered:
			default:
				close(entered)
			}
			<-release
			if ctx.Err() == nil {
				t.Error("service cancellation missing from rejection sink")
			}
			records <- record
		}))
	// Nested exported boundaries must preserve the observer, not duplicate it.
	h = b.wrap(life, h)
	reject := func(method string) {
		w := httptest.NewRecorder()
		started := time.Now()
		h.ServeHTTP(w, httptest.NewRequest(method, "/private?token=private-query", strings.NewReader("private-body")))
		if time.Since(started) > 100*time.Millisecond || w.Code != 503 || !strings.Contains(w.Body.String(), "request_not_started") {
			t.Fatal("invalid capacity rejection", w.Code)
		}
	}
	reject("PRIVATE-METHOD")
	awaitBoundary(t, entered, "rejection audit entry")
	for i := 0; i < Capacity+7; i++ {
		reject("POST")
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := Drain(ctx); err == nil {
		t.Error("blocked rejection sink disappeared from Drain")
	}
	stop()
	if calls.Load() != 0 {
		t.Fatal("capacity rejection invoked business handler")
	}
	observer := h.(*auditedHandler).audit
	observer.mu.Lock()
	size, overflow := observer.size, observer.overflow.record.Count
	observer.mu.Unlock()
	if size != Capacity || overflow != 7 {
		t.Fatal("unbounded or lost rejection queue", size, overflow)
	}
	time.Sleep(125 * time.Millisecond)
	cancel()
	releaseSink()
	ctx, stop = context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := Drain(ctx); err != nil {
		t.Fatal("rejection lane did not drain", err)
	}
	close(records)
	var total uint64
	var individual, coalesced int
	for record := range records {
		total += record.Count
		if record.StartedAt.IsZero() || record.LastAt.Before(record.StartedAt) {
			t.Error("missing rejection interval", record)
		}
		if record.Coalesced {
			coalesced++
			if record.Count != 7 || record.API != "other" || record.HTTPMethod != "other" {
				t.Error("invalid aggregate", record)
			}
		} else {
			individual++
			if record.API != "owner_api" || (record.HTTPMethod != "POST" && record.HTTPMethod != "other") {
				t.Error("unsafe or missing identity", record)
			}
		}
	}
	if total != Capacity+8 || individual != Capacity+1 || coalesced != 1 {
		t.Fatal("rejections lost or duplicated", total, individual, coalesced)
	}
}

func TestRejectionAuditIncludesCanceledBeforeAdmission(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	cancel()
	records := make(chan Rejection, 1)
	h := WrapContext(life, AuditRejections(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("canceled service started handler") }), func(*http.Request) string { return "healthz" }, func(ctx context.Context, record Rejection) {
		if ctx.Err() == nil {
			t.Error("lifetime not canceled")
		}
		records <- record
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 503 {
		t.Fatal("unexpected response", w.Code)
	}
	select {
	case record := <-records:
		if record.Count != 1 {
			t.Fatal(record)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled rejection not recorded")
	}
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := Drain(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCoalescedRejectionIntervalSurvivesOutOfOrderEnqueue(t *testing.T) {
	now := time.Now().UTC()
	// Model a full blocked lane; callers can reach its mutex in a different
	// order from their request start times.
	a := rejectionAudit{classify: func(*http.Request) string { return "fixed" }, size: Capacity, running: true,
		overflow: rejectionJob{record: Rejection{StartedAt: now, LastAt: now, Count: 1, Coalesced: true}}}
	a.enqueue(context.Background(), httptest.NewRequest("POST", "/", nil), now.Add(time.Second))
	a.enqueue(context.Background(), httptest.NewRequest("POST", "/", nil), now.Add(-time.Second))
	got := a.overflow.record
	if got.Count != 3 || !got.StartedAt.Equal(now.Add(-time.Second)) || !got.LastAt.Equal(now.Add(time.Second)) {
		t.Fatal("aggregate lost the actual request interval", got)
	}
}

func TestRequestSpecificResponseLimitPreservesOtherRoutes(t *testing.T) {
	b := boundary{make(chan struct{}, 1), Duration, 32}
	h := b.wrap(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tool":
			LimitResponse(r.Context(), 4)
			LimitResponse(r.Context(), 32)
			w.Header().Set("Private", "discarded")
			_, _ = io.WriteString(w, "private-result")
		case "/buffered":
			_, _ = io.WriteString(w, "private-buffered")
			LimitResponse(r.Context(), 4)
		default:
			_, _ = io.WriteString(w, "resource-content")
		}
	}))
	for _, path := range []string{"/tool", "/resource", "/buffered", "/resource"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if path == "/resource" {
			if w.Code != 200 || w.Body.String() != "resource-content" {
				t.Fatal("request limit leaked to another route", w.Code, w.Body.String())
			}
		} else if w.Code != 500 || strings.Contains(w.Body.String(), "private-") || w.Header().Get("Private") != "" {
			t.Fatal("oversize scoped response escaped its boundary", w.Code, w.Body.String())
		}
	}
}
