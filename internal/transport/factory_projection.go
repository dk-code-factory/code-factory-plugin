package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/factory"
	"codefactory/internal/store"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type factoryStateInput struct {
	Empty
	View         string `json:"view,omitempty"`
	Search       string `json:"search,omitempty"`
	Filter       string `json:"filter,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	ItemID       string `json:"itemId,omitempty"`
	SinceVersion *int64 `json:"sinceVersion,omitempty"`
}
type sessionSummary struct {
	ID            string        `json:"id"`
	ItemID        string        `json:"itemId,omitempty"`
	Repository    string        `json:"repository,omitempty"`
	Kind          string        `json:"kind"`
	Agent         factory.Agent `json:"agent"`
	Execution     string        `json:"execution"`
	Reported      string        `json:"reported,omitempty"`
	Summary       string        `json:"summary,omitempty"`
	Hold          bool          `json:"hold"`
	Error         string        `json:"error,omitempty"`
	Due           time.Time     `json:"due"`
	ThreadID      string        `json:"threadId,omitempty"`
	ThreadPending bool          `json:"threadPending,omitempty"`
	BatchID       string        `json:"batchId,omitempty"`
	PendingCount  int           `json:"pendingCount"`
	DeliveryState string        `json:"deliveryState,omitempty"`
}

func factoryDetailVersion(revision string, links []domain.Relationship) string {
	data, _ := json.Marshal(struct {
		Revision string
		Links    []domain.Relationship
	}{revision, links})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func factoryState(ctx context.Context, a *app.App, f *factory.Service, in factoryStateInput) (any, error) {
	if in.View == "" {
		in.View = "issues"
	}
	if in.Filter == "" {
		in.Filter = "open"
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return nil, fmt.Errorf("offset must be nonnegative and limit 1–200")
	}
	switch in.Filter {
	case "open", "all", "new", "unread":
	default:
		return nil, fmt.Errorf("unknown inbox filter")
	}
	switch in.View {
	case "issues", "working", "mine", "review", "messages", "configuration", "conversation", "events":
	default:
		return nil, fmt.Errorf("unknown factory view")
	}
	// Capture before projection: a concurrent mutation must remain visible to the next poll.
	version, err := a.Store.UILatest(ctx)
	if err != nil {
		return nil, err
	}
	if in.SinceVersion != nil && *in.SinceVersion == version {
		return map[string]any{"unchanged": true, "version": version}, nil
	}
	inbox := []domain.InboxEntry{}
	total := 0
	visible := map[string]bool{in.ItemID: true}
	if in.View == "configuration" {
		visible["configuration"] = true
	}
	if in.View == "issues" || in.View == "working" || in.View == "mine" || in.View == "review" {
		entries, err := a.InboxSummaries(ctx, "all")
		if err != nil {
			return nil, err
		}
		var matches map[string]bool
		if strings.TrimSpace(in.Search) != "" {
			matches, err = a.Store.SearchIDs(ctx, "item", strings.TrimSpace(in.Search))
			if err != nil {
				return nil, err
			}
		}
		for _, entry := range entries {
			if !app.ContainsQueue(entry, in.View) || in.Filter == "open" && entry.Item.State != "open" || in.Filter == "new" && !entry.User.New || in.Filter == "unread" && !entry.Unread || matches != nil && !matches[entry.Item.ID] {
				continue
			}
			if total >= in.Offset && len(inbox) < in.Limit {
				entry.Item.Body = ""
				inbox = append(inbox, entry)
				visible[entry.Item.ID] = true
			}
			total++
		}
	}
	questions, err := store.Select[factory.Question](ctx, a.Store, "factory-question", "status", "pending")
	if err != nil {
		return nil, err
	}
	for _, q := range questions {
		visible[q.SessionID] = true
	}
	history, err := store.Recent[factory.Question](ctx, a.Store, "factory-question", 25, "$.params", "$.requestId", "$.epoch")
	if err != nil {
		return nil, err
	}
	for _, q := range history {
		if q.Status != "pending" {
			questions = append(questions, q)
		}
	}
	sessions, err := store.SessionSummaries[sessionSummary](ctx, a.Store)
	if err != nil {
		return nil, err
	}
	selected := []sessionSummary{}
	for _, s := range sessions {
		if visible[s.ID] {
			if s.BatchID != "" {
				var b struct{ State string }
				if err = a.Store.Get(ctx, "factory-batch", s.BatchID, &b); err != nil {
					return nil, err
				}
				s.DeliveryState = b.State
			}
			selected = append(selected, s)
		}
	}
	result := map[string]any{"version": version, "inbox": inbox, "inboxTotal": total, "offset": in.Offset, "limit": in.Limit, "sessions": selected, "questions": questions}
	if in.View == "conversation" && in.ItemID != "" {
		entries, e := a.InboxSummaries(ctx, "all")
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			if entry.Item.ID == in.ItemID {
				entry.Item.Body = ""
				links, e := store.Select[domain.Relationship](ctx, a.Store, "relationship", "related", in.ItemID)
				if e != nil {
					return nil, e
				}
				result["selectedEntry"] = struct {
					domain.InboxEntry
					DetailVersion string `json:"detailVersion"`
				}{entry, factoryDetailVersion(entry.Item.Revision, links)}
				break
			}
		}
	}
	if in.View == "configuration" {
		settings, e := f.Settings(ctx)
		if e != nil {
			return nil, e
		}
		proposals, e := store.Recent[factory.Proposal](ctx, a.Store, "factory-proposal", 50)
		if e != nil {
			return nil, e
		}
		result["settings"] = settings
		result["proposals"] = proposals
	}
	return result, nil
}
