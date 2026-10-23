package setup

import (
	"codefactory/internal/commandenv"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Runtime struct {
	ProviderBin string            `json:"providerBin,omitempty"`
	Paths       map[string]string `json:"paths"`
	Path        string            `json:"path"`
	DataDir     string            `json:"dataDir"`
	Listen      string            `json:"listen"`
}

func ReadRuntime(dir string) (Runtime, error) {
	var r Runtime
	b, e := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if e != nil {
		return r, e
	}
	e = json.Unmarshal(b, &r)
	return r, e
}
func ResolveRuntime(ctx context.Context, dir, listen string) (Runtime, error) {
	r := Runtime{Paths: map[string]string{}, Path: os.Getenv("PATH"), DataDir: dir, Listen: listen}
	previous, previousErr := ReadRuntime(dir)
	if previousErr == nil {
		r.Path = previous.Path + string(os.PathListSeparator) + r.Path
	}
	for _, name := range []string{"git", "gh", "codex", "systemctl"} {
		p, e := exec.LookPath(name)
		if old := previous.Paths[name]; old != "" {
			if info, err := os.Stat(old); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				p = old
				e = nil
			}
		}
		if e != nil {
			return r, fmt.Errorf("setup blocker: missing %s; existing provider installations and authentication are preserved", name)
		}
		p, e = filepath.EvalSymlinks(p)
		if e == nil {
			p, e = filepath.Abs(p)
		}
		if e != nil {
			return r, e
		}
		r.Paths[name] = p
	}
	providerBin, e := commandenv.Prepare(filepath.Join(dir, "provider-bin"), r.Paths)
	if e != nil {
		return r, e
	}
	r.ProviderBin = providerBin
	dirs := []string{providerBin}
	seen := map[string]bool{providerBin: true}
	for _, name := range []string{"git", "gh", "codex", "systemctl"} {
		path := filepath.Dir(r.Paths[name])
		if !seen[path] {
			dirs = append(dirs, path)
			seen[path] = true
		}
	}
	for _, path := range filepath.SplitList(r.Path) {
		if path != "" && !seen[path] {
			dirs = append(dirs, path)
			seen[path] = true
		}
	}
	r.Path = strings.Join(dirs, string(os.PathListSeparator))
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, e := exec.CommandContext(check, r.Paths["codex"], "--version").Output()
	if e != nil || string(b) != "codex-cli 0.161.0\n" {
		return r, fmt.Errorf("setup blocker: Codex 0.161.0 is required")
	}
	return r, nil
}
func AtomicJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return atomicFile(path, append(b, '\n'), 0600)
}
func atomicFile(path string, b []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".factory-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}
