package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/prreview"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ReviewImport struct {
	Scope     string   `json:"scope"`
	Imported  []string `json:"imported"`
	Conflicts []string `json:"conflicts"`
	Completed bool     `json:"completed"`
}

// ImportDKReviews reads only the owner's private DK settings. It never changes
// installed skills, profiles or DK files. Existing Factory settings always win.
func (a *App) ImportDKReviews(ctx context.Context, repository string) (ReviewImport, error) {
	root := os.Getenv("XDG_DATA_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ReviewImport{}, err
		}
		root = filepath.Join(home, ".local", "share")
	}
	return a.importDKReviews(ctx, repository, filepath.Join(root, "codex-repo-rules"))
}
func (a *App) importDKReviews(ctx context.Context, repository, root string) (out ReviewImport, err error) {
	scope := "global"
	folder := root
	if repository != "" {
		if !repoPattern.MatchString(repository) {
			return out, errors.New("invalid DK import repository")
		}
		scope = repository
		folder = filepath.Join(root, prreview.Hash([]byte("remote:github.com/" + strings.ToLower(repository)))[:24])
	}
	out = ReviewImport{Scope: scope, Imported: []string{}, Conflicts: []string{}}
	var prior ReviewImport
	if err = a.Store.Get(ctx, "review-import", scope, &prior); err == nil {
		return prior, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	var rawReviewers struct {
		Reviewers []json.RawMessage `json:"reviewers"`
	}
	reviewersPresent := false
	if b, e := os.ReadFile(filepath.Join(folder, "reviewers.json")); e == nil {
		if e = json.Unmarshal(b, &rawReviewers); e != nil {
			return out, e
		}
		if rawReviewers.Reviewers == nil {
			return out, errors.New("malformed DK reviewer list")
		}
		reviewersPresent = true
	} else if !os.IsNotExist(e) {
		return out, e
	}
	reviewers := []prreview.Reviewer{}
	skills, e := store.List[domain.Skill](ctx, a.Store, "skill")
	if e != nil {
		return out, e
	}
	for _, raw := range rawReviewers.Reviewers {
		var name string
		var binding struct {
			Skill   string   `json:"skill"`
			Host    string   `json:"host"`
			Command []string `json:"command"`
		}
		if json.Unmarshal(raw, &name) == nil {
			aliases := map[string]string{"code-review-ad": "code-review-ad:code-review-ad", "code-review-matpocock": "code-review-matpocock:code-review-matpocock", "code-review-qodo": "code-review-qodo:code-review-qodo"}
			binding.Skill = name
			if v, ok := aliases[name]; ok {
				binding.Skill = v
			}
			if !strings.Contains(binding.Skill, ":") {
				binding.Skill = "dk-workflows:" + binding.Skill
			}
		} else if e = json.Unmarshal(raw, &binding); e != nil {
			return out, e
		}
		if len(binding.Command) > 0 {
			reviewers = append(reviewers, prreview.Reviewer{ID: "command-" + prreview.Digest(binding.Command)[:20], Kind: "command", Argv: binding.Command})
			continue
		}
		if binding.Host != "" && binding.Host != "current" && binding.Host != "codex" {
			out.Conflicts = append(out.Conflicts, "Reviewer "+binding.Skill+" requires unavailable host "+binding.Host)
			continue
		}
		found := ""
		for _, skill := range skills {
			if skill.Name == binding.Skill || skill.ID == binding.Skill {
				if found != "" {
					return out, fmt.Errorf("ambiguous installed reviewer %s", binding.Skill)
				}
				found = skill.ID
			}
		}
		if found == "" {
			out.Conflicts = append(out.Conflicts, "Reviewer "+binding.Skill+" is not installed in Code Factory; configure its skill binding explicitly")
			continue
		}
		reviewers = append(reviewers, prreview.Reviewer{ID: binding.Skill, Kind: "skill", SkillID: found})
	}
	// Never silently import a shorter reviewer list when any binding cannot resolve.
	if len(reviewers) != len(rawReviewers.Reviewers) {
		reviewersPresent = false
	}
	if err = prreview.ValidateReviewers(reviewers); err != nil {
		return out, err
	}
	rules := ""
	rulesPresent := false
	rulesFolder := folder
	if repository == "" {
		rulesFolder = filepath.Join(root, "user")
	}
	if b, e := os.ReadFile(filepath.Join(rulesFolder, "rules.json")); e == nil {
		var data struct {
			Rules []struct {
				Text  string `json:"text"`
				Scope string `json:"scope"`
			} `json:"rules"`
			Conflicts []json.RawMessage `json:"conflicts"`
		}
		if e = json.Unmarshal(b, &data); e != nil {
			return out, e
		}
		for _, r := range data.Rules {
			if r.Scope != "" {
				rules += "[" + r.Scope + "] "
			}
			rules += r.Text + "\n"
		}
		rulesPresent = true
		if len(data.Conflicts) > 0 {
			rulesPresent = false
			out.Conflicts = append(out.Conflicts, "DK rule conflict resolutions need explicit reconciliation; private rules were preserved in DK")
		}
	} else if !os.IsNotExist(e) {
		return out, e
	}
	err = a.Store.Write(ctx, func(t *store.Tx) error {
		var exists ReviewImport
		if e := t.Get("review-import", scope, &exists); e == nil {
			out = exists
			return nil
		} else if !errors.Is(e, store.ErrNotFound) {
			return e
		}
		if repository == "" {
			var c domain.Config
			if e := t.Get("config", "settings", &c); e != nil {
				return e
			}
			if reviewersPresent {
				if c.Reviewers == nil && !c.ReviewersConfigured {
					c.Reviewers = reviewers
					c.ReviewersConfigured = true
					out.Imported = append(out.Imported, "reviewers")
				} else {
					out.Conflicts = append(out.Conflicts, "Code Factory global reviewers preserved")
				}
			}
			if rulesPresent {
				if c.ReviewRules == "" && !c.ReviewRulesConfigured {
					c.ReviewRules = rules
					c.ReviewRulesConfigured = true
					out.Imported = append(out.Imported, "reviewRules")
				} else {
					out.Conflicts = append(out.Conflicts, "Code Factory global review rules preserved")
				}
			}
			if e := t.Put("config", "settings", c); e != nil {
				return e
			}
		} else {
			var r domain.Repository
			if e := t.Get("repository", repository, &r); e != nil {
				return e
			}
			if reviewersPresent {
				if r.Reviewers == nil {
					r.Reviewers = &reviewers
					out.Imported = append(out.Imported, "reviewers")
				} else {
					out.Conflicts = append(out.Conflicts, "Code Factory repository reviewers preserved")
				}
			}
			if rulesPresent {
				if r.ReviewRules == nil {
					r.ReviewRules = &rules
					out.Imported = append(out.Imported, "reviewRules")
				} else {
					out.Conflicts = append(out.Conflicts, "Code Factory repository review rules preserved")
				}
			}
			if e := t.Put("repository", repository, r); e != nil {
				return e
			}
		}
		out.Completed = true
		if e := t.Put("review-import", scope, out); e != nil {
			return e
		}
		return t.Event("review.import", scope, out)
	})
	return out, err
}
