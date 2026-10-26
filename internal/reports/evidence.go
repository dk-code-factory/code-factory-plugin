package reports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"codefactory/internal/buildinfo"
	"codefactory/internal/domain"
	"codefactory/internal/pluginbundle"
	"codefactory/internal/store"
)

var credentialPattern = regexp.MustCompile(`(?i)(?:["']?(?:access_token|refresh_token|id_token|password|(?:proxy-)?authorization|api[_-]?key|owner[_-]?token|secret)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*')|["']?(?:proxy-)?authorization["']?\s*[:=]\s*["']?(?:bearer|basic)\s+[a-z0-9._~+/=-]+|(?:bearer|basic)\s+[a-z0-9._~+/=-]+|(?:gh[pousr]_[a-z0-9_]+|github_pat_[a-z0-9_]+|sk-[a-z0-9_-]{12,})|(?:["']?(?:access_token|refresh_token|id_token|password|authorization|api[_-]?key|owner[_-]?token|secret)["']?\s*[:=]\s*["']?)[^\s"',}]+`)
var headerPattern = regexp.MustCompile(`(?i)\b(?:set-cookie|cookie)[ \t]*:[^\r\n]*|\b(?:proxy-)?authorization[ \t]*[:=][^\r\n]*|["'](?:set-cookie|cookie|(?:proxy-)?authorization)["']\s*:\s*(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|\[(?:\s*(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*')\s*,?)*\s*\])`)
var secretKeyPattern = regexp.MustCompile(`(?i)^(access_token|refresh_token|id_token|cookie|set-cookie|password|(?:proxy-)?authorization|api[_-]?key|owner[_-]?token|secret)$`)

func redactCredentialText(text string) string {
	return redactCredentialValue(text, 0).(string)
}

func redactCredentialValue(v any, depth int) any {
	switch x := v.(type) {
	case string:
		// Tool text can itself contain serialized MCP results and header records.
		// Bound repeated decoding and fail closed on the remaining private text.
		if depth >= 32 {
			return "[redacted]"
		}
		plain := func(text string) string {
			return credentialPattern.ReplaceAllString(headerPattern.ReplaceAllString(text, "[redacted]"), "[redacted]")
		}
		var out strings.Builder
		start, attempts := 0, 0
		headers, header := headerPattern.FindAllStringIndex(x, -1), 0
		for i := 0; i < len(x); i++ {
			if x[i] != '{' && x[i] != '[' && (x[i] != '"' || i+1 == len(x) || !strings.ContainsRune(`{[\`, rune(x[i+1]))) {
				continue
			}
			// A JSON-looking proof still belongs to its entire raw header value.
			// Matches inside an already decoded structure are handled recursively.
			for header < len(headers) && (headers[header][1] <= i || headers[header][0] < start) {
				header++
			}
			if header < len(headers) && headers[header][0] < i {
				startOfTail := headers[header][1]
				out.WriteString(plain(x[start:startOfTail]))
				i, start = startOfTail-1, startOfTail
				continue
			}
			if attempts >= 256 {
				out.WriteString(plain(x[start:i]))
				out.WriteString("[redacted]")
				return out.String()
			}
			attempts++
			decoder := json.NewDecoder(strings.NewReader(x[i:]))
			decoder.UseNumber() // Preserve adjacent diagnostic integers exactly.
			var nested any
			if err := decoder.Decode(&nested); err != nil {
				continue
			}
			out.WriteString(plain(x[start:i]))
			b, _ := json.Marshal(redactCredentialValue(nested, depth+1))
			out.Write(b)
			i += int(decoder.InputOffset()) - 1
			start = i + 1
		}
		out.WriteString(plain(x[start:]))
		return out.String()
	case []any:
		if len(x) == 2 {
			if name, ok := x[0].(string); ok && secretKeyPattern.MatchString(name) {
				x[1] = "[redacted]"
				return x
			}
		}
		for i := range x {
			x[i] = redactCredentialValue(x[i], depth)
		}
	case map[string]any:
		for k, value := range x {
			if secretKeyPattern.MatchString(k) {
				x[k] = "[redacted]"
			} else {
				x[k] = redactCredentialValue(value, depth)
			}
		}
	}
	return v
}

func (s *Service) sanitize(ctx context.Context, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	text := string(b)
	secrets := []string{}
	if s.Vault != nil {
		secrets = s.Vault.Secrets(ctx, nil)
	}
	if b, e := os.ReadFile(filepath.Join(s.Dir, "owner.token")); e == nil && len(b) > 0 {
		secrets = append(secrets, strings.TrimSpace(string(b)))
	}
	// Redact JSON-escaped spellings as well as ordinary text, retaining valid JSON.
	for _, secret := range secrets {
		if secret != "" {
			q, _ := json.Marshal(secret)
			text = strings.ReplaceAll(text, string(q[1:len(q)-1]), "[redacted]")
		}
	}
	// Redact decoded values, including serialized tool text and header tuples.
	var tree any
	if e = json.Unmarshal([]byte(text), &tree); e != nil {
		return e
	}
	b, e = json.Marshal(redactCredentialValue(tree, 0))
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func privateWrite(path string, data []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".report-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func (s *Service) requirePrivateReportDir(ctx context.Context) error {
	probe := exec.CommandContext(ctx, "git", "-C", s.Dir, "rev-parse", "--git-dir")
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		switch name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM":
			continue
		}
		probe.Env = append(probe.Env, variable)
	}
	probe.Env = append(probe.Env, "LC_ALL=C", "GIT_DISCOVERY_ACROSS_FILESYSTEM=1")
	output, err := probe.CombinedOutput()
	if err == nil {
		return fmt.Errorf("report data directory must be outside Git checkouts")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !strings.Contains(string(output), "not a git repository (or any") {
		return fmt.Errorf("cannot verify report data directory is outside Git checkouts")
	}
	return nil
}

func (s *Service) capture(ctx context.Context, r *Report) error {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	current, err := s.Get(ctx, r.ID)
	if err != nil {
		return err
	}
	*r = current
	// Never put private evidence into a source checkout, even with a custom data-dir.
	root := filepath.Join(s.Dir, "issue-reports", r.ID)
	if err := s.requirePrivateReportDir(ctx); err != nil {
		return err
	}
	if len(r.Evidence) > 0 {
		for _, v := range r.Evidence {
			if v.Origin == "runtime:manifest" {
				return nil
			}
		}
	}
	bundle := map[string]any{}
	manifest := []Evidence{}
	at := time.Now().UTC()
	add := func(origin string, v any, omission string) {
		if omission != "" {
			manifest = append(manifest, Evidence{Origin: origin, At: at, Omission: omission})
			return
		}
		if e := s.sanitize(ctx, &v); e != nil {
			manifest = append(manifest, Evidence{Origin: origin, At: at, Omission: "Redaction failed"})
			return
		}
		b, _ := json.Marshal(v)
		truncated := len(b) > 256<<10
		if truncated {
			manifest = append(manifest, Evidence{Origin: origin, At: at, Truncated: true, Omission: "Record exceeds 256 KiB; investigator can read the referenced local record"})
			return
		}
		h := sha256.Sum256(b)
		bundle[origin] = v
		manifest = append(manifest, Evidence{Origin: origin, At: at, SHA256: hex.EncodeToString(h[:]), Bytes: len(b)})
	}
	versions := map[string]string{"api": buildinfo.Version, "plugin": pluginbundle.Version(), "host-plugin": r.Observation.PluginVersion, "go": runtime.Version(), "codex": "Unknown", "agent-workflow": "Unknown", "dk-workflows": "Unknown"}
	if versions["host-plugin"] == "" {
		versions["host-plugin"] = "Unknown"
	}
	build := map[string]any{"version": buildinfo.Version, "os": runtime.GOOS, "arch": runtime.GOARCH}
	if info, ok := debug.ReadBuildInfo(); ok {
		build["settings"] = info.Settings
		build["dependencies"] = info.Deps
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				versions["source-revision"] = setting.Value
			}
		}
	}
	add("build", build, "")
	add("observation", r.Observation, "")
	var config domain.Config
	if e := s.Store.Get(ctx, "config", "settings", &config); e == nil {
		add("configuration", map[string]any{"priorityMappings": len(config.PriorityLabels), "notifications": config.Notifications, "jiraConfigured": config.JiraURL != "", "questionHelperConfigured": config.QuestionHelperModel != ""}, "")
	}
	repositories, e := store.List[domain.Repository](ctx, s.Store, "repository")
	if e != nil {
		return e
	}
	summary := []map[string]any{}
	for _, repo := range repositories {
		referenced := false
		for _, id := range r.Observation.ItemIDs {
			referenced = referenced || strings.HasPrefix(id, repo.ID+"#")
		}
		for _, id := range r.Observation.RunIDs {
			var run domain.Run
			if s.Store.Get(ctx, "run", id, &run) == nil {
				referenced = referenced || strings.HasPrefix(run.ItemID, repo.ID+"#")
			}
		}
		if referenced {
			summary = append(summary, map[string]any{"id": repo.ID, "lastSync": repo.LastSync, "error": repo.Error, "base": repo.Base})
		}
	}
	add("synchronization", summary, "")
	refs := map[string]bool{}
	for _, id := range r.Observation.ItemIDs {
		refs[id] = true
		var item domain.Item
		e := s.Store.Get(ctx, "item", id, &item)
		if e != nil {
			add("item:"+id, nil, "Referenced item unavailable")
		} else {
			add("item:"+id, item, "")
		}
	}
	for _, id := range r.Observation.RunIDs {
		refs[id] = true
		var run domain.Run
		e := s.Store.Get(ctx, "run", id, &run)
		if e != nil {
			add("run:"+id, nil, "Referenced run unavailable")
			continue
		}
		add("run:"+id, run, "")
		if run.ItemID != "" && !refs[run.ItemID] {
			refs[run.ItemID] = true
			var item domain.Item
			if s.Store.Get(ctx, "item", run.ItemID, &item) == nil {
				add("item:"+run.ItemID, item, "")
			} else {
				add("item:"+run.ItemID, nil, "Run-associated item unavailable")
			}
		}
		atts, e := store.List[domain.Attention](ctx, s.Store, "attention")
		if e != nil {
			return e
		}
		selected := []domain.Attention{}
		for _, a := range atts {
			if a.RunID == id {
				selected = append(selected, a)
			}
		}
		add("attention:"+id, selected, "")
	}
	through := r.Observation.EventThrough
	if through == 0 {
		through = s.Store.Latest(ctx)
	}
	after := r.Observation.EventAfter
	if after == 0 && through > 1000 {
		after = through - 1000
		manifest = append(manifest, Evidence{Origin: "referenced-events:history", At: at, Truncated: true, Omission: fmt.Sprintf("Default capture starts after event %d; earlier transcript events remain in the local journal", after)})
	}
	events := []domain.Event{}
	scanned := 0
	for after < through && scanned < 2000 {
		page, e := s.Store.Events(ctx, after, 200)
		if e != nil {
			return e
		}
		if len(page) == 0 {
			break
		}
		for _, event := range page {
			after = event.Seq
			scanned++
			if event.Seq > through {
				break
			}
			if refs[event.Entity] {
				events = append(events, event)
			}
		}
	}
	add("referenced-events", events, "")
	if after < through {
		manifest = append(manifest, Evidence{Origin: "referenced-events:range", At: at, Truncated: true, Omission: "Journal ended or the 2000-event scan limit was reached before the requested upper bound; omitted history remains explicitly unavailable in this capture"})
	}
	add("service-logs", nil, "Unscoped service logs excluded to avoid unrelated private work; referenced journal events captured. Investigator may inspect relevant local logs around observedAt.")
	add("host-display", nil, "Host display and exact chat tool response are observable only by the submitting host")
	add("authentication", nil, "Credentials, browser profiles and authentication material excluded")
	add("versions", versions, "")
	data, e := json.MarshalIndent(map[string]any{"version": 1, "capturedAt": at, "evidence": bundle, "manifest": manifest}, "", "  ")
	if e != nil {
		return e
	}
	if e = privateWrite(filepath.Join(root, "evidence.json"), data); e != nil {
		return e
	}
	digest := sha256.Sum256(data)
	manifest = append(manifest, Evidence{Origin: "runtime:manifest", At: at, SHA256: hex.EncodeToString(digest[:]), Bytes: len(data)})
	_, e = s.update(ctx, r.ID, func(current *Report) error {
		current.Evidence = append(current.Evidence, manifest...)
		current.Versions = versions
		if buildinfo.Version != "dev" {
			current.InstalledVersion = buildinfo.Version
		}
		return nil
	})
	return e
}

// Supplement the observation snapshot with bounded, timestamped version probes.
// These execute on the worker, never on the report submission path.
func (s *Service) enrichVersions(ctx context.Context, r *Report) error {
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	manifest := []Evidence{}
	versions := map[string]string{}
	for _, name := range []string{"codex", "agent-workflow", "gh"} {
		binary := name
		if engine, ok := s.Engine.(*NativeEngine); ok {
			if name == "codex" && engine.Codex != "" {
				binary = engine.Codex
			}
			if name == "agent-workflow" {
				binary = engine.binary()
			}
		}
		var output bytes.Buffer
		cmd := exec.CommandContext(check, binary, "--version")
		cmd.Stdout = &limitedWriter{Writer: &output, Remaining: 4096}
		entry := Evidence{Origin: "version:" + name, At: time.Now().UTC()}
		if cmd.Run() != nil {
			entry.Omission = "Installed version unavailable"
			versions[name] = "Unknown"
		} else {
			value := strings.TrimSpace(output.String())
			if err := s.sanitize(ctx, &value); err != nil {
				return err
			}
			versions[name] = value
			entry.Bytes = len(value)
			entry.SHA256 = hash(value)
		}
		manifest = append(manifest, entry)
	}
	data, err := json.Marshal(map[string]any{"versions": versions, "manifest": manifest})
	if err != nil {
		return err
	}
	if err = privateWrite(filepath.Join(s.Dir, "issue-reports", r.ID, "versions.json"), data); err != nil {
		return err
	}
	_, err = s.update(ctx, r.ID, func(current *Report) error {
		for k, v := range versions {
			current.Versions[k] = v
		}
		current.Evidence = append(current.Evidence, manifest...)
		return nil
	})
	return err
}
