// Package dependencies stages verified dependency distributions without changing
// authentication, global executables, or the running service.
package dependencies

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Extract accepts regular files and directories only. The caller supplies a new
// private staging directory, and the whole distribution has one byte/file budget.
func Extract(r io.Reader, destination string, limit int64) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("invalid dependency archive")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	var total int64
	for count := 0; ; count++ {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil || count >= 10000 {
			return fmt.Errorf("invalid or oversized dependency archive")
		}
		name := path.Clean(h.Name)
		if name == "." && h.Typeflag == tar.TypeDir {
			continue
		}
		if !filepath.IsLocal(name) || strings.Contains(name, "\\") || h.Size < 0 {
			return fmt.Errorf("dependency archive path rejected")
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if h.Typeflag != tar.TypeReg || seen[name] || h.Size > limit-total {
			return fmt.Errorf("dependency archive entry rejected")
		}
		seen[name] = true
		total += h.Size
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|os.FileMode(h.Mode)&0100)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, tr, h.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("incomplete dependency archive")
		}
		if closeErr != nil {
			return closeErr
		}
	}
}

// TreeDigest detects local edits and rejects links/special files before an update.
func TreeDigest(root string) (string, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("skill must be a regular directory")
	}
	files := []string{}
	directories := []string{}
	var size int64
	err = filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(root, p)
			directories = append(directories, filepath.ToSlash(rel))
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("skill links and special files are unsupported")
		}
		size += info.Size()
		if size > 64<<20 || len(files) >= 10000 {
			return fmt.Errorf("skill exceeds update limits")
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	sort.Strings(directories)
	h := sha256.New()
	for _, name := range directories {
		fmt.Fprintf(h, "dir:%d:%s\x00", len(name), name)
	}
	for _, p := range files {
		rel, _ := filepath.Rel(root, p)
		info, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d:%s\x00%d\x00%d\x00", len(filepath.ToSlash(rel)), filepath.ToSlash(rel), info.Size(), info.Mode().Perm()&0100)
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func CopyTree(source, target string) error {
	if _, err := TreeDigest(source); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(source, p)
		dest := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0600|info.Mode().Perm()&0100)
	})
}
