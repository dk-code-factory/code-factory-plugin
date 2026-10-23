package app

import (
	"os"
	"path/filepath"
	"testing"

	"codefactory/internal/domain"
)

func TestRunScratchCleanupSealedDirectoriesPreservesExternalReferences(t *testing.T) {
	data := t.TempDir()
	outside := filepath.Join(t.TempDir(), "owner")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(outside, "keep")
	if err := os.WriteFile(file, []byte("owner data"), 0400); err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(data, "scratch", "run")
	sealed := filepath.Join(run, "runtime", "tests")
	if err := os.MkdirAll(sealed, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sealed, "fixture.py"), []byte("sealed"), 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(run, "owner-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(run, "shared-file")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{sealed, filepath.Dir(sealed), run} {
		if err := os.Chmod(path, 0500); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeRunScratch(data, "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(run); !os.IsNotExist(err) {
		t.Fatal("scratch retained", err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "owner data" {
		t.Fatal("owner file changed", err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0400 {
		t.Fatal("shared file permissions changed", err)
	}
	if info, err := os.Stat(outside); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("owner directory permissions changed", err)
	}
	if err := removeRunScratch(data, "run"); err != nil {
		t.Fatal("retry failed", err)
	}
	if err := removeRunScratch(data, "../owner"); err == nil {
		t.Fatal("accepted escaping ID")
	}
}

func TestCleanupRetriesScratchFailureAfterWorktreeWasRemoved(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "hold")
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "running" && r.TurnID != "" })
	if err := a.Runs.Interrupt(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "interrupted" })
	scratch := filepath.Join(a.DataDir, "scratch", r.ID)
	if err := os.RemoveAll(scratch); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scratch, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Cleanup(ctx, r.ID); err == nil {
		t.Fatal("scratch failure concealed")
	}
	if _, err := os.Stat(r.Worktree); !os.IsNotExist(err) {
		t.Fatal("worktree removal did not precede injected failure", err)
	}
	if err := os.Remove(scratch); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(scratch, 0500); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.Cleanup(ctx, r.ID); err != nil {
		t.Fatal("partial cleanup could not resume", err)
	}
	var saved domain.Run
	if err := a.Store.Get(ctx, "run", r.ID, &saved); err != nil || saved.Status != "cleaned" || saved.ThreadID != r.ThreadID {
		t.Fatal("history or cleanup state lost", err)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatal("scratch retained", err)
	}
}
