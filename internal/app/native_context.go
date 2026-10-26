package app

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Native child threads need durable context to delegate. Keep that state inside
// the task; authentication/configuration remain references to the existing owner
// installation, never copies. The context root is mounted read-only, with only
// its native session/log/temp directories writable.
func prepareNativeContext(scratch, owner string) (string, []string, error) {
	if !filepath.IsAbs(scratch) || !filepath.IsAbs(owner) || filepath.Clean(scratch) == "/" || filepath.Clean(owner) == "/" {
		return "", nil, fmt.Errorf("invalid native context roots")
	}
	root, err := os.OpenRoot(scratch)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	relative := filepath.Join("state", "native-context")
	if err := root.MkdirAll(relative, 0700); err != nil {
		return "", nil, err
	}
	info, err := root.Lstat(relative)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("invalid private native context directory")
	}
	context, err := root.OpenRoot(relative)
	if err != nil {
		return "", nil, err
	}
	defer context.Close()
	for _, name := range []string{"config.toml", "auth.json", "accounts", "models_cache.json", "plugins", "skills", "agents", "AGENTS.md"} {
		target := filepath.Join(owner, name)
		if _, err := os.Stat(target); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return "", nil, err
		}
		if info, err := context.Lstat(name); err == nil {
			link, readErr := context.Readlink(name)
			if info.Mode()&os.ModeSymlink == 0 || readErr != nil || link != target {
				return "", nil, fmt.Errorf("retained native context reference changed")
			}
		} else if os.IsNotExist(err) {
			if err := context.Symlink(target, name); err != nil {
				return "", nil, err
			}
		} else {
			return "", nil, err
		}
	}
	directory := filepath.Join(scratch, relative)
	writable := []string{}
	for _, name := range []string{"sessions", "log", "tmp", "shell_snapshots", "thread-writer-locks"} {
		if err := context.MkdirAll(name, 0700); err != nil {
			return "", nil, err
		}
		info, err := context.Lstat(name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("invalid native context state directory")
		}
		writable = append(writable, filepath.Join(directory, name))
	}
	// Write roots in the pinned sandbox are directories. Reference nonsecret
	// installation state from a separate writable directory, preserving the
	// read-only context root and its authentication references.
	state := filepath.Join("state", "native-codex")
	if err := root.MkdirAll(state, 0700); err != nil {
		return "", nil, err
	}
	stateFile := filepath.Join(state, "installation_id")
	file, err := root.OpenFile(stateFile, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0644)
	if err == nil {
		if err := file.Close(); err != nil {
			return "", nil, err
		}
	} else if !os.IsExist(err) {
		return "", nil, err
	}
	info, err = root.Lstat(stateFile)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("invalid native installation state file")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
		return "", nil, fmt.Errorf("native installation state must not be a shared file")
	}
	target := filepath.Join(scratch, stateFile)
	if info, err := context.Lstat("installation_id"); err == nil {
		link, readErr := context.Readlink("installation_id")
		if info.Mode()&os.ModeSymlink == 0 || readErr != nil || link != target {
			return "", nil, fmt.Errorf("native installation reference changed")
		}
	} else if os.IsNotExist(err) {
		if err := context.Symlink(target, "installation_id"); err != nil {
			return "", nil, err
		}
	} else {
		return "", nil, err
	}
	return directory, writable, nil
}
