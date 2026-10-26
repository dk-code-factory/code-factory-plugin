package app

import (
	"codefactory/internal/domain"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const skillBundleLimit = 5 << 20

// Plugin skills may refer to sibling package resources. Preserve their package
// layout and entrypoint instead of relocating only the leaf skill directory.
func skillBundleRoot(path string) (string, string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	leaf := filepath.Dir(path)
	for root := leaf; ; root = filepath.Dir(root) {
		data, err := os.ReadFile(filepath.Join(root, ".codex-plugin", "plugin.json"))
		if err == nil {
			var manifest struct {
				Skills string `json:"skills"`
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return "", "", fmt.Errorf("invalid plugin skill manifest: %w", err)
			}
			if manifest.Skills != "" {
				if !filepath.IsLocal(manifest.Skills) {
					return "", "", fmt.Errorf("plugin skills must be inside their package")
				}
				relative, err := filepath.Rel(filepath.Join(root, manifest.Skills), path)
				if err != nil || !filepath.IsLocal(relative) {
					return "", "", fmt.Errorf("skill is outside the plugin skills directory")
				}
				entry, err := filepath.Rel(root, path)
				return root, entry, err
			}
		} else if !os.IsNotExist(err) {
			return "", "", err
		}
		if filepath.Dir(root) == root {
			break
		}
	}
	return leaf, "SKILL.md", nil
}

func snapshotSkill(s domain.Skill) (domain.Skill, error) {
	root, entry, err := skillBundleRoot(s.Path)
	if err != nil {
		return s, err
	}
	files := map[string]string{}
	modes := map[string]uint32{}
	total := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill bundles may not contain symlinks")
		}
		if d.IsDir() {
			if p != root && d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("invalid skill file")
		}
		if info.Size() > int64(skillBundleLimit-total) {
			return fmt.Errorf("skill bundle exceeds 5 MiB")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		total += len(b)
		if total > skillBundleLimit {
			return fmt.Errorf("skill bundle exceeds 5 MiB")
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files[rel] = base64.StdEncoding.EncodeToString(b)
		modes[rel] = uint32(info.Mode().Perm())
		if rel == entry {
			s.Content = string(b)
		}
		return nil
	})
	if err == nil {
		if _, ok := files[entry]; !ok {
			return s, fmt.Errorf("skill entrypoint is missing")
		}
	}
	s.Files, s.Modes = files, modes
	if entry != "SKILL.md" {
		s.EntryPath = entry
	} else {
		s.EntryPath = ""
	}
	return s, err
}

func materializeSkill(root string, s domain.Skill) (string, error) {
	entry := s.EntryPath
	if entry == "" {
		entry = "SKILL.md"
	}
	if !filepath.IsLocal(entry) {
		return "", fmt.Errorf("invalid skill entrypoint")
	}
	if _, ok := s.Files[entry]; !ok {
		return "", fmt.Errorf("retained skill entrypoint is missing")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	total := 0
	for rel, encoded := range s.Files {
		if !filepath.IsLocal(rel) || filepath.Clean(rel) != rel || strings.ContainsRune(rel, '\x00') {
			return "", fmt.Errorf("invalid retained skill path")
		}
		if len(encoded) > base64.StdEncoding.EncodedLen(skillBundleLimit-total) {
			return "", fmt.Errorf("skill bundle exceeds 5 MiB")
		}
		b, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", err
		}
		total += len(b)
		if total > skillBundleLimit {
			return "", fmt.Errorf("skill bundle exceeds 5 MiB")
		}
		if err := dir.MkdirAll(filepath.Dir(rel), 0700); err != nil {
			return "", err
		}
		mode := os.FileMode(0600)
		if s.Modes[rel]&0111 != 0 {
			mode = 0700
		}
		if err := dir.WriteFile(rel, b, mode); err != nil {
			return "", err
		}
	}
	return filepath.Join(root, entry), nil
}
