// A test-only assembled daemon: real MCP/application/store/run code, local Git
// and provider fixtures. No production provider authentication enters the iframe.
package main

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/transport"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	basePort := 4173
	if value := os.Getenv("CODE_FACTORY_TEST_BASE_PORT"); value != "" {
		port, err := strconv.Atoi(value)
		if err != nil || strings.Trim(value, "0123456789") != "" || port < 1 || port > 65534 {
			return fmt.Errorf("CODE_FACTORY_TEST_BASE_PORT must be a decimal integer from 1 to 65534")
		}
		basePort = port
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	dir, e := os.MkdirTemp("", "factory-browser-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	s, e := store.Open(filepath.Join(dir, "state.db"))
	if e != nil {
		return e
	}
	defer s.Close()
	v, e := store.NewVault(s, dir)
	if e != nil {
		return e
	}
	github := providers.NewGitHub()
	a := &app.App{Store: s, Vault: v, DataDir: dir, GitHub: providers.NewCachedGitHub(github)}
	binary, e := filepath.Abs("bin/fake-codex")
	if e != nil {
		return e
	}
	a.Runs = app.NewManager(ctx, a, binary)
	defer a.Runs.Close()
	if e = a.Init(ctx); e != nil {
		return e
	}
	checkout := filepath.Join(dir, "checkout")
	if e = os.MkdirAll(checkout, 0700); e != nil {
		return e
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Pilot", "-c", "user.email=pilot@example.invalid", "commit", "--allow-empty", "-m", "fixture"}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		if e = cmd.Run(); e != nil {
			return e
		}
	}
	repo := domain.Repository{ID: "pilot/fixture", Checkout: checkout, Base: "HEAD"}
	if e = a.SaveRepository(ctx, repo); e != nil {
		return e
	}
	// The synchronization adapter uses a real HTTP provider fixture.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"pilot"}`)
		case "/repos/pilot/fixture/issues":
			fmt.Fprint(w, `[{"number":1,"title":"Assembled daemon fixture","body":"Inspect the fixture","state":"open","html_url":"https://github.com/pilot/fixture/issues/1","updated_at":"2026-10-19T12:00:00Z","user":{"login":"pilot"},"labels":[{"name":"P1"}]},{"number":2,"title":"Fresh first message fixture","body":"Investigate the first-message workflow","state":"open","html_url":"https://github.com/pilot/fixture/issues/2","updated_at":"2026-10-19T12:00:00Z","user":{"login":"pilot"},"labels":[{"name":"P1"}]}]`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer provider.Close()
	github.BaseURL = provider.URL
	github.Token = func(context.Context) (string, error) { return "provider-fixture", nil }
	if e = a.Sync(ctx); e != nil {
		return e
	}
	svc := transport.New(a, events.New(s, v))
	handler := svc.Handler("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"ready": true})
	})
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", basePort+1), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = server.Shutdown(shutdown)
	}()
	e = server.ListenAndServe()
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
