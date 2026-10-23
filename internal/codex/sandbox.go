package codex

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// TaskSandbox is an invocation-local permissions profile. It never writes the
// owner's Codex configuration or replaces filesystem isolation with escalation.
type TaskSandbox struct {
	ID, Mode, Workspace, Scratch string
	Readable                     []string
}

var sandboxID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (s TaskSandbox) Config() (map[string]any, error) {
	if s.Mode != "read-only" && s.Mode != "workspace-write" {
		return nil, fmt.Errorf("bounded sandbox requires read-only or workspace-write")
	}
	if !sandboxID.MatchString(s.ID) || !filepath.IsAbs(s.Workspace) || !filepath.IsAbs(s.Scratch) || filepath.Clean(s.Workspace) == "/" || filepath.Clean(s.Scratch) == "/" || s.Workspace == s.Scratch {
		return nil, fmt.Errorf("invalid task sandbox roots")
	}
	fs := map[string]any{":root": "deny", ":minimal": "read", ":slash_tmp": "deny"}
	for _, path := range s.Readable {
		if !filepath.IsAbs(path) || filepath.Clean(path) == "/" {
			return nil, fmt.Errorf("invalid sandbox readable root")
		}
		fs[path] = "read"
	}
	fs[s.Workspace] = "read"
	if s.Mode == "workspace-write" {
		fs[s.Workspace] = "write"
		for _, path := range []string{".git", ".codex", ".agents", ".aws"} {
			fs[filepath.Join(s.Workspace, path)] = "read"
		}
	}
	fs[s.Scratch] = "write"
	name := "code-factory-" + s.ID
	return map[string]any{
		"default_permissions":    name,
		"features.network_proxy": true,
		"permissions." + name: map[string]any{
			"filesystem": fs,
			"network": map[string]any{
				"enabled":             true,
				"allow_local_binding": false,
				"domains": map[string]any{
					"api.github.com": "allow", "github.com": "allow",
					"raw.githubusercontent.com": "allow", "objects.githubusercontent.com": "allow", "codeload.github.com": "allow",
					"api.openai.com": "allow", "chatgpt.com": "allow", "auth.openai.com": "allow",
				},
			},
		},
		"shell_environment_policy.set": map[string]any{
			"TMPDIR":                  filepath.Join(s.Scratch, "tmp"),
			"TMPPREFIX":               filepath.Join(s.Scratch, "tmp", "zsh"),
			"TMP":                     filepath.Join(s.Scratch, "tmp"),
			"TEMP":                    filepath.Join(s.Scratch, "tmp"),
			"XDG_CACHE_HOME":          filepath.Join(s.Scratch, "cache"),
			"XDG_DATA_HOME":           filepath.Join(s.Scratch, "data"),
			"PYTHONDONTWRITEBYTECODE": "1",
			"GIT_OPTIONAL_LOCKS":      "0",
		},
	}, nil
}

func (s TaskSandbox) Args() ([]string, error) {
	config, err := s.Config()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := []string{}
	for _, key := range keys {
		value, err := inlineTOML(config[key])
		if err != nil {
			return nil, err
		}
		args = append(args, "-c", key+"="+value)
	}
	return args, nil
}

func tomlString(s string) string { b, _ := json.Marshal(s); return string(b) }

// JSON string quoting is also valid for these TOML keys/values; objects require
// TOML's equals syntax. Values are argv entries, never interpolated shell code.
func inlineTOML(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return tomlString(x), nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fields := make([]string, 0, len(x))
		for _, k := range keys {
			value, err := inlineTOML(x[k])
			if err != nil {
				return "", err
			}
			fields = append(fields, tomlString(k)+"="+value)
		}
		return "{" + strings.Join(fields, ",") + "}", nil
	}
	return "", fmt.Errorf("unsupported sandbox configuration value")
}
