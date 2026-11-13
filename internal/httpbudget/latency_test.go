package httpbudget

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSlowRequestSignalsBeforeCompletionAndCorrelatesOutcome(t *testing.T) {
	var output lockedBuffer
	old, writer, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := Drain(ctx); err != nil {
			t.Error(err)
		}
		slog.SetDefault(old)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
	b := boundary{make(chan struct{}, 1), Duration, 64}
	release, returned := make(chan struct{}), make(chan struct{})
	releaseWork := releaseBoundaryOnExit(t, &b, release)
	h := b.wrap(context.Background(), AuditRejections(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		if r.Context().Err() != nil {
			t.Error("slow request was canceled")
		}
		w.WriteHeader(201)
		io.WriteString(w, "saved")
	}), func(*http.Request) string { return "owner_api" }, func(context.Context, Rejection) {}))
	w := httptest.NewRecorder()
	go func() {
		defer close(returned)
		h.ServeHTTP(w, httptest.NewRequest("PRIVATE-METHOD", "/private?token=private-secret", strings.NewReader("private-body")))
	}()
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(output.String(), `"phase":"slow"`) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(output.String(), `"phase":"slow"`) {
		t.Fatal("no signal while request was still pending")
	}
	select {
	case <-returned:
		t.Fatal("slow signal ended request")
	default:
	}
	releaseWork()
	awaitBoundary(t, returned, "slow completion")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if w.Code != 201 || w.Body.String() != "saved" {
		t.Fatal("slow success changed", w)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatal("expected one threshold and one completion record", output.String())
	}
	var first, last map[string]any
	if json.Unmarshal([]byte(lines[0]), &first) != nil || json.Unmarshal([]byte(lines[1]), &last) != nil {
		t.Fatal("invalid diagnostic JSON")
	}
	if first["request_id"] == float64(0) || first["request_id"] != last["request_id"] || last["phase"] != "finished" || last["status"] != float64(201) || last["duration_ms"].(float64) < 75 || last["active_slow_requests"] != float64(0) {
		t.Fatal("incorrect slow request tracking", first, last)
	}
	if first["api"] != "owner_api" || first["http_method"] != "other" || strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "PRIVATE-METHOD") {
		t.Fatal("unsafe slow diagnostics", output.String())
	}
}

type heldWire struct {
	*httptest.ResponseRecorder
	stream           bool
	entered, release chan struct{}
	once             sync.Once
}

func (w *heldWire) wait() { w.once.Do(func() { close(w.entered); <-w.release }) }
func (w *heldWire) Write(body []byte) (int, error) {
	if !w.stream {
		w.wait()
	}
	return w.ResponseRecorder.Write(body)
}
func (w *heldWire) Flush() {
	if w.stream {
		w.wait()
	}
	w.ResponseRecorder.Flush()
}

func TestSlowWireSignalsWhilePendingAndIncludesAcknowledgement(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "finite_write"
		if stream {
			name = "stream_flush"
		}
		t.Run(name, func(t *testing.T) {
			var output lockedBuffer
			old, writer, flags := slog.Default(), log.Writer(), log.Flags()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := Drain(ctx); err != nil {
					t.Error(err)
				}
				slog.SetDefault(old)
				log.SetOutput(writer)
				log.SetFlags(flags)
			})
			b := boundary{make(chan struct{}, 1), Duration, 64}
			release, returned := make(chan struct{}), make(chan struct{})
			releaseWire := releaseBoundaryOnExit(t, &b, release)
			elapsed := make(chan time.Duration, 1)
			h := b.wrap(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				io.WriteString(w, "data: useful\n\n")
				if stream {
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Error(err)
					}
				} else {
					Complete(r.Context())
				}
				elapsed <- ResponseDuration(r.Context())
			}))
			w := &heldWire{ResponseRecorder: httptest.NewRecorder(), stream: stream, entered: make(chan struct{}), release: release}
			go func() { defer close(returned); h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil)) }()
			t.Cleanup(func() {
				releaseWire()
				awaitBoundary(t, returned, "wire fixture cleanup")
			})
			awaitBoundary(t, w.entered, "wire output readiness")
			deadline := time.Now().Add(time.Second)
			for !strings.Contains(output.String(), `"phase":"slow"`) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !strings.Contains(output.String(), `"phase":"slow"`) || activeSlowRequests.Load() != 1 {
				t.Fatal("blocked output suppressed pending slow observation", output.String())
			}
			select {
			case <-returned:
				t.Fatal("threshold ended wire output")
			default:
			}
			time.Sleep(50 * time.Millisecond)
			releaseWire()
			awaitBoundary(t, returned, "wire completion")
			select {
			case got := <-elapsed:
				if got < 125*time.Millisecond {
					t.Fatal("audit duration omitted wire output", got)
				}
			case <-time.After(time.Second):
				t.Fatal("response duration unavailable")
			}
			if w.Code != 200 || w.Body.String() != "data: useful\n\n" {
				t.Fatal("slow wire response was lost", w.ResponseRecorder)
			}
		})
	}
}

type stalledLatencySink struct {
	entered, release chan struct{}
	once             sync.Once
}

func (s *stalledLatencySink) Enabled(context.Context, slog.Level) bool { return true }
func (s *stalledLatencySink) Handle(context.Context, slog.Record) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return nil
}
func (s *stalledLatencySink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *stalledLatencySink) WithGroup(string) slog.Handler      { return s }

func TestSlowDiagnosticsRemainBoundedAndNeverBlockResponse(t *testing.T) {
	old, writer, flags := slog.Default(), log.Writer(), log.Flags()
	sink := &stalledLatencySink{entered: make(chan struct{}), release: make(chan struct{})}
	slog.SetDefault(slog.New(sink))
	defer func() {
		close(sink.release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := Drain(ctx); err != nil {
			t.Error(err)
		}
		slog.SetDefault(old)
		log.SetOutput(writer)
		log.SetFlags(flags)
	}()
	before := droppedLatencyRecords.Load()
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(125 * time.Millisecond)
		io.WriteString(w, "slow result")
	}))
	w, returned := httptest.NewRecorder(), make(chan struct{})
	go func() { defer close(returned); h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil)) }()
	awaitBoundary(t, sink.entered, "diagnostic sink")
	// Saturate the observer while its only sink is blocked.
	for range 2*Capacity + 8 {
		observeLatency(latencyRecord{API: "other"})
	}
	awaitBoundary(t, returned, "response with blocked diagnostics")
	if w.Code != 200 || w.Body.String() != "slow result" {
		t.Fatal("diagnostic sink changed response")
	}
	latencyAudit.Lock()
	size := latencyAudit.size
	latencyAudit.Unlock()
	if size != 2*Capacity || droppedLatencyRecords.Load() <= before {
		t.Fatal("diagnostic overflow was unbounded or uncounted", size)
	}
}

func TestLatencyMetadataDoesNotRetainCallerRequestLines(t *testing.T) {
	old, writer, flags := slog.Default(), log.Writer(), log.Flags()
	sink := &stalledLatencySink{entered: make(chan struct{}), release: make(chan struct{})}
	slog.SetDefault(slog.New(sink))
	defer func() {
		close(sink.release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := Drain(ctx); err != nil {
			t.Error(err)
		}
		slog.SetDefault(old)
		log.SetOutput(writer)
		log.SetFlags(flags)
	}()
	observeLatency(latencyRecord{API: "other", Method: "GET"})
	awaitBoundary(t, sink.entered, "metadata sink readiness")
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for id := uint64(1); id <= Capacity; id++ {
		queueParsedLatencyMetadata(t, id)
	}
	observeLatency(latencyRecord{RequestID: Capacity + 1, API: strings.Repeat("private", 1<<18), Method: strings.Repeat("PRIVATE", 1<<18)})
	runtime.GC()
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc+8<<20 {
		t.Fatal("metadata retained caller request-line allocations", after.HeapAlloc-before.HeapAlloc)
	}
	latencyAudit.Lock()
	defer latencyAudit.Unlock()
	if latencyAudit.size != Capacity+1 {
		t.Fatal("metadata queue lost records", latencyAudit.size)
	}
	for index := range Capacity {
		record := latencyAudit.queue[(latencyAudit.head+index)%len(latencyAudit.queue)]
		if record.RequestID != uint64(index+1) || record.Method != "GET" || record.API != "owner_api" {
			t.Fatal("safe metadata lost its identity or label", record)
		}
	}
	last := latencyAudit.queue[(latencyAudit.head+Capacity)%len(latencyAudit.queue)]
	if last.RequestID != Capacity+1 || last.Method != "other" || last.API != "other" {
		t.Fatal("oversized metadata was retained", last.RequestID, len(last.Method), len(last.API))
	}
}

func queueParsedLatencyMetadata(t *testing.T, id uint64) {
	t.Helper()
	// net/http parses both fields as substrings of one megabyte request line.
	// Only the three-byte method and nine-byte fixed label may outlive this call.
	line := "GET /owner_api?private=" + strings.Repeat("x", 1<<20) + " HTTP/1.1\r\nHost: fixture\r\n\r\n"
	r, err := http.ReadRequest(bufio.NewReader(strings.NewReader(line)))
	if err != nil {
		t.Fatal(err)
	}
	observeLatency(latencyRecord{RequestID: id, Method: r.Method, API: r.RequestURI[1:10]})
}

func TestSlowRequestBodyStillCompletes(t *testing.T) {
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(w, r.Body) }))
	done, w := make(chan struct{}), httptest.NewRecorder()
	produced, writeResult := make(chan struct{}), make(chan error, 1)
	// Closing both pipe ends must remain reachable if the handler stops reading.
	t.Cleanup(func() {
		cancel()
		reader.Close()
		writer.Close()
		for _, worker := range []struct {
			done  <-chan struct{}
			label string
		}{{produced, "upload producer"}, {done, "upload request"}} {
			select {
			case <-worker.done:
			case <-time.After(2 * time.Second):
				t.Error(worker.label + " cleanup timed out")
			}
		}
		drainCtx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := Drain(drainCtx); err != nil {
			t.Error("upload worker cleanup", err)
		}
	})
	go func() { defer close(done); h.ServeHTTP(w, httptest.NewRequest("POST", "/", reader).WithContext(ctx)) }()
	go func() {
		defer close(produced)
		time.Sleep(125 * time.Millisecond)
		_, err := io.WriteString(writer, "complete body")
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		writeResult <- err
	}()
	select {
	case err := <-writeResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delayed body upload timed out")
	}
	awaitBoundary(t, done, "delayed body response")
	if w.Code != 200 || w.Body.String() != "complete body" {
		t.Fatal("request upload was cut off", w)
	}
}

// Run a deliberately slow useful response through the same measurement gate
// as CI, and verify that the test fails while production still returns success.
func TestPerformanceGateFailsSlowSuccess(t *testing.T) {
	if os.Getenv("CODE_FACTORY_SLOW_GATE_CHILD") == "1" {
		w, started := httptest.NewRecorder(), time.Now()
		Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(125 * time.Millisecond)
			io.WriteString(w, "useful response")
		})).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 200 || w.Body.String() != "useful response" {
			t.Fatal("production must succeed")
		}
		checkResponseLatency(t, "slow useful response", time.Since(started))
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestPerformanceGateFailsSlowSuccess$", "-test.count=1", "-test.timeout=3s")
	cmd.Env = append(os.Environ(), "CODE_FACTORY_SLOW_GATE_CHILD=1")
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("slow-success negative control timed out", ctx.Err())
	}
	if err == nil || !strings.Contains(string(output), "exceeded 75 ms") || strings.Contains(string(output), "production must succeed") {
		t.Fatal("performance gate did not fail slow success", err, string(output))
	}
}
