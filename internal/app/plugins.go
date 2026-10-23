package app

import (
	"bytes"
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

type PluginAction struct {
	Action string `json:"action"`
	Target string `json:"target,omitempty"`
	Ref    string `json:"ref,omitempty"`
}

var pluginSelector = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,199}@[A-Za-z0-9][A-Za-z0-9_.-]{0,199}$`)
var marketplaceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,199}$`)

// PluginState reads cached metadata. Opening settings never installs code or contacts a marketplace.
type PluginAdminInput struct {
	Query     string `json:"query,omitempty"`
	Available bool   `json:"available,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

func (m *Manager) PluginState(ctx context.Context, in PluginAdminInput) (map[string]any, error) {
	inventory := domain.PluginInventory{ID: "current", Plugins: []domain.CodexPlugin{}, Marketplaces: []domain.PluginMarketplace{}}
	if err := m.app.Store.Get(ctx, "plugin-inventory", "current", &inventory); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	operations, err := store.List[domain.PluginOperation](ctx, m.app.Store, "plugin-operation")
	if err != nil {
		return nil, err
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].Created.After(operations[j].Created) })
	if len(operations) > 20 {
		operations = operations[:20]
	}
	plugins := []domain.CodexPlugin{}
	for _, p := range inventory.Plugins {
		if (in.Available || p.Installed) && strings.Contains(strings.ToLower(p.ID+" "+p.Name), strings.ToLower(in.Query)) {
			plugins = append(plugins, p)
		}
	}
	total := len(plugins)
	if in.Offset < 0 {
		in.Offset = 0
	}
	if in.Offset > total {
		in.Offset = total
	}
	if in.Limit < 1 || in.Limit > 100 {
		in.Limit = 40
	}
	end := in.Offset + in.Limit
	if end > total {
		end = total
	}
	inventory.Plugins = plugins[in.Offset:end]
	return map[string]any{"inventory": inventory, "operations": operations, "total": total, "offset": in.Offset}, nil
}

// PluginAction claims a single operation before execution; callers poll its durable result.
func (m *Manager) PluginAction(ctx context.Context, in PluginAction) (domain.PluginOperation, error) {
	var op domain.PluginOperation
	switch in.Action {
	case "refresh", "check-updates", "discover-skills":
		if in.Target != "" || in.Ref != "" {
			return op, fmt.Errorf("this action takes no target or ref")
		}
	case "install", "update", "remove":
		if !pluginSelector.MatchString(in.Target) || in.Ref != "" {
			return op, fmt.Errorf("use plugin-name@marketplace-name")
		}
	case "remove-marketplace":
		if !marketplaceName.MatchString(in.Target) || in.Ref != "" {
			return op, fmt.Errorf("use a configured marketplace name")
		}
	case "add-marketplace":
		if !validMarketplaceSource(in.Target) || strings.HasPrefix(in.Ref, "-") || strings.ContainsAny(in.Ref, "\x00\r\n") {
			return op, fmt.Errorf("use a local absolute path or a Git marketplace URL without credentials")
		}
	default:
		return op, fmt.Errorf("unknown plugin action")
	}
	if !m.adminGate.TryLock() {
		return op, fmt.Errorf("agent or plugin activity is in progress; wait until inactive before maintenance")
	}
	m.mu.Lock()
	if m.closing || m.ctx.Err() != nil {
		m.mu.Unlock()
		m.adminGate.Unlock()
		return op, fmt.Errorf("daemon is stopping")
	}
	for _, s := range m.live {
		s.mu.Lock()
		busy := s.run.Status == "queued" || s.run.Status == "starting" || s.run.Status == "running" || s.run.Status == "waiting"
		s.mu.Unlock()
		if busy {
			m.mu.Unlock()
			m.adminGate.Unlock()
			return op, fmt.Errorf("wait for active agent work before plugin maintenance")
		}
	}
	op = domain.PluginOperation{ID: ID(), Action: in.Action, Target: in.Target, Status: "queued", Created: time.Now().UTC()}
	if err := m.app.Store.Put(ctx, "plugin-operation", op.ID, op, "plugin.operation"); err != nil {
		m.mu.Unlock()
		m.adminGate.Unlock()
		return op, err
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() { defer m.wg.Done(); defer m.adminGate.Unlock(); m.runPluginOperation(op, in) }()
	return op, nil
}
func validMarketplaceSource(s string) bool {
	if s == "" || len(s) > 4096 || strings.ContainsAny(s, "\x00\r\n") || strings.HasPrefix(s, "-") {
		return false
	}
	if filepath.IsAbs(s) {
		return true
	}
	if repoPattern.MatchString(s) && !strings.Contains(s, "..") {
		return true
	}
	u, err := url.Parse(s)
	if err == nil && u.Host != "" && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" && u.User == nil || u.Scheme == "ssh" && (u.User == nil || u.User.Username() == "git" && !strings.Contains(u.User.String(), ":"))) {
		return true
	}
	return regexp.MustCompile(`^git@[A-Za-z0-9.-]+:[A-Za-z0-9_./-]+$`).MatchString(s)
}

// Never return provider stdout/stderr on failure: third-party messages may contain credentials.
func (m *Manager) pluginCommand(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, m.Binary, append([]string{"plugin"}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Stderr = io.Discard
	var output bytes.Buffer
	cmd.Stdout = &boundedOutput{dst: &output, remaining: 16 << 20}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("Codex plugin command did not complete successfully; refresh inventory before retrying")
	}
	return output.Bytes(), nil
}

type boundedOutput struct {
	dst       *bytes.Buffer
	remaining int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.remaining {
		return 0, fmt.Errorf("provider output too large")
	}
	b.remaining -= len(p)
	return b.dst.Write(p)
}

func (m *Manager) runPluginOperation(op domain.PluginOperation, in PluginAction) {
	ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
	defer cancel()
	defer func() {
		op.Finished = time.Now().UTC()
		persist, cancelPersist := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelPersist()
		_ = m.app.Store.Put(persist, "plugin-operation", op.ID, op, "plugin.operation")
	}()
	op.Status = "running"
	if err := m.app.Store.Put(ctx, "plugin-operation", op.ID, op, "plugin.operation"); err != nil {
		op.Status = "uncertain"
		op.Error = "Operation state could not be recorded. Refresh inventory before retrying."
		return
	}
	_, versionErr := codex.Version(ctx, m.Binary)
	err := versionErr
	if err == nil {
		switch in.Action {
		case "add-marketplace":
			args := []string{"marketplace", "add", "--json"}
			if in.Ref != "" {
				args = append(args, "--ref", in.Ref)
			}
			args = append(args, "--", in.Target)
			_, err = m.pluginCommand(ctx, args...)
		case "remove-marketplace":
			_, err = m.pluginCommand(ctx, "marketplace", "remove", "--json", "--", in.Target)
		case "check-updates":
			_, err = m.pluginCommand(ctx, "marketplace", "upgrade", "--json")
		case "install", "update", "remove":
			action := "add"
			if in.Action == "remove" {
				action = "remove"
			}
			_, err = m.pluginCommand(ctx, action, "--json", "--", in.Target)
		}
	}
	// Even an uncertain mutation can have succeeded. Observe actual state without replaying it.
	mutationErr := err
	if versionErr == nil && ctx.Err() == nil && in.Action != "discover-skills" {
		err = m.refreshPluginInventory(ctx)
	}
	if mutationErr != nil {
		err = mutationErr
	}
	if err == nil && in.Action != "remove" {
		_, err = m.discoverAdminSkills(ctx)
	}
	if in.Action == "remove" && err == nil {
		_, err = m.discoverAdminSkills(ctx)
	}
	op.Status = "succeeded"
	if err != nil {
		op.Status = "failed"
		op.Error = "Plugin operation failed. Refresh inventory to inspect current state; the action was not replayed."
	}
	if ctx.Err() != nil || versionErr == nil && mutationErr != nil && (in.Action == "install" || in.Action == "update" || in.Action == "remove" || in.Action == "add-marketplace" || in.Action == "remove-marketplace") {
		op.Status = "uncertain"
		op.Error = "The operation may have changed Codex state. Refresh inventory before deciding whether to retry; no automatic replay was performed."
	}

}

type cliPlugin struct {
	ID            string `json:"pluginId"`
	Name          string `json:"name"`
	Marketplace   string `json:"marketplaceName"`
	Version       string `json:"version"`
	Installed     bool   `json:"installed"`
	Enabled       bool   `json:"enabled"`
	InstallPolicy string `json:"installPolicy"`
	AuthPolicy    string `json:"authPolicy"`
	Source        struct {
		Type string `json:"source"`
		Path string `json:"path"`
	} `json:"source"`
}

func (m *Manager) refreshPluginInventory(ctx context.Context) error {
	data, err := m.pluginCommand(ctx, "list", "--available", "--json")
	if err != nil {
		return m.inventoryFailure(ctx, err)
	}
	var result struct {
		Installed []cliPlugin `json:"installed"`
		Available []cliPlugin `json:"available"`
	}
	var shape map[string]json.RawMessage
	if json.Unmarshal(data, &shape) != nil || shape["installed"] == nil || shape["available"] == nil || json.Unmarshal(data, &result) != nil || result.Installed == nil || result.Available == nil {
		return m.inventoryFailure(ctx, fmt.Errorf("invalid plugin inventory"))
	}
	inv := domain.PluginInventory{ID: "current", CheckedAt: time.Now().UTC(), LastAttempt: time.Now().UTC(), Plugins: []domain.CodexPlugin{}, Marketplaces: []domain.PluginMarketplace{}}
	for _, entry := range append(result.Installed, result.Available...) {
		if !pluginSelector.MatchString(entry.ID) {
			continue
		}
		p := domain.CodexPlugin{ID: entry.ID, Name: entry.Name, Marketplace: entry.Marketplace, Installed: entry.Installed, Enabled: entry.Enabled, InstallPolicy: entry.InstallPolicy, AuthPolicy: entry.AuthPolicy, SourceType: entry.Source.Type, UpdateStatus: "not-installed"}
		if entry.Installed {
			p.InstalledVersion = entry.Version
			p.UpdateStatus = "unknown"
			if entry.Source.Type == "local" && filepath.IsAbs(entry.Source.Path) {
				if version := pluginManifestVersion(entry.Source.Path); version != "" {
					p.AvailableVersion = version
					p.UpdateStatus = "current"
					if version != entry.Version {
						p.UpdateStatus = "available"
					}
				}
			}
		} else {
			p.AvailableVersion = entry.Version
		}
		inv.Plugins = append(inv.Plugins, p)
	}
	data, err = m.pluginCommand(ctx, "marketplace", "list", "--json")
	if err != nil {
		return m.inventoryFailure(ctx, err)
	}
	var markets struct {
		Marketplaces []struct {
			Name   string `json:"name"`
			Source struct {
				Type string `json:"sourceType"`
			} `json:"marketplaceSource"`
		} `json:"marketplaces"`
	}
	if json.Unmarshal(data, &markets) != nil || markets.Marketplaces == nil {
		return m.inventoryFailure(ctx, fmt.Errorf("invalid marketplace inventory"))
	}
	for _, e := range markets.Marketplaces {
		inv.Marketplaces = append(inv.Marketplaces, domain.PluginMarketplace{Name: e.Name, SourceType: e.Source.Type})
	}
	sort.Slice(inv.Plugins, func(i, j int) bool {
		if inv.Plugins[i].Installed != inv.Plugins[j].Installed {
			return inv.Plugins[i].Installed
		}
		return inv.Plugins[i].ID < inv.Plugins[j].ID
	})
	return m.app.Store.Write(ctx, func(t *store.Tx) error {
		if err := t.Put("plugin-inventory", "current", inv); err != nil {
			return err
		}
		return t.Event("plugin.inventory", "current", map[string]any{"checkedAt": inv.CheckedAt, "plugins": len(inv.Plugins)})
	})
}
func pluginManifestVersion(root string) string {
	for _, rel := range []string{".codex-plugin/plugin.json", "plugin.json"} {
		f, err := os.Open(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(f, 1<<20))
		_ = f.Close()
		if err != nil {
			continue
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(b, &manifest) == nil {
			return manifest.Version
		}
	}
	return ""
}
func (m *Manager) inventoryFailure(ctx context.Context, cause error) error {
	var inv domain.PluginInventory
	if m.app.Store.Get(ctx, "plugin-inventory", "current", &inv) == nil {
		inv.Error = "Could not refresh plugin inventory; previous observations are retained."
		inv.LastAttempt = time.Now().UTC()
		_ = m.app.Store.Put(ctx, "plugin-inventory", "current", inv, "plugin.inventory")
	}
	return cause
}
func (m *Manager) discoverAdminSkills(ctx context.Context) ([]domain.Skill, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	repos, err := store.List[domain.Repository](ctx, m.app.Store, "repository")
	if err != nil {
		return nil, err
	}
	roots := []string{home}
	for _, r := range repos {
		if r.Checkout != "" {
			roots = append(roots, r.Checkout)
		} else {
			p := filepath.Join(m.app.DataDir, "repos", filepath.FromSlash(r.ID))
			if _, err := os.Stat(p); err == nil {
				roots = append(roots, p)
			}
		}
	}
	out := []domain.Skill{}
	for _, root := range roots {
		s, err := m.DiscoverSkills(ctx, root)
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	existing, err := store.List[domain.Skill](ctx, m.app.Store, "skill")
	if err != nil {
		return nil, err
	}
	for _, skill := range existing {
		if skill.Managed {
			continue
		}
		if _, err := os.Stat(skill.Path); os.IsNotExist(err) {
			skill.Disabled = true
			if err = m.app.Store.Put(ctx, "skill", skill.ID, skill, "skill.changed"); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
func (m *Manager) recoverPluginOperations(ctx context.Context) error {
	ops, err := store.List[domain.PluginOperation](ctx, m.app.Store, "plugin-operation")
	if err != nil {
		return err
	}
	for _, op := range ops {
		if op.Status == "queued" || op.Status == "running" {
			op.Status = "uncertain"
			op.Error = "Daemon stopped during plugin maintenance. Refresh inventory; the operation was not replayed."
			op.Finished = time.Now().UTC()
			if err = m.app.Store.Put(ctx, "plugin-operation", op.ID, op, "plugin.operation"); err != nil {
				return err
			}
		}
	}
	return nil
}

// Only metadata is refreshed on a schedule. Installation and updates remain explicit operations.
func (m *Manager) maybeCheckPlugins(ctx context.Context) {
	config, err := m.app.Config(ctx)
	if err != nil || config.PluginCheckMinutes == 0 {
		return
	}
	var inv domain.PluginInventory
	if m.app.Store.Get(ctx, "plugin-inventory", "current", &inv) != nil || inv.CheckedAt.IsZero() {
		return
	}
	at := inv.LastAttempt
	if at.IsZero() {
		at = inv.CheckedAt
	}
	if time.Since(at) < time.Duration(config.PluginCheckMinutes)*time.Minute {
		return
	}
	_, _ = m.PluginAction(ctx, PluginAction{Action: "check-updates"})
}
