package setup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Installation struct {
	Current              string  `json:"current"`
	Previous             string  `json:"previous,omitempty"`
	Backup               string  `json:"backup,omitempty"`
	GatewayProfileBackup string  `json:"gatewayProfileBackup,omitempty"`
	Runtime              Runtime `json:"runtime"`
}

func installRoot() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "lib", "code-factory")
}
func unitRoot() string {
	h, _ := os.UserHomeDir()
	c := os.Getenv("XDG_CONFIG_HOME")
	if c == "" {
		c = filepath.Join(h, ".config")
	}
	return filepath.Join(c, "systemd", "user")
}
func ReadInstallation() (Installation, error) {
	var s Installation
	b, e := os.ReadFile(filepath.Join(installRoot(), "installation.json"))
	if e != nil {
		return s, e
	}
	e = json.Unmarshal(b, &s)
	return s, e
}
func ctl(ctx context.Context, r Runtime, args ...string) error {
	p := r.Paths["systemctl"]
	if p == "" {
		p = "systemctl"
	}
	return exec.CommandContext(ctx, p, append([]string{"--user"}, args...)...).Run()
}
func active(ctx context.Context, r Runtime, name string) bool {
	return ctl(ctx, r, "is-active", "--quiet", name) == nil
}
func switchLink(path, target string) error {
	tmp := path + ".next"
	_ = os.Remove(tmp)
	if e := os.Symlink(target, tmp); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}
func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(source, path)
		if e != nil {
			return e
		}
		dst := filepath.Join(target, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0700)
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release contains nonregular file %s", rel)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		return atomicFile(dst, b, info.Mode().Perm())
	})
}
func validateRelease(source string) (string, error) {
	var m struct{ Version, OS, Arch string }
	b, e := os.ReadFile(filepath.Join(source, "release.json"))
	if e != nil {
		return "", fmt.Errorf("install from an extracted release archive: %w", e)
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return "", e
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,100}$`).MatchString(m.Version) || m.OS != "linux" || m.Arch != "amd64" {
		return "", fmt.Errorf("invalid release manifest")
	}
	sums, e := os.ReadFile(filepath.Join(source, "SHA256SUMS"))
	if e != nil {
		return "", e
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid checksums")
		}
		name := parts[1]
		if !filepath.IsLocal(name) || seen[name] {
			return "", fmt.Errorf("invalid release path")
		}
		seen[name] = true
		info, e := os.Lstat(filepath.Join(source, name))
		if e != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("invalid release file %s", name)
		}
		b, e := os.ReadFile(filepath.Join(source, name))
		if e != nil {
			return "", e
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != parts[0] {
			return "", fmt.Errorf("release checksum mismatch: %s", name)
		}
	}
	for _, name := range []string{"code-factory", "release.json", "plugin/plugin.json", "plugin/skills/code-factory-setup/SKILL.md"} {
		if !seen[name] {
			return "", fmt.Errorf("release missing %s", name)
		}
	}
	e = filepath.WalkDir(source, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(source, path)
		if rel != "SHA256SUMS" && !seen[rel] {
			return fmt.Errorf("unchecked release file %s", rel)
		}
		return nil
	})
	return m.Version, e
}

// BackupDatabase uses SQLite's snapshot operation, including committed WAL pages,
// without running this version's schema migrations on an older database.
func BackupDatabase(ctx context.Context, path, backup string) error {
	if _, e := os.Stat(path); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return e
	}
	defer db.Close()
	if e = os.MkdirAll(filepath.Dir(backup), 0700); e != nil {
		return e
	}
	_, e = db.ExecContext(ctx, "VACUUM INTO ?", backup)
	if e != nil {
		return e
	}
	return os.Chmod(backup, 0600)
}
func restoreDatabase(dir, backup string) error {
	if backup == "" {
		return nil
	}
	if _, e := os.Stat(backup); e != nil {
		return e
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if e := os.Remove(filepath.Join(dir, "state.db") + suffix); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	b, e := os.ReadFile(backup)
	if e != nil {
		return e
	}
	return atomicFile(filepath.Join(dir, "state.db"), b, 0600)
}

// Only transport configuration follows the binary/database version. OAuth
// grants, owner tokens, and the rest of the private data directory stay intact.
func backupGatewayProfile(dir, databaseBackup string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "tailscale.json"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read gateway profile for backup: %w", err)
	}
	backup := databaseBackup + ".tailscale.json"
	if err = atomicFile(backup, b, 0600); err != nil {
		return "", fmt.Errorf("back up gateway profile: %w", err)
	}
	return backup, nil
}

func readGatewayProfileBackup(backup string) ([]byte, error) {
	if backup == "" {
		return nil, nil
	}
	b, err := os.ReadFile(backup)
	if err != nil {
		return nil, fmt.Errorf("recorded gateway profile snapshot unavailable: %w", err)
	}
	return b, nil
}

func restoreGatewayProfile(dir, backup string) error {
	if backup == "" {
		return nil
	}
	b, err := readGatewayProfileBackup(backup)
	if err != nil {
		return err
	}
	return atomicFile(filepath.Join(dir, "tailscale.json"), b, 0600)
}

func writeUnits(r Runtime, launcher string) error {
	unit := func(subcommand string) string {
		ordering := "After=network-online.target\n"
		if subcommand == "tunnel" {
			ordering += "Requires=code-factory.service\nAfter=code-factory.service\n"
		}
		if subcommand == "gateway" {
			ordering += "Wants=code-factory.service\nAfter=code-factory.service\n"
		}
		return "[Unit]\nDescription=Code Factory " + subcommand + "\n" + ordering + "\n[Service]\nExecStart=" + systemdArg(launcher) + " " + subcommand + " --data-dir " + systemdArg(r.DataDir) + " --listen " + systemdArg(r.Listen) + "\nEnvironment=" + systemdQuote("PATH="+r.Path) + "\nRestart=on-failure\nRestartSec=3\nTimeoutStopSec=20\nUMask=0077\n\n[Install]\nWantedBy=default.target\n"
	}
	services := map[string]string{"code-factory.service": "serve", "code-factory-tunnel.service": "tunnel"}
	if _, e := os.Stat(filepath.Join(r.DataDir, "tailscale.json")); e == nil {
		services["code-factory-gateway.service"] = "gateway"
	}
	for name, sub := range services {
		if e := atomicFile(filepath.Join(unitRoot(), name), []byte(unit(sub)), 0600); e != nil {
			return e
		}
	}
	return nil
}
func health(ctx context.Context, r Runtime, version string) error {
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		req, e := http.NewRequestWithContext(ctx, "GET", "http://"+r.Listen+"/healthz", nil)
		if e != nil {
			return e
		}
		token, e := os.ReadFile(filepath.Join(r.DataDir, "owner.token"))
		if e == nil {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
			client := &http.Client{Timeout: time.Second}
			res, e := client.Do(req)
			if e == nil {
				var status struct{ Version string }
				e = json.NewDecoder(res.Body).Decode(&status)
				res.Body.Close()
				if res.StatusCode == 200 && e == nil && status.Version == version {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("installed daemon did not become healthy")
		case <-tick.C:
		}
	}
}
func InstallRelease(ctx context.Context, dir, source, listen string) (err error) {
	if e := LoopbackAddress(listen); e != nil {
		return e
	}
	version, e := validateRelease(source)
	if e != nil {
		return e
	}
	r, e := ResolveRuntime(ctx, dir, listen)
	if e != nil {
		return e
	}
	root := installRoot()
	if e = os.MkdirAll(root, 0700); e != nil {
		return e
	}
	lock, e := lockFile(filepath.Join(root, "install.lock"))
	if e != nil {
		return fmt.Errorf("another installation is running")
	}
	defer lock.Close()
	old, oldErr := ReadInstallation()
	if oldErr != nil && !os.IsNotExist(oldErr) {
		return oldErr
	}
	if oldErr == nil && (old.Runtime.DataDir != dir || old.Runtime.Listen != listen) {
		return fmt.Errorf("use the existing data directory and listener for upgrades")
	}
	if e = ctl(ctx, r, "show-environment"); e != nil {
		return fmt.Errorf("setup blocker: Linux user systemd session unavailable")
	}
	if oldErr != nil {
		ln, e := net.Listen("tcp", listen)
		if e != nil {
			return fmt.Errorf("setup blocker: listener is already occupied")
		}
		ln.Close()
	}
	if old.Current == version {
		if _, e = validateRelease(filepath.Join(root, version)); e != nil {
			return e
		}
		if e = ctl(ctx, r, "enable", "--now", "code-factory.service"); e != nil {
			return e
		}
		return health(ctx, r, version)
	}
	target := filepath.Join(root, version)
	if _, e = os.Stat(target); e == nil {
		if _, e = validateRelease(target); e != nil {
			return e
		}
	} else {
		tmp, e := os.MkdirTemp(root, ".release-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		if e = copyTree(source, tmp); e != nil {
			return e
		}
		if e = os.Rename(tmp, target); e != nil {
			return e
		}
	}

	home, _ := os.UserHomeDir()
	launcher := filepath.Join(home, ".local", "bin", "code-factory")
	if e = os.MkdirAll(filepath.Dir(launcher), 0700); e != nil {
		return e
	}
	if info, e := os.Lstat(launcher); e == nil && info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("refusing to replace unmanaged launcher")
	}
	wasTunnel := active(ctx, r, "code-factory-tunnel.service")
	wasGateway := active(ctx, r, "code-factory-gateway.service")
	backup := filepath.Join(root, "backups", fmt.Sprintf("%d.db", time.Now().UnixNano()))
	profileBackup, e := backupGatewayProfile(dir, backup)
	if e != nil {
		return e
	}
	next := Installation{Current: version, Previous: old.Current, Backup: backup, GatewayProfileBackup: profileBackup, Runtime: r}
	if e = AtomicJSON(filepath.Join(root, "pending.json"), next); e != nil {
		return e
	}
	stopped := false
	activated := false
	defer func() {
		if err == nil || !stopped {
			return
		}
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		recoverErr := func() error {
			if activated && wasGateway {
				if e := ctl(recovery, r, "stop", "code-factory-gateway.service"); e != nil {
					return e
				}
			}
			if activated {
				if e := ctl(recovery, r, "stop", "code-factory-tunnel.service", "code-factory.service"); e != nil {
					return e
				}
			}
			if old.Current == "" {
				return nil
			}
			if activated {
				guard, e := LockDaemon(dir)
				if e != nil {
					return e
				}
				e = restoreDatabase(dir, backup)
				if e == nil {
					e = restoreGatewayProfile(dir, next.GatewayProfileBackup)
				}
				guard.Close()
				if e != nil {
					return e
				}
				if e = switchLink(filepath.Join(root, "current"), old.Current); e != nil {
					return e
				}
				if e = AtomicJSON(filepath.Join(dir, "runtime.json"), old.Runtime); e != nil {
					return e
				}
				if e = writeUnits(old.Runtime, launcher); e != nil {
					return e
				}
				if e = copyTree(filepath.Join(root, old.Current, "plugin"), filepath.Join(root, "plugin")); e != nil {
					return e
				}
				if _, e = os.Stat(filepath.Join(root, "plugin", ".app.json")); e == nil {
					if e = linkManifest(filepath.Join(root, "plugin")); e != nil {
						return e
					}
				}
				if e = ctl(recovery, r, "daemon-reload"); e != nil {
					return e
				}
			}
			if e := ctl(recovery, r, "start", "code-factory.service"); e != nil {
				return e
			}
			if e := health(recovery, old.Runtime, old.Current); e != nil {
				return e
			}
			if wasGateway {
				if e := ctl(recovery, r, "start", "code-factory-gateway.service"); e != nil {
					return e
				}
			}
			if wasTunnel {
				return ctl(recovery, r, "start", "code-factory-tunnel.service")
			}
			return nil
		}()
		if recoverErr != nil {
			err = errors.Join(err, fmt.Errorf("automatic recovery failed; inspect pending.json and run rollback: %w", recoverErr))
		}
	}()
	if wasGateway {
		if e = ctl(ctx, r, "stop", "code-factory-gateway.service"); e != nil {
			return e
		}
	}
	if oldErr == nil {
		stopped = true
		if e = ctl(ctx, r, "stop", "code-factory-tunnel.service", "code-factory.service"); e != nil {
			return e
		}
	}
	guard, e := LockDaemon(dir)
	if e != nil {
		return e
	}
	e = BackupDatabase(ctx, filepath.Join(dir, "state.db"), backup)
	guard.Close()
	if e != nil {
		return e
	}
	stopped = true
	activated = true
	if e = switchLink(filepath.Join(root, "current"), version); e != nil {
		return e
	}
	if e = switchLink(launcher, filepath.Join(root, "current", "code-factory")); e != nil {
		return e
	}
	if e = AtomicJSON(filepath.Join(dir, "runtime.json"), r); e != nil {
		return e
	}
	if e = writeUnits(r, launcher); e != nil {
		return e
	}
	// The editable installed plugin has a persistent app mapping independent of releases.
	plugin := filepath.Join(root, "plugin")
	if e = copyTree(filepath.Join(target, "plugin"), plugin); e != nil {
		return e
	}
	if _, e = os.Stat(filepath.Join(plugin, ".app.json")); e == nil {
		if e = linkManifest(plugin); e != nil {
			return e
		}
	}
	if e = ctl(ctx, r, "daemon-reload"); e != nil {
		return e
	}
	if e = ctl(ctx, r, "enable", "code-factory.service"); e != nil {
		return e
	}
	if e = ctl(ctx, r, "restart", "code-factory.service"); e != nil {
		return e
	}
	if e = health(ctx, r, version); e != nil {
		return e
	}
	if wasGateway {
		if e = ctl(ctx, r, "restart", "code-factory-gateway.service"); e != nil {
			return e
		}
	}
	if wasTunnel {
		if e = ctl(ctx, r, "restart", "code-factory-tunnel.service"); e != nil {
			return e
		}
	}
	if e = AtomicJSON(filepath.Join(root, "installation.json"), next); e != nil {
		return e
	}
	return os.Remove(filepath.Join(root, "pending.json"))
}
func Rollback(ctx context.Context) error {
	root := installRoot()
	lock, e := lockFile(filepath.Join(root, "install.lock"))
	if e != nil {
		return e
	}
	defer lock.Close()
	s, e := ReadInstallation()
	var pending Installation
	if b, err := os.ReadFile(filepath.Join(root, "pending.json")); err == nil {
		if err = json.Unmarshal(b, &pending); err != nil {
			return err
		}
		s = pending
		e = nil
	}
	if e != nil {
		return e
	}
	if s.Previous == "" {
		return fmt.Errorf("no previous installed version")
	}
	if _, e = validateRelease(filepath.Join(root, s.Previous)); e != nil {
		return e
	}
	// Fail before stopping working services if a recorded compatibility snapshot
	// is missing. Legacy installation records deliberately leave this unset.
	if _, e = readGatewayProfileBackup(s.GatewayProfileBackup); e != nil {
		return e
	}
	r := s.Runtime
	wasTunnel := active(ctx, r, "code-factory-tunnel.service")
	wasGateway := active(ctx, r, "code-factory-gateway.service")
	if wasGateway {
		if e = ctl(ctx, r, "stop", "code-factory-gateway.service"); e != nil {
			return e
		}
	}
	if e = ctl(ctx, r, "stop", "code-factory-tunnel.service", "code-factory.service"); e != nil {
		return e
	}
	guard, e := LockDaemon(r.DataDir)
	if e != nil {
		return e
	}
	// Retain the current database before restoring the compatible pre-upgrade snapshot.
	archive := filepath.Join(root, "backups", fmt.Sprintf("pre-rollback-%d.db", time.Now().UnixNano()))
	if e = BackupDatabase(ctx, filepath.Join(r.DataDir, "state.db"), archive); e != nil {
		guard.Close()
		return e
	}
	if _, e = backupGatewayProfile(r.DataDir, archive); e != nil {
		guard.Close()
		return e
	}
	e = restoreDatabase(r.DataDir, s.Backup)
	if e == nil {
		e = restoreGatewayProfile(r.DataDir, s.GatewayProfileBackup)
	}
	guard.Close()
	if e != nil {
		return e
	}
	if e = switchLink(filepath.Join(root, "current"), s.Previous); e != nil {
		return e
	}
	home, _ := os.UserHomeDir()
	if e = copyTree(filepath.Join(root, s.Previous, "plugin"), filepath.Join(root, "plugin")); e != nil {
		return e
	}
	if _, err := os.Stat(filepath.Join(root, "plugin", ".app.json")); err == nil {
		if e = linkManifest(filepath.Join(root, "plugin")); e != nil {
			return e
		}
	}
	if e = AtomicJSON(filepath.Join(r.DataDir, "runtime.json"), r); e != nil {
		return e
	}
	if e = writeUnits(r, filepath.Join(home, ".local", "bin", "code-factory")); e != nil {
		return e
	}
	if e = ctl(ctx, r, "daemon-reload"); e != nil {
		return e
	}
	s.Current = s.Previous
	s.Previous = ""
	s.Backup = ""
	s.GatewayProfileBackup = ""
	if e = ctl(ctx, r, "start", "code-factory.service"); e != nil {
		return e
	}
	if e = health(ctx, r, s.Current); e != nil {
		return e
	}
	if wasGateway {
		if e = ctl(ctx, r, "start", "code-factory-gateway.service"); e != nil {
			return e
		}
	}
	if wasTunnel {
		if e = ctl(ctx, r, "start", "code-factory-tunnel.service"); e != nil {
			return e
		}
	}
	if e = AtomicJSON(filepath.Join(root, "installation.json"), s); e != nil {
		return e
	}
	_ = os.Remove(filepath.Join(root, "pending.json"))
	return nil
}
func Uninstall(ctx context.Context) error {
	root := installRoot()
	lock, e := lockFile(filepath.Join(root, "install.lock"))
	if e != nil {
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	defer lock.Close()
	s, e := ReadInstallation()
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if e = RemoveTailscale(ctx, s.Runtime.DataDir); e != nil {
		return e
	}
	names := []string{"code-factory-tunnel.service", "code-factory.service"}
	if _, err := os.Stat(filepath.Join(unitRoot(), "code-factory-gateway.service")); err == nil {
		names = append([]string{"code-factory-gateway.service"}, names...)
	}
	for _, name := range names {
		if e = ctl(ctx, s.Runtime, "disable", "--now", name); e != nil {
			return e
		}
		if e = os.Remove(filepath.Join(unitRoot(), name)); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	if e = ctl(ctx, s.Runtime, "daemon-reload"); e != nil {
		return e
	}
	h, _ := os.UserHomeDir()
	if e = os.Remove(filepath.Join(h, ".local", "bin", "code-factory")); e != nil && !os.IsNotExist(e) {
		return e
	}
	// Keep releases/backups and plugin mapping for recovery/reinstall, alongside all application data.
	return os.Remove(filepath.Join(root, "installation.json"))
}
func linkManifest(plugin string) error {
	path := filepath.Join(plugin, "plugin.json")
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	var m map[string]any
	if e = json.Unmarshal(b, &m); e != nil {
		return e
	}
	if m["name"] != "code-factory" {
		return fmt.Errorf("target is not the installed Code Factory plugin")
	}
	extensions, ok := m["extensions"].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid plugin extensions")
	}
	openai, ok := extensions["com.openai"].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid plugin OpenAI extension")
	}
	openai["apps"] = "./.app.json"
	return AtomicJSON(path, m)
}
