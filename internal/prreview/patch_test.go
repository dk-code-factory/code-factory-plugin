package prreview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPatchInventoryAcceptsGitPathsContainingHeaderDelimiter(t *testing.T) {
	const before = "one\ntwo\nthree\nfour\n"
	const after = "one\nchanged\nthree\nfour\n"
	for _, test := range []struct {
		name, oldPath, newPath, oldText, newText string
		executable                               bool
		addedLine                                int
	}{
		{name: "modified", oldPath: "dir b/file.go", newPath: "dir b/file.go", oldText: before, newText: after, addedLine: 2},
		{name: "added", newPath: "dir b/file.go", newText: before, addedLine: 1},
		{name: "deleted", oldPath: "dir b/file.go", oldText: before},
		{name: "mode only", oldPath: "dir b/file.go", newPath: "dir b/file.go", oldText: before, newText: before, executable: true},
		{name: "binary", oldPath: "dir b/file.bin", newPath: "dir b/file.bin", oldText: "\x00old", newText: "\x00new"},
		{name: "rename only", oldPath: "old b/file.go", newPath: "new b/file.go", oldText: before, newText: before},
		{name: "binary rename", oldPath: "old b/file.bin", newPath: "new b/file.bin", oldText: "\x00binary", newText: "\x00binary"},
		{name: "mode and rename", oldPath: "old b/file.go", newPath: "new b/file.go", oldText: before, newText: before, executable: true},
		{name: "rename and edit", oldPath: "old b/file.go", newPath: "new b/file.go", oldText: before, newText: after, addedLine: 2},
		{name: "quoted", oldPath: "dir \"quoted b/file.go", newPath: "dir \"quoted b/file.go", oldText: before, newText: after, addedLine: 2},
		{name: "quoted source rename", oldPath: "old \"quoted b/file.go", newPath: "new b/file.go", oldText: before, newText: before},
		{name: "quoted destination rename", oldPath: "old b/file.go", newPath: "new \"quoted b/file.go", oldText: before, newText: before},
		{name: "quoted rename", oldPath: "old \"quoted b/file.go", newPath: "new \"quoted b/file.go", oldText: before, newText: before},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			gitTest(t, dir, "init", "-b", "main")
			write := func(name, text string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if test.oldPath != "" {
				write(test.oldPath, test.oldText)
			}
			gitTest(t, dir, "add", ".")
			gitTest(t, dir, "-c", "user.name=Fixture", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "base")
			base := gitTest(t, dir, "rev-parse", "HEAD")
			if test.oldPath != "" && test.oldPath != test.newPath {
				if err := os.Remove(filepath.Join(dir, test.oldPath)); err != nil {
					t.Fatal(err)
				}
			}
			if test.newPath != "" {
				write(test.newPath, test.newText)
				if test.executable {
					if err := os.Chmod(filepath.Join(dir, test.newPath), 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			gitTest(t, dir, "add", ".")
			gitTest(t, dir, "-c", "user.name=Fixture", "-c", "user.email=test@example.invalid", "commit", "-m", "head")
			patch := gitTest(t, dir, "-c", "core.quotePath=false", "diff", "--binary", "--full-index", "--find-renames", base+"...HEAD")
			parsed, err := ParsePatch(patch)
			if err != nil {
				t.Fatalf("parse complete Git patch: %v\n%s", err, patch)
			}
			name := test.newPath
			if name == "" {
				name = test.oldPath
			}
			if !reflect.DeepEqual(parsed.Files, map[string]bool{name: true}) {
				t.Fatalf("inventory = %v, want only %q\n%s", parsed.Files, name, patch)
			}
			if test.addedLine != 0 && !parsed.Added[Location{name, test.addedLine, "RIGHT"}] {
				t.Fatalf("missing added line for complete destination path: %v", parsed.Added)
			}
			for location := range parsed.Lines {
				if location.Path != name {
					t.Fatalf("line attributed to %q instead of %q", location.Path, name)
				}
			}
			e := Evidence{
				Metadata: Metadata{URL: "https://github.com/o/r/pull/2", NodeID: "PR_test", Base: base, Head: gitTest(t, dir, "rev-parse", "HEAD"), Tree: gitTest(t, dir, "rev-parse", "HEAD^{tree}"), Author: "owner", ChangedFiles: 1},
				Files:    []File{{Path: name, Status: "modified"}},
				Patch:    patch, Checkout: dir, Repository: dir,
				Scope: map[string]json.RawMessage{}, Sources: map[string]string{"patch": patch},
			}
			e.Decisions, err = PublishedDecisions(e.Metadata)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.Validate(); err != nil {
				t.Fatalf("complete real Git evidence rejected: %v", err)
			}
		})
	}
}

func TestPatchRejectsUnsafeAndConflictingRenameSources(t *testing.T) {
	for _, metadata := range []string{
		"rename from ../escape\nrename to new.go\n",
		"rename from /absolute\nrename to new.go\n",
		"rename from dir\\old.go\nrename to new.go\n",
		"rename from \"../escape\"\nrename to new.go\n",
		"rename from \"old\\n.go\"\nrename to new.go\n",
		"rename from \"unterminated\nrename to new.go\n",
		"rename from other.go\nrename to new.go\n",
		"rename from other.go\nrename to new.go\n--- a/old.go\n+++ b/new.go\n",
		"rename from old.go\nrename to new.go\n--- /dev/null\n+++ b/new.go\n",
		"rename from old.go\nrename from other.go\nrename to new.go\n",
	} {
		t.Run(strings.Split(metadata, "\n")[0], func(t *testing.T) {
			patch := "diff --git a/old.go b/new.go\nsimilarity index 100%\n" + metadata
			if _, err := ParsePatch(patch); err == nil {
				t.Fatalf("accepted unsafe or conflicting rename source: %q", patch)
			}
		})
	}
}

func TestPatchRejectsConflictingRepeatedRenameDestinations(t *testing.T) {
	const header = "diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\n"
	for _, destinations := range []string{
		"rename to other.go\nrename to new.go\n",
		"rename to new.go\nrename to other.go\nrename to new.go\n",
	} {
		if _, err := ParsePatch(header + destinations); err == nil {
			t.Fatalf("accepted conflicting repeated rename destinations: %q", destinations)
		}
	}
	parsed, err := ParsePatch(header + "rename from old.go\nrename to new.go\nrename to new.go\n")
	if err != nil || !reflect.DeepEqual(parsed.Files, map[string]bool{"new.go": true}) {
		t.Fatalf("identical repeated rename metadata rejected: %+v, %v", parsed, err)
	}
}

func TestPatchRejectsUnsafeMalformedAndAmbiguousHeaders(t *testing.T) {
	for _, patch := range []string{
		"diff --git a/file.go b/../escape\nold mode 100644\nnew mode 100755\n",
		"diff --git a/../escape b/file.go\nold mode 100644\nnew mode 100755\n",
		"diff --git a/file.go b//absolute\nold mode 100644\nnew mode 100755\n",
		"diff --git a/file.go b/dir\\file.go\nold mode 100644\nnew mode 100755\n",
		"diff --git a/file.go b/file\x00.go\n",
		"diff --git \"a/unterminated b/file.go\n",
		"diff --git a/file.go \"b/unterminated\n",
		"diff --git a/file.go\n",
		"diff --git a/old b/new b/file.go\nold mode 100644\nnew mode 100755\n",
		"diff --git a/file.go b/file.go\n--- a/file.go\n+++ b/other.go\n",
		"diff --git a/file.go b/file.go\n--- a/other.go\n+++ b/file.go\n",
		"diff --git a/old.go b/new.go\nrename to ../escape\n",
		"diff --git a/old.go b/new.go\nrename to other.go\n--- a/old.go\n+++ b/new.go\n",
		"diff --git a/file.go b/file.go\nBinary files a/file.go and b/file.go differ\n",
		"diff --git a/file.go b/file.go\n--- a/file.go\n+++ b/file.go\n@@ -1 +1,2 @@\n-old\n+new\n",
	} {
		t.Run(strings.Split(patch, "\n")[0], func(t *testing.T) {
			if _, err := ParsePatch(patch); err == nil {
				t.Fatalf("accepted malformed or unsafe patch: %q", patch)
			}
		})
	}
}
