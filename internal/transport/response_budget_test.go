package transport

import (
	"bufio"
	"codefactory/internal/app"
	"codefactory/internal/httpbudget"
	"codefactory/internal/store"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAPILogSlowSinkPreservesSuccessAndLateBusinessLogs503(t *testing.T) {
	s := &Server{App: &app.App{DataDir: t.TempDir()}}
	log := s.callLog()
	log.mu.Lock()
	var unlockOnce sync.Once
	unlockAPI := func() { unlockOnce.Do(log.mu.Unlock) }
	defer func() { unlockAPI(); drainTransportBudget(t) }()
	h := s.LogAPI("owner", "token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "saved-state") }))
	w := httptest.NewRecorder()
	started := time.Now()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 || w.Body.String() != "saved-state" || time.Since(started) > 100*time.Millisecond {
		t.Fatal("audit blocked normal success", w.Code)
	}
	time.Sleep(125 * time.Millisecond)
	unlockAPI()
	records := apiLogRecords(t, filepath.Join(s.App.DataDir, "logs", "api-calls.jsonl"), 1)
	if records[0].HTTPStatus != 200 {
		t.Fatal("success audit incorrect", records)
	}
	release, exited := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseWork := func() { releaseOnce.Do(func() { close(release) }) }
	defer func() { releaseWork(); drainTransportBudget(t) }()
	h = s.LogAPI("owner", "token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(exited)
		<-release
		io.WriteString(w, "private-late-success")
	}))
	w = httptest.NewRecorder()
	started = time.Now()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 503 || time.Since(started) > 100*time.Millisecond || strings.Contains(w.Body.String(), "private-late-success") {
		t.Fatal("late business escaped boundary", w)
	}
	releaseWork()
	awaitTransportSignal(t, exited, "business handler exit")
	records = apiLogRecords(t, filepath.Join(s.App.DataDir, "logs", "api-calls.jsonl"), 2)
	if records[1].HTTPStatus != 503 {
		t.Fatal("late audit mislabeled actual503", records)
	}
}
func TestMCPActualSubscriptionListenHandshakeAndCancellation(t *testing.T) {
	subscriptionListenHandshake(t)
}
func subscriptionListenHandshake(t *testing.T) (time.Duration, int) {
	f := newTrackingFixture(t)
	defer func() { f.http.CloseClientConnections(); drainTransportBudget(t) }()
	body := `{"jsonrpc":"2.0","id":41,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"fixture","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body)).WithContext(ctx)
	r.Host = strings.TrimPrefix(f.http.URL, "http://")
	r.URL.Scheme = "http"
	r.URL.Host = strings.TrimPrefix(f.http.URL, "http://")
	r.RequestURI = ""
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", "2026-07-28")
	r.Header.Set("Mcp-Method", "subscriptions/listen")
	started := time.Now()
	res, err := f.http.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		t.Fatal("promised stream failed", res.StatusCode, string(b))
	}
	reader := bufio.NewReader(res.Body)
	ack, frame := "", ""
	for received := 0; ack == ""; {
		lineBytes, err := reader.ReadSlice('\n')
		if err != nil {
			t.Fatal("missing stream ack", err, frame)
		}
		received += len(lineBytes)
		if received > 64<<10 {
			t.Fatal("stream acknowledgement exceeded bounded parser limit")
		}
		line := string(lineBytes)
		frame += line
		if line == "\n" || line == "\r\n" {
			if strings.Contains(frame, "subscriptions/acknowledged") {
				ack = frame
			}
			frame = ""
		}
	}
	ackDuration := time.Since(started)
	if ackDuration > 100*time.Millisecond {
		t.Fatal("stream acknowledgement exceeded100ms", ackDuration)
	}
	// Consume beyond the complete ack frame. An unread frame separator must not
	// masquerade as liveness, and EOF before cancellation must fail the fixture.
	type readResult struct {
		n   int64
		err error
	}
	readDone := make(chan readResult, 1)
	readerExited := make(chan struct{})
	go func() {
		defer close(readerExited)
		n, err := io.Copy(io.Discard, io.LimitReader(reader, (64<<10)+1))
		readDone <- readResult{n, err}
	}()
	defer func() {
		cancel()
		res.Body.Close()
		select {
		case <-readerExited:
		case <-time.After(time.Second):
			t.Error("stream reader cleanup timed out")
		}
	}()
	liveTimer := time.NewTimer(150 * time.Millisecond)
	defer liveTimer.Stop()
	select {
	case result := <-readDone:
		t.Fatal("acknowledged stream ended before liveness interval", result.n, result.err)
	case <-liveTimer.C:
	}
	// Check again before initiating cancellation, including a read that raced
	// with the liveness timer. The request deadline must still be in the future.
	select {
	case result := <-readDone:
		t.Fatal("acknowledged stream ended before cancellation", result.n, result.err)
	default:
	}
	if ctx.Err() != nil {
		t.Fatal("request expired before deliberate stream cancellation", ctx.Err())
	}
	cancel()
	select {
	case result := <-readDone:
		if result.err == nil {
			t.Fatal("stream did not report request cancellation", result.n)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not promptly observe cancellation")
	}
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := httpbudget.Drain(drainCtx); err != nil {
		t.Fatal("actual SDK stream worker did not exit after cancellation", err)
	}
	return ackDuration, len(ack)
}

func TestAPILogVaultPoolDelayDoesNotBlockFinishedResponse(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	vault, err := store.NewVault(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Set(context.Background(), "jira", "private-vault-value"); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	var wg sync.WaitGroup
	poolCtx, cancelPool := context.WithTimeout(context.Background(), 2*time.Second)
	var releaseOnce sync.Once
	releasePool := func() { releaseOnce.Do(func() { close(release) }) }
	defer func() {
		releasePool()
		cancelPool()
		awaitTransportGroup(t, &wg, "vault readers cleanup")
		drainTransportBudget(t)
	}()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := db.ReadView(poolCtx, func(*store.Store) error {
				entered <- struct{}{}
				select {
				case <-release:
				case <-poolCtx.Done():
				}
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	for range 4 {
		awaitTransportSignal(t, entered, "vault reader readiness")
	}
	server := &Server{App: &app.App{Store: db, Vault: vault, DataDir: dir}}
	h := server.LogAPI("owner", "token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	w := httptest.NewRecorder()
	started := time.Now()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz?custom=private-vault-value", nil))
	if w.Code != 200 || time.Since(started) > 100*time.Millisecond {
		t.Fatal("vault pool blocked completed response", w.Code)
	}
	time.Sleep(time.Until(started.Add(125 * time.Millisecond)))
	releasePool()
	awaitTransportGroup(t, &wg, "vault reader exit")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := httpbudget.Drain(ctx); err != nil {
		t.Fatal("audit did not finish", err)
	}
	records := apiLogRecords(t, filepath.Join(dir, "logs", "api-calls.jsonl"), 1)
	if records[0].HTTPStatus != 200 {
		t.Fatal("incorrect vault-delay wire status")
	}
}

func TestAPILogUncommittedPanicAndSlowSinkNeverInventWire500(t *testing.T) {
	s := &Server{App: &app.App{DataDir: t.TempDir()}}
	log := s.callLog()
	log.mu.Lock()
	var unlockOnce sync.Once
	unlockAPI := func() { unlockOnce.Do(log.mu.Unlock) }
	defer func() { unlockAPI(); drainTransportBudget(t) }()
	h := s.LogAPI("owner", "token", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private-panic-fixture") }))
	w := httptest.NewRecorder()
	started := time.Now()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 503 || time.Since(started) > 100*time.Millisecond {
		t.Fatal("panic audit blocked response", w.Code)
	}
	time.Sleep(time.Until(started.Add(125 * time.Millisecond)))
	unlockAPI()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := httpbudget.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	record := apiLogRecords(t, filepath.Join(s.App.DataDir, "logs", "api-calls.jsonl"), 1)[0]
	if !record.Panicked || record.HandlerStatus != 500 {
		t.Fatal("inner panic status lost", record)
	}
	if record.HTTPStatusKnown {
		if record.HTTPStatus != 503 {
			t.Fatal("invented committed panic status", record)
		}
	} else if record.HTTPStatus != 0 {
		t.Fatal("unknown wire status must be zero", record)
	}
}

func awaitTransportSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(label + " timed out")
	}
}
func awaitTransportGroup(t *testing.T, wg *sync.WaitGroup, label string) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error(label + " timed out")
	}
}
func drainTransportBudget(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := httpbudget.Drain(ctx); err != nil {
		t.Error("HTTP workers did not exit", err)
	}
}
