package app

import (
	"codefactory/internal/dependencies"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type GitHubSkillSource struct {
	Name       string `json:"name"`
	Root       string `json:"root"`
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Ref        string `json:"ref"`
	Revision   string `json:"revision,omitempty"`
	Digest     string `json:"digest,omitempty"`
}

func (s GitHubSkillSource) id() string { return s.Root + ":" + s.Name }
func (s GitHubSkillSource) validate() error {
	if !regexpSkillName(s.Name) || (s.Root != "codex" && s.Root != "agents") || !repoPattern.MatchString(s.Repository) || strings.Contains(s.Repository, "..") || strings.HasPrefix(s.Repository, "-") || !filepath.IsLocal(s.Path) || s.Path == "." || path.Clean(s.Path) != s.Path || strings.Contains(s.Path, "\\") || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./-]{0,199}$`).MatchString(s.Ref) || strings.Contains(s.Ref, "..") {
		return fmt.Errorf("a skill name, codex/agents root, GitHub owner/repository, relative skill path and valid ref are required")
	}
	return nil
}
func (s GitHubSkillSource) target() (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, "."+s.Root, "skills")
	// Parent links must not redirect an update outside the declared skill root.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if resolved != root {
		return "", fmt.Errorf("skill install root must not be a symlink")
	}
	return filepath.Join(root, s.Name), nil
}

func (m *Manager) RegisterGitHubSkill(ctx context.Context, s GitHubSkillSource) (GitHubSkillSource, error) {
	if s.Ref == "" {
		s.Ref = "HEAD"
	}
	if err := s.validate(); err != nil {
		return s, err
	}
	if !m.adminGate.TryLock() {
		return s, ErrMaintenanceBusy
	}
	defer m.adminGate.Unlock()
	m.mu.Lock()
	busy := m.closing
	for _, run := range m.live {
		run.mu.Lock()
		busy = busy || activeDependencyRun(run.run.Status)
		run.mu.Unlock()
	}
	m.mu.Unlock()
	if busy {
		return s, ErrMaintenanceBusy
	}
	target, err := s.target()
	if err != nil {
		return s, err
	}
	s.Digest, err = dependencies.TreeDigest(target)
	if err != nil {
		return s, err
	}
	if _, err = os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		return s, fmt.Errorf("installed skill entrypoint is missing")
	}
	// Revision is observed from GitHub by check/update, never trusted from input.
	s.Revision = ""
	err = m.app.Store.Put(ctx, "dependency-skill-source", s.id(), s, "dependency.source")
	return s, err
}

func (m *Manager) dependencyCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	binary := name
	m.providerMu.RLock()
	if p := m.ProviderPaths[name]; p != "" {
		binary = p
	}
	m.providerMu.RUnlock()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1")
	var out strings.Builder
	cmd.Stdout = &boundedOutput{dst: &out, remaining: 2 << 20}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("GitHub dependency command failed; verify source and existing authentication")
	}
	return []byte(out.String()), nil
}

func (m *Manager) updateSkillDependencies(ctx context.Context, opID, action string, add func(DependencyResult, func(*DependencyResult))) {
	if err := m.ImportSkillLocks(ctx); err != nil {
		add(DependencyResult{ID: "github-skill-provenance", Kind: "skill", Status: "failed"}, func(r *DependencyResult) {
			r.Detail = "GitHub skill provenance could not be imported; existing skills preserved."
		})
		return
	}
	sources, err := store.List[GitHubSkillSource](ctx, m.app.Store, "dependency-skill-source")
	if err != nil {
		add(DependencyResult{ID: "github-skills", Kind: "skill", Status: "failed"}, func(r *DependencyResult) { r.Detail = "Could not read registered GitHub skill sources." })
		return
	}
	if len(sources) == 0 {
		add(DependencyResult{ID: "github-skills", Kind: "skill", Status: "unmanaged"}, func(r *DependencyResult) {
			r.Detail = "No standalone GitHub source provenance registered. Plugin skills update with their owning plugins; other local skills are preserved."
		})
		return
	}
	for _, source := range sources {
		source := source
		add(DependencyResult{ID: source.id(), Kind: "skill", Current: source.Revision, Status: "running"}, func(r *DependencyResult) {
			r.Status = "failed"
			if err := source.validate(); err != nil {
				r.Detail = "Invalid registered GitHub source."
				return
			}
			target, err := source.target()
			if err != nil {
				r.Detail = "Skill destination is unavailable or redirected."
				return
			}
			digest, err := dependencies.TreeDigest(target)
			if err != nil || digest != source.Digest {
				r.Status = "conflict"
				r.Detail = "Local skill changed since source registration; existing work preserved."
				return
			}
			data, err := m.dependencyCommand(ctx, "gh", "api", "repos/"+source.Repository+"/commits/"+source.Ref, "--jq", ".sha")
			if err != nil {
				r.Detail = err.Error()
				return
			}
			sha := strings.TrimSpace(string(data))
			if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(sha) {
				r.Detail = "GitHub returned an invalid revision."
				return
			}
			r.Available = sha
			if source.Revision == sha {
				r.Status = "current"
				return
			}
			if action == "check" {
				r.Status = "available"
				return
			}
			if err = m.installGitHubSkill(ctx, opID, source, sha, target); err != nil {
				r.Detail = err.Error()
				return
			}
			r.Current = sha
			r.Status = "updated"
			r.Detail = "Pinned GitHub skill installed; previous files retained in private backup."
		})
	}
}

func (m *Manager) installGitHubSkill(ctx context.Context, opID string, source GitHubSkillSource, sha, target string) error {
	stage, err := os.MkdirTemp(m.app.DataDir, ".github-skill-")
	if err != nil {
		return fmt.Errorf("could not stage GitHub skill")
	}
	defer os.RemoveAll(stage)
	archive, err := os.CreateTemp(stage, "archive-")
	if err != nil {
		return fmt.Errorf("could not stage GitHub skill")
	}
	defer archive.Close()
	binary := "gh"
	m.providerMu.RLock()
	if p := m.ProviderPaths["gh"]; p != "" {
		binary = p
	}
	m.providerMu.RUnlock()
	cmd := exec.CommandContext(ctx, binary, "api", "repos/"+source.Repository+"/tarball/"+sha)
	cmd.Stdout = &boundedOutput{dst: archive, remaining: 128 << 20}
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("pinned GitHub skill archive unavailable")
	}
	if _, err = archive.Seek(0, 0); err != nil {
		return fmt.Errorf("could not read GitHub skill archive")
	}
	extracted := filepath.Join(stage, "repo")
	if os.Mkdir(extracted, 0700) != nil {
		return fmt.Errorf("could not stage GitHub skill")
	}
	if err = dependencies.Extract(archive, extracted, 128<<20); err != nil {
		return err
	}
	roots, err := os.ReadDir(extracted)
	if err != nil || len(roots) != 1 || !roots[0].IsDir() {
		return fmt.Errorf("GitHub archive must have one repository root")
	}
	repo := filepath.Join(extracted, roots[0].Name())
	if _, err = os.Stat(filepath.Join(repo, ".codex-plugin", "plugin.json")); err == nil {
		return fmt.Errorf("source is a native plugin; update it through the plugin manager to retain package resources")
	}
	candidate := filepath.Join(repo, filepath.FromSlash(source.Path))
	digest, err := dependencies.TreeDigest(candidate)
	if err != nil {
		return fmt.Errorf("GitHub skill path is missing or invalid")
	}
	entry, err := os.ReadFile(filepath.Join(candidate, "SKILL.md"))
	if err != nil || len(entry) > 1<<20 || !strings.HasPrefix(string(entry), "---\n") {
		return fmt.Errorf("GitHub skill requires valid frontmatter and entrypoint")
	}
	// Keep backup and replacement on the destination filesystem. Linux's atomic
	// exchange leaves the installed skill present even if the daemon is killed.
	backup := filepath.Join(m.app.DataDir, "dependency-backups", opID, "skills", source.Root, source.Name)
	if err = dependencies.CopyTree(target, backup); err != nil {
		return fmt.Errorf("could not back up installed skill")
	}
	replacement, err := os.MkdirTemp(filepath.Dir(target), ".dependency-skill-")
	if err != nil {
		return fmt.Errorf("could not stage skill replacement")
	}
	defer os.RemoveAll(replacement)
	if err = dependencies.CopyTree(candidate, replacement); err != nil {
		return fmt.Errorf("could not copy skill replacement")
	}
	if current, err := dependencies.TreeDigest(target); err != nil || current != source.Digest {
		return fmt.Errorf("installed skill changed while update was staged")
	}
	if err = unix.Renameat2(unix.AT_FDCWD, target, unix.AT_FDCWD, replacement, unix.RENAME_EXCHANGE); err != nil {
		return fmt.Errorf("atomic skill replacement unavailable; original retained")
	}
	source.Revision, source.Digest = sha, digest
	if err = m.app.Store.Put(ctx, "dependency-skill-source", source.id(), source, "dependency.source"); err != nil {
		if unix.Renameat2(unix.AT_FDCWD, target, unix.AT_FDCWD, replacement, unix.RENAME_EXCHANGE) != nil {
			return fmt.Errorf("skill changed but provenance persistence failed; inspect backup before retry")
		}
		return fmt.Errorf("skill provenance could not be saved; original restored")
	}
	return nil
}

// ImportSkillLocks adopts only explicit GitHub provenance from the commonly used
// skills CLI lock file. Unrecognized/local entries remain untouched.
func (m *Manager) ImportSkillLocks(ctx context.Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, root := range []string{"agents", "codex"} {
		filename := filepath.Join(home, "."+root, ".skill-lock.json")
		f, err := os.Open(filename)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		var lock struct {
			Skills map[string]struct {
				Source     string `json:"source"`
				SourceType string `json:"sourceType"`
				SkillPath  string `json:"skillPath"`
			} `json:"skills"`
		}
		err = json.NewDecoder(io.LimitReader(f, 2<<20)).Decode(&lock)
		f.Close()
		if err != nil {
			return fmt.Errorf("invalid skill provenance lock file")
		}
		for name, row := range lock.Skills {
			if row.SourceType != "github" {
				continue
			}
			s := GitHubSkillSource{Name: name, Root: root, Repository: row.Source, Path: strings.TrimSuffix(row.SkillPath, "/SKILL.md"), Ref: "HEAD"}
			if s.validate() != nil {
				continue
			}
			var existing GitHubSkillSource
			if m.app.Store.Get(ctx, "dependency-skill-source", s.id(), &existing) == nil {
				continue
			}
			target, err := s.target()
			if err != nil {
				continue
			}
			s.Digest, err = dependencies.TreeDigest(target)
			if err != nil {
				continue
			}
			if err = m.app.Store.Put(ctx, "dependency-skill-source", s.id(), s, "dependency.source"); err != nil {
				return err
			}
		}
	}
	return nil
}
