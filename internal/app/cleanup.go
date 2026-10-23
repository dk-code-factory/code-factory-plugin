package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Workflow runtimes can seal their directories read-only. Make only this run's
// directories removable; never chmod files or follow references into owner state.
func removeRunScratch(data, id string) error {
	if id == "" || id == "." || filepath.Base(id) != id {
		return fmt.Errorf("invalid scratch run identifier")
	}
	parent, err := os.OpenRoot(filepath.Join(data, "scratch"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Lstat(id)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return parent.Remove(id)
	}
	root, err := parent.OpenRoot(id)
	if err != nil {
		return err
	}
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return root.Chmod(path, info.Mode().Perm()|0700)
		}
		return nil
	})
	closeErr := root.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return parent.RemoveAll(id)
}
