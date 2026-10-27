package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type UIStateInput struct {
	Views     []string `json:"views,omitempty"`
	Queue     string   `json:"queue,omitempty"`
	Filter    string   `json:"filter,omitempty"`
	Search    string   `json:"search,omitempty"`
	Offset    int      `json:"offset,omitempty"`
	Limit     int      `json:"limit,omitempty"`
	ID        string   `json:"id,omitempty"`
	Freshness string   `json:"freshness,omitempty"`
}
type WatchUIInput struct {
	After     int64  `json:"after"`
	WaitMs    int    `json:"waitMs,omitempty"`
	Freshness string `json:"freshness,omitempty"`
}
type uiProjection struct {
	cursor    int64
	published time.Time
	data      map[string]json.RawMessage
	inbox     []domain.InboxEntry
	counts    map[string]int
}
type uiInvalidation struct {
	cursor  int64
	changes map[string]map[string]bool
}
type uiState struct {
	mu      sync.Mutex
	current *uiProjection
	changed chan struct{}
	history []uiInvalidation
	err     error
}

// All projection work belongs to this worker, never to an opening/read request.
// ReadView binds both the derived state and its cursor to the same WAL snapshot.
func (s *Server) startUIState() {
	s.ui = &uiState{changed: make(chan struct{})}
	go func() {
		ctx := s.App.Store.Context()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			s.rebuildUIState(ctx)
			select {
			case <-ctx.Done():
				return
			case <-s.App.Store.Changes():
				// Publish one coherent result for a burst of related commits.
				timer := time.NewTimer(16 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			case <-ticker.C: // Reconcile commits made by other processes.
			}
		}
	}()
}
func (s *Server) rebuildUIState(ctx context.Context) {
	s.ui.mu.Lock()
	old := s.ui.current
	s.ui.mu.Unlock()
	latest, err := s.App.Store.UILatest(ctx)
	if err != nil || old != nil && latest == old.cursor {
		return
	}
	var next *uiProjection
	var changes map[string]map[string]bool
	err = s.App.Store.ReadView(ctx, func(view *store.Store) error {
		cursor, err := view.UILatest(ctx)
		if err != nil {
			return err
		}
		// A projection has no providers, manager, vault or diagnostics capability.
		reader := &Server{App: &app.App{Store: view, SavedOnly: true}}
		data, err := reader.snapshot(ctx)
		if err != nil {
			return err
		}
		config, err := reader.App.Config(ctx)
		if err != nil {
			return err
		}
		usage, err := providers.SavedGitHubUsage(ctx, view, config.GitHubRequestsPerHour, config.GitHubReserve)
		if err != nil {
			return err
		}
		status, err := reader.App.GitHubStatus(ctx)
		if err != nil {
			return err
		}
		status.GitHubUsage = usage
		data["githubUsage"] = status
		next = &uiProjection{cursor: cursor, published: time.Now().UTC(), data: map[string]json.RawMessage{}, counts: map[string]int{}}
		next.inbox = data["inbox"].([]domain.InboxEntry)
		for _, entry := range next.inbox {
			for _, queue := range entry.Queues {
				next.counts[queue]++
			}
		}
		for key, value := range data {
			if key == "cursor" || key == "inbox" {
				continue
			}
			b, err := json.Marshal(value)
			if err != nil {
				return err
			}
			next.data[key] = b
		}
		after := int64(0)
		if old != nil {
			after = old.cursor
		}
		changes, err = view.UIChanges(ctx, after, cursor)
		return err
	})
	if err != nil || ctx.Err() != nil {
		s.ui.mu.Lock()
		s.ui.err = err
		close(s.ui.changed)
		s.ui.changed = make(chan struct{})
		s.ui.mu.Unlock()
		return
	}
	s.ui.mu.Lock()
	s.ui.current = next
	s.ui.err = nil
	s.ui.history = append(s.ui.history, uiInvalidation{cursor: next.cursor, changes: changes})
	if len(s.ui.history) > 256 {
		s.ui.history = s.ui.history[len(s.ui.history)-256:]
	}
	close(s.ui.changed)
	s.ui.changed = make(chan struct{})
	s.ui.mu.Unlock()
}
func (s *Server) publishedUI(ctx context.Context) (*uiProjection, error) {
	for {
		s.ui.mu.Lock()
		p, changed, err := s.ui.current, s.ui.changed, s.ui.err
		s.ui.mu.Unlock()
		if p != nil {
			return p, nil
		}
		if err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.App.Store.Context().Done():
			return nil, fmt.Errorf("store closed")
		case <-changed:
		}
	}
}
func (s *Server) readUIState(ctx context.Context, in UIStateInput) (map[string]any, error) {
	if in.Offset < 0 || in.Limit < 0 {
		return nil, fmt.Errorf("invalid pagination")
	}
	p, err := s.publishedUI(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"cursor": p.cursor, "counts": p.counts, "freshness": map[string]any{"publishedAt": p.published, "saved": true}}
	if in.ID != "" {
		for _, entry := range p.inbox {
			if entry.Item.ID == in.ID {
				out["selectedEntry"] = entry
				break
			}
		}
	}
	views := in.Views
	if len(views) == 0 {
		views = []string{"inbox"}
	}
	for _, view := range views {
		var keys []string
		switch view {
		case "selection": // Selected entries remain reachable outside the current page.
		case "shell":
			keys = []string{"viewer", "repositories", "profiles", "config"}
		case "messages":
			keys = []string{"runs", "attention", "questionRecoveries"}
		case "launch":
			keys = []string{"repositories", "profiles", "config", "skills"}
		case "settings":
			keys = []string{"repositories", "profiles", "config", "skills", "githubUsage"}
		case "inbox":
			rows := []domain.InboxEntry{}
			for _, entry := range p.inbox {
				if matchUIEntry(entry, in) {
					rows = append(rows, entry)
				}
			}
			total := len(rows)
			offset := min(in.Offset, total)
			limit := in.Limit
			if limit == 0 || limit > 200 {
				limit = 50
			}
			end := min(offset+limit, total)
			out["inbox"] = rows[offset:end]
			out["page"] = map[string]any{"offset": offset, "limit": limit, "total": total, "hasMore": end < total}
		case "reviews":
			keys = []string{"reviewVersions"}
		case "item":
			// Targeted saved detail reads do not call the live App's observation scheduler.
			err := s.App.Store.ReadView(ctx, func(view *store.Store) error {
				reader := &app.App{Store: view, SavedOnly: true}
				detail, err := reader.ItemDetail(ctx, in.ID)
				if err != nil {
					return err
				}
				out["detail"] = detail
				out["detailCursor"], err = view.UILatest(ctx)
				return err
			})
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unknown UI view %q", view)
		}
		for _, key := range keys {
			out[key] = p.data[key]
		}
	}
	return out, nil
}
func matchUIEntry(e domain.InboxEntry, in UIStateInput) bool {
	if !app.ContainsQueue(e, in.Queue) {
		return false
	}
	switch in.Filter {
	case "", "all":
	case "open":
		if e.Item.State != "open" {
			return false
		}
	case "pending":
		if e.Item.State != "open" || e.HasPullRequest {
			return false
		}
	case "new":
		if !e.User.New {
			return false
		}
	case "unread":
		if !e.Unread {
			return false
		}
	case "acted-on":
		if len(e.User.Actions) == 0 {
			return false
		}
	default:
		return false
	}
	return strings.Contains(strings.ToLower(e.Item.ID+" "+e.Item.Title+" "+e.Item.Body+" "+e.Item.Author+" "+strings.Join(e.Item.Labels, " ")), strings.ToLower(in.Search))
}
func (s *Server) watchUIChanges(ctx context.Context, in WatchUIInput) (map[string]any, error) {
	if in.After < 0 || in.WaitMs < 0 || in.WaitMs > 25000 {
		return nil, fmt.Errorf("invalid watch cursor or wait")
	}
	wait := in.WaitMs
	if wait == 0 {
		wait = 25000
	}
	timer := time.NewTimer(time.Duration(wait) * time.Millisecond)
	defer timer.Stop()
	for {
		s.ui.mu.Lock()
		p, changed := s.ui.current, s.ui.changed
		if p != nil && p.cursor != in.After {
			resync := in.After > p.cursor
			if len(s.ui.history) > 0 && in.After < s.ui.history[0].cursor {
				resync = true
			}
			domains := map[string]map[string]bool{}
			if !resync {
				for _, event := range s.ui.history {
					if event.cursor <= in.After {
						continue
					}
					for domain, ids := range event.changes {
						if domains[domain] == nil {
							domains[domain] = map[string]bool{}
						}
						for id := range ids {
							domains[domain][id] = true
						}
					}
				}
			}
			out := map[string][]string{}
			for domain, ids := range domains {
				for id := range ids {
					out[domain] = append(out[domain], id)
				}
				sort.Strings(out[domain])
			}
			s.ui.mu.Unlock()
			return map[string]any{"cursor": p.cursor, "resync": resync, "changes": out}, nil
		}
		s.ui.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.App.Store.Context().Done():
			return nil, fmt.Errorf("store closed")
		case <-timer.C:
			return map[string]any{"cursor": in.After, "resync": false, "changes": map[string][]string{}}, nil
		case <-changed:
		}
	}
}
