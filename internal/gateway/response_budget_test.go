package gateway

import (
	"codefactory/internal/httpbudget"
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func waitBudgetWorkers(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := httpbudget.Drain(ctx); err != nil {
		t.Error("admitted HTTP workers did not exit", err)
	}
}
func budgetGateway(t *testing.T, backend string) *Gateway {
	t.Helper()
	if backend == "" {
		backend = "127.0.0.1:8787"
	}
	g, err := New(Config{PublicURL: "https://public.example", ApprovalURL: "https://private.example:8444", Owner: "owner@example.com", PublicListen: "127.0.0.1:8788", ApprovalListen: "127.0.0.1:8789", Daemon: backend}, t.TempDir(), strings.Repeat("f", 64))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { waitBudgetWorkers(t) })
	return g
}
func boundedGatewayRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	started := time.Now()
	w := gatewayRequest(t, h, method, path, body)
	recordGatewayTiming(t, method, path, w.Code, started)
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("response exceeded100ms", path, time.Since(started))
	}
	return w
}
func gatewayRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Tailscale-User-Login", "owner@example.com")
	r.Header.Set("Origin", "https://private.example:8444")
	h.ServeHTTP(w, r)
	return w
}
func TestGatewayFiniteResponseContentionAndDelayedProxy(t *testing.T) {
	g := budgetGateway(t, "")
	public, private := g.Public(), g.Private()
	for _, row := range []struct {
		h                  http.Handler
		method, path, body string
		status             int
	}{{public, "POST", "/register", `{"redirect_uris":["https://chatgpt.com/connector_platform_oauth_redirect"]}`, 201}, {public, "GET", "/authorize", "", 400}, {public, "POST", "/token", "grant_type=refresh_token", 400}, {public, "POST", "/revoke", "token=x", 200}, {public, "POST", "/mcp", "{}", 401}, {private, "GET", "/approve?request=x", "", 410}, {private, "POST", "/approve", "request=x", 410}} {
		g.mu.Lock()
		released := make(chan struct{})
		go func() { time.Sleep(125 * time.Millisecond); g.mu.Unlock(); close(released) }()
		w := gatewayRequest(t, row.h, row.method, row.path, row.body)
		if w.Code != row.status {
			t.Fatal("slow request changed protocol result", row.path, w.Code, "want", row.status, w.Body.String())
		}
		awaitGatewaySignal(t, released, "gateway mutex release")
	}
	// A finite upstream that flushes ordinary JSON headers must still be buffered.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "private-partial")
		http.NewResponseController(w).Flush()
		select {
		case <-time.After(125 * time.Millisecond):
		case <-r.Context().Done():
		}
		io.WriteString(w, "late-success")
	}))
	defer func() { backend.CloseClientConnections(); backend.Close() }()
	proxied := budgetGateway(t, strings.TrimPrefix(backend.URL, "http://"))
	w := gatewayRequest(t, proxied.Public(), "POST", "/github/webhook", "{}")
	if w.Code != 200 || w.Body.String() != "private-partiallate-success" {
		t.Fatal("slow upstream response was lost", w)
	}
}
func TestGatewayDelayedRegistrationMappingRemainsFinite(t *testing.T) {
	for _, path := range []string{"/", "/plugin-browser.zip"} {
		t.Run(path, func(t *testing.T) {
			g := budgetGateway(t, "")
			fifo := filepath.Join(filepath.Dir(g.path), "registered-app.json")
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			done := make(chan struct{})
			requestDone := make(chan struct{})
			// Replacement prevents a handler that arrives late from opening a FIFO with
			// no writer. The old inode is also released for an already-blocked reader.
			defer func() {
				cancel()
				retired := fifo + ".retired"
				_ = os.Rename(fifo, retired)
				_ = os.WriteFile(fifo, []byte(`{"id":"asdk_app_delay"}`), 0600)
				if fd, err := syscall.Open(retired, syscall.O_RDWR|syscall.O_NONBLOCK, 0600); err == nil {
					_, _ = syscall.Write(fd, []byte(`{"id":"asdk_app_delay"}`))
					_ = syscall.Close(fd)
				}
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Error("mapping writer cleanup timed out")
				}
				select {
				case <-requestDone:
				case <-time.After(2 * time.Second):
					t.Error("mapping request cleanup timed out")
				}
				waitBudgetWorkers(t)
			}()
			go func() {
				defer close(done)
				timer := time.NewTimer(125 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
					return
				}
				tick := time.NewTicker(time.Millisecond)
				defer tick.Stop()
				for {
					fd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0600)
					if err == nil {
						_, _ = syscall.Write(fd, []byte(`{"id":"asdk_app_delay"}`))
						_ = syscall.Close(fd)
						return
					}
					if err != syscall.ENXIO {
						t.Error("mapping writer unavailable", err)
						return
					}
					select {
					case <-ctx.Done():
						t.Error("mapping reader never opened FIFO")
						return
					case <-tick.C:
					}
				}
			}()
			var w *httptest.ResponseRecorder
			go func() {
				defer close(requestDone)
				h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					g.Public().ServeHTTP(w, r.WithContext(ctx))
				})
				w = gatewayRequest(t, h, "GET", path, "")
			}()
			awaitGatewaySignal(t, requestDone, "delayed registration response")
			if w.Code != 200 {
				t.Fatal("delayed registration read failed", w.Code)
			}
			awaitGatewaySignal(t, done, "mapping writer exit")
		})
	}
}

type blockedAudit struct {
	release chan struct{}
	records chan slog.Record
	once    sync.Once
}

func (h *blockedAudit) Enabled(context.Context, slog.Level) bool { return true }
func (h *blockedAudit) Handle(_ context.Context, r slog.Record) error {
	h.once.Do(func() { <-h.release })
	select {
	case h.records <- r.Clone():
		return nil
	case <-time.After(2 * time.Second):
		return context.DeadlineExceeded
	}
}
func (h *blockedAudit) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *blockedAudit) WithGroup(string) slog.Handler      { return h }
func TestGatewayAuditKeepsActualWireStatusWithSlowSink(t *testing.T) {
	sink := &blockedAudit{release: make(chan struct{}), records: make(chan slog.Record, 3)}
	old := slog.Default()
	oldWriter, oldFlags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(sink))
	var releaseOnce sync.Once
	releaseAudit := func() { releaseOnce.Do(func() { close(sink.release) }) }
	var restoreOnce sync.Once
	restoreLogger := func() {
		restoreOnce.Do(func() {
			releaseAudit()
			waitBudgetWorkers(t)
			slog.SetDefault(old)
			log.SetOutput(oldWriter)
			log.SetFlags(oldFlags)
		})
	}
	defer restoreLogger()
	g := budgetGateway(t, "")
	started := time.Now()
	w := boundedGatewayRequest(t, g.Public(), "GET", "/healthz", "")
	if w.Code != 200 {
		t.Fatal("slow audit changed completed success", w.Code)
	}
	time.Sleep(time.Until(started.Add(125 * time.Millisecond)))
	releaseAudit()
	record := awaitGatewayAudit(t, sink.records)
	var status int64
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == "status" {
			status = a.Value.Int64()
		}
		return true
	})
	if status != 200 {
		t.Fatal("audit mislabeled wire success", status)
	}
	g.mu.Lock()
	var unlockOnce sync.Once
	unlockMu := func() { unlockOnce.Do(g.mu.Unlock) }
	defer unlockMu()
	go func() { time.Sleep(125 * time.Millisecond); unlockMu() }()
	w = gatewayRequest(t, g.Public(), "POST", "/token", "grant_type=refresh_token")
	unlockMu()
	if w.Code != 400 {
		t.Fatal("slow business lost protocol result")
	}
	record = awaitGatewayAudit(t, sink.records)
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == "status" {
			status = a.Value.Int64()
		}
		return true
	})
	if status != 400 {
		t.Fatal("late audit lost protocol result", status)
	}
	// Restoring slog alone leaves the standard-log bridge aimed at the test
	// sink when old used the default handler. More than capacity requests must
	// still reach their ordinary sink and release real worker admission.
	restoreLogger()
	for range httpbudget.Capacity + 8 {
		if got := boundedGatewayRequest(t, g.Public(), "GET", "/healthz", ""); got.Code != 200 {
			t.Fatal("restored logger retained audit workers", got.Code)
		}
	}
	waitBudgetWorkers(t)
}

func TestOAuthDeadlineDoesNotReplayConsumedRefresh(t *testing.T) {
	g := budgetGateway(t, "")
	secret := "refresh-fixture"
	g.state.Refresh[hash(secret)] = credential{Client: "client", Resource: g.resource(), Expires: time.Now().Add(time.Hour)}
	// The business mutation settles once. A lost response must expose uncertainty,
	// and the exact consumed refresh cannot authorize a second exchange.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { g.token(w, r); <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	// Use a separate wrapper because normal production audit signals completion.
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { httpbudget.Wrap(inner).ServeHTTP(w, r.WithContext(ctx)) })
	w := boundedGatewayRequest(t, h, "POST", "/token", "grant_type=refresh_token&refresh_token="+secret+"&client_id=client&resource=https%3A%2F%2Fpublic.example%2Fmcp")
	if w.Code != 503 {
		t.Fatal("lost token exchange claimed success", w.Code)
	}
	w = boundedGatewayRequest(t, g.Public(), "POST", "/token", "grant_type=refresh_token&refresh_token="+secret+"&client_id=client&resource=https%3A%2F%2Fpublic.example%2Fmcp")
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 400 || body["error"] != "invalid_grant" {
		t.Fatal("consumed refresh replayed", w.Code, body)
	}
}

func TestGatewaySSERequiresFirstDataAndKeepsAcknowledgedStream(t *testing.T) {
	for _, delay := range []time.Duration{0, 125 * time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			canceled := make(chan struct{})
			acknowledgedAt := make(chan time.Time, 1)
			const ackFrame = "data: {\"method\":\"subscriptions/acknowledged\"}\n\n"
			const aliveFrame = "data: {\"method\":\"fixture/still-alive\"}\n\n"
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(canceled)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				http.NewResponseController(w).Flush()
				if delay > 0 {
					select {
					case <-time.After(delay):
					case <-r.Context().Done():
						return
					}
				}
				acknowledgedAt <- time.Now()
				io.WriteString(w, ackFrame)
				http.NewResponseController(w).Flush()
				select {
				case <-time.After(150 * time.Millisecond):
					io.WriteString(w, aliveFrame)
					http.NewResponseController(w).Flush()
				case <-r.Context().Done():
					return
				}
				<-r.Context().Done()
			}))
			defer func() { backend.CloseClientConnections(); backend.Close() }()
			g := budgetGateway(t, strings.TrimPrefix(backend.URL, "http://"))
			g.state.Tokens[hash("access")] = credential{Resource: g.resource(), Expires: time.Now().Add(time.Hour)}
			server := httptest.NewServer(g.Public())
			defer func() {
				server.CloseClientConnections()
				backend.CloseClientConnections()
				waitBudgetWorkers(t)
				server.Close()
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/mcp", strings.NewReader(`{"method":"subscriptions/listen"}`))
			r.Header.Set("Authorization", "Bearer access")
			started := time.Now()
			res, err := server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body := make([]byte, len(ackFrame))
			if _, err := io.ReadFull(res.Body, body); err != nil {
				t.Fatal(err)
			}
			if delay == 0 {
				recordGatewayTiming(t, "POST", "/mcp", res.StatusCode, started)
			}
			if *apiPerformance && delay == 0 {
				gatewaySamples.Lock()
				gatewaySamples.Rows[len(gatewaySamples.Rows)-1].Outcome = "stream_ack"
				gatewaySamples.Unlock()
			}
			if res.StatusCode != 200 || string(body) != ackFrame || (delay == 0 && time.Since(started) > 100*time.Millisecond) {
				t.Fatal("slow real proxy stream handshake", res.StatusCode)
			}
			var acknowledged time.Time
			select {
			case acknowledged = <-acknowledgedAt:
			case <-ctx.Done():
				t.Fatal("upstream acknowledgement timestamp unavailable", ctx.Err())
			}
			alive := make([]byte, len(aliveFrame))
			if _, err := io.ReadFull(res.Body, alive); err != nil {
				t.Fatal("proxy closed after acknowledgement instead of retaining stream", err)
			}
			if string(alive) != aliveFrame || time.Since(acknowledged) < 125*time.Millisecond {
				t.Fatal("proxy did not forward later live data beyond125ms", string(alive))
			}
			select {
			case <-canceled:
				t.Fatal("upstream ended before client closed the live stream")
			default:
			}
			res.Body.Close()
			select {
			case <-canceled:
				if ctx.Err() != nil {
					t.Fatal("upstream stopped only at outer deadline, not body close")
				}
			case <-time.After(time.Second):
				t.Fatal("body close did not cancel upstream stream")
			}
		})
	}
}

func awaitGatewaySignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(label + " timed out")
	}
}
func awaitGatewayAudit(t *testing.T, records <-chan slog.Record) slog.Record {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case record := <-records:
			if record.Message == "Code Factory gateway request" {
				return record
			}
		case <-timer.C:
			t.Fatal("gateway audit completion timed out")
			return slog.Record{}
		}
	}
}
