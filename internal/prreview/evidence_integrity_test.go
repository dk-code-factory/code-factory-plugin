package prreview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCheckoutValidatesTrackedBytesAndModesDespiteIndexFlags(t *testing.T) {
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		for _, change := range []string{"unchanged", "bytes", "owner execute", "other execute", "missing", "symlink", "fifo"} {
			t.Run(flag+"/"+change, func(t *testing.T) {
				_, _, _, collector := workflowFixture(t)
				e := collector.e
				name := filepath.Join(e.Checkout, "a.go")
				gitTest(t, e.Checkout, "update-index", flag, "a.go")
				var err error
				switch change {
				case "bytes":
					err = os.WriteFile(name, []byte("altered source\n"), 0600)
				case "owner execute":
					err = os.Chmod(name, 0700)
				case "other execute":
					err = os.Chmod(name, 0611)
				case "missing", "symlink", "fifo":
					if err = os.Remove(name); err != nil {
						t.Fatal(err)
					}
					if change == "symlink" {
						err = os.Symlink("missing-target", name)
					} else if change == "fifo" {
						err = syscall.Mkfifo(name, 0600)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if status := gitTest(t, e.Checkout, "status", "--porcelain=v1", "--untracked-files=all"); status != "" {
					t.Fatalf("fixture did not hide tracked change: %q", status)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- e.ValidateCheckout(ctx) }()
				select {
				case err = <-done:
				case <-ctx.Done():
					t.Fatal("checkout validation blocked on unsupported file")
				}
				valid := change == "unchanged" || change == "other execute"
				if (err == nil) != valid {
					t.Fatalf("checkout change %q: %v", change, err)
				}
			})
		}
	}
}

func TestHiddenCheckoutEditsStopReviewersAndCheckpointReuse(t *testing.T) {
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		for _, boundary := range []string{"next reviewer", "checkpoint reuse"} {
			t.Run(flag+"/"+boundary, func(t *testing.T) {
				w, s, workers, collector := workflowFixture(t)
				mutate := func() {
					gitTest(t, collector.e.Checkout, "update-index", flag, "a.go")
					if err := os.WriteFile(filepath.Join(collector.e.Checkout, "a.go"), []byte("hidden edit\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if boundary == "next reviewer" {
					workers.hook = func(string) { mutate() }
					result, err := w.Run(context.Background(), s)
					if err == nil || !result.GuardFailure || workers.calls["two"] != 0 {
						t.Fatalf("hidden edit allowed another reviewer: %+v, %v", result, err)
					}
					return
				}
				workers.fail["two"] = errors.New("offline")
				if _, err := w.Run(context.Background(), s); err != nil {
					t.Fatal(err)
				}
				if _, exists := s.Reviews["one"]; !exists {
					t.Fatal("fixture has no checkpoint to reuse")
				}
				mutate()
				calls := workers.calls["one"] + workers.calls["two"]
				result, err := w.Run(context.Background(), s)
				if err == nil || len(result.Reused) != 0 || workers.calls["one"]+workers.calls["two"] != calls {
					t.Fatalf("hidden edit allowed retained review reuse: %+v, %v", result, err)
				}
			})
		}
	}
}

func TestCheckoutValidationHonorsCancellation(t *testing.T) {
	_, _, _, collector := workflowFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := collector.e.ValidateCheckout(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("checkout validation lost cancellation: %v", err)
	}
}

func TestCheckoutValidatesCommittedSymlinkBytesWithoutFollowing(t *testing.T) {
	for _, change := range []string{"unchanged", "target bytes", "regular file"} {
		t.Run(change, func(t *testing.T) {
			_, _, _, collector := workflowFixture(t)
			name := filepath.Join(collector.e.Checkout, "link")
			if err := os.Symlink("missing-target", name); err != nil {
				t.Fatal(err)
			}
			gitTest(t, collector.e.Checkout, "add", "link")
			gitTest(t, collector.e.Checkout, "-c", "user.name=Fixture", "-c", "user.email=test@example.invalid", "commit", "-m", "link")
			collector.e.Metadata.Head = gitTest(t, collector.e.Checkout, "rev-parse", "HEAD")
			collector.e.Metadata.Tree = gitTest(t, collector.e.Checkout, "rev-parse", "HEAD^{tree}")
			gitTest(t, collector.e.Checkout, "update-index", "--assume-unchanged", "link")
			if change != "unchanged" {
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				var err error
				if change == "target bytes" {
					err = os.Symlink("other-target", name)
				} else {
					err = os.WriteFile(name, []byte("missing-target"), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := collector.e.ValidateCheckout(context.Background()); (err == nil) != (change == "unchanged") {
				t.Fatalf("committed symlink change %q: %v", change, err)
			}
		})
	}
}

func TestCheckoutBlobObjectFormatsAndExecutableFiles(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			gitTest(t, dir, "init", "-b", "main", "--object-format="+format)
			name := filepath.Join(dir, "script")
			if err := os.WriteFile(name, []byte("#!/bin/sh\n\x00\xff\n"), 0700); err != nil {
				t.Fatal(err)
			}
			gitTest(t, dir, "add", "script")
			gitTest(t, dir, "-c", "user.name=Fixture", "-c", "user.email=test@example.invalid", "commit", "-m", "binary executable")
			e := Evidence{Checkout: dir, Metadata: Metadata{Head: gitTest(t, dir, "rev-parse", "HEAD"), Tree: gitTest(t, dir, "rev-parse", "HEAD^{tree}")}}
			if err := e.ValidateCheckout(context.Background()); err != nil {
				t.Fatalf("unchanged executable blob in %s repository: %v", format, err)
			}
			gitTest(t, dir, "update-index", "--skip-worktree", "script")
			if err := os.Chmod(name, 0600); err != nil {
				t.Fatal(err)
			}
			if err := e.ValidateCheckout(context.Background()); err == nil {
				t.Fatal("hidden removal of committed owner execute mode accepted")
			}
		})
	}
}

func TestCheckoutPreservesGitlinkDirectoryBoundary(t *testing.T) {
	_, _, _, collector := workflowFixture(t)
	e := collector.e
	gitTest(t, e.Checkout, "clone", "--quiet", "--no-hardlinks", "--", e.Checkout, "module")
	gitTest(t, e.Checkout, "add", "module")
	gitTest(t, e.Checkout, "-c", "user.name=Fixture", "-c", "user.email=test@example.invalid", "commit", "-m", "gitlink")
	e.Metadata.Head = gitTest(t, e.Checkout, "rev-parse", "HEAD")
	e.Metadata.Tree = gitTest(t, e.Checkout, "rev-parse", "HEAD^{tree}")
	if err := e.ValidateCheckout(context.Background()); err != nil {
		t.Fatalf("clean initialized gitlink rejected: %v", err)
	}
	name := filepath.Join(e.Checkout, "module")
	if err := os.RemoveAll(name); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(name, 0700); err != nil {
		t.Fatal(err)
	}
	if err := e.ValidateCheckout(context.Background()); err != nil {
		t.Fatalf("clean uninitialized gitlink rejected: %v", err)
	}
}
