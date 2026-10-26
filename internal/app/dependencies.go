package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/commandenv"
	"codefactory/internal/dependencies"
	"codefactory/internal/domain"
	"codefactory/internal/setup"
	"codefactory/internal/store"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var ErrMaintenanceBusy = errors.New("agent or dependency maintenance is active")
var dependencyID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,95}$`)

type DependencyRequest struct {
	ID              string   `json:"id"`
	Action          string   `json:"action"`
	Targets         []string `json:"targets"`
	MigrateLegacyDK bool     `json:"migrateLegacyDK,omitempty"`
}
type DependencyResult struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Source    string `json:"source,omitempty"`
	Current   string `json:"current,omitempty"`
	Available string `json:"available,omitempty"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
}
type DependencyOperation struct {
	DependencyRequest
	Status   string             `json:"status"`
	Created  time.Time          `json:"created"`
	Finished time.Time          `json:"finished,omitempty"`
	Results  []DependencyResult `json:"results"`
}

// DependencyState is a local cached read. Network work happens only in explicit
// check/update jobs, whose caller-provided ID is also their idempotency key.
func (m *Manager) DependencyState(ctx context.Context) (map[string]any, error) {
	ops, err := store.List[DependencyOperation](ctx, m.app.Store, "dependency-operation")
	if err != nil {
		return nil, err
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Created.After(ops[j].Created) })
	if len(ops) > 30 {
		ops = ops[:30]
	}
	sources, err := store.List[GitHubSkillSource](ctx, m.app.Store, "dependency-skill-source")
	if err != nil {
		return nil, err
	}
	var latest DependencyOperation
	if len(ops) > 0 {
		latest = ops[0]
	}
	var plugins domain.PluginInventory
	if err := m.app.Store.Get(ctx, "plugin-inventory", "current", &plugins); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	skills, err := store.List[domain.Skill](ctx, m.app.Store, "skill")
	if err != nil {
		return nil, err
	}
	metadata := []map[string]any{}
	for _, s := range skills {
		metadata = append(metadata, map[string]any{"id": s.ID, "name": s.Name, "path": s.Path, "disabled": s.Disabled, "managed": s.Managed})
	}
	installed := []domain.CodexPlugin{}
	for _, p := range plugins.Plugins {
		if p.Installed {
			installed = append(installed, p)
		}
	}
	return map[string]any{"operations": ops, "githubSkills": sources, "plugins": installed, "skills": metadata, "latest": latest, "managedKinds": []string{"codex", "plugins", "skills"}, "externalDependencies": []string{"git", "gh", "python3", "node", "systemctl", "tailscale"}, "externalDependencyPolicy": "OS/package-manager dependencies are inventoried but not replaced by this owner-user service.", "codexRepository": dependencies.CodexRepository, "activeCodex": m.CodexBinary()}, nil
}

func (m *Manager) DependencyOperation(ctx context.Context, id string) (DependencyOperation, error) {
	var op DependencyOperation
	if !dependencyID.MatchString(id) {
		return op, fmt.Errorf("invalid operation id")
	}
	err := m.app.Store.Get(ctx, "dependency-operation", id, &op)
	return op, err
}

func normalizeDependencyRequest(in DependencyRequest) (DependencyRequest, error) {
	if !dependencyID.MatchString(in.ID) || (in.Action != "check" && in.Action != "update") {
		return in, fmt.Errorf("id and check/update action required")
	}
	if len(in.Targets) == 0 {
		in.Targets = []string{"all"}
	}
	seen := map[string]bool{}
	for _, t := range in.Targets {
		if t == "all" {
			seen["codex"], seen["plugins"], seen["skills"] = true, true, true
			continue
		}
		if t != "codex" && t != "plugins" && t != "skills" {
			return in, fmt.Errorf("targets must be all, codex, plugins or skills")
		}
		seen[t] = true
	}
	in.Targets = nil
	for t := range seen {
		in.Targets = append(in.Targets, t)
	}
	sort.Strings(in.Targets)
	return in, nil
}

func (m *Manager) UpdateDependencies(ctx context.Context, in DependencyRequest) (DependencyOperation, error) {
	in, err := normalizeDependencyRequest(in)
	if err != nil {
		return DependencyOperation{}, err
	}
	// Read before acquiring maintenance: a retried accepted operation is returned
	// even while it owns the gate. A changed request with that ID is rejected.
	var previous DependencyOperation
	if err = m.app.Store.Get(ctx, "dependency-operation", in.ID, &previous); err == nil {
		if string(raw(previous.DependencyRequest)) != string(raw(in)) {
			return previous, fmt.Errorf("operation id already belongs to a different request")
		}
		return previous, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return previous, err
	}
	if !m.adminGate.TryLock() {
		return previous, ErrMaintenanceBusy
	}
	busy := false
	for _, s := range m.sessions() {
		s.mu.Lock()
		busy = busy || activeDependencyRun(s.run.Status)
		s.mu.Unlock()
	}
	m.mu.Lock()
	if busy || m.closing || m.ctx.Err() != nil {
		m.mu.Unlock()
		m.adminGate.Unlock()
		return previous, ErrMaintenanceBusy
	}
	// Recheck after serialization to cover concurrent deliveries of the same ID.
	if err = m.app.Store.Get(ctx, "dependency-operation", in.ID, &previous); err == nil {
		m.mu.Unlock()
		m.adminGate.Unlock()
		if string(raw(previous.DependencyRequest)) != string(raw(in)) {
			return previous, fmt.Errorf("operation id already belongs to a different request")
		}
		return previous, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		m.mu.Unlock()
		m.adminGate.Unlock()
		return previous, err
	}
	op := DependencyOperation{DependencyRequest: in, Status: "queued", Created: time.Now().UTC(), Results: []DependencyResult{}}
	if err = m.saveDependencyOperation(ctx, op); err != nil {
		m.mu.Unlock()
		m.adminGate.Unlock()
		return op, err
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() { defer m.wg.Done(); defer m.adminGate.Unlock(); m.runDependencyUpdate(op) }()
	return op, nil
}

func activeDependencyRun(status string) bool {
	return status == "queued" || status == "starting" || status == "running" || status == "waiting"
}
func (m *Manager) saveDependencyOperation(ctx context.Context, op DependencyOperation) error {
	return m.app.Store.Put(ctx, "dependency-operation", op.ID, op, "dependency.operation")
}

func (m *Manager) runDependencyUpdate(op DependencyOperation) {
	ctx, cancel := context.WithTimeout(m.ctx, 20*time.Minute)
	defer cancel()
	op.Status = "running"
	if m.saveDependencyOperation(ctx, op) != nil {
		return
	}
	defer func() {
		if ctx.Err() != nil {
			op.Status = "uncertain"
			for i := range op.Results {
				if op.Results[i].Status == "running" {
					op.Results[i].Status = "uncertain"
					op.Results[i].Detail = "Interrupted; inspect installed state before any new update."
				}
			}
		}
		op.Finished = time.Now().UTC()
		persist, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = m.saveDependencyOperation(persist, op)
	}()
	add := func(r DependencyResult, apply func(*DependencyResult)) {
		op.Results = append(op.Results, r)
		i := len(op.Results) - 1
		if m.saveDependencyOperation(ctx, op) != nil {
			op.Status = "uncertain"
			cancel()
			return
		}
		apply(&op.Results[i])
		_ = m.saveDependencyOperation(ctx, op)
	}
	for _, target := range op.Targets {
		if ctx.Err() != nil {
			break
		}
		switch target {
		case "codex":
			add(DependencyResult{ID: "codex", Kind: "codex", Status: "running"}, func(r *DependencyResult) { m.updateCodexDependency(ctx, op.ID, op.Action, r) })
		case "plugins":
			m.updatePluginDependencies(ctx, op.Action, add)
		case "skills":
			m.updateSkillDependencies(ctx, op.ID, op.Action, add)
			if op.Action == "update" && op.MigrateLegacyDK {
				m.migrateLegacyDKSkills(ctx, op.ID, add)
			}
		}
	}
	op.Status = "succeeded"
	for _, r := range op.Results {
		if r.Status == "failed" || r.Status == "uncertain" || r.Status == "conflict" {
			op.Status = "partial"
		}
	}
}

func (m *Manager) updateCodexDependency(ctx context.Context, id, action string, r *DependencyResult) {
	r.Status = "failed"
	r.Source = "https://github.com/" + dependencies.CodexRepository
	current, err := codex.ExecutableVersion(ctx, m.CodexBinary())
	if err != nil {
		r.Detail = "Current Codex could not be inspected."
		return
	}
	r.Current = strings.TrimPrefix(current, "codex-cli ")
	release, err := dependencies.LatestCodex(ctx, m.dependencyHTTP)
	if err != nil {
		r.Detail = err.Error()
		return
	}
	r.Available = strings.TrimPrefix(release.Tag, "rust-v")
	if codex.CompareVersions(r.Available, r.Current) <= 0 {
		r.Status = "current"
		r.Detail = "Recorded Codex is at least the latest stable upstream version."
		return
	}
	if action == "check" {
		r.Status = "available"
		return
	}
	stage, err := os.MkdirTemp(m.app.DataDir, ".codex-stage-")
	if err != nil {
		r.Detail = "Could not create Codex staging directory."
		return
	}
	defer os.RemoveAll(stage)
	binary, err := dependencies.StageCodex(ctx, m.dependencyHTTP, release, stage)
	if err != nil {
		r.Detail = err.Error()
		return
	}
	v, err := codex.Version(ctx, binary)
	if err != nil || strings.TrimPrefix(v, "codex-cli ") != r.Available {
		r.Detail = "Downloaded Codex version does not match release metadata."
		return
	}
	probe, cancel := context.WithTimeout(ctx, 45*time.Second)
	err = codex.Probe(probe, binary, m.app.DataDir)
	cancel()
	if err != nil {
		r.Detail = "Candidate from dkropachev/codex failed compatibility: " + err.Error() + "; previous provider retained."
		return
	}
	rel, _ := filepath.Rel(stage, binary)
	versions := filepath.Join(m.app.DataDir, "dependencies", "codex")
	if os.MkdirAll(versions, 0700) != nil {
		r.Detail = "Could not retain Codex distribution."
		return
	}
	target, err := os.MkdirTemp(versions, r.Available+"-")
	if err != nil {
		r.Detail = "Could not retain Codex distribution."
		return
	}
	_ = os.Remove(target)
	if err = os.Rename(stage, target); err != nil {
		r.Detail = "Could not retain Codex distribution."
		return
	}
	binary = filepath.Join(target, rel)
	if err = m.activateCodex(ctx, id, binary); err != nil {
		r.Detail = "Codex activation failed; previous recorded provider retained."
		return
	}
	r.Status = "updated"
	r.Current = r.Available
	r.Detail = "Verified dkropachev/codex distribution activated; existing login and global installation preserved."
}

func (m *Manager) activateCodex(ctx context.Context, id, binary string) error {
	runtime, err := setup.ReadRuntime(m.app.DataDir)
	if err != nil {
		return err
	}
	if runtime.Paths == nil {
		return fmt.Errorf("recorded provider configuration is missing")
	}
	backup := filepath.Join(m.app.DataDir, "dependency-backups", id, "runtime.json")
	if err = setup.AtomicJSON(backup, runtime); err != nil {
		return err
	}
	runtime.Paths["codex"] = binary
	bin, err := commandenv.Prepare(filepath.Join(m.app.DataDir, "provider-bin"), runtime.Paths)
	if err != nil {
		return err
	}
	runtime.ProviderBin = bin
	runtime.Path = commandenv.Path(bin, runtime.Path)
	if err = setup.AtomicJSON(filepath.Join(m.app.DataDir, "runtime.json"), runtime); err != nil {
		return err
	}
	// No task can start while this job owns adminGate. Existing idle processes
	// retire so an explicitly continued thread opens on the new provider.
	m.mu.Lock()
	old := m.live
	m.live = map[string]*session{}
	m.providerMu.Lock()
	m.Binary = binary
	m.ProviderBin = bin
	m.ProviderPaths = runtime.Paths
	m.providerMu.Unlock()
	m.mu.Unlock()
	for _, s := range old {
		s.mu.Lock()
		s.retired = true
		if s.client != nil {
			_ = s.client.Close()
		}
		s.mu.Unlock()
	}
	return nil
}

func (m *Manager) updatePluginDependencies(ctx context.Context, action string, add func(DependencyResult, func(*DependencyResult))) {
	before, _ := store.List[domain.Skill](ctx, m.app.Store, "skill")
	if err := m.refreshPluginInventory(ctx); err != nil {
		add(DependencyResult{ID: "plugin-inventory", Kind: "plugins", Status: "failed"}, func(r *DependencyResult) { r.Detail = "Plugin inventory refresh failed." })
		return
	}
	var inv domain.PluginInventory
	if m.app.Store.Get(ctx, "plugin-inventory", "current", &inv) != nil {
		return
	}
	failed := map[string]bool{}
	for _, market := range inv.Marketplaces {
		if market.SourceType != "git" {
			continue
		}
		market := market
		if !marketplaceName.MatchString(market.Name) {
			continue
		}
		add(DependencyResult{ID: "marketplace:" + market.Name, Kind: "marketplace", Status: "running"}, func(r *DependencyResult) {
			if _, err := m.pluginCommand(ctx, "marketplace", "upgrade", "--json", "--", market.Name); err != nil {
				failed[market.Name] = true
				r.Status = "failed"
				r.Detail = "This Git marketplace could not refresh; its plugins are preserved and independent marketplaces continue."
				return
			}
			r.Status = "checked"
		})
	}
	if err := m.refreshPluginInventory(ctx); err != nil {
		add(DependencyResult{ID: "plugin-inventory", Kind: "plugins", Status: "failed"}, func(r *DependencyResult) {
			r.Detail = "Refreshed marketplace inventory unavailable; installed plugins preserved."
		})
		return
	}
	if m.app.Store.Get(ctx, "plugin-inventory", "current", &inv) != nil {
		return
	}
	for _, p := range inv.Plugins {
		if !p.Installed || !pluginSelector.MatchString(p.ID) {
			continue
		}
		p := p
		add(DependencyResult{ID: p.ID, Kind: "plugin", Current: p.InstalledVersion, Available: p.AvailableVersion, Status: "running"}, func(r *DependencyResult) {
			if failed[p.Marketplace] {
				r.Status = "failed"
				r.Detail = "Owning marketplace refresh failed; this plugin was preserved."
				return
			}
			if action == "check" {
				r.Status = p.UpdateStatus
				return
			}
			if !p.Enabled {
				r.Status = "disabled"
				r.Detail = "Disabled plugin preserved; native installation would enable it."
				return
			}
			_, updateErr := m.pluginCommand(ctx, "add", "--json", "--", p.ID)
			observeErr := m.refreshPluginInventory(ctx)
			if updateErr != nil {
				r.Status = "uncertain"
				r.Detail = "Native update did not confirm success; inventory observed without replay."
				return
			}
			if observeErr != nil {
				r.Status = "uncertain"
				r.Detail = "Update returned but installed version could not be verified."
				return
			}
			var now domain.PluginInventory
			if m.app.Store.Get(ctx, "plugin-inventory", "current", &now) != nil {
				r.Status = "uncertain"
				return
			}
			for _, entry := range now.Plugins {
				if entry.ID == p.ID && entry.Installed {
					if entry.Enabled != p.Enabled {
						r.Status = "uncertain"
						r.Detail = "Native update changed enabled state; inspect before retry."
						return
					}
					r.Current = entry.InstalledVersion
					r.Available = entry.AvailableVersion
					r.Status = "updated"
					if p.InstalledVersion == entry.InstalledVersion {
						r.Status = "current"
						if entry.AvailableVersion == "" {
							r.Status = "observed"
							r.Detail = "Native update completed and installed version was verified; latest upstream version is unavailable."
						}
					}
					for _, market := range now.Marketplaces {
						if market.Name == p.Marketplace && market.SourceType == "local" {
							r.Detail = "Local marketplace source retained, including its compatibility changes."
						}
					}
					return
				}
			}
			r.Status = "uncertain"
			r.Detail = "Installed plugin missing from post-update inventory."
		})
	}
	if action == "update" {
		if skills, err := m.discoverAdminSkills(ctx); err != nil {
			add(DependencyResult{ID: "skill-discovery", Kind: "skills", Status: "failed"}, func(r *DependencyResult) { r.Detail = "Installed plugin skills could not be refreshed." })
		} else if err := m.remapProfileSkills(ctx, before, skills); err != nil {
			add(DependencyResult{ID: "profile-skills", Kind: "skills", Status: "failed"}, func(r *DependencyResult) {
				r.Detail = "Plugin updates completed but profile skill references require reconciliation."
			})
		}
	}
}

func (m *Manager) recoverDependencyOperations(ctx context.Context) error {
	ops, err := store.List[DependencyOperation](ctx, m.app.Store, "dependency-operation")
	if err != nil {
		return err
	}
	for _, op := range ops {
		if op.Status == "queued" || op.Status == "running" {
			if err := m.recoverLegacySkillReferences(ctx, op); err != nil {
				return err
			}
			op.Status = "uncertain"
			op.Finished = time.Now().UTC()
			for i := range op.Results {
				if op.Results[i].Status == "running" {
					op.Results[i].Status = "uncertain"
					op.Results[i].Detail = "Daemon interrupted operation; inspect installed state before an explicit new update."
				}
			}
			if err = m.saveDependencyOperation(ctx, op); err != nil {
				return err
			}
		}
	}
	return nil
}

// CodexBinary protects read-only diagnostics/discovery against a provider switch.
func (m *Manager) CodexBinary() string {
	m.providerMu.RLock()
	defer m.providerMu.RUnlock()
	return m.Binary
}
