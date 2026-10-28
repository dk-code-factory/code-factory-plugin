package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/commandenv"
	"codefactory/internal/domain"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (m *Manager) taskSandbox(ctx context.Context, r domain.Run) ([]string, error) {
	return m.taskSandboxWithReadables(ctx, r, nil)
}

func (m *Manager) taskSandboxWithReadables(ctx context.Context, r domain.Run, additional []string) ([]string, error) {
	return m.taskSandboxWithNativeChild(ctx, r, additional, false)
}

// Explicit command reviewers can launch native providers without a selected
// profile skill. Reuse the same child context as packaged-skill execution.
func (m *Manager) taskSandboxWithNativeChild(ctx context.Context, r domain.Run, additional []string, nativeChild bool) ([]string, error) {
	scratch := filepath.Join(m.app.DataDir, "scratch", r.ID)
	for _, dir := range []string{"tmp", "cache", "data", "state"} {
		if err := os.MkdirAll(filepath.Join(scratch, dir), 0700); err != nil {
			return nil, err
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	// Every mode uses private XDG data, including explicit full access. Seed the
	// owner's existing rules and Decisions before selecting execution authority.
	for _, name := range []string{"codex-repo-rules", "dk-pr-decisions"} {
		if err := seedSandboxData(filepath.Join(data, name), filepath.Join(scratch, "data", name)); err != nil {
			return nil, err
		}
	}
	// Explicit full access retains private scratch and selected executables while
	// omitting the bounded filesystem/network permissions profile.
	if r.Profile.Sandbox == "danger-full-access" {
		return codex.ConfigArgs(map[string]any{
			"features.network_proxy": false,
			"shell_environment_policy.set": map[string]any{
				"TMPDIR":    filepath.Join(scratch, "tmp"),
				"TMPPREFIX": filepath.Join(scratch, "tmp", "zsh"),
				"TMP":       filepath.Join(scratch, "tmp"), "TEMP": filepath.Join(scratch, "tmp"),
				"XDG_CACHE_HOME":          filepath.Join(scratch, "cache"),
				"XDG_DATA_HOME":           filepath.Join(scratch, "data"),
				"XDG_STATE_HOME":          filepath.Join(scratch, "state"),
				"PYTHONDONTWRITEBYTECODE": "1",
				"PATH":                    commandenv.Path(m.ProviderBin, os.Getenv("PATH")),
			},
		})
	}
	binary, err := exec.LookPath(m.CodexBinary())
	if err != nil {
		return nil, fmt.Errorf("sandbox provider executable is unavailable")
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return nil, err
	}
	gitDir, err := command(ctx, r.Worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	ghConfig := os.Getenv("GH_CONFIG_DIR")
	if ghConfig == "" {
		ghConfig = filepath.Join(config, "gh")
	}
	readable := []string{binary, gitDir, filepath.Join(m.app.DataDir, "snapshots", r.ID),
		filepath.Join(home, ".codex", "skills"), filepath.Join(home, ".codex", "agents"),
		filepath.Join(home, ".codex", "plugins", "cache"), filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".gitconfig"), filepath.Join(config, "git"), ghConfig}
	readable = append(readable, additional...)
	commandPath := commandenv.Path(m.ProviderBin, os.Getenv("PATH"))
	writableScratch := []string{}
	for _, skill := range r.Skills {
		if _, packaged := skill.Files[filepath.Join(".codex-plugin", "plugin.json")]; packaged {
			nativeChild = true
		}
	}
	if nativeChild {
		readable = append(readable, filepath.Join(home, ".codex", "config.toml"), filepath.Join(home, ".codex", "auth.json"), filepath.Join(home, ".codex", "accounts"), filepath.Join(home, ".codex", "models_cache.json"), filepath.Join(config, "dk-code-factory", "updates.json"))
		companion, err := commandenv.CodexCompanion(binary)
		if err != nil {
			return nil, err
		}
		if companion != "" {
			readable = append(readable, companion)
		}
		// Nested CLI providers otherwise start every ambient MCP connection before
		// reviewing. They need the native plugin registry and existing login, but
		// not unrelated connectors that bypass or fail the command destination gate.
		overrides, err := m.childProviderOverrides(ctx, r.Worktree, scratch)
		if err != nil {
			return nil, err
		}
		registry, digest, err := m.nativeRegistrySnapshot(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		options := commandenv.ChildOptions{Registry: registry, RegistrySHA256: digest, ExternallyReadOnly: r.Profile.Sandbox == "read-only"}
		if options.ExternallyReadOnly {
			contextHome, writable, err := prepareNativeContext(scratch, filepath.Join(home, ".codex"))
			if err != nil {
				return nil, err
			}
			options.ContextHome = contextHome
			readable = append(readable, contextHome, filepath.Join(home, ".codex", "AGENTS.md"))
			writableScratch = append(writableScratch, writable...)
		}
		if os.Getenv("CODE_FACTORY_NATIVE_CHILD_AUDIT") == "1" && options.ExternallyReadOnly {
			options.AuditDir = filepath.Join(scratch, "child-audit")
		}
		childBin, err := commandenv.PrepareCodex(scratch, binary, overrides, options)
		if err != nil {
			return nil, err
		}
		commandPath = commandenv.Path(childBin, commandPath)
		check, cancel := context.WithTimeout(ctx, 15*time.Second)
		data, err := m.pluginCommand(check, "marketplace", "list", "--json")
		cancel()
		if err != nil {
			return nil, fmt.Errorf("native plugin marketplace metadata is unavailable: %w", err)
		}
		paths, err := marketplaceMetadataPaths(data)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("marketplace manifest is not a regular file")
			}
			readable = append(readable, path)
		}
	}
	if m.ProviderBin != "" {
		readable = append(readable, m.ProviderBin)
	}
	for _, tool := range []string{"git", "gh", "python3", "node"} {
		path, err := exec.LookPath(tool)
		if selected := m.ProviderPaths[tool]; selected != "" {
			path, err = selected, nil
		}
		if err == nil {
			if path, err = filepath.EvalSymlinks(path); err != nil {
				return nil, err
			}
			readable = append(readable, path)
		}
	}
	roots := []string{}
	for _, path := range readable {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		roots = append(roots, path)
	}
	return (codex.TaskSandbox{ID: r.ID, Mode: r.Profile.Sandbox, Workspace: r.Worktree, Scratch: scratch, Readable: roots, WritableScratch: writableScratch, CommandPath: commandPath}).Args()
}

// Retain authoritative native inventory outside writable task scratch. Resume
// reuses it; a new run captures a new inventory. No installed cache is modified.
func (m *Manager) nativeRegistrySnapshot(ctx context.Context, id string) (string, string, error) {
	dir := filepath.Join(m.app.DataDir, "snapshots", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	const name = "native-plugin-registry.json"
	var data []byte
	if info, err := root.Lstat(name); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return "", "", fmt.Errorf("invalid retained native plugin inventory")
		}
		data, err = root.ReadFile(name)
		if err != nil {
			return "", "", err
		}
	} else if !os.IsNotExist(err) {
		return "", "", err
	} else {
		check, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		data, err = m.pluginCommand(check, "list", "--json")
		if err != nil {
			return "", "", fmt.Errorf("native plugin inventory unavailable: %w", err)
		}
	}
	var inventory struct {
		Installed []map[string]json.RawMessage `json:"installed"`
	}
	if err := json.Unmarshal(data, &inventory); err != nil || inventory.Installed == nil {
		return "", "", fmt.Errorf("invalid native plugin inventory")
	}
	for _, record := range inventory.Installed {
		if record == nil {
			return "", "", fmt.Errorf("invalid native plugin inventory record")
		}
	}
	// Only installed metadata is needed by workflow bindings and lifecycle code.
	data, err = json.Marshal(map[string]any{"installed": inventory.Installed, "available": []any{}})
	if err != nil {
		return "", "", err
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil {
			return "", "", writeErr
		}
		if closeErr != nil {
			return "", "", closeErr
		}
	} else if !os.IsExist(err) {
		return "", "", err
	}
	digest := sha256.Sum256(data)
	return filepath.Join(dir, name), fmt.Sprintf("%x", digest), nil
}

func (m *Manager) childProviderOverrides(ctx context.Context, cwd, scratch string) ([]string, error) {
	check, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	client, err := codex.Start(check, m.CodexBinary(), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("child provider configuration unavailable: %w", err)
	}
	defer client.Close()
	if err = client.Initialize(check); err != nil {
		return nil, err
	}
	var local struct{ Config map[string]json.RawMessage }
	if err = client.Call(check, "config/read", map[string]any{"includeLayers": false, "cwd": cwd}, &local); err != nil {
		return nil, err
	}
	var servers map[string]json.RawMessage
	if raw := local.Config["mcp_servers"]; len(raw) > 0 {
		if err = json.Unmarshal(raw, &servers); err != nil {
			return nil, fmt.Errorf("invalid child provider MCP configuration")
		}
	}
	entries := []string{}
	for name := range servers {
		quoted, _ := json.Marshal(name)
		entries = append(entries, string(quoted)+"={enabled=false}")
	}
	sort.Strings(entries)
	// CLI override paths split dots literally; quoted server names belong inside
	// the TOML table value so punctuation cannot create a different server entry.
	state, _ := json.Marshal(filepath.Join(scratch, "state", "native-codex"))
	return []string{"features.apps=false", "features.code_mode=false", "features.tool_router=false", "features.workflows=false", "features.cyber_policy_auto_recovery=false", "sqlite_home=" + string(state), "mcp_servers={" + strings.Join(entries, ",") + "}"}, nil
}

// Skills may refresh their private indexes. Give them a bounded per-run copy,
// preserving the owner's settings and the retained run's own cache on resume.
func seedSandboxData(source, target string) error {
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".seed-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var total int64
	files := 0
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("sandbox data cannot include symlinks")
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(stage, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("sandbox data must contain regular files")
		}
		files++
		if total+info.Size() > 64<<20 || files > 10000 {
			return fmt.Errorf("sandbox data exceeds 64 MiB")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(file, (64<<20)-total+1))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		total += int64(len(b))
		if total > 64<<20 {
			return fmt.Errorf("sandbox data exceeds 64 MiB")
		}
		return os.WriteFile(dest, b, 0600)
	})
	if err != nil {
		return err
	}
	return os.Rename(stage, target)
}

// Native CLI inventory loads configured marketplace manifests, including local
// sources. Grant the metadata files, never their containing owner directories.
func marketplaceMetadataPaths(data []byte) ([]string, error) {
	var inventory struct {
		Marketplaces []struct {
			Root string `json:"root"`
		} `json:"marketplaces"`
	}
	if err := json.Unmarshal(data, &inventory); err != nil || inventory.Marketplaces == nil {
		return nil, fmt.Errorf("invalid native marketplace metadata")
	}
	paths := []string{}
	for _, entry := range inventory.Marketplaces {
		if entry.Root == "" {
			continue
		}
		if !filepath.IsAbs(entry.Root) {
			return nil, fmt.Errorf("native marketplace root must be absolute")
		}
		paths = append(paths, filepath.Join(entry.Root, ".agents", "plugins", "marketplace.json"), filepath.Join(entry.Root, ".codex-plugin", "plugin.json"))
	}
	return paths, nil
}
