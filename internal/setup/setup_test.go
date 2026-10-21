package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDaemonLockAndOwnerToken(t *testing.T) {
	dir := t.TempDir()
	first, e := LockDaemon(dir)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := LockDaemon(dir); e == nil {
		second.Close()
		t.Fatal("second daemon accepted")
	}
	first.Close()
	third, e := LockDaemon(dir)
	if e != nil {
		t.Fatal("lock survived process exit", e)
	}
	defer third.Close()
	token, e := Token(dir)
	if e != nil || len(token) != 64 {
		t.Fatal(e)
	}
	again, e := Token(dir)
	if e != nil || again != token {
		t.Fatal("token did not persist", e)
	}
	info, e := os.Stat(filepath.Join(dir, "owner.token"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe token permissions", e)
	}
}
