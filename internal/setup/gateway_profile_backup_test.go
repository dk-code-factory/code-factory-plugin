package setup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type profileBackupFixture struct {
	runtime                Runtime
	root, source, next     string
	oldProfile, newProfile []byte
}

func newProfileBackupFixture(t *testing.T) profileBackupFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	data := filepath.Join(home, "data")
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("CF_PROFILE_DATA", data)
	t.Setenv("CF_PROFILE_CALLS", filepath.Join(home, "systemctl.calls"))
	t.Setenv("CF_PROFILE_STARTS", filepath.Join(home, "started-profiles"))
	t.Setenv("CF_PROFILE_FAIL_UPGRADE", "")
	t.Setenv("CF_PROFILE_NEW", filepath.Join(home, "new-profile.json"))
	paths := map[string]string{}
	for _, name := range []string{"git", "gh", "codex", "systemctl"} {
		body := "#!/bin/sh\nexit 0\n"
		if name == "codex" {
			body = "#!/bin/sh\nprintf 'codex-cli 0.161.0\\n'\n"
		}
		if name == "systemctl" {
			body = `#!/bin/sh
printf '%s\n' "$*" >> "$CF_PROFILE_CALLS"
if [ "$2" = restart ] && [ "$3" = code-factory.service ] && [ "$CF_PROFILE_FAIL_UPGRADE" = 1 ]; then
  /usr/bin/cp "$CF_PROFILE_NEW" "$CF_PROFILE_DATA/tailscale.json"
  exit 1
fi
if [ "$2" = start ]; then
  /usr/bin/cat "$CF_PROFILE_DATA/tailscale.json" >> "$CF_PROFILE_STARTS"
fi
exit 0
`
		}
		paths[name] = filepath.Join(bin, name)
		if err := os.WriteFile(paths[name], []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 64) {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"version":"old"}`))
	}))
	t.Cleanup(server.Close)
	f := profileBackupFixture{runtime: Runtime{Paths: paths, Path: bin, DataDir: data, Listen: strings.TrimPrefix(server.URL, "http://")}, root: installRoot(), source: filepath.Join(home, "new-release"), next: "new",
		oldProfile: []byte(`{"publicUrl":"https://pilot.ts.net:10000"}`), newProfile: []byte(`{"publicUrl":"https://pilot.ts.net/code-factory"}`)}
	for name, b := range map[string][]byte{"tailscale.json": f.oldProfile, "owner.token": []byte(strings.Repeat("t", 64)), "gateway-auth.json": []byte(`{"opaque":"retained-oauth-grants"}`), "vault.key": []byte("retained-vault-key")} {
		if err := atomicFile(filepath.Join(data, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := atomicFile(os.Getenv("CF_PROFILE_NEW"), f.newProfile, 0600); err != nil {
		t.Fatal(err)
	}
	for path, version := range map[string]string{filepath.Join(f.root, "old"): "old", f.source: f.next} {
		writeProfileTestRelease(t, path, version)
	}
	if err := switchLink(filepath.Join(f.root, "current"), "old"); err != nil {
		t.Fatal(err)
	}
	if err := AtomicJSON(filepath.Join(data, "runtime.json"), f.runtime); err != nil {
		t.Fatal(err)
	}
	if err := AtomicJSON(filepath.Join(f.root, "installation.json"), Installation{Current: "old", Runtime: f.runtime}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(data, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE profile_fixture(value TEXT); INSERT INTO profile_fixture VALUES('old')")
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	return f
}

func writeProfileTestRelease(t *testing.T, dir, version string) {
	t.Helper()
	files := map[string]string{"release.json": `{"Version":"` + version + `","OS":"linux","Arch":"amd64"}`, "code-factory": "#!/bin/sh\nexit 0\n", "plugin/plugin.json": `{"name":"code-factory","extensions":{"com.openai":{}}}`, "plugin/skills/code-factory-setup/SKILL.md": "fixture"}
	var sums strings.Builder
	for name, body := range files {
		if err := atomicFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256([]byte(body)), name)
	}
	if err := atomicFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0600); err != nil {
		t.Fatal(err)
	}
}

func (f profileBackupFixture) assertPreserved(t *testing.T, profile []byte) {
	t.Helper()
	for name, want := range map[string]string{"tailscale.json": string(profile), "owner.token": strings.Repeat("t", 64), "gateway-auth.json": `{"opaque":"retained-oauth-grants"}`, "vault.key": "retained-vault-key"} {
		b, err := os.ReadFile(filepath.Join(f.runtime.DataDir, name))
		if err != nil || string(b) != want {
			t.Fatalf("%s was not preserved correctly: %v", name, err)
		}
	}
	starts, err := os.ReadFile(os.Getenv("CF_PROFILE_STARTS"))
	if err != nil || len(starts) == 0 || strings.ReplaceAll(string(starts), string(profile), "") != "" {
		t.Fatal("compatible gateway profile was not present before service startup", err)
	}
}

func TestRollbackRestoresGatewayProfileAndArchivesCurrent(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy-record-%t", legacy), func(t *testing.T) {
			f := newProfileBackupFixture(t)
			backup := filepath.Join(f.root, "backups", "before-upgrade.db")
			if err := BackupDatabase(context.Background(), filepath.Join(f.runtime.DataDir, "state.db"), backup); err != nil {
				t.Fatal(err)
			}
			profile, err := backupGatewayProfile(f.runtime.DataDir, backup)
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				profile = ""
			}
			if err := atomicFile(filepath.Join(f.runtime.DataDir, "tailscale.json"), f.newProfile, 0600); err != nil {
				t.Fatal(err)
			}
			if err := AtomicJSON(filepath.Join(f.root, "installation.json"), Installation{Current: f.next, Previous: "old", Backup: backup, GatewayProfileBackup: profile, Runtime: f.runtime}); err != nil {
				t.Fatal(err)
			}
			if err := Rollback(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := f.oldProfile
			if legacy {
				want = f.newProfile
			}
			f.assertPreserved(t, want)
			archives, _ := filepath.Glob(filepath.Join(f.root, "backups", "pre-rollback-*.db.tailscale.json"))
			if len(archives) != 1 {
				t.Fatal("current transport profile was not archived with the current database")
			}
			b, err := os.ReadFile(archives[0])
			info, statErr := os.Stat(archives[0])
			if err != nil || statErr != nil || string(b) != string(f.newProfile) || info.Mode().Perm() != 0600 {
				t.Fatal("current profile archive lost content or private permissions")
			}
			installed, err := ReadInstallation()
			if err != nil || installed.Current != "old" || installed.GatewayProfileBackup != "" {
				t.Fatal("rollback did not clear consumed profile snapshot reference", err)
			}
		})
	}
}

func TestFailedUpgradeRestoresGatewayProfileBeforeRecoveryStarts(t *testing.T) {
	f := newProfileBackupFixture(t)
	t.Setenv("CF_PROFILE_FAIL_UPGRADE", "1")
	if err := InstallRelease(context.Background(), f.runtime.DataDir, f.source, f.runtime.Listen); err == nil || strings.Contains(err.Error(), "automatic recovery failed") {
		t.Fatal("expected upgrade failure with successful automatic recovery", err)
	}
	f.assertPreserved(t, f.oldProfile)
	var pending Installation
	b, err := os.ReadFile(filepath.Join(f.root, "pending.json"))
	if err != nil || json.Unmarshal(b, &pending) != nil || pending.GatewayProfileBackup == "" {
		t.Fatal("pending recovery omitted compatible gateway snapshot", err)
	}
	if profile, err := os.ReadFile(pending.GatewayProfileBackup); err != nil || string(profile) != string(f.oldProfile) {
		t.Fatal("pending snapshot did not retain pre-upgrade gateway configuration", err)
	}
}

func TestRollbackMissingRecordedGatewayProfileFailsBeforeStoppingServices(t *testing.T) {
	f := newProfileBackupFixture(t)
	if err := AtomicJSON(filepath.Join(f.root, "installation.json"), Installation{Current: f.next, Previous: "old", GatewayProfileBackup: filepath.Join(f.root, "missing-profile.json"), Runtime: f.runtime}); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(context.Background()); err == nil || !strings.Contains(err.Error(), "recorded gateway profile snapshot unavailable") {
		t.Fatal("missing recorded profile was not reported truthfully", err)
	}
	if _, err := os.Stat(os.Getenv("CF_PROFILE_CALLS")); !os.IsNotExist(err) {
		t.Fatal("rollback touched services before checking its compatibility snapshot")
	}
}
