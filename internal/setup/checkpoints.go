package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

type TunnelCheckpoint struct {
	ID           string    `json:"id"`
	Organization string    `json:"organization"`
	Workspace    string    `json:"workspace"`
	OwnerOnly    bool      `json:"ownerOnly"`
	At           time.Time `json:"at"`
	Configured   bool      `json:"configured"`
}

func ReadTunnel(dir string) (TunnelCheckpoint, error) {
	var c TunnelCheckpoint
	b, e := os.ReadFile(filepath.Join(dir, "tunnel.json"))
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	return c, e
}
func ConfigureOwnedTunnel(ctx context.Context, binary, dir string, c TunnelCheckpoint) error {
	if !regexp.MustCompile(`^tunnel_[A-Za-z0-9_-]+$`).MatchString(c.ID) {
		return fmt.Errorf("invalid tunnel ID")
	}
	if !c.OwnerOnly || c.Organization == "" || c.Workspace == "" {
		return fmt.Errorf("setup blocker: verify owner-only Platform organization and Work workspace access, then supply --owner-only --organization and --workspace; no tunnel is exposed")
	}
	if old, e := ReadTunnel(dir); e == nil && old.Configured {
		if old.ID != c.ID || old.Organization != c.Organization || old.Workspace != c.Workspace {
			return fmt.Errorf("configured tunnel identity differs; inspect the existing profile before replacing it")
		}
		return nil
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	// Intent is saved before the external mutation. A retry may replace only the
	// profile whose creation was already authorized by this checkpoint.
	intent := filepath.Join(dir, "tunnel-intent.json")
	force := false
	if b, e := os.ReadFile(intent); e == nil {
		var old TunnelCheckpoint
		if json.Unmarshal(b, &old) != nil || old.ID != c.ID {
			return fmt.Errorf("unfinished tunnel setup uses another identity")
		}
		force = true
	}
	if e := AtomicJSON(intent, c); e != nil {
		return e
	}
	args := []string{"init", "--sample", "sample_mcp_stdio_local", "--profile", "code-factory", "--tunnel-id", c.ID, "--mcp-command", Quote(binary) + " mcp --data-dir " + Quote(dir), "--health-listen-addr", "127.0.0.1:0"}
	if force {
		args = append(args, "--force")
	}
	if e := exec.CommandContext(ctx, "tunnel-client", args...).Run(); e != nil {
		return fmt.Errorf("tunnel profile initialization failed; inspect official client and rerun the same setup")
	}
	c.At = time.Now().UTC()
	c.Configured = true
	if e := AtomicJSON(filepath.Join(dir, "tunnel.json"), c); e != nil {
		return e
	}
	return os.Remove(intent)
}
func RequireOwnedTunnel(dir string) error {
	c, e := ReadTunnel(dir)
	if e != nil || !c.Configured || !c.OwnerOnly || c.Organization == "" || c.Workspace == "" {
		return fmt.Errorf("setup blocker: configure and confirm owner-only tunnel associations before credential entry or service startup")
	}
	return nil
}
