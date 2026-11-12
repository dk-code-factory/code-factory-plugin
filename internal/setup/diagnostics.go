package setup

import (
	"bytes"
	"codefactory/internal/buildinfo"
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/gateway"
	"codefactory/internal/store"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type State string

const (
	Verified   State = "verified"
	Failed     State = "failed"
	Unverified State = "unverified"
)

type Check struct {
	ID     string    `json:"id"`
	State  State     `json:"state"`
	Detail string    `json:"detail"`
	At     time.Time `json:"at"`
}
type Diagnostics struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
	Checks  []Check   `json:"checks"`
}

// Diagnose is the shared CLI/MCP contract. It does not expose provider output,
// run prompts, callback URLs/keys, environment variables, or credential values.
func Diagnose(ctx context.Context, dir, binary, address string, s *store.Store) Diagnostics {
	d := Diagnostics{Version: buildinfo.Version, At: time.Now().UTC(), Checks: []Check{}}
	add := func(id string, state State, detail string) {
		d.Checks = append(d.Checks, Check{id, state, detail, d.At})
	}
	var tailscale gateway.Config
	b, te := os.ReadFile(filepath.Join(dir, "tailscale.json"))
	useTailscale := te == nil && json.Unmarshal(b, &tailscale) == nil
	names := []string{"git", "gh", "codex", "systemctl"}
	if useTailscale {
		names = append(names, "tailscale")
	} else {
		names = append(names, "tunnel-client")
	}
	for _, name := range names {
		if _, e := exec.LookPath(name); e != nil {
			add("prerequisite."+name, Failed, "Executable unavailable")
		} else {
			add("prerequisite."+name, Verified, "Executable resolved")
		}
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	status := codex.Inspect(check, binary)
	if status.Compatible && status.Authenticated {
		add("provider.codex", Verified, status.Version+" authenticated; real task acceptance is separate")
	} else {
		add("provider.codex", Failed, "Codex compatibility or authentication check failed")
	}
	if token, err := exec.CommandContext(check, "gh", "auth", "token", "--hostname", "github.com").Output(); err == nil && len(bytes.TrimSpace(token)) > 0 {
		add("provider.github", Verified, "GitHub CLI credential available locally; repository access is checked during budgeted synchronization")
	} else {
		add("provider.github", Failed, "GitHub CLI authentication unavailable")
	}
	if s != nil {
		add("daemon", Verified, "Authenticated daemon answered diagnostics")
	} else {
		state := Failed
		token, e := os.ReadFile(filepath.Join(dir, "owner.token"))
		if e == nil {
			req, e := http.NewRequestWithContext(check, "GET", "http://"+address+"/healthz", nil)
			if e == nil {
				req.Header.Set("Authorization", "Bearer "+string(token))
				client := &http.Client{Timeout: 2 * time.Second}
				res, e := client.Do(req)
				if e == nil {
					var out struct{ Version string }
					decode := json.NewDecoder(res.Body).Decode(&out)
					res.Body.Close()
					if res.StatusCode == 200 && decode == nil && out.Version == buildinfo.Version {
						state = Verified
					}
				}
			}
		}
		add("daemon", state, "Authenticated health endpoint for this version")
	}
	if useTailscale {
		add("transport.tailscale", Verified, "Tailscale Funnel with owner-only Serve approval configured")
		state := Failed
		if exec.CommandContext(check, "systemctl", "--user", "is-active", "--quiet", "code-factory-gateway.service").Run() == nil {
			state = Verified
		}
		add("gateway.process", state, "OAuth gateway service")
		state = Failed
		if LoopbackAddress(tailscale.PublicListen) == nil {
			req, e := http.NewRequestWithContext(check, "GET", "http://"+tailscale.PublicListen+"/healthz", nil)
			if e == nil {
				res, e := (&http.Client{Timeout: 2 * time.Second}).Do(req)
				if e == nil {
					res.Body.Close()
					if res.StatusCode == 200 {
						state = Verified
					}
				}
			}
		}
		add("gateway.readiness", state, "Local OAuth gateway health; Work connection acceptance remains separate")
	} else {
		if _, e := os.Stat(filepath.Join(dir, "tunnel.json")); e == nil {
			add("tunnel.configured", Verified, "Local tunnel profile recorded")
		} else {
			add("tunnel.configured", Unverified, "Configure an owner-only tunnel in Platform")
		}
		// Service status establishes a running process, not cloud readiness.
		if exec.CommandContext(check, "systemctl", "--user", "is-active", "--quiet", "code-factory-tunnel.service").Run() == nil {
			add("tunnel.process", Verified, "Tunnel service active")
		} else {
			add("tunnel.process", Unverified, "Tunnel service is not active")
		}
		readiness := Unverified
		if b, e := os.ReadFile(filepath.Join(dir, "tunnel-health.url")); e == nil {
			u, e := url.Parse(strings.TrimSpace(string(b)))
			if e == nil && u.Scheme == "http" && u.User == nil && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback() {
				req, e := http.NewRequestWithContext(check, "GET", strings.TrimRight(u.String(), "/")+"/readyz", nil)
				if e == nil {
					client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					res, e := client.Do(req)
					if e == nil {
						res.Body.Close()
						readiness = Failed
						if res.StatusCode == 200 {
							readiness = Verified
						}
					}
				}
			}
		}
		add("tunnel.readiness", readiness, "Local tunnel /readyz; Work discovery is a separate acceptance checkpoint")

	}

	if s != nil {
		repos, e := store.List[domain.Repository](ctx, s, "repository")
		if e != nil {
			add("sync", Failed, "Could not read synchronization state")
		} else if len(repos) == 0 {
			add("sync", Unverified, "No repositories configured")
		} else {
			for _, r := range repos {
				state := Verified
				detail := "Last successful sync: " + r.LastSync.UTC().Format(time.RFC3339)
				if r.Paused {
					state = Unverified
					detail = "Repository monitoring paused"
				} else if time.Now().Before(r.RetryAt) {
					state = Unverified
					detail = "Synchronization deferred until " + r.RetryAt.UTC().Format(time.RFC3339)
				} else if r.Error != "" {
					state = Failed
					detail = "Last synchronization failed"
				} else if r.LastSync.IsZero() {
					state = Unverified
					detail = "First successful sync pending"
				} else if time.Since(r.LastSync) > 30*time.Minute {
					state = Failed
					detail = "Successful synchronization is overdue"
				}
				add("sync."+r.ID, state, detail)
			}
		}
	} else {
		add("sync", Unverified, "Query the running daemon diagnostics tool for sync evidence")
	}
	if s != nil {
		type callback struct {
			VerifiedAt, DeliveredAt, Expires time.Time
			Disabled                         bool
			Error                            string
		}
		subs, e := store.List[callback](ctx, s, "subscription")
		state := Unverified
		detail := "No delivered callback observed"
		if e != nil {
			state = Failed
			detail = "Callback state unavailable"
		} else {
			for _, sub := range subs {
				if sub.Disabled || time.Now().After(sub.Expires) {
					continue
				}
				if sub.Error != "" {
					state = Failed
					detail = "Callback delivery failed"
					break
				}
				if !sub.DeliveredAt.IsZero() {
					state = Verified
					detail = "Callback endpoint accepted a signed event at " + sub.DeliveredAt.Format(time.RFC3339) + "; Work receipt must be checked separately"
				}
			}
		}
		add("callback.delivery", state, detail)
	} else {
		add("callback.delivery", Unverified, "Query the authenticated daemon for delivery evidence")
	}
	all := true
	for _, id := range AcceptanceChecks {
		record, e := readEvidence(dir, id)
		state := Unverified
		detail := "No evidence for this build"
		if e == nil && record.Version == buildinfo.Version && !record.At.IsZero() {
			state = record.State
			detail = "Operator evidence recorded " + record.At.Format(time.RFC3339)
		}
		if state != Verified {
			all = false
		}
		add("acceptance."+id, state, detail)
	}
	state := Unverified
	detail := "Required real-host checkpoints remain incomplete"
	if all {
		state = Verified
		detail = "All required dated checkpoints recorded for this build"
	}
	for _, c := range d.Checks {
		if !strings.HasPrefix(c.ID, "acceptance.") && c.State == Failed {
			state = Failed
			detail = "Observed operational failure; required acceptance evidence may also be incomplete"
			break
		}
	}
	add("acceptance", state, detail)

	return d
}

// FetchDiagnostics lets doctor use exactly the report exposed by the running
// daemon, including sync/callback observations, without opening its database.
func FetchDiagnostics(ctx context.Context, dir, address string) (Diagnostics, error) {
	return fetchDiagnostics(ctx, dir, address, 20*time.Second)
}
func fetchDiagnostics(ctx context.Context, dir, address string, budget time.Duration) (Diagnostics, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var d Diagnostics
	if e := LoopbackAddress(address); e != nil {
		return d, e
	}
	token, e := os.ReadFile(filepath.Join(dir, "owner.token"))
	if e != nil {
		return d, e
	}

	client := &http.Client{Timeout: 20 * time.Second}
	type result struct {
		APIVersion int    `json:"apiVersion"`
		InstanceID string `json:"instanceId"`
		Response   struct {
			State             string          `json:"state"`
			Error             string          `json:"error"`
			Result            json.RawMessage `json:"result"`
			ResultUnavailable bool            `json:"resultUnavailable"`
			Version           string          `json:"version"`
			Index             int             `json:"index"`
			Chunk             string          `json:"chunk"`
		} `json:"response"`
	}
	call := func(name string, args map[string]any) (result, error) {
		var out result
		params := map[string]any{"name": name, "arguments": args, "_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientInfo": map[string]string{"name": "code-factory-doctor", "version": buildinfo.Version}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}}
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
		if err != nil {
			return out, err
		}
		req, err := http.NewRequestWithContext(ctx, "POST", "http://"+address+"/mcp", bytes.NewReader(body))
		if err != nil {
			return out, err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Mcp-Method", "tools/call")
		req.Header.Set("Mcp-Name", name)
		res, err := client.Do(req)
		if err != nil {
			return out, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return out, fmt.Errorf("daemon diagnostics unavailable")
		}
		var envelope struct {
			Result struct {
				StructuredContent struct{ Data result }
				IsError           bool
			}
			Error any
		}
		if err = json.NewDecoder(res.Body).Decode(&envelope); err != nil {
			return out, err
		}
		if envelope.Result.IsError || envelope.Error != nil {
			return out, fmt.Errorf("invalid daemon diagnostics")
		}
		out = envelope.Result.StructuredContent.Data
		if out.APIVersion != 2 || out.InstanceID == "" {
			return out, fmt.Errorf("daemon diagnostics requires coordinated API v2 upgrade")
		}
		return out, nil
	}
	pause := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
			return nil
		}
	}
	boot, err := call("open_factory", map[string]any{})
	if err != nil {
		return d, err
	}
	id := rand.Text()
	args := map[string]any{"requestId": id, "instanceId": boot.InstanceID}
	out, err := call("diagnostics", args)
	if err == nil && out.Response.State == "busy" {
		return d, fmt.Errorf("daemon diagnostics busy; operation was not accepted")
	}
	for err == nil {
		switch out.Response.State {
		case "succeeded":
			if out.Response.ResultUnavailable {
				return d, fmt.Errorf("completed daemon diagnostics result unavailable; inspect saved state")
			}
			data := out.Response.Result
			var descriptor struct {
				Document *struct {
					Version  string `json:"version"`
					Chunks   int    `json:"chunks"`
					Bytes    int    `json:"bytes"`
					Encoding string `json:"encoding"`
				} `json:"document"`
			}
			if json.Unmarshal(data, &descriptor) != nil {
				return d, fmt.Errorf("invalid daemon diagnostics")
			}
			if doc := descriptor.Document; doc != nil {
				if doc.Encoding != "base64-json" || len(doc.Version) != 64 || doc.Bytes < 1 || doc.Bytes > 48<<20 || doc.Chunks != (doc.Bytes+(24<<10)-1)/(24<<10) {
					return d, fmt.Errorf("invalid daemon diagnostics document")
				}
				data = make([]byte, 0, doc.Bytes)
				for index := 0; index < doc.Chunks; index++ {
					chunk, err := call("document_chunk", map[string]any{"version": doc.Version, "index": index})
					for err == nil && chunk.Response.State == "busy" {
						if err = pause(); err != nil {
							break
						}
						chunk, err = call("document_chunk", map[string]any{"version": doc.Version, "index": index})
					}
					if err != nil {
						return d, err
					}
					if chunk.Response.State != "ready" || chunk.Response.Version != doc.Version || chunk.Response.Index != index || len(chunk.Response.Chunk) > 32<<10 {
						return d, fmt.Errorf("daemon diagnostics document unavailable")
					}
					part, err := base64.StdEncoding.DecodeString(chunk.Response.Chunk)
					if err != nil || len(data)+len(part) > doc.Bytes {
						return d, fmt.Errorf("invalid daemon diagnostics chunk")
					}
					data = append(data, part...)
				}
				if len(data) != doc.Bytes || fmt.Sprintf("%x", sha256.Sum256(data)) != doc.Version {
					return d, fmt.Errorf("incomplete daemon diagnostics document")
				}
			}
			if json.Unmarshal(data, &d) != nil || d.Version == "" {
				return d, fmt.Errorf("invalid daemon diagnostics")
			}
			return d, nil
		case "queued", "running", "pending", "busy":
		default:
			return d, fmt.Errorf("daemon diagnostics %s: %s", out.Response.State, out.Response.Error)
		}
		if err = pause(); err != nil {
			return d, err
		}
		out, err = call("operation_status", args)
	}
	return d, err
}
