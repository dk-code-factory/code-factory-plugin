package dependencies

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveEntry struct {
	name, body string
	kind       byte
}

func archiveBytes(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(e.body))
		if kind != tar.TypeReg {
			size = 0
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0700, Typeflag: kind, Size: size, Linkname: "../outside"}); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			_, _ = tw.Write([]byte(e.body))
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDependencyArchivesRejectTraversalLinksDuplicatesAndBudget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []archiveEntry
		limit   int64
	}{
		{"traversal", []archiveEntry{{name: "../outside", body: "x"}}, 100},
		{"absolute", []archiveEntry{{name: "/outside", body: "x"}}, 100},
		{"symlink", []archiveEntry{{name: "link", kind: tar.TypeSymlink}}, 100},
		{"hardlink", []archiveEntry{{name: "link", kind: tar.TypeLink}}, 100},
		{"duplicate", []archiveEntry{{name: "a", body: "x"}, {name: "a", body: "y"}}, 100},
		{"budget", []archiveEntry{{name: "a", body: "longer"}}, 2},
		{"cumulative budget", []archiveEntry{{name: "a", body: "one"}, {name: "b", body: "two"}}, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Extract(bytes.NewReader(archiveBytes(t, tc.entries...)), t.TempDir(), tc.limit); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	root := t.TempDir()
	if err := Extract(bytes.NewReader(archiveBytes(t, archiveEntry{name: "bin/tool", body: "data"})), root, 4); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "bin/tool"))
	if string(b) != "data" {
		t.Fatal("valid archive lost")
	}
}

func TestDependencyArchiveRejectsCumulativeSizeOverflowBeforeWriting(t *testing.T) {
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "first", Mode: 0600, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "overflow", Mode: 0600, Size: 1<<63 - 1, Format: tar.FormatGNU}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("beyond the remaining budget")); err != nil {
		t.Fatal(err)
	}
	// Deliberately leave the huge tar entry incomplete. Its declared size must be
	// rejected before any of its finite, highly compressible payload is written.
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := Extract(bytes.NewReader(archive.Bytes()), root, 8); err == nil || !strings.Contains(err.Error(), "entry rejected") {
		t.Fatal("overflowing entry was not rejected by the budget", err)
	}
	if _, err := os.Stat(filepath.Join(root, "overflow")); !os.IsNotExist(err) {
		t.Fatal("over-budget entry was created", err)
	}
	if body, err := os.ReadFile(filepath.Join(root, "first")); err != nil || string(body) != "a" {
		t.Fatal("prior accepted entry was changed", string(body), err)
	}
}

func TestSkillFingerprintSurvivesPrivateCopyAndDetectsEdits(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "copy")
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("original"), 0755); err != nil {
		t.Fatal(err)
	}
	one, err := TreeDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = CopyTree(source, target); err != nil {
		t.Fatal(err)
	}
	two, err := TreeDigest(target)
	if err != nil || one != two {
		t.Fatal("private permissions changed fingerprint", err)
	}
	_ = os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("local edit"), 0700)
	three, _ := TreeDigest(target)
	if three == one {
		t.Fatal("edit not detected")
	}
	_ = os.Symlink(filepath.Join(source, "SKILL.md"), filepath.Join(target, "link"))
	if _, err = TreeDigest(target); err == nil {
		t.Fatal("linked skill accepted")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCodexDownloadVerifiesDigestAndRetainsCompanions(t *testing.T) {
	data := archiveBytes(t, archiveEntry{name: "bin/codex", body: "cli"}, archiveEntry{name: "bin/codex-code-mode-host", body: "companion"}, archiveEntry{name: "codex-resources/bwrap", body: "sandbox"})
	hash := sha256.Sum256(data)
	release := Release{Tag: "rust-v0.164.0", Assets: []Asset{{Name: "codex-package-x86_64-unknown-linux-musl.tar.gz", URL: "https://github.com/dkropachev/codex/releases/download/rust-v0.164.0/package.tar.gz", Digest: "sha256:" + hex.EncodeToString(hash[:])}}}
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body := data
		if r.URL.Host == "api.github.com" {
			if r.URL.Path != "/repos/dkropachev/codex/releases/latest" {
				t.Errorf("unexpected Codex source: %s", r.URL.Path)
			}
			body, _ = json.Marshal(release)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	observed, err := LatestCodex(context.Background(), client)
	if err != nil || observed.Tag != release.Tag {
		t.Fatal(observed, err)
	}
	dest := t.TempDir()
	binary, err := StageCodex(context.Background(), client, release, dest)
	if err != nil || binary != filepath.Join(dest, "bin/codex") {
		t.Fatal(binary, err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "bin/codex-code-mode-host")); err != nil || string(b) != "companion" {
		t.Fatal("companion lost", err)
	}
	release.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
	if _, err = StageCodex(context.Background(), client, release, t.TempDir()); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	release.Assets[0].Digest = "sha256:" + hex.EncodeToString(hash[:])
	release.Assets[0].URL = "https://github.com/openai/codex/releases/download/rust-v0.164.0/package.tar.gz"
	if _, err = StageCodex(context.Background(), client, release, t.TempDir()); err == nil {
		t.Fatal("unrequested upstream distribution accepted")
	}
}
