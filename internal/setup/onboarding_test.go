package setup

import (
	"codefactory/internal/httpbudget"
	"codefactory/internal/store"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCredentialFormReachabilityOriginExpiryAndSingleSubmission(t *testing.T) {
	var saves atomic.Int32
	f := &credentialForm{nonce: "local-session", expires: time.Now().Add(time.Minute), done: make(chan error, 1), save: func(context.Context, string) error { saves.Add(1); return nil }}
	server := httptest.NewServer(f)
	defer server.Close()
	f.origin = server.URL
	post := func(path, origin, nonce string) int {
		r, _ := http.NewRequest("POST", server.URL+path, strings.NewReader("key=secret-test-key"))
		r.Header.Set("Origin", origin)
		r.Header.Set("Authorization", "Bearer "+nonce)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, e := server.Client().Do(r)
		if e != nil {
			t.Error(e)
			return 0
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if strings.Contains(string(b), "secret-test-key") {
			t.Error("secret echoed")
		}
		return res.StatusCode
	}
	if post("/credential", server.URL, f.nonce) != 409 {
		t.Fatal("accepted without browser check")
	}
	if post("/ready", "http://attacker.invalid", f.nonce) != 403 {
		t.Fatal("cross origin accepted")
	}
	if post("/ready", server.URL, "wrong") != 403 {
		t.Fatal("wrong nonce accepted")
	}
	if post("/ready", server.URL, f.nonce) != 200 {
		t.Fatal("browser check failed")
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			code := post("/credential", server.URL, f.nonce)
			if code != 200 && code != 409 {
				t.Errorf("unexpected %d", code)
			}
		})
	}
	wg.Wait()
	if saves.Load() != 1 {
		t.Fatal("credential saved more than once")
	}
	f.expires = time.Now().Add(-time.Second)
	if post("/credential", server.URL, f.nonce) != 410 {
		t.Fatal("expired form accepted")
	}
}

func TestOnboardReusesSavedCredentialWithoutBrowser(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if e := os.Mkdir(bin, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nexit 0\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin)
	s, e := store.Open(filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v, e := store.NewVault(s, dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = v.Set(context.Background(), "tunnel-key", "saved-test-runtime-key"); e != nil {
		t.Fatal(e)
	}
	if e = Onboard(context.Background(), v, dir, false, false); e != nil {
		t.Fatal("saved credential required another browser session", e)
	}
}

func TestCredentialFormDeadlinePreservesSingleSubmissionAndUncertainty(t *testing.T) {
	var saves atomic.Int32
	entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseSave := func() { releaseOnce.Do(func() { close(release) }) }
	defer func() {
		releaseSave()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := httpbudget.Drain(ctx); err != nil {
			t.Error("onboarding worker cleanup failed", err)
		}
	}()
	f := &credentialForm{origin: "http://127.0.0.1:8787", nonce: "private-nonce", expires: time.Now().Add(time.Minute), done: make(chan error, 1), save: func(ctx context.Context, key string) error {
		saves.Add(1)
		close(entered)
		defer close(exited)
		<-release
		return ctx.Err()
	}}
	f.ready.Store(true)
	post := func(ctx context.Context) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", f.origin+"/credential", strings.NewReader("key=private-key")).WithContext(ctx)
		r.Header.Set("Origin", f.origin)
		r.Header.Set("Authorization", "Bearer "+f.nonce)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		return w
	}
	started := time.Now()
	requestCtx, cancelRequest := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelRequest()
	w := post(requestCtx)
	awaitOnboarding(t, entered, "credential save readiness")
	if time.Since(started) > 100*time.Millisecond || w.Code != 503 || !strings.Contains(w.Body.String(), "outcome_unknown") || strings.Contains(w.Body.String(), "private-key") {
		t.Fatal("save did not expose bounded uncertainty", w.Code, w.Body.String())
	}
	if post(context.Background()).Code != 409 || saves.Load() != 1 {
		t.Fatal("uncertain credential was replayed")
	}
	time.Sleep(time.Until(started.Add(125 * time.Millisecond)))
	releaseSave()
	awaitOnboarding(t, exited, "credential save exit")
	var result error
	select {
	case result = <-f.done:
	case <-time.After(2 * time.Second):
		t.Fatal("credential form did not finish")
	}
	if err := result; err == nil {
		t.Fatal("deadline cancellation lost")
	}
	if strings.Contains(w.Body.String(), "Credential saved") {
		t.Fatal("late onboarding success escaped")
	}
}

func TestCredentialFormSlowSaveCompletesOnce(t *testing.T) {
	var saves atomic.Int32
	f := &credentialForm{origin: "http://127.0.0.1:8787", nonce: "fixture-nonce", expires: time.Now().Add(time.Minute), done: make(chan error, 1), save: func(ctx context.Context, _ string) error {
		saves.Add(1)
		time.Sleep(125 * time.Millisecond)
		return ctx.Err()
	}}
	f.ready.Store(true)
	post := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", f.origin+"/credential", strings.NewReader("key=fixture-key"))
		r.Header.Set("Origin", f.origin)
		r.Header.Set("Authorization", "Bearer "+f.nonce)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		return w
	}
	w := post()
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Credential saved") || saves.Load() != 1 || strings.Contains(w.Body.String(), "fixture-key") {
		t.Fatal("slow credential save lost its successful result", w.Code)
	}
	if post().Code != 409 || saves.Load() != 1 {
		t.Fatal("slow credential save was replayed")
	}
	select {
	case err := <-f.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow save did not finish")
	}
}

func awaitOnboarding(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(label + " timed out")
	}
}
