package setup

import (
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
