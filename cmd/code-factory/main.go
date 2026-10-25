package main

import (
	"bufio"
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/buildinfo"
	"codefactory/internal/events"
	"codefactory/internal/gateway"
	"codefactory/internal/providers"
	"codefactory/internal/reports"
	"codefactory/internal/setup"
	"codefactory/internal/store"
	"codefactory/internal/transport"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
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
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: code-factory serve|mcp|doctor|version|install|upgrade|rollback|uninstall|install-dependencies|configure-tunnel|onboarding|tunnel [--data-dir path]")
	}
	command := os.Args[1]
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	dir := f.String("data-dir", setup.DataDir(), "Private local data directory")
	address := f.String("listen", "127.0.0.1:8787", "Loopback HTTP listener")
	binary := f.String("codex", "codex", "Codex executable")
	source := f.String("from", "", "Extracted release directory")
	replaceKey := f.Bool("replace-key", false, "Replace the saved tunnel runtime key through the secure form")
	manual := f.Bool("manual-browser", false, "Print a local setup URL if browser opening is unavailable")
	organization := f.String("organization", "", "Observed Platform organization ID")
	workspace := f.String("workspace", "", "Observed Work workspace ID")
	ownerOnly := f.Bool("owner-only", false, "Confirm verified owner-only tunnel associations")
	appID := f.String("app-id", "", "Observed registered Work app ID")
	checkpoint := f.String("checkpoint", "", "Acceptance checkpoint to record")
	evidence := f.String("evidence", "", "Nonsecret evidence reference")
	state := f.String("state", "unverified", "verified, failed or unverified")
	tunnelID := f.String("id", "", "Tunnel identifier (configure-tunnel only)")
	if e := f.Parse(os.Args[2:]); e != nil {
		return e
	}
	var e error
	*dir, e = filepath.Abs(*dir)
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if command == "record-evidence" {
		return setup.RecordEvidence(*dir, *checkpoint, setup.State(*state), *evidence)
	}
	if command == "install-plugin" {
		name, e := setup.RegisterPersonalPlugin()
		if e != nil {
			return e
		}
		fmt.Println("Code Factory is available in the personal plugin marketplace:", name)
		return nil
	}
	if command == "link-plugin" {
		return setup.LinkPlugin(*appID)
	}
	if command == "version" {
		fmt.Println(buildinfo.Version)
		return nil
	}
	if command == "install-dependencies" {
		return setup.InstallTunnel(ctx)
	}
	if command == "rollback" {
		return setup.Rollback(ctx)
	}
	if command == "uninstall" {
		return setup.Uninstall(ctx)
	}
	if command == "install" || command == "upgrade" {
		if *source == "" {
			exe, e := os.Executable()
			if e != nil {
				return e
			}
			*source = filepath.Dir(exe)
		}
		return setup.InstallRelease(ctx, *dir, *source, *address)
	}
	if r, err := setup.ReadRuntime(*dir); err == nil {
		if err = os.Setenv("PATH", r.Path); err != nil {
			return err
		}
		if *binary == "codex" {
			if p := r.Paths["codex"]; p != "" {
				*binary = p
			}
		}
		if *address == "127.0.0.1:8787" {
			*address = r.Listen
		}
	}
	if command == "configure-tailscale" {
		return setup.ConfigureTailscale(ctx, *dir)
	}
	if command == "configure-tunnel" {
		h, e := os.UserHomeDir()
		if e != nil {
			return e
		}
		return setup.ConfigureOwnedTunnel(ctx, filepath.Join(h, ".local", "bin", "code-factory"), *dir, setup.TunnelCheckpoint{ID: *tunnelID, Organization: *organization, Workspace: *workspace, OwnerOnly: *ownerOnly})
	}
	if command == "doctor" {
		if e = setup.LoopbackAddress(*address); e != nil {
			return e
		}
		d, e := setup.FetchDiagnostics(ctx, *dir, *address)
		if e != nil {
			d = setup.Diagnose(ctx, *dir, *binary, *address, nil)
		}
		return json.NewEncoder(os.Stdout).Encode(d)
	}

	token, e := setup.Token(*dir)
	if e != nil {
		return e
	}
	if command == "mcp" {
		host, _, err := net.SplitHostPort(*address)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return fmt.Errorf("proxy destination must use a loopback IP address")
		}
		return proxy(ctx, *address, token)
	}
	if command == "serve" {
		lock, err := setup.LockDaemon(*dir)
		if err != nil {
			return err
		}
		defer lock.Close()
	}
	s, e := store.Open(filepath.Join(*dir, "state.db"))
	if e != nil {
		return e
	}
	defer s.Close()
	vault, e := store.NewVault(s, *dir)
	if e != nil {
		return e
	}
	if command == "gateway" {
		var cfg gateway.Config
		b, e := os.ReadFile(filepath.Join(*dir, "tailscale.json"))
		if e != nil {
			return fmt.Errorf("configure Tailscale first: %w", e)
		}
		if e = json.Unmarshal(b, &cfg); e != nil {
			return e
		}
		g, e := gateway.New(cfg, *dir, token)
		if e != nil {
			return e
		}
		return g.Serve(ctx)
	}
	if command == "onboarding" {
		if e = setup.RequireOwnedTunnel(*dir); e != nil {
			return e
		}
		return setup.Onboard(ctx, vault, *dir, *manual, *replaceKey)
	}
	if command == "tunnel" || command == "tunnel-doctor" {
		if e = setup.RequireOwnedTunnel(*dir); e != nil {
			return e
		}
		key, e := vault.Get(ctx, "tunnel-key")
		if e != nil {
			return fmt.Errorf("setup blocker: enter tunnel runtime key in the secure local onboarding form")
		}
		args := []string{"run", "--profile", "code-factory", "--health.url-file", filepath.Join(*dir, "tunnel-health.url")}
		if command == "tunnel-doctor" {
			args = []string{"doctor", "--profile", "code-factory", "--explain", "--json"}
		}
		cmd := exec.CommandContext(ctx, "tunnel-client", args...)
		owned, e := setup.ReadTunnel(*dir)
		if e != nil {
			return e
		}
		cmd.Env = append(os.Environ(), "CONTROL_PLANE_API_KEY="+key, "CONTROL_PLANE_ORGANIZATION_ID="+owned.Organization)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		result := cmd.Run()
		if command == "tunnel-doctor" {
			state := setup.Verified
			if result != nil {
				state = setup.Failed
			}
			if e := setup.AtomicJSON(filepath.Join(*dir, "tunnel-doctor.json"), setup.Check{ID: "tunnel.doctor", State: state, At: time.Now().UTC(), Detail: "Official client validation; raw output suppressed"}); e != nil {
				return e
			}
			if result != nil {
				return fmt.Errorf("tunnel doctor failed; check selected associations, runtime-key permissions and network reachability")
			}
			fmt.Println("Tunnel doctor passed; verify readiness and Work discovery separately")
		}
		return result
	}
	if command == "report-publish" {
		service := &reports.Service{Store: s, Vault: vault, Dir: *dir, GitHub: &reports.GitHubAdapter{Client: providers.NewGitHub()}}
		return service.PublishReport(ctx, *tunnelID)
	}
	if command != "serve" {
		return fmt.Errorf("unknown command %s", command)
	}
	host, _, e := net.SplitHostPort(*address)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("listener must use a loopback IP address")
	}
	a := &app.App{Store: s, Vault: vault, GitHub: providers.NewGitHub(), DataDir: *dir}
	a.Runs = app.NewManager(ctx, a, *binary)
	if e = a.Init(ctx); e != nil {
		return e
	}
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	a.Reports.PublishCommand = func(ctx context.Context, id string) error {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "report-publish", "--data-dir", *dir, "--id", id)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("report publication command interrupted; retained intent will be reconciled")
		}
		return nil
	}
	if e = a.Runs.Recover(ctx); e != nil {
		return e
	}
	ev := events.New(s, vault)
	server := transport.New(a, ev)
	mux := http.NewServeMux()
	mux.Handle("/mcp", server.Handler(token))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "Unauthorized", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": buildinfo.Version, "status": "verified"})
	})
	httpServer := &http.Server{Addr: *address, Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){a.SyncLoop, a.AssessmentLoop, a.Reports.Loop, ev.Loop} {
		wg.Add(1)
		go func(loop func(context.Context)) { defer wg.Done(); loop(ctx) }(loop)
	}
	go func() {
		<-ctx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = httpServer.Shutdown(shutdown)
	}()
	e = httpServer.ListenAndServe()
	cancel()
	a.Runs.Close()
	wg.Wait()
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
func proxy(ctx context.Context, address, token string) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	client := &http.Client{Timeout: 110 * time.Second}
	writer := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		body := append([]byte(nil), scanner.Bytes()...)
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Meta map[string]any `json:"_meta"`
				Name string         `json:"name"`
				URI  string         `json:"uri"`
			}
		}
		if json.Unmarshal(body, &msg) != nil {
			return fmt.Errorf("invalid MCP JSON")
		}
		r, e := http.NewRequestWithContext(ctx, "POST", "http://"+address+"/mcp", bytes.NewReader(body))
		if e != nil {
			return e
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		version := "2025-11-25"
		if msg.Method == "server/discover" || msg.Params.Meta["io.modelcontextprotocol/protocolVersion"] == "2026-07-28" {
			version = "2026-07-28"
		}
		r.Header.Set("MCP-Protocol-Version", version)
		r.Header.Set("Mcp-Method", msg.Method)
		if msg.Params.Name != "" {
			r.Header.Set("Mcp-Name", msg.Params.Name)
		}
		if msg.Params.URI != "" {
			r.Header.Set("Mcp-Name", msg.Params.URI)
		}
		res, e := client.Do(r)
		if e != nil {
			return fmt.Errorf("local daemon unavailable; start Code Factory service")
		}
		b, e := io.ReadAll(io.LimitReader(res.Body, 10<<20))
		res.Body.Close()
		if e != nil {
			return e
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return fmt.Errorf("local MCP request failed (HTTP %d)", res.StatusCode)
		}
		if len(msg.ID) > 0 && len(b) > 0 {
			if strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
				for _, line := range strings.Split(string(b), "\n") {
					if strings.HasPrefix(line, "data: ") {
						if _, e = fmt.Fprintln(writer, strings.TrimPrefix(line, "data: ")); e != nil {
							return e
						}
					}
				}
			} else if _, e = fmt.Fprintln(writer, strings.TrimSpace(string(b))); e != nil {
				return e
			}
			if e = writer.Flush(); e != nil {
				return e
			}
		}
	}
	return scanner.Err()
}
