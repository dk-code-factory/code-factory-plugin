package prreview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Reference struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type Artifacts struct{ Root string }

func (a Artifacts) check() error {
	if !filepath.IsAbs(a.Root) {
		return errors.New("review artifact root must be absolute")
	}
	if err := os.MkdirAll(a.Root, 0700); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(a.Root)
	if err != nil {
		return err
	}
	if resolved != filepath.Clean(a.Root) {
		return errors.New("artifact storage cannot traverse symlinks")
	}
	return os.Chmod(a.Root, 0700)
}
func (a Artifacts) Retain(value any) (Reference, error) {
	if err := a.check(); err != nil {
		return Reference{}, err
	}
	b, err := CanonicalJSON(value)
	if err != nil {
		return Reference{}, err
	}
	return a.RetainBytes(b)
}
func (a Artifacts) RetainBytes(b []byte) (Reference, error) {
	if err := a.check(); err != nil {
		return Reference{}, err
	}
	ref := Reference{filepath.Join(a.Root, Hash(b)+".json"), Hash(b)}
	if old, err := os.ReadFile(ref.Path); err == nil {
		if !bytes.Equal(old, b) {
			return ref, errors.New("immutable review artifact changed")
		}
		return ref, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ref, err
	}
	f, err := os.CreateTemp(a.Root, ".retain-")
	if err != nil {
		return ref, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return ref, err
	}
	if closeErr != nil {
		return ref, closeErr
	}
	// Link installs complete bytes atomically without overwriting an existing artifact.
	if err = os.Link(f.Name(), ref.Path); err != nil {
		if old, e := os.ReadFile(ref.Path); e != nil || !bytes.Equal(old, b) {
			return ref, err
		}
	}
	dir, err := os.Open(a.Root)
	if err != nil {
		return ref, err
	}
	defer dir.Close()
	return ref, dir.Sync()
}
func (a Artifacts) Load(ref Reference, out any) error {
	if err := a.check(); err != nil {
		return err
	}
	if len(ref.SHA256) != 64 || filepath.Dir(ref.Path) != a.Root || filepath.Base(ref.Path) != ref.SHA256+".json" {
		return errors.New("invalid artifact reference")
	}
	info, err := os.Lstat(ref.Path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("artifact is not a regular file")
	}
	b, err := os.ReadFile(ref.Path)
	if err != nil {
		return err
	}
	if Hash(b) != ref.SHA256 {
		return errors.New("review artifact fingerprint changed")
	}
	return StrictDecode(b, out)
}

// Lock excludes concurrent coordinators across daemon processes and releases on exit.
func (a Artifacts) Lock(id string) (func(), error) {
	if id == "" || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" {
		return nil, errors.New("invalid review execution identity")
	}
	if err := a.check(); err != nil {
		return nil, err
	}
	p := filepath.Join(a.Root, id+".lock")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || info.Size() != 0 {
		f.Close()
		return nil, errors.New("review lock contains existing data")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("review is already executing: %w", err)
	}
	return func() { _ = f.Close() }, nil
}
func cloneJSON[T any](v T) T {
	b, _ := json.Marshal(v)
	var copy T
	_ = json.Unmarshal(b, &copy)
	return copy
}
