package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

func (m *Manager) taskSandbox(ctx context.Context, r domain.Run) ([]string, error) {
	// Full access remains an explicit existing profile choice, never a fallback
	// for a bounded sandbox that failed to initialize.
	if r.Profile.Sandbox == "danger-full-access" {
		return nil, nil
	}
	scratch := filepath.Join(m.app.DataDir, "scratch", r.ID)
	for _, dir := range []string{"tmp", "cache", "data"} {
		if err := os.MkdirAll(filepath.Join(scratch, dir), 0700); err != nil {
			return nil, err
		}
	}
	binary, err := exec.LookPath(m.Binary)
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
	home, err := os.UserHomeDir()
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
	for _, tool := range []string{"git", "gh", "python3", "node"} {
		if path, err := exec.LookPath(tool); err == nil {
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
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	for _, name := range []string{"codex-repo-rules", "dk-pr-decisions"} {
		if err := seedSandboxData(filepath.Join(data, name), filepath.Join(scratch, "data", name)); err != nil {
			return nil, err
		}
	}
	return (codex.TaskSandbox{ID: r.ID, Mode: r.Profile.Sandbox, Workspace: r.Worktree, Scratch: scratch, Readable: roots}).Args()
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
