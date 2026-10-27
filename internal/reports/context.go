package reports

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"codefactory/internal/providers"
)

const structuredContract = 2

type investigationContext struct {
	Version      int                 `json:"version"`
	CapturedAt   time.Time           `json:"capturedAt"`
	Repositories []repositoryContext `json:"repositories"`
}
type repositoryContext struct {
	Repository string               `json:"repository"`
	Available  bool                 `json:"available"`
	Revision   string               `json:"revision"`
	Labels     []string             `json:"labels"`
	Documents  []contextDocument    `json:"documents"`
	Templates  []issueTemplate      `json:"templates"`
	Candidates []duplicateCandidate `json:"candidates"`
	Omissions  []string             `json:"omissions"`
}
type contextDocument struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"` // hash of retained sanitized content
}
type duplicateCandidate struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// PrepareContext is read-only. Requests share authentication, cache, budget and
// cooldown with publication. Partial metadata never authorizes filing.
func (g *GitHubAdapter) PrepareContext(ctx context.Context, observation Submission) (investigationContext, error) {
	ctx = providers.Revalidate(g.operation(ctx))
	result := investigationContext{Version: 1, CapturedAt: time.Now().UTC()}
	for _, repo := range []string{DefaultRepository, routes["agent-workflow"], routes["dk-workflows"]} {
		r := repositoryContext{Repository: repo}
		omit := func(kind string) { r.Omissions = append(r.Omissions, kind+" unavailable or incomplete") }
		var metadata struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
			HasIssues     bool   `json:"has_issues"`
			Archived      bool   `json:"archived"`
		}
		if err := g.get(ctx, "/repos/"+repo, &metadata); err != nil {
			omit("repository status")
		} else {
			r.Available = strings.EqualFold(metadata.FullName, repo) && metadata.HasIssues && !metadata.Archived
			if metadata.DefaultBranch != "" {
				var commit struct {
					SHA string `json:"sha"`
				}
				if err := g.get(ctx, "/repos/"+repo+"/commits/"+url.PathEscape(metadata.DefaultBranch), &commit); err == nil && commitPattern.MatchString(commit.SHA) {
					r.Revision = commit.SHA
				} else {
					omit("default branch revision")
				}
			} else {
				omit("default branch revision")
			}
		}
		labels, err := listPages[struct {
			Name string `json:"name"`
		}](ctx, g, "/repos/"+repo+"/labels")
		if err != nil {
			omit("labels")
		} else {
			for _, l := range labels {
				r.Labels = append(r.Labels, l.Name)
			}
		}
		if r.Revision != "" {
			g.prepareDocuments(ctx, &r)
		}
		var matches struct {
			Items []struct {
				githubIssue
				Title string `json:"title"`
			} `json:"items"`
			Incomplete bool `json:"incomplete_results"`
		}
		query := "repo:" + repo + " is:issue" + searchTerms(observation)
		if err := g.get(ctx, "/search/issues?q="+url.QueryEscape(query)+"&per_page=10", &matches); err != nil {
			omit("duplicate search")
		} else {
			if matches.Incomplete {
				omit("duplicate search")
			}
			for _, item := range matches.Items {
				if len(r.Candidates) == 10 {
					break
				}
				if _, err := item.issue(repo); err == nil {
					body := item.Body
					if len(body) > 8000 {
						body = body[:8000]
						omit("duplicate body")
					}
					r.Candidates = append(r.Candidates, duplicateCandidate{item.Number, item.URL, item.Title, redactCredentialText(body)})
				}
			}
		}
		result.Repositories = append(result.Repositories, r)
	}
	return result, ctx.Err()
}
func searchTerms(o Submission) string {
	words := strings.FieldsFunc(o.Surface+" "+o.Actual, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') })
	seen := map[string]bool{}
	terms := []string{}
	for _, word := range words {
		word = strings.ToLower(word)
		if len(word) < 3 || len(word) > 32 || seen[word] {
			continue
		}
		seen[word] = true
		terms = append(terms, word)
		if len(terms) == 6 {
			break
		}
	}
	if len(terms) == 0 {
		return ""
	}
	return " " + strings.Join(terms, " ")
}
func (g *GitHubAdapter) prepareDocuments(ctx context.Context, r *repositoryContext) {
	var tree struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := g.get(ctx, "/repos/"+r.Repository+"/git/trees/"+r.Revision+"?recursive=1", &tree); err != nil {
		r.Omissions = append(r.Omissions, "repository document inventory unavailable")
		return
	}
	if tree.Truncated {
		r.Omissions = append(r.Omissions, "repository document inventory truncated")
	}
	paths := []string{}
	for _, entry := range tree.Tree {
		lower := strings.ToLower(entry.Path)
		base := path.Base(lower)
		dir := path.Dir(lower)
		template := strings.HasPrefix(lower, ".github/issue_template/") && (strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")) || lower == ".github/issue_template.md" || lower == "issue_template.md" || lower == "docs/issue_template.md"
		guide := (dir == "." || dir == ".github" || dir == "docs") && (base == "contributing.md" || base == "security.md" || base == "support.md")
		if entry.Type == "blob" && (template || guide) {
			paths = append(paths, entry.Path)
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		a, b := strings.Contains(strings.ToLower(paths[i]), "issue_template"), strings.Contains(strings.ToLower(paths[j]), "issue_template")
		if a != b {
			return !a
		} // Read routing guidance before bounded template discovery.
		return paths[i] < paths[j]
	})
	for i, p := range paths {
		if i >= 24 {
			r.Omissions = append(r.Omissions, "repository documents exceed 24-file limit")
			break
		}
		var file struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
			Type     string `json:"type"`
		}
		err := g.get(ctx, "/repos/"+r.Repository+"/contents/"+escapePath(p)+"?ref="+r.Revision, &file)
		data, decodeErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
		if err != nil || decodeErr != nil || file.Encoding != "base64" || file.Type != "file" || len(data) > 32768 {
			r.Omissions = append(r.Omissions, p+": content unavailable or exceeds limit")
			continue
		}
		content := redactCredentialText(string(data))
		r.Documents = append(r.Documents, contextDocument{p, content, hashContent(content)})
		if strings.Contains(strings.ToLower(p), "issue_template") && path.Base(strings.ToLower(p)) != "config.yml" && path.Base(strings.ToLower(p)) != "config.yaml" {
			r.Templates = append(r.Templates, normalizeTemplate(p, content))
		}
	}
}
func hashContent(text string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(text))) }
func (c *investigationContext) repository(repo string) *repositoryContext {
	if c != nil {
		for i := range c.Repositories {
			if c.Repositories[i].Repository == repo {
				return &c.Repositories[i]
			}
		}
	}
	return nil
}
func marshalContext(c *investigationContext) []byte { b, _ := json.Marshal(c); return b }

func refreshContextHashes(c *investigationContext) {
	for i := range c.Repositories {
		for j := range c.Repositories[i].Documents {
			d := &c.Repositories[i].Documents[j]
			d.SHA256 = hashContent(d.Content)
		}
		for j := range c.Repositories[i].Templates {
			t := &c.Repositories[i].Templates[j]
			for _, d := range c.Repositories[i].Documents {
				if d.Path == t.ID {
					*t = normalizeTemplate(d.Path, d.Content)
					break
				}
			}
		}
	}
}
