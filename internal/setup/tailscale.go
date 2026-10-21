package setup

import (
	"bytes"
	"codefactory/internal/gateway"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ConfigureTailscale adds only a dedicated Funnel/Serve pair. Existing listeners
// and routes are never reset or replaced. No OpenAI tunnel credential is needed.
func ConfigureTailscale(ctx context.Context, dir string) (result error) {
	binary, e := exec.LookPath("tailscale")
	if e != nil {
		return fmt.Errorf("Tailscale is not installed")
	}
	output, e := exec.CommandContext(ctx, binary, "status", "--json").Output()
	if e != nil {
		return fmt.Errorf("Tailscale is unavailable")
	}
	var status struct {
		BackendState string
		Self         struct {
			DNSName string
			UserID  json.Number
		}
		User map[string]struct{ LoginName string }
	}
	if e = json.Unmarshal(output, &status); e != nil {
		return e
	}
	owner := status.User[status.Self.UserID.String()].LoginName
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	if status.BackendState != "Running" || dns == "" || owner == "" {
		return fmt.Errorf("sign in to Tailscale as the owner before setup")
	}
	cfg := gateway.Config{PublicURL: "https://" + dns + "/code-factory", ApprovalURL: "https://" + dns + ":8444", Owner: owner, PublicListen: "127.0.0.1:8788", ApprovalListen: "127.0.0.1:8789", Daemon: "127.0.0.1:8787"}
	if r, e := ReadRuntime(dir); e == nil {
		cfg.Daemon = r.Listen
	}
	routes, e := inspectTailscaleRoutes(ctx, binary)
	if e != nil {
		return e
	}
	desired := gatewayRoutes(cfg)
	if e = validateGatewayRoutes(routes, desired); e != nil {
		return e
	}
	// Validate the gateway config before any persistent change.
	token, e := Token(dir)
	if e != nil {
		return e
	}
	if _, e = gateway.New(cfg, dir, token); e != nil {
		return e
	}
	for _, address := range []string{cfg.PublicListen, cfg.ApprovalListen} {
		listener, e := net.Listen("tcp", address)
		if e != nil {
			if _, err := os.Stat(filepath.Join(dir, "tailscale.json")); err != nil {
				return fmt.Errorf("gateway listener occupied: %s", address)
			}
		} else {
			listener.Close()
		}
	}
	configPath := filepath.Join(dir, "tailscale.json")
	previous, readErr := os.ReadFile(configPath)
	changed := readErr != nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	if readErr == nil {
		var old gateway.Config
		if json.Unmarshal(previous, &old) != nil {
			return fmt.Errorf("invalid existing Tailscale configuration")
		}
		changed = old != cfg
		legacy := old
		legacy.PublicURL = cfg.PublicURL
		if changed && (old.PublicURL != "https://"+dns+":10000" || legacy != cfg) {
			return fmt.Errorf("existing Tailscale configuration differs; inspect it before replacement")
		}
		if changed {
			if e = atomicFile(filepath.Join(dir, "tailscale-before-443.json"), previous, 0600); e != nil {
				return e
			}
		}
	}
	r, _ := ReadRuntime(dir)
	var added []gatewayRoute
	persisted := false
	// A failed setup restores its old configuration and removes only its own
	// additions. No whole-node route reset or existing handler replacement.
	defer func() {
		if result == nil {
			return
		}
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for i := len(added) - 1; i >= 0; i-- {
			if err := removeGatewayRoute(recovery, binary, added[i]); err != nil {
				result = fmt.Errorf("%w; route rollback failed: %v", result, err)
			}
		}
		if !changed || !persisted {
			return
		}
		if previous != nil {
			if err := atomicFile(configPath, previous, 0600); err != nil {
				result = fmt.Errorf("%w; profile rollback failed: %v", result, err)
			} else if err := ctl(recovery, r, "restart", "code-factory-gateway.service"); err != nil {
				result = fmt.Errorf("%w; gateway rollback restart failed: %v", result, err)
			}
		} else {
			if err := ctl(recovery, r, "disable", "--now", "code-factory-gateway.service"); err != nil {
				result = fmt.Errorf("%w; gateway rollback stop failed: %v", result, err)
			}
			if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
				result = fmt.Errorf("%w; profile rollback failed: %v", result, err)
			}
		}
	}()
	if e = AtomicJSON(filepath.Join(dir, "tailscale.json"), cfg); e != nil {
		return e
	}
	persisted = true
	h, _ := os.UserHomeDir()
	launcher := filepath.Join(h, ".local", "bin", "code-factory")
	unit := "[Unit]\nDescription=Code Factory Tailscale OAuth gateway\nWants=code-factory.service\nAfter=code-factory.service network-online.target\n\n[Service]\nExecStart=" + systemdArg(launcher) + " gateway --data-dir " + systemdArg(dir) + "\nRestart=on-failure\nRestartSec=3\nUMask=0077\n\n[Install]\nWantedBy=default.target\n"
	if e = atomicFile(filepath.Join(unitRoot(), "code-factory-gateway.service"), []byte(unit), 0600); e != nil {
		return e
	}
	if e = ctl(ctx, r, "daemon-reload"); e != nil {
		return e
	}
	if e = ctl(ctx, r, "enable", "--now", "code-factory-gateway.service"); e != nil {
		return fmt.Errorf("could not start OAuth gateway: %w", e)
	}
	if changed && previous != nil {
		if e = ctl(ctx, r, "restart", "code-factory-gateway.service"); e != nil {
			return e
		}
	}
	// The first desired route is private consent. Public routes use only the
	// dedicated prefix and RFC metadata aliases on standard HTTPS port 443.
	for _, route := range desired {
		if existing, exists := routes.Web[route.host].Handlers[route.path]; exists && existing.Proxy == route.target {
			continue
		}
		if e = runTailscale(ctx, binary, route.arguments(false)...); e != nil {
			return fmt.Errorf("Tailscale configuration failed: %w", e)
		}
		added = append(added, route)
	}
	if e = SyncPluginEndpoint(cfg.PublicURL + "/mcp"); e != nil {
		return e
	}
	fmt.Println("Work MCP URL:", cfg.PublicURL+"/mcp")
	fmt.Println("Authentication: OAuth. Approve the connection using your private Tailscale browser page.")
	return nil
}

// RemoveTailscale removes only routes still pointing at this gateway.
func RemoveTailscale(ctx context.Context, dir string) error {
	b, e := os.ReadFile(filepath.Join(dir, "tailscale.json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var cfg gateway.Config
	if e = json.Unmarshal(b, &cfg); e != nil {
		return e
	}
	binary, e := exec.LookPath("tailscale")
	if e != nil {
		return e
	}
	owned := gatewayRoutes(cfg)
	if len(owned) == 0 {
		return fmt.Errorf("invalid Tailscale configuration; refusing route removal")
	}
	// Also remove the old, retained port-10000 endpoint only if still ours.
	if strings.HasSuffix(cfg.PublicURL, "/code-factory") {
		legacy := cfg
		legacy.PublicURL = strings.TrimSuffix(cfg.PublicURL, "/code-factory") + ":10000"
		legacyRoutes := gatewayRoutes(legacy)
		owned = append(owned, legacyRoutes[len(legacyRoutes)-1])
	}
	for i := len(owned) - 1; i >= 0; i-- {
		if e = removeGatewayRoute(ctx, binary, owned[i]); e != nil {
			return e
		}
	}
	return nil
}

// Elevate only the exact route mutation when the local Tailscale daemon requires
// it and passwordless sudo is already available. Never change operator policy.
func runTailscale(ctx context.Context, binary string, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stderr = &stderr
	if e := cmd.Run(); e == nil {
		return nil
	} else if !strings.Contains(stderr.String(), "Access denied") {
		return fmt.Errorf("tailscale route update failed: %w", e)
	}
	sudo, e := exec.LookPath("sudo")
	if e != nil {
		return fmt.Errorf("Tailscale route updates require local administrator access")
	}
	cmd = exec.CommandContext(ctx, sudo, append([]string{"-n", binary}, args...)...)
	cmd.Stderr = &stderr
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("Tailscale route updates require administrator access; passwordless sudo is unavailable")
	}
	return nil
}
