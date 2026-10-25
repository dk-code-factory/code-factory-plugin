package app

import (
	"codefactory/internal/domain"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func writeSkillFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestPluginSkillSnapshotKeepsPackageResourcesAndRetainedEntrypoint(t *testing.T) {
	source := t.TempDir()
	writeSkillFile(t, filepath.Join(source, ".codex-plugin/plugin.json"), `{"name":"package","skills":"./codex/skills/"}`, 0600)
	entry := filepath.Join(source, "codex/skills/review/SKILL.md")
	writeSkillFile(t, entry, "---\nname: review\n---\nRead ../../../common/lifecycle.md", 0600)
	helper := filepath.Join(source, "scripts/check.py")
	writeSkillFile(t, helper, "#!/usr/bin/python3\nprint('frozen')\n", 0700)
	writeSkillFile(t, filepath.Join(source, "common/lifecycle.md"), "Frozen lifecycle", 0600)
	writeSkillFile(t, filepath.Join(source, "runtime/review/procedure.md"), "Frozen procedure", 0600)
	writeSkillFile(t, filepath.Join(source, ".git/objects/ignored"), "not package content", 0600)
	skill, err := snapshotSkill(domain.Skill{Path: entry})
	if err != nil {
		t.Fatal(err)
	}
	if skill.EntryPath != "codex/skills/review/SKILL.md" || skill.Files["common/lifecycle.md"] == "" || skill.Files["runtime/review/procedure.md"] == "" || skill.Modes["scripts/check.py"]&0111 == 0 {
		t.Fatal("package resources/layout lost", skill.EntryPath)
	}
	if _, ok := skill.Files[".git/objects/ignored"]; ok {
		t.Fatal("Git object database copied")
	}
	writeSkillFile(t, helper, "replacement", 0600)
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	for attempt := 0; attempt < 2; attempt++ {
		path, err := materializeSkill(dest, skill)
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(filepath.Join(filepath.Dir(path), "../../../common/lifecycle.md"))
		if err != nil || string(content) != "Frozen lifecycle" {
			t.Fatal("relative package resource unavailable", err)
		}
		content, err = os.ReadFile(filepath.Join(dest, "scripts/check.py"))
		if err != nil || string(content) != "#!/usr/bin/python3\nprint('frozen')\n" {
			t.Fatal("retained runtime changed", err)
		}
		info, err := os.Stat(filepath.Join(dest, "scripts/check.py"))
		if err != nil || info.Mode().Perm()&0111 == 0 {
			t.Fatal("executable permission lost", err)
		}
	}
}

func TestSkillSnapshotRejectsEscapesLinksMissingEntryAndOversize(t *testing.T) {
	for _, kind := range []string{"manifest-escape", "outside-skills", "symlink", "missing-entry", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			entry := filepath.Join(source, "skills/review/SKILL.md")
			writeSkillFile(t, entry, "---\nname: review\n---\n", 0600)
			switch kind {
			case "manifest-escape":
				writeSkillFile(t, filepath.Join(source, ".codex-plugin/plugin.json"), `{"skills":"../outside"}`, 0600)
			case "outside-skills":
				writeSkillFile(t, filepath.Join(source, ".codex-plugin/plugin.json"), `{"skills":"other"}`, 0600)
			case "symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(filepath.Dir(entry), "link")); err != nil {
					t.Fatal(err)
				}
			case "missing-entry":
				if err := os.Remove(entry); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				writeSkillFile(t, filepath.Join(filepath.Dir(entry), "large"), string(make([]byte, skillBundleLimit+1)), 0600)
			}
			if _, err := snapshotSkill(domain.Skill{Path: entry}); err == nil {
				t.Fatal("invalid bundle accepted")
			}
		})
	}
}

func TestRetainedSkillRejectsTraversalAndPreservesStandaloneCompatibility(t *testing.T) {
	encode := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	for _, kind := range []string{"entry", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dest := t.TempDir()
			outside := t.TempDir()
			s := domain.Skill{Files: map[string]string{"SKILL.md": encode("original")}}
			switch kind {
			case "entry":
				s.EntryPath = "../outside"
			case "file":
				s.Files["../outside"] = encode("escape")
			case "symlink":
				if err := os.Symlink(outside, filepath.Join(dest, "link")); err != nil {
					t.Fatal(err)
				}
				s.Files["link/outside"] = encode("escape")
			}
			if _, err := materializeSkill(dest, s); err == nil {
				t.Fatal("retained escape accepted")
			}
			if _, err := os.Stat(filepath.Join(outside, "outside")); !os.IsNotExist(err) {
				t.Fatal("outside write", err)
			}
		})
	}
	source := t.TempDir()
	entry := filepath.Join(source, "SKILL.md")
	writeSkillFile(t, entry, "---\nname: standalone\n---\n", 0600)
	s, err := snapshotSkill(domain.Skill{Path: entry})
	if err != nil || s.EntryPath != "" {
		t.Fatal("standalone snapshot incompatible", err)
	}
	path, err := materializeSkill(t.TempDir(), s)
	if err != nil || filepath.Base(path) != "SKILL.md" {
		t.Fatal("legacy entrypoint unavailable", err)
	}
}
