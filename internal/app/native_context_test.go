package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeContextReferencesOwnerWithoutCopyingAuthentication(t *testing.T) {
	owner, scratch := t.TempDir(), t.TempDir()
	for _, name := range []string{"config.toml", "auth.json", "models_cache.json"} {
		if err := os.WriteFile(filepath.Join(owner, name), []byte("owner-fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	directory, writable, err := prepareNativeContext(scratch, owner)
	if err != nil || len(writable) != 5 {
		t.Fatal("native context preparation failed", err)
	}
	for _, name := range []string{"config.toml", "auth.json", "models_cache.json"} {
		info, err := os.Lstat(filepath.Join(directory, name))
		link, linkErr := os.Readlink(filepath.Join(directory, name))
		if err != nil || linkErr != nil || info.Mode()&os.ModeSymlink == 0 || link != filepath.Join(owner, name) {
			t.Fatal("owner configuration or authentication was copied/substituted", name)
		}
	}
	session := filepath.Join(directory, "sessions", "retained")
	if err := os.WriteFile(session, []byte("history"), 0600); err != nil {
		t.Fatal(err)
	}
	again, _, err := prepareNativeContext(scratch, owner)
	data, readErr := os.ReadFile(session)
	if err != nil || readErr != nil || again != directory || string(data) != "history" {
		t.Fatal("native context restoration lost history", err, readErr)
	}
	if err := os.Remove(filepath.Join(directory, "auth.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "auth.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareNativeContext(scratch, owner); err == nil {
		t.Fatal("changed authentication reference accepted")
	}
	data, err = os.ReadFile(filepath.Join(owner, "auth.json"))
	if err != nil || string(data) != "owner-fixture" {
		t.Fatal("owner authentication changed")
	}
}

func TestNativeContextRejectsEscapingStateAndSharedWritableFile(t *testing.T) {
	owner, scratch := t.TempDir(), t.TempDir()
	if err := os.Symlink(owner, filepath.Join(scratch, "state")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareNativeContext(scratch, owner); err == nil {
		t.Fatal("native context followed state link outside private scratch")
	}
	entries, err := os.ReadDir(owner)
	if err != nil || len(entries) != 0 {
		t.Fatal("native context wrote outside private scratch", err)
	}
	scratch = t.TempDir()
	directory, _, err := prepareNativeContext(scratch, owner)
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(owner, "preserve")
	if err := os.WriteFile(shared, []byte("owner-data"), 0600); err != nil {
		t.Fatal(err)
	}
	stateFile, err := os.Readlink(filepath.Join(directory, "installation_id"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(shared, stateFile); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareNativeContext(scratch, owner); err == nil {
		t.Fatal("shared writable native state could modify an owner file")
	}
	data, err := os.ReadFile(shared)
	if err != nil || string(data) != "owner-data" {
		t.Fatal("shared owner file changed", err)
	}
}
