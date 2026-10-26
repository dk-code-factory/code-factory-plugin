package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var pullURL = regexp.MustCompile(`https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/([0-9]+)\b`)

func ContainsQueue(entry domain.InboxEntry, queue string) bool {
	if queue == "" || queue == "all" {
		return true
	}
	for _, q := range entry.Queues {
		if q == queue {
			return true
		}
	}
	return false
}

func (a *App) projectWork(ctx context.Context, items []domain.Item, links []domain.Relationship, entries []domain.InboxEntry) error {
	runs, err := store.ListWithout[domain.Run](ctx, a.Store, "run", "$.skills", "$.profile", "$.prompt", "$.reviewPromptSnapshot", "$.sourceSnapshot")
	if err != nil {
		return err
	}
	attention, err := store.List[domain.Attention](ctx, a.Store, "attention")
	if err != nil {
		return err
	}
	external, err := store.ListWithout[domain.Item](ctx, a.Store, "linked-pr", "$.activity", "$.checks", "$.body")
	if err != nil {
		return err
	}
	return a.projectWorkRecords(ctx, items, links, entries, runs, attention, external)
}

func (a *App) projectWorkRecords(ctx context.Context, items []domain.Item, links []domain.Relationship, entries []domain.InboxEntry, runs []domain.Run, attention []domain.Attention, external []domain.Item) error {
	var viewer domain.GitHubViewer
	if err := a.Store.Get(ctx, "identity", "github", &viewer); err != nil && err != store.ErrNotFound {
		return err
	}
	byID := map[string]domain.Item{}
	for _, i := range external {
		byID[i.ID] = i
	}
	for _, i := range items {
		byID[i.ID] = i
	}
	for index := range entries {
		e := &entries[index]
		e.Queues = []string{}
		e.LinkedPullRequests = []domain.PullRequest{}
		e.Work = domain.WorkSummary{Status: "Not started", RunIDs: []string{}}
		seen := map[string]int{}
		for _, l := range links {
			target := ""
			if l.Source == e.Item.ID {
				target = l.Target
			} else if l.Target == e.Item.ID {
				target = l.Source
			}
			if target == "" {
				continue
			}
			pr, known := byID[target]
			if known && pr.Kind != "pr" {
				continue
			}
			if !known && !(strings.Contains(l.Evidence, "github.com/") && strings.Contains(l.Evidence, "/pull/")) {
				continue
			}
			p := domain.PullRequest{ID: target, URL: l.Evidence, State: "unknown", ReviewDecision: "unknown", CI: "unknown", Checks: []domain.Check{}, Error: "PR information unavailable"}
			if known {
				p = summary(pr)
			}
			p.Implementation = l.Manual || l.Kind == "implementation"
			if n, ok := seen[target]; ok {
				e.LinkedPullRequests[n].Implementation = e.LinkedPullRequests[n].Implementation || p.Implementation
				continue
			}
			seen[target] = len(e.LinkedPullRequests)
			e.LinkedPullRequests = append(e.LinkedPullRequests, p)
		}
		sort.Slice(e.LinkedPullRequests, func(i, j int) bool { return e.LinkedPullRequests[i].ID < e.LinkedPullRequests[j].ID })
		e.HasPullRequest = len(e.LinkedPullRequests) > 0
		// Runtime state is not completion: idle/failed/cleaned work stays tracked.
		for _, r := range runs {
			if r.ItemID == e.Item.ID && r.Status != "prepared" {
				e.Work.Tracked = true
				e.Work.RunIDs = append(e.Work.RunIDs, r.ID)
				state := "Inactive"
				switch r.Status {
				case "deferred":
					state = "Waiting for GitHub"
				case "queued", "starting":
					state = "Starting"
				case "running", "waiting":
					state = "Active"
				case "failed":
					state = "Failed"
				case "interrupted":
					state = "Interrupted"
				case "cleaned":
					state = "Session closed"
				case "terminated":
					state = "Stopped"
				}
				if workRank(state) > workRank(e.Work.Status) {
					e.Work.Status = state
				}
				for _, at := range attention {
					if at.RunID == r.ID && at.Status == "pending" && (at.Origin == "async" || at.Origin == "text" || r.Status == "running" || r.Status == "waiting") {
						e.Work.AttentionCount++
						if at.Blocking {
							e.Work.Status = "Waiting for you"
						}
					}
				}
			}
		}
		e.Work.Complete = e.Item.State == "closed" || e.Item.State == "merged"
		if e.Item.Kind == "issue" {
			for _, p := range e.LinkedPullRequests {
				if p.Implementation && p.State == "merged" {
					e.Work.Complete = true
				}
			}
			e.Queues = append(e.Queues, "issues")
		}
		if e.Work.Tracked && !e.Work.Complete {
			e.Queues = append(e.Queues, "working")
		}
		if e.Item.Kind == "pr" && viewer.Login != "" {
			if strings.EqualFold(e.Item.Author, viewer.Login) {
				e.Queues = append(e.Queues, "mine")
			}
			requested := false
			for _, name := range e.Item.ReviewRequests {
				requested = requested || strings.EqualFold(name, viewer.Login)
			}
			for _, team := range e.Item.ReviewTeams {
				for _, mine := range viewer.Teams {
					requested = requested || strings.EqualFold(team, mine)
				}
			}
			if requested {
				e.Queues = append(e.Queues, "review")
			}
		}
	}
	return nil
}

func workRank(s string) int {
	return map[string]int{"Not started": 0, "Session closed": 1, "Stopped": 1, "Inactive": 2, "Interrupted": 3, "Failed": 4, "Waiting for GitHub": 5, "Starting": 5, "Active": 6, "Waiting for you": 7}[s]
}
func summary(i domain.Item) domain.PullRequest {
	if i.PR != nil {
		return *i.PR
	}
	return domain.PullRequest{ID: i.ID, Title: i.Title, URL: i.URL, State: i.State, Head: i.Head, Draft: i.Draft, ReviewDecision: "unknown", CI: "unknown", Checks: []domain.Check{}}
}

func retainPRMetadata(old domain.Item, next *domain.Item) {
	if next.PR != nil && next.PR.Error != "" {
		next.ClosingIssues = old.ClosingIssues
	}
	if next.PR != nil && next.PR.Error != "" && old.PR != nil && old.Head == next.Head {
		next.PR.ReviewDecision = old.PR.ReviewDecision
		next.PR.CI = old.PR.CI
		next.PR.SyncedAt = old.PR.SyncedAt
		next.ClosingIssues = old.ClosingIssues
	}
}

// RefreshPullRequests watches retained work and explicit references, including
// terminal PRs (which may reopen). No repository is configured as a side effect.
func (a *App) RefreshPullRequests(ctx context.Context) error {
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	links, err := store.List[domain.Relationship](ctx, a.Store, "relationship")
	if err != nil {
		return err
	}
	runs, err := store.ListWithout[struct{ ID, ItemID, Status string }](ctx, a.Store, "run", "$.skills", "$.profile", "$.prompt", "$.reviewPromptSnapshot", "$.sourceSnapshot")
	if err != nil {
		return err
	}
	tracked := map[string]bool{}
	retirement := map[string]bool{}
	for _, r := range runs {
		if r.Status != "prepared" {
			tracked[r.ItemID] = true
			if r.Status != "cleaned" && r.Status != "terminated" {
				retirement[r.ItemID] = true
			}
		}
	}
	// Accepted source closure is a local retirement obligation, even while
	// remote metadata is fresh, cooling down or paused after an interrupted pass.
	for id := range retirement {
		var saved domain.Item
		if a.Runs != nil && a.Store.Get(ctx, "item", id, &saved) == nil && closedWork(saved) {
			if err := a.Runs.stopClosedItem(ctx, saved); err != nil {
				return err
			}
		}
	}
	focuses, err := store.List[detailFocus](ctx, a.Store, "github-detail-focus")
	if err != nil {
		return err
	}
	for _, focus := range focuses {
		if time.Since(focus.SeenAt) < providers.GitHubDetailTTL {
			tracked[focus.ID] = true
		}
	}
	wanted := map[string]bool{}
	for _, l := range links {
		if !l.Manual && !strings.HasPrefix(l.ID, "run:") && !tracked[l.Source] && !tracked[l.Target] {
			continue
		}
		for _, id := range []string{l.Source, l.Target} {
			var item domain.Item
			if a.Store.Get(ctx, "item", id, &item) == nil && item.Kind == "pr" {
				wanted[id] = true
			}
		}
		for _, match := range pullURL.FindAllStringSubmatch(l.Evidence, -1) {
			wanted[match[1]+"#"+match[2]] = true
		}
	}
	for _, r := range runs {
		var i domain.Item
		if r.Status != "prepared" && a.Store.Get(ctx, "item", r.ItemID, &i) == nil && i.Kind == "pr" {
			wanted[i.ID] = true
		}
	}
	ids := []string{}
	for id := range wanted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	start := sort.SearchStrings(ids, a.prRefreshAfter)
	if start < len(ids) && ids[start] == a.prRefreshAfter {
		start++
	}
	ids = append(ids[start:], ids[:start]...)
	processed := 0
	for _, id := range ids {
		if processed >= 2 {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.prRefreshAfter = id
		parts := strings.SplitN(id, "#", 2)
		if len(parts) != 2 || !repoPattern.MatchString(parts[0]) {
			continue
		}
		number, err := strconv.Atoi(parts[1])
		if err != nil || number < 1 {
			continue
		}
		kind := "item"
		var old domain.Item
		if err = a.Store.Get(ctx, kind, id, &old); err == store.ErrNotFound {
			kind = "linked-pr"
			_ = a.Store.Get(ctx, kind, id, &old)
		} else if err != nil {
			return err
		}
		// The ordinary sync has already fetched these data on this cycle.
		if old.PR != nil && time.Now().Before(old.PR.RetryAt) {
			continue
		}
		if old.PR != nil && old.PR.Error == "" && time.Since(old.PR.SyncedAt) < providers.GitHubDetailTTL {
			continue
		}
		var repository domain.Repository
		if a.Store.Get(ctx, "repository", parts[0], &repository) == nil {
			if repository.Paused {
				continue
			}

		}
		processed++
		fresh, err := a.GitHub.PullRequest(ctx, parts[0], number)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if old.ID == "" {
				old = domain.Item{ID: id, Repository: parts[0], Number: number, Kind: "pr", URL: fmt.Sprintf("https://github.com/%s/pull/%d", parts[0], number), State: "unknown"}
			}
			p := summary(old)
			p.Error = err.Error()
			p.RetryAt = time.Now().Add(providers.GitHubDetailTTL)
			var rate *providers.RateLimitError
			if errors.As(err, &rate) {
				p.RetryAt = rate.Until
			}
			old.PR = &p
			if err = a.Store.Put(ctx, kind, id, old, "pr.changed"); err != nil {
				return err
			}
			continue
		}
		if fresh.ReviewObservedAt.Before(old.ReviewObservedAt) {
			continue
		}
		fresh.Activity = old.Activity
		retainPRMetadata(old, &fresh)
		fresh.Revision = fresh.ContentRevision()
		if err = a.Store.Write(ctx, func(t *store.Tx) error {
			if err := t.Put(kind, id, fresh); err != nil {
				return err
			}
			for _, l := range links {
				if l.Source == id && l.Kind == "implementation" && !l.Manual {
					if err := t.Delete("relationship", l.ID); err != nil {
						return err
					}
				}
			}
			for _, issue := range fresh.ClosingIssues {
				l := domain.Relationship{ID: "implementation:" + id + "->" + issue, Source: id, Target: issue, Kind: "implementation", Evidence: fresh.URL}
				if err := t.Put("relationship", l.ID, l); err != nil {
					return err
				}
			}
			return t.Event("pr.changed", id, map[string]string{"id": id})
		}); err != nil {
			return err
		}
		if a.Runs != nil {
			if err := a.Runs.stopClosedItem(ctx, fresh); err != nil {
				return err
			}
		}
	}
	return nil
}
