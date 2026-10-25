package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) remapProfileSkills(ctx context.Context, before, after []domain.Skill) error {
	old := map[string]string{}
	for _, s := range before {
		old[s.ID] = s.Name
	}
	current := map[string]map[string]bool{}
	for _, s := range after {
		if !s.Disabled {
			if current[s.Name] == nil {
				current[s.Name] = map[string]bool{}
			}
			current[s.Name][s.ID] = true
		}
	}
	replacements := map[string]string{}
	for id, name := range old {
		if len(current[name]) == 1 {
			for next := range current[name] {
				if next != id {
					replacements[id] = next
				}
			}
		}
	}
	return m.replaceProfileSkillIDs(ctx, replacements)
}

func (m *Manager) replaceProfileSkillIDs(ctx context.Context, replacements map[string]string) error {
	profiles, err := store.List[domain.Profile](ctx, m.app.Store, "profile")
	if err != nil {
		return err
	}
	return m.app.Store.Write(ctx, func(tx *store.Tx) error {
		for _, p := range profiles {
			// Preserve edits made after the inventory read but before this write.
			if err := tx.Get("profile", p.ID, &p); err != nil {
				return err
			}
			changed := false
			for i, id := range p.Skills {
				if replacement, ok := replacements[id]; ok && replacement != id {
					p.Skills[i] = replacement
					changed = true
				}
			}
			if changed {
				seen := map[string]bool{}
				unique := []string{}
				for _, id := range p.Skills {
					if !seen[id] {
						seen[id] = true
						unique = append(unique, id)
					}
				}
				p.Skills = unique
				if err := tx.Put("profile", p.ID, p); err != nil {
					return err
				}
				if err := tx.Event("profile.changed", p.ID, map[string]string{"reason": "dependency skill update"}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// This opt-in migration is restricted to DK's own native workflow names. The
// selected current native package must identify DK's canonical repository.
func (m *Manager) migrateLegacyDKSkills(ctx context.Context, opID string, add func(DependencyResult, func(*DependencyResult))) {
	skills, err := m.discoverAdminSkills(ctx)
	if err != nil {
		add(DependencyResult{ID: "legacy-dk", Kind: "skill", Status: "failed"}, func(r *DependencyResult) { r.Detail = "Native workflow skill discovery failed." })
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, skill := range skills {
		if skill.Disabled || !canonicalDKSkill(skill.Name) {
			continue
		}
		root, _, err := skillBundleRoot(skill.Path)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, ".codex-plugin/plugin.json"))
		var manifest struct{ Name, Repository string }
		parts := strings.SplitN(skill.Name, ":", 2)
		if err != nil || json.Unmarshal(data, &manifest) != nil || manifest.Name != parts[0] || manifest.Repository != "https://github.com/dk-code-factory/"+parts[0] {
			continue
		}
		name := parts[1]
		if !regexpSkillName(name) {
			continue
		}
		target := filepath.Join(home, ".codex/skills", name)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		var registered GitHubSkillSource
		if m.app.Store.Get(ctx, "dependency-skill-source", "codex:"+name, &registered) == nil {
			continue
		}
		skill := skill
		add(DependencyResult{ID: "legacy:" + name, Kind: "skill", Current: "standalone", Available: skill.Name, Status: "running"}, func(r *DependencyResult) {
			backup := filepath.Join(m.app.DataDir, "dependency-backups", opID, "legacy-skills", name)
			if err := os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
				r.Status = "failed"
				r.Detail = "Legacy skill backup unavailable."
				return
			}
			if _, err := os.Lstat(backup); !os.IsNotExist(err) {
				r.Status = "conflict"
				r.Detail = "Backup already exists; inspect previous migration before retry."
				return
			}
			if err := os.Rename(target, backup); err != nil {
				r.Status = "failed"
				r.Detail = "Legacy skill archive failed; original retained."
				return
			}
			if err := m.replaceProfileSkillIDs(ctx, map[string]string{filepath.Join(target, "SKILL.md"): skill.ID}); err != nil {
				if os.Rename(backup, target) != nil {
					r.Status = "uncertain"
					r.Detail = "Profile migration failed; restore the retained legacy backup before retry."
					return
				}
				r.Status = "failed"
				r.Detail = "Profile migration failed; legacy skill restored."
				return
			}
			r.Status = "migrated"
			r.Current = skill.Name
			r.Detail = "Legacy files archived; configured profiles use the current native plugin skill. Prepared run snapshots are unchanged."
		})
	}
	if _, err := m.discoverAdminSkills(ctx); err != nil {
		add(DependencyResult{ID: "post-migration-discovery", Kind: "skill", Status: "failed"}, func(r *DependencyResult) {
			r.Detail = "Legacy migration completed; refreshed skill discovery unavailable."
		})
	}
}

func (m *Manager) recoverLegacySkillReferences(ctx context.Context, op DependencyOperation) error {
	if !op.MigrateLegacyDK {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	profiles, err := store.List[domain.Profile](ctx, m.app.Store, "profile")
	if err != nil {
		return err
	}
	for _, p := range profiles {
		for _, id := range p.Skills {
			root := filepath.Join(home, ".codex/skills")
			rel, err := filepath.Rel(root, id)
			if err != nil || !filepath.IsLocal(rel) {
				continue
			}
			parts := strings.Split(rel, string(os.PathSeparator))
			if len(parts) != 2 || (!strings.HasPrefix(parts[0], "dk-") && parts[0] != "code-review-ad" && parts[0] != "code-review-matpocock") || parts[1] != "SKILL.md" {
				continue
			}
			target := filepath.Join(root, parts[0])
			backup := filepath.Join(m.app.DataDir, "dependency-backups", op.ID, "legacy-skills", parts[0])
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				continue
			}
			if _, err := os.Lstat(backup); os.IsNotExist(err) {
				continue
			}
			if err := os.Rename(backup, target); err != nil {
				return fmt.Errorf("interrupted legacy skill migration requires backup recovery")
			}
		}
	}
	return nil
}

func canonicalDKSkill(name string) bool {
	parts := strings.SplitN(name, ":", 2)
	if len(parts) != 2 || !regexpSkillName(parts[1]) {
		return false
	}
	return parts[0] == "dk-workflows" && strings.HasPrefix(parts[1], "dk-") || parts[0] == "code-review-ad" && parts[1] == "code-review-ad" || parts[0] == "code-review-matpocock" && parts[1] == "code-review-matpocock"
}
