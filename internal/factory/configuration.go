package factory

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var eventTypes = []string{"pull_request", "pull_request_review", "pull_request_review_comment", "pull_request_review_thread", "issue_comment", "ci"}

func validateSettings(c Settings) error {
	if c.Version != 1 || c.QuietSeconds < 0 || c.QuietSeconds > 86400 {
		return fmt.Errorf("version 1 and quiet period 0–86400 seconds required")
	}
	if c.GitHubRequestsPerHour != 0 && (c.GitHubRequestsPerHour < 60 || c.GitHubRequestsPerHour > 5000) {
		return fmt.Errorf("GitHub budget must be 60–5000 requests per hour")
	}
	if c.GitHubReserve != nil && (*c.GitHubReserve < 0 || *c.GitHubReserve > 5000) {
		return fmt.Errorf("invalid GitHub reserve")
	}
	if c.GitHubPollSeconds != 0 && (c.GitHubPollSeconds < 60 || c.GitHubPollSeconds > 3600) {
		return fmt.Errorf("GitHub polling must be 60–3600 seconds")
	}
	for _, interval := range []int{c.GitHubRepositoryPollSeconds, c.GitHubDetailPollSeconds} {
		if interval != 0 && (interval < 60 || interval > 86400) {
			return fmt.Errorf("GitHub repository and detail polling must be 60–86400 seconds")
		}
	}
	seen := map[string]bool{}
	for _, r := range c.Repositories {
		if !repoName.MatchString(r.ID) || strings.Contains(r.ID, "..") || seen[r.ID] || strings.HasPrefix(r.Base, "-") {
			return fmt.Errorf("invalid or duplicate repository")
		}
		seen[r.ID] = true
		if r.Checkout != "" && !filepath.IsAbs(r.Checkout) {
			return fmt.Errorf("checkout must be absolute")
		}
		if r.QuietSeconds != nil && (*r.QuietSeconds < 0 || *r.QuietSeconds > 86400) {
			return fmt.Errorf("invalid repository quiet period")
		}
	}
	ids := map[string]bool{}
	for _, r := range c.Rules {
		if r.ID == "" || ids[r.ID] || len(r.Repositories) == 0 || len(r.Types) == 0 || (strings.TrimSpace(r.Instructions) == "" && len(r.Skills) == 0) {
			return fmt.Errorf("rules need unique IDs, repository selectors, event types and instructions or skills")
		}
		ids[r.ID] = true
		for _, repo := range r.Repositories {
			if repo != "*" && !seen[repo] {
				return fmt.Errorf("rule repository %q is not configured", repo)
			}
		}
		for _, kind := range r.Types {
			if !contains(eventTypes, kind) {
				return fmt.Errorf("unsupported event type %q", kind)
			}
		}
		for _, sk := range r.Skills {
			if strings.TrimSpace(sk) == "" {
				return fmt.Errorf("skill name required")
			}
		}
	}
	for _, p := range c.PriorityLabels {
		if p < 0 || p > 3 {
			return fmt.Errorf("priority must be P0–P3")
		}
	}
	if strings.TrimSpace(c.PriorityInstructions) == "" {
		return fmt.Errorf("priority instructions required")
	}
	return nil
}
func (s *Service) Propose(ctx context.Context, after *Settings, native *NativeEdit) (Proposal, error) {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.propose(ctx, after, native)
}
func (s *Service) propose(ctx context.Context, after *Settings, native *NativeEdit) (Proposal, error) {
	before, err := s.Settings(ctx)
	if err != nil {
		return Proposal{}, err
	}
	if (after == nil) == (native == nil) {
		return Proposal{}, fmt.Errorf("propose either application settings or native edits")
	}
	p := Proposal{ID: app.ID(), State: "proposed", Base: digest(before), Before: before, After: before, Created: s.Now()}
	if after != nil {
		if err = validateSettings(*after); err != nil {
			return p, err
		}
		p.After = *after
		p.Diff = settingsDiff(before, *after)
	} else {
		if native.CWD == "" || !filepath.IsAbs(native.CWD) || native.File == "" || !filepath.IsAbs(native.File) || native.Version == "" || len(native.Edits) == 0 {
			return p, fmt.Errorf("native edits need inspected cwd, absolute layer file, version and edits")
		}
		for _, edit := range native.Edits {
			if edit.KeyPath == "" || (edit.MergeStrategy != "replace" && edit.MergeStrategy != "upsert") {
				return p, fmt.Errorf("native edits need key paths and merge strategies")
			}
		}
		actual, err := s.nativeConfig(ctx, native.CWD)
		if err != nil {
			return p, err
		}
		if err = checkLayer(actual, *native); err != nil {
			return p, err
		}
		copy := *native
		copy.Before = actual
		p.Native = &copy
		p.Diff = "--- native configuration layers\n" + string(actual) + "\n+++ proposed native key edits\n" + string(bytes(copy.Edits))
	}
	return p, s.Store.Put(ctx, "factory-proposal", p.ID, p, "configuration.proposed")
}

// Include only changed top-level settings. Values remain complete, so the owner
// can review repository/rule replacements without unrelated settings noise.
func settingsDiff(before, after Settings) string {
	var old, next map[string]json.RawMessage
	_ = json.Unmarshal(bytes(before), &old)
	_ = json.Unmarshal(bytes(after), &next)
	keys := map[string]bool{}
	for k := range old {
		keys[k] = true
	}
	for k := range next {
		keys[k] = true
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	var diff strings.Builder
	for _, k := range ordered {
		if string(old[k]) == string(next[k]) {
			continue
		}
		fmt.Fprintf(&diff, "%s\n", k)
		for i, value := range []json.RawMessage{old[k], next[k]} {
			if len(value) == 0 {
				value = json.RawMessage("null")
			}
			var formatted strings.Builder
			var v any
			_ = json.Unmarshal(value, &v)
			b, _ := json.MarshalIndent(v, "", "  ")
			prefix := "- "
			if i == 1 {
				prefix = "+ "
			}
			for _, line := range strings.Split(string(b), "\n") {
				formatted.WriteString(prefix + line + "\n")
			}
			diff.WriteString(formatted.String())
		}
	}
	if diff.Len() == 0 {
		return "No settings changes."
	}
	return diff.String()
}
func checkLayer(data json.RawMessage, n NativeEdit) error {
	var config struct {
		Layers []struct {
			Name struct {
				File string
				Type string
			}
			Version        string
			DisabledReason *string `json:"disabledReason"`
		}
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	for _, l := range config.Layers {
		if l.Name.File == n.File && l.Version == n.Version && l.DisabledReason == nil && (l.Name.Type == "user" || l.Name.Type == "project") {
			return nil
		}
	}
	return fmt.Errorf("native configuration layer is unavailable, managed or stale")
}

// Confirm is exposed only to the owner UI/API, never as a configuration-agent tool.
func (s *Service) Confirm(ctx context.Context, id string, confirm bool) (Proposal, error) {
	release := s.lockOperations("proposal:" + id)
	defer release()
	s.configMu.RLock()
	p, err := s.confirm(ctx, id, confirm)
	s.configMu.RUnlock()
	if err != nil || !confirm {
		return p, err
	}
	err = s.applyPending(ctx)
	_ = s.Store.Get(ctx, "factory-proposal", id, &p)
	return p, err
}
func (s *Service) confirm(ctx context.Context, id string, confirm bool) (Proposal, error) {
	var p Proposal
	if err := s.Store.Get(ctx, "factory-proposal", id, &p); err != nil {
		return p, err
	}
	if p.State != "proposed" {
		return p, fmt.Errorf("proposal already decided")
	}
	if !confirm {
		p.State = "rejected"
		return p, s.Store.Put(ctx, "factory-proposal", id, p, "configuration.rejected")
	}
	c, err := s.Settings(ctx)
	if err != nil {
		return p, err
	}
	if digest(c) != p.Base {
		return p, fmt.Errorf("proposal is stale; inspect and propose again")
	}
	if p.Native != nil {
		actual, e := s.nativeConfig(ctx, p.Native.CWD)
		if e != nil {
			return p, e
		}
		if digest(actual) != digest(p.Native.Before) {
			return p, fmt.Errorf("native layers changed; proposal is stale")
		}
		if e = checkLayer(actual, *p.Native); e != nil {
			return p, e
		}
	}
	p.State = "confirmed"
	if err = s.Store.Put(ctx, "factory-proposal", id, p, "configuration.confirmed"); err != nil {
		return p, err
	}
	return p, nil
}
func (s *Service) applyPending(ctx context.Context) error {
	if !s.configMu.TryLock() {
		return nil
	}
	defer s.configMu.Unlock()
	return s.applyConfirmed(ctx)
}
func (s *Service) applyConfirmed(ctx context.Context) error {
	ps, err := store.Select[Proposal](ctx, s.Store, "factory-proposal", "state", "confirmed")
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		return nil
	}
	active, err := s.Store.ActiveSession(ctx)
	if err != nil {
		return err
	}
	if active {
		return nil
	}
	for _, p := range ps {
		if p.State != "confirmed" {
			continue
		}
		current, err := s.Settings(ctx)
		if err != nil {
			return err
		}
		if digest(current) != p.Base {
			p.State = "stale"
			if err = s.Store.Put(ctx, "factory-proposal", p.ID, p, "configuration.stale"); err != nil {
				return err
			}
			continue
		}
		if p.Native != nil {
			actual, e := s.nativeConfig(ctx, p.Native.CWD)
			if e != nil {
				return e
			}
			if digest(actual) != digest(p.Native.Before) {
				p.State = "stale"
				if err = s.Store.Put(ctx, "factory-proposal", p.ID, p, "configuration.stale"); err != nil {
					return err
				}
				continue
			}
			// Starting a client cannot write configuration. Leave the confirmed
			// proposal retryable on failure; claim only immediately before the write.
			c, e := s.Start(s.ctx, Agent{}, nil)
			if e != nil {
				return e
			}
			p.State = "applying"
			if err = s.Store.Put(ctx, "factory-proposal", p.ID, p, ""); err != nil {
				_ = c.Close()
				return err
			}
			e = c.Call(ctx, "config/batchWrite", map[string]any{"filePath": p.Native.File, "expectedVersion": p.Native.Version, "edits": p.Native.Edits, "reloadUserConfig": false}, nil)
			_ = c.Close()
			if e != nil {
				p.State = "uncertain"
				_ = s.Store.Put(ctx, "factory-proposal", p.ID, p, "configuration.uncertain")
				return e
			}
		}
		// Read current bindings per proposal. Earlier native proposals may have
		// already retired their connections while this safe boundary is held.
		sessions, err := s.Sessions(ctx)
		if err != nil {
			return err
		}
		questions, err := s.Questions(ctx)
		if err != nil {
			return err
		}
		for index := range sessions {
			v := &sessions[index]
			settings := p.After
			if v.Kind == "configuration" {
				v.Agent = settings.ConfigurationAgent
			}
			if v.Kind == "priority" {
				v.Agent = settings.PriorityAgent
			}
			if agent, ok := settings.ItemAgents[v.ItemID]; ok {
				v.Agent = agent
			} else if _, previous := p.Before.ItemAgents[v.ItemID]; previous {
				if repo, ok := repository(settings, v.Repository); ok {
					v.Agent = chosen(settings, repo)
				}
			}
			v.Epoch = ""
			v.TurnID = ""
		}
		p.State = "applied"
		if err = s.Store.Write(ctx, func(t *store.Tx) error {
			if p.Native == nil {
				if err := t.Put("factory-settings", "effective", p.After); err != nil {
					return err
				}
				if err := s.projectSettingsTx(ctx, t, p.After); err != nil {
					return err
				}
			}
			for _, v := range sessions {
				if err := t.Put("factory-session", v.ID, v); err != nil {
					return err
				}
			}
			for _, q := range questions {
				if q.Status == "pending" || q.Status == "sending" {
					q.Status = "stale"
					if err := t.Put("factory-question", q.ID, q); err != nil {
						return err
					}
				}
			}
			if err := t.Put("factory-proposal", p.ID, p); err != nil {
				return err
			}
			return t.Event("configuration.applied", p.ID, map[string]string{"state": "applied"})
		}); err != nil {
			return err
		}
		// Callbacks wait for configMu and must see the committed retired epochs.
		for _, v := range sessions {
			if client := s.client(v.ID); client != nil {
				s.dropClient(v.ID)
				_ = client.Close()
			}
		}

	}
	return nil
}
func (s *Service) projectSettings(ctx context.Context) error {
	c, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	return s.Store.Write(ctx, func(t *store.Tx) error { return s.projectSettingsTx(ctx, t, c) })
}
func (s *Service) projectSettingsTx(ctx context.Context, t *store.Tx, c Settings) error {
	old := domain.Defaults()
	if err := t.Get("config", "settings", &old); err != nil && err != store.ErrNotFound {
		return err
	}
	old.PriorityLabels = c.PriorityLabels
	old.GitHubRequestsPerHour = c.GitHubRequestsPerHour
	old.GitHubReserve = c.GitHubReserve
	old.GitHubPollSeconds = c.GitHubPollSeconds
	old.GitHubRepositoryPollSeconds = c.GitHubRepositoryPollSeconds
	old.GitHubDetailPollSeconds = c.GitHubDetailPollSeconds
	old.AssessmentPrompt = c.PriorityInstructions
	old.PolicyVersion = digest([]any{c.PriorityLabels, c.PriorityInstructions, c.PriorityAgent, c.PrioritySkill})
	if err := t.Put("config", "settings", old); err != nil {
		return err
	}
	for _, r := range c.Repositories {
		var repo domain.Repository
		_ = t.Get("repository", r.ID, &repo)
		repo.ID = r.ID
		repo.Checkout = r.Checkout
		repo.Base = r.Base
		repo.Paused = r.Paused
		if err := t.Put("repository", r.ID, repo); err != nil {
			return err
		}
	}
	records, err := s.Store.Records(ctx, "repository")
	if err != nil {
		return err
	}
	for id := range records {
		if _, ok := repository(c, id); !ok {
			if err = t.Delete("repository", id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) AssessNext(ctx context.Context) error {
	if !s.priorityMu.TryLock() {
		return nil
	}
	defer s.priorityMu.Unlock()
	if !s.configMu.TryRLock() {
		return nil
	}
	defer s.configMu.RUnlock()
	c, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if c.PrioritySkill == "" {
		return nil
	}
	ss, err := store.List[Session](ctx, s.Store, "factory-session-runtime")
	if err != nil {
		return err
	}
	for _, v := range ss {
		if v.Kind != "priority" && (v.Execution == "running" || v.Execution == "starting") {
			return nil
		}
	}
	entries, err := s.App.InboxSummaries(ctx, "open")
	if err != nil {
		return err
	}
	policy, err := s.App.Config(ctx)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.PrioritySource == "override" || entry.PrioritySource == "label" {
			continue
		}
		// A valid compact assessment is enough to skip cached work. Decode full
		// source history only for the one item actually selected for a turn.
		if entry.PrioritySource == "inference" {
			continue
		}
		contextID := s.App.AssessmentContext(ctx, entry.Item)
		a := entry.Assessment
		if a.Revision == entry.Item.Revision && a.Context == contextID && a.Policy == policy.PolicyVersion && (a.Status == "complete" || s.Now().Before(a.RetryAt)) {
			continue
		}
		var item domain.Item
		if err = s.Store.Get(ctx, "item", entry.Item.ID, &item); err != nil {
			return err
		}
		id := "priority:" + item.Repository
		release := s.lockOperations(id)
		defer release()
		v, e := s.Session(ctx, id)
		if e == store.ErrNotFound {
			v = newSession(id, "priority", "", item.Repository, c.PriorityAgent, s.Now())
		} else if e != nil {
			return e
		}
		if v.Execution == "running" || v.Execution == "waiting" || v.Hold {
			return nil
		}
		a = domain.Assessment{ID: item.ID, Revision: item.Revision, Context: contextID, Policy: policy.PolicyVersion, Status: "running", RetryAt: s.Now().Add(5 * time.Minute)}
		if err = s.Store.Put(ctx, "assessment", item.ID, a, "assessment.changed"); err != nil {
			return err
		}
		instructions := c.PriorityInstructions + "\n" + string(bytes(map[string]any{"item": item, "revision": item.Revision, "context": contextID, "policy": policy.PolicyVersion, "related": s.priorityContext(ctx, item.ID)}))
		v.Pending = []Pending{{Event: Event{ID: app.ID(), Context: item}, Actions: []Action{{Instructions: instructions, Skills: []string{c.PrioritySkill}}}}}
		if err = s.save(ctx, v); err != nil {
			return err
		}
		err = s.flush(ctx, &v, "Assess the supplied item and record its priority with the local tool.", true)
		if err != nil {
			a.Status = "error"
			a.Error = err.Error()
			_ = s.Store.Put(ctx, "assessment", item.ID, a, "assessment.changed")
			v.Error = err.Error()
			_ = s.save(ctx, v)
		}
		return err
	}
	return nil
}

func (s *Service) priorityContext(ctx context.Context, id string) []map[string]any {
	links, err := store.List[domain.Relationship](ctx, s.Store, "relationship")
	if err != nil {
		return nil
	}
	out := []map[string]any{}
	for _, link := range links {
		target := ""
		if link.Source == id {
			target = link.Target
		} else if link.Target == id {
			target = link.Source
		}
		if target == "" {
			continue
		}
		row := map[string]any{"relationship": link}
		var item domain.Item
		if s.Store.Get(ctx, "item", target, &item) == nil {
			row["item"] = item
		}
		var ticket domain.Ticket
		if s.Store.Get(ctx, "ticket", target, &ticket) == nil {
			row["ticket"] = ticket
		}
		out = append(out, row)
	}
	return out
}
