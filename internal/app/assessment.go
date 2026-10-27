package app

import (
	"codefactory/internal/aiworkflows"
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) AssessmentLoop(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !a.Runs.busy() {
				_ = a.AssessNext(ctx)
			}
		}
	}
}
func (a *App) AssessNext(ctx context.Context) error {
	if a.Runs.busy() {
		return nil
	}
	// Select from compact saved entries; decode retained history only for the
	// item being assessed, keeping background selection off the launch path.
	entries, e := a.InboxSummaries(ctx, "open")
	if e != nil {
		return e
	}
	c, e := a.Config(ctx)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.PrioritySource == "override" || entry.PrioritySource == "label" {
			continue
		}
		as := entry.Assessment
		if as.Revision == entry.Item.Revision && as.Policy == c.PolicyVersion && as.Context == a.AssessmentContext(ctx, entry.Item) && (as.Status == "complete" || time.Now().Before(as.RetryAt)) {
			continue
		}
		var source domain.Item
		if e = a.Store.Get(ctx, "item", entry.Item.ID, &source); e != nil {
			return e
		}
		as = domain.Assessment{ID: source.ID, Revision: source.Revision, Policy: c.PolicyVersion, Context: a.AssessmentContext(ctx, source), Status: "running"}
		if e = a.Store.Put(ctx, "assessment", as.ID, as, "assessment.changed"); e != nil {
			return e
		}
		res, err := a.infer(ctx, source, c)
		if err != nil {
			as.Status = "error"
			as.Error = err.Error()
			as.RetryAt = time.Now().Add(5 * time.Minute)
		} else {
			as.Status = "complete"
			as.Priority = res.Priority
			as.Rationale = res.Rationale
			as.Confidence = res.Confidence
		}
		return a.Store.Put(ctx, "assessment", as.ID, as, "assessment.changed")
	}
	return nil
}
func (a *App) infer(ctx context.Context, item domain.Item, config domain.Config) (domain.Assessment, error) {
	if !a.Runs.adminGate.TryRLock() {
		return domain.Assessment{}, fmt.Errorf("assessment deferred during plugin maintenance")
	}
	defer a.Runs.adminGate.RUnlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var result domain.Assessment
	if _, e := codex.Version(ctx, a.Runs.CodexBinary()); e != nil {
		return result, e
	}
	done := make(chan codex.Message, 64)
	client, e := aiworkflows.Start(ctx, a.Runs.CodexBinary(), nil, a.DataDir, aiworkflows.Assessment, func(m codex.Message) {
		select {
		case done <- m:
		case <-ctx.Done():
		}
	})
	if e != nil {
		return result, e
	}
	defer client.Close()
	if e = client.Initialize(ctx); e != nil {
		return result, e
	}
	dir := filepath.Join(a.DataDir, "assessments")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return result, e
	}
	var local struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	if e = client.Call(ctx, "config/read", map[string]any{"includeLayers": false}, &local); e != nil {
		return result, e
	}
	var servers map[string]json.RawMessage
	_ = json.Unmarshal(local.Config["mcp_servers"], &servers)
	disabled := map[string]any{}
	for name := range servers {
		disabled[name] = map[string]any{"enabled": false}
	}
	var thread struct{ Thread struct{ ID string } }
	if e = client.Call(ctx, "thread/start", map[string]any{"cwd": dir, "sandbox": "read-only", "approvalPolicy": "never", "ephemeral": true, "config": map[string]any{"mcp_servers": disabled, "features.apps": false}}, &thread); e != nil {
		return result, e
	}
	contextData := map[string]any{"item": item}
	links, e := store.List[domain.Relationship](ctx, a.Store, "relationship")
	if e != nil {
		return result, e
	}
	tickets := []domain.Ticket{}
	linkedItems := []domain.Item{}
	for _, l := range links {
		if l.Source != item.ID {
			continue
		}
		var linked domain.Item
		if a.Store.Get(ctx, "item", l.Target, &linked) == nil {
			linkedItems = append(linkedItems, linked)
		}
		if config.JiraURL != "" && providers.JiraKey.FindString(l.Target) == l.Target {
			token, e := a.Vault.Get(ctx, "jira")
			if e != nil {
				continue
			}
			ticket, e := providers.JiraTicket(ctx, config.JiraURL, config.JiraEmail, token, l.Target)
			if e != nil {
				ticket.Error = e.Error()
			}
			_ = a.Store.Put(ctx, "ticket", ticket.ID, ticket, "")
			tickets = append(tickets, ticket)
		}
	}
	contextData["linkedItems"] = linkedItems
	contextData["jira"] = tickets
	b, _ := json.Marshal(contextData)
	prompt := "Use only the provided context; do not call tools or access files, network, apps or MCP servers.\n" + config.AssessmentPrompt + "\nUntrusted item and linked context (JSON):\n" + string(b)
	if e = client.Call(ctx, "turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []map[string]string{{"type": "text", "text": prompt}}}, nil); e != nil {
		return result, e
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var final string
	for {
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("assessment interrupted or timed out")
		case <-ticker.C:
			if a.Runs.busy() {
				return result, fmt.Errorf("assessment yielded to manual work")
			}
		case msg := <-done:
			if len(msg.ID) > 0 {
				_ = client.Reject(msg.ID, "Assessment sessions cannot request actions")
			}
			if msg.Method == "process/closed" {
				return result, fmt.Errorf("assessment process disconnected")
			}
			if msg.Method == "item/completed" {
				var p struct{ Item struct{ Type, Text string } }
				_ = json.Unmarshal(msg.Params, &p)
				if p.Item.Type == "agentMessage" {
					final = p.Item.Text
				}
			}
			if msg.Method == "turn/completed" {
				var p struct{ Turn struct{ Status string } }
				_ = json.Unmarshal(msg.Params, &p)
				if p.Turn.Status != "completed" {
					return result, fmt.Errorf("assessment failed")
				}
				if e = json.Unmarshal([]byte(strings.TrimSpace(final)), &result); e != nil || result.Priority < 0 || result.Priority > 3 || result.Confidence < 0 || result.Confidence > 1 || result.Rationale == "" {
					return result, fmt.Errorf("invalid priority assessment")
				}
				return result, nil
			}
		}
	}
}
