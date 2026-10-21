// Package setup owns Linux packaging and host diagnostics, outside the domain layer.
package setup

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func DataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "code-factory")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "code-factory")
}
func Token(dir string) (string, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	p := filepath.Join(dir, "owner.token")
	b, e := os.ReadFile(p)
	if os.IsNotExist(e) {
		b = make([]byte, 32)
		if _, e = rand.Read(b); e != nil {
			return "", e
		}
		b = []byte(hex.EncodeToString(b))
		e = os.WriteFile(p, b, 0600)
	}
	if e != nil {
		return "", e
	}
	if len(b) < 32 {
		return "", fmt.Errorf("invalid owner token")
	}
	return strings.TrimSpace(string(b)), os.Chmod(p, 0600)
}
func Quote(s string) string      { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func systemdArg(s string) string { return systemdQuote(strings.ReplaceAll(s, "$", "$$")) }
func systemdQuote(s string) string {
	b, _ := json.Marshal(strings.ReplaceAll(s, "%", "%%"))
	return string(b)
}

// LockDaemon prevents two processes from recovering or running the same state directory.
func LockDaemon(dir string) (*os.File, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	return lockFile(filepath.Join(dir, "daemon.lock"))
}
func lockFile(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("Code Factory is already running for this data directory")
	}
	return f, nil
}

func LoopbackAddress(address string) error {
	host, _, e := net.SplitHostPort(address)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("listener and diagnostic destination must use a loopback IP address")
	}
	return nil
}
