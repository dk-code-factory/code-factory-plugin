package setup

import (
	"codefactory/internal/store"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotIncludesCommittedWALAndPreservesSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Put(ctx, "fixture", "value", map[string]string{"value": "retained"}, ""); e != nil {
		t.Fatal(e)
	}
	backup := filepath.Join(dir, "backup.db")
	if e = BackupDatabase(ctx, path, backup); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", backup)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var b string
	if e = db.QueryRow("SELECT data FROM entities WHERE kind='fixture'").Scan(&b); e != nil {
		t.Fatal(e)
	}
	if b != "{\"value\":\"retained\"}" {
		t.Fatal("snapshot omitted committed WAL")
	}
	info, _ := os.Stat(backup)
	if info.Mode().Perm() != 0600 {
		t.Fatal("backup permission")
	}
}
func TestSystemdQuotesSpecialPaths(t *testing.T) {
	q := systemdQuote("/home/a b/%pilot/\"quoted\"/$HOME")
	if q != `"/home/a b/%%pilot/\"quoted\"/$HOME"` {
		t.Fatal(q)
	}
}

func TestGatewayRemainsAvailableAcrossDaemonRestart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	dir := filepath.Join(root, "data")
	if e := AtomicJSON(filepath.Join(dir, "tailscale.json"), map[string]any{}); e != nil {
		t.Fatal(e)
	}
	if e := writeUnits(Runtime{DataDir: dir, Path: "/usr/bin", Listen: "127.0.0.1:8787"}, "/home/test path/code-factory"); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(unitRoot(), "code-factory-gateway.service"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "Requires=code-factory.service") || !strings.Contains(string(b), "Wants=code-factory.service") || !strings.Contains(string(b), "After=code-factory.service") {
		t.Fatal("gateway would stop with daemon", string(b))
	}
}
