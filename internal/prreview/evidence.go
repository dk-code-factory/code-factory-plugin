package prreview

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

type Metadata struct {
	URL          string `json:"url"`
	NodeID       string `json:"node_id"`
	Base         string `json:"base_sha"`
	Head         string `json:"head_sha"`
	Tree         string `json:"tree"`
	Body         string `json:"body"`
	Author       string `json:"author"`
	ChangedFiles int    `json:"changed_files"`
}
type File struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"`
}
type Decisions struct {
	SchemaVersion int          `json:"schema_version"`
	Command       string       `json:"command"`
	Status        string       `json:"status"`
	Error         any          `json:"error"`
	Data          DecisionData `json:"data"`
}
type LocalDecision struct {
	Text   string `json:"text"`
	Source string `json:"source"`
}
type DiscussionDecision struct {
	Text       string   `json:"text"`
	References []string `json:"references"`
}
type DecisionData struct {
	Verified            bool                 `json:"verified"`
	PRURL               string               `json:"pr_url"`
	Head                string               `json:"head_sha"`
	BodyHash            string               `json:"body_hash"`
	LiveDecisions       string               `json:"live_decisions"`
	DiscussionDecisions []DiscussionDecision `json:"discussion_decisions"`
	LocalDecisions      []LocalDecision      `json:"local_decisions"`
	Decisions           []json.RawMessage    `json:"decisions"`
}
type Evidence struct {
	Metadata    Metadata                   `json:"metadata"`
	Files       []File                     `json:"files"`
	Patch       string                     `json:"patch"`
	Checkout    string                     `json:"checkout"`
	Repository  string                     `json:"repository"`
	Scope       map[string]json.RawMessage `json:"scope"`
	Decisions   Decisions                  `json:"decisions"`
	Rules       string                     `json:"rules"`
	Constraints string                     `json:"constraints"`
	Sources     map[string]string          `json:"sources"`
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

func (e Evidence) Validate() error {
	m := e.Metadata
	if !shaPattern.MatchString(m.Head) || !shaPattern.MatchString(m.Base) || !shaPattern.MatchString(m.Tree) || m.NodeID == "" || m.Author == "" {
		return errors.New("incomplete remote review identity")
	}
	if !strings.HasPrefix(m.URL, "https://github.com/") || !strings.Contains(m.URL, "/pull/") {
		return errors.New("invalid PR target")
	}
	if len(e.Files) == 0 || m.ChangedFiles != len(e.Files) {
		return errors.New("incomplete changed-file inventory")
	}
	patch, err := ParsePatch(e.Patch)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, f := range e.Files {
		if !SafePath(f.Path) || seen[f.Path] || f.Additions < 0 || f.Deletions < 0 || f.Status == "" {
			return errors.New("invalid changed-file inventory")
		}
		seen[f.Path] = true
		if !patch.Files[f.Path] {
			return fmt.Errorf("normalized diff omits %s", f.Path)
		}
	}
	for p := range patch.Files {
		if !seen[p] {
			return errors.New("diff and changed-file inventory disagree")
		}
	}
	d := e.Decisions
	if d.SchemaVersion != 2 || d.Command != "context" || d.Error != nil || d.Status != "ok" || !d.Data.Verified || d.Data.PRURL != m.URL || d.Data.Head != m.Head || d.Data.BodyHash != Hash([]byte(m.Body)) || d.Data.Decisions == nil {
		return errors.New("missing, unverified or stale Decisions context")
	}
	if err := validateDecisionContext(m, d); err != nil {
		return err
	}
	if e.Scope == nil || e.Sources == nil || e.Checkout == "" || e.Repository == "" {
		return errors.New("missing engineering evidence")
	}
	return nil
}
func git(ctx context.Context, cwd string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = cwd
	b, err := c.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("review checkout git %s failed", strings.Join(args, " "))
	}
	return strings.TrimSpace(string(b)), nil
}
func (e Evidence) ValidateCheckout(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	head, err := git(ctx, e.Checkout, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	tree, err := git(ctx, e.Checkout, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	status, err := git(ctx, e.Checkout, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if head != e.Metadata.Head || tree != e.Metadata.Tree || status != "" {
		return errors.New("prepared isolated checkout changed")
	}
	// Status respects index flags and stat caches. Read the committed inventory
	// and actual files so hidden edits cannot retain the original review identity.
	entries, err := gitEntries(ctx, e)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	for name, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateCheckoutEntry(ctx, filepath.Join(e.Checkout, name), entry); err != nil {
			return fmt.Errorf("prepared isolated checkout changed at %s: %w", name, err)
		}
	}
	return nil
}

func validateCheckoutEntry(ctx context.Context, name string, entry []string) error {
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	// Preserve the existing nonrecursive submodule boundary: Git status checks
	// initialized submodules, while an uninitialized gitlink remains a directory.
	if entry[0] == "160000" && entry[1] == "commit" && info.IsDir() {
		return nil
	}
	if entry[1] != "blob" {
		return errors.New("unsupported committed entry")
	}
	var source io.Reader
	size := info.Size()
	switch entry[0] {
	case "120000":
		if info.Mode()&os.ModeSymlink == 0 {
			return errors.New("tracked symlink type differs")
		}
		target, err := os.Readlink(name)
		if err != nil {
			return err
		}
		source, size = strings.NewReader(target), int64(len(target))
	case "100644", "100755":
		if !info.Mode().IsRegular() || (info.Mode()&0100 != 0) != (entry[0] == "100755") {
			return errors.New("tracked file type or owner execute mode differs")
		}
		// A concurrent replacement must not follow a link or block on a FIFO.
		f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(info, opened) || !opened.Mode().IsRegular() || (opened.Mode()&0100 != 0) != (entry[0] == "100755") {
			return errors.New("tracked file changed while opening")
		}
		source, size = f, opened.Size()
	default:
		return errors.New("unsupported committed file mode")
	}
	// Git hashes the blob header and unfiltered bytes. Stream the object identity
	// rather than spawning Git or retaining a second full copy of every file.
	var digest hash.Hash = sha1.New()
	if len(entry[2]) == 64 {
		digest = sha256.New()
	}
	fmt.Fprintf(digest, "blob %d\x00", size)
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := source.Read(buffer)
		digest.Write(buffer[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if hex.EncodeToString(digest.Sum(nil)) != entry[2] {
		return errors.New("tracked file bytes differ from committed blob")
	}
	return ctx.Err()
}

// Collector separates deterministic remote collection from model preparation.
// Capture and Current must use complete fresh reads, never a cached partial diff.
type Collector interface {
	Capture(context.Context, string, string) (Evidence, error)
	ValidateCurrent(context.Context, Evidence) error
}

// ValidateStorage keeps private review material outside both source trees.
func (e Evidence) ValidateStorage(root string) error {
	for _, tree := range []string{e.Repository, e.Checkout} {
		if !filepath.IsAbs(tree) {
			continue
		}
		clean, err := filepath.EvalSymlinks(tree)
		if err != nil {
			clean = filepath.Clean(tree)
		}
		relative, err := filepath.Rel(clean, root)
		if err != nil {
			return err
		}
		if relative == "." || filepath.IsLocal(relative) {
			return errors.New("private review artifacts must stay outside the repository and isolated checkout")
		}
	}
	return nil
}
