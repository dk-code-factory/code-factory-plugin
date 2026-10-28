package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/store"
)

type TargetCandidate struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}
type TargetResolution struct {
	ItemID     string            `json:"itemId,omitempty"`
	RunID      string            `json:"runId,omitempty"`
	Candidates []TargetCandidate `json:"candidates,omitempty"`
	Status     string            `json:"status"`
}

func (a *App) ResolveTarget(ctx context.Context, target string) (TargetResolution, error) {
	out := TargetResolution{Status: "not_found"}
	target = strings.TrimSpace(target)
	if target == "" {
		return out, fmt.Errorf("target is required")
	}
	runs, err := store.Select[domain.Run](ctx, a.Store, "run", "id", target, "$.skills", "$.prompt", "$.profile", "$.sourceSnapshot", "$.reviewPromptSnapshot")
	if err != nil {
		return out, err
	}
	if len(runs) > 0 {
		return TargetResolution{ItemID: runs[0].ItemID, RunID: runs[0].ID, Status: "resolved"}, nil
	}
	if strings.Contains(target, "://") {
		u, err := url.Parse(target)
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil {
			return out, fmt.Errorf("target must be a GitHub issue/PR URL or synchronized identifier")
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 4 || (parts[2] != "pull" && parts[2] != "issues") {
			return out, fmt.Errorf("target must identify a GitHub issue or PR")
		}
		target = parts[0] + "/" + parts[1] + "#" + parts[3]
	}
	items, err := store.ListWithout[domain.Item](ctx, a.Store, "item", "$.body", "$.activity", "$.checks", "$.pr")
	if err != nil {
		return out, err
	}
	for _, i := range items {
		if strings.EqualFold(i.ID, target) || (!strings.Contains(target, "/") && strings.EqualFold(strings.TrimPrefix(i.ID, i.Repository[:strings.Index(i.Repository, "/")+1]), target)) {
			out.Candidates = append(out.Candidates, TargetCandidate{ID: i.ID, Title: i.Title, URL: i.URL})
		}
	}
	if len(out.Candidates) == 1 {
		out.ItemID = out.Candidates[0].ID
		out.Candidates = nil
		out.Status = "resolved"
	} else if len(out.Candidates) > 1 {
		out.Status = "ambiguous"
	}
	return out, nil
}

type TrackInput struct {
	Target          string `json:"target,omitempty" jsonschema:"Run ID, canonical item ID, GitHub URL or repository shorthand; omit to triage a queue."`
	Queue           string `json:"queue,omitempty"`
	State           string `json:"state,omitempty"`
	Limit           int    `json:"limit,omitempty" jsonschema:"Result or history page size, default 5, maximum 20."`
	Cursor          string `json:"cursor,omitempty"`
	AttentionCursor string `json:"attentionCursor,omitempty"`
	ReceiptCursor   string `json:"receiptCursor,omitempty"`
	Freshness       string `json:"freshness,omitempty" jsonschema:"New host read marker; never forces another GitHub request."`
}
type trackCursor struct {
	Scope string
	Last  string
}

func pageStart(cursor, scope string, ids []string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	var c trackCursor
	b, e := base64.RawURLEncoding.DecodeString(cursor)
	if len(cursor) > 4096 || e != nil || json.Unmarshal(b, &c) != nil || c.Scope != scope {
		return 0, fmt.Errorf("invalid tracking cursor for these filters")
	}
	for n, id := range ids {
		if c.Last == id {
			return n + 1, nil
		}
	}
	return 0, fmt.Errorf("tracking page changed; refresh without cursor")
}
func pageCursor(scope, last string) string {
	b, _ := json.Marshal(trackCursor{scope, last})
	return base64.RawURLEncoding.EncodeToString(b)
}
func runOrder(runs []domain.Run) {
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].Created.Equal(runs[j].Created) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].Created.After(runs[j].Created)
	})
}

// LatestRun matches the UI: newest started, preferring a retained workspace.
func LatestRun(runs []domain.Run) *domain.Run {
	ordered := append([]domain.Run{}, runs...)
	runOrder(ordered)
	var fallback *domain.Run
	for n := range ordered {
		r := &ordered[n]
		if r.Status == "prepared" {
			continue
		}
		if fallback == nil {
			fallback = r
		}
		if r.Status != "cleaned" {
			return r
		}
	}
	return fallback
}
func runSummary(r domain.Run) map[string]any {
	out := map[string]any{"id": r.ID, "itemId": r.ItemID, "profileId": r.Profile.ID, "profileName": r.Profile.Name, "status": r.Status, "error": r.Error, "threadId": r.ThreadID, "turnId": r.TurnID, "created": r.Created, "base": r.Base, "resumePending": r.ResumePending, "interruptRequested": r.InterruptRequested, "launchRetryAt": r.LaunchRetryAt}
	if r.ReviewWorkflow != "" {
		review := map[string]any{"workflow": r.ReviewWorkflow, "stage": r.ReviewStage, "complete": r.ReviewComplete, "artifact": r.ReviewArtifact, "preparedPostingPolicy": r.EffectiveAutoPostReview, "postingState": r.PostingState, "postingOperation": r.PostingOperation, "receipt": r.PostingReceipt, "omissions": r.ReviewOmissions}
		if r.ReviewResult != nil {
			review["findingCount"] = r.ReviewResult.FindingCount
			review["outcome"] = r.ReviewResult.Outcome
			review["skippedReviewers"] = r.ReviewResult.Skipped
			review["failedReviewers"] = r.ReviewResult.Failed
		}
		out["review"] = review
	}
	return out

}
func (a *App) TrackWork(ctx context.Context, in TrackInput) (map[string]any, error) {
	if in.Limit == 0 {
		in.Limit = 5
	}
	if in.Limit < 1 || in.Limit > 20 {
		return nil, fmt.Errorf("limit must be between 1 and 20")
	}
	if in.Queue != "" && in.Queue != "all" && in.Queue != "working" && in.Queue != "issues" && in.Queue != "mine" && in.Queue != "review" {
		return nil, fmt.Errorf("unknown queue")
	}
	if in.Target == "" {
		return a.trackQueue(ctx, in)
	}
	resolved, err := a.ResolveTarget(ctx, in.Target)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"resolution": resolved}
	if resolved.Status != "resolved" {
		return out, nil
	}
	rows, err := store.Select[domain.Item](ctx, a.Store, "item", "id", resolved.ItemID, "$.body", "$.activity", "$.checks", "$.pr.checks")
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, store.ErrNotFound
	}
	item := rows[0]
	out["item"] = map[string]any{"id": item.ID, "title": item.Title, "url": item.URL, "kind": item.Kind, "state": item.State, "head": item.Head, "draft": item.Draft, "updated": item.Updated, "pr": item.PR}
	observation, err := a.ItemObservation(ctx, item)
	if err != nil {
		return nil, err
	}
	out["observation"] = observation
	runs, err := store.Select[domain.Run](ctx, a.Store, "run", "itemId", item.ID, "$.skills", "$.prompt", "$.profile.prompt", "$.sourceSnapshot", "$.reviewPromptSnapshot")
	if err != nil {
		return nil, err
	}
	runOrder(runs)
	current := LatestRun(runs)
	if resolved.RunID != "" {
		for n := range runs {
			if runs[n].ID == resolved.RunID {
				current = &runs[n]
			}
		}
	}
	if current != nil {
		out["currentRun"] = runSummary(*current)
		delivery, err := a.MessageDelivery(ctx, current.ID)
		if err != nil {
			return nil, err
		}
		out["messageDelivery"] = delivery
		progress, err := a.Store.Progress(ctx, current.ID)
		if err != nil {
			return nil, err
		}
		out["latestOutput"] = progress
		expectedTurn := ""
		if current.Status == "running" || current.Status == "waiting" {
			expectedTurn = current.TurnID
		}
		out["continuation"] = map[string]any{"runId": current.ID, "expectedTurn": expectedTurn, "transcriptTool": "read_transcript", "after": 0, "projectionCursor": progress.Cursor}
	}
	ids := []string{}
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	scope := "runs:" + item.ID
	start, err := pageStart(in.Cursor, scope, ids)
	if err != nil {
		return nil, err
	}
	end := min(start+in.Limit, len(runs))
	history := []map[string]any{}
	for _, r := range runs[start:end] {
		history = append(history, runSummary(r))
	}
	out["runs"] = history
	out["runCount"] = len(runs)
	if end < len(runs) {
		out["nextCursor"] = pageCursor(scope, runs[end-1].ID)
	}
	// Questions for every run on this item remain exact; the page is independent
	// of retained-history pagination and can also be read via list_attention.
	attention, err := a.ListAttention(ctx, AttentionInput{ItemID: item.ID, RunID: resolved.RunID, Limit: in.Limit, Cursor: in.AttentionCursor})
	if err != nil {
		return nil, err
	}
	out["attention"] = attention
	if item.Kind == "pr" {
		reviews, err := a.readPRReviews(ctx, item.ID, true)
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for _, r := range reviews.Reviews {
			ids = append(ids, r.ID)
		}
		scope := "receipts:" + item.ID
		start, err := pageStart(in.ReceiptCursor, scope, ids)
		if err != nil {
			return nil, err
		}
		end := min(start+in.Limit, len(ids))
		receipts := []map[string]any{}
		for _, r := range reviews.Reviews[start:end] {
			headState := "unknown"
			if item.Head != "" && r.Head != "" {
				headState = "earlier"
				if item.Head == r.Head {
					headState = "current"
				}
			}
			receipts = append(receipts, map[string]any{"id": r.ID, "url": r.URL, "author": r.Author, "state": r.State, "head": r.Head, "headState": headState, "submittedAt": r.SubmittedAt, "commentCount": r.CommentCount})
		}
		out["publication"] = map[string]any{"receipts": receipts, "syncedAt": reviews.SyncedAt, "refresh": reviews.Refresh, "error": reviews.Error, "retryAt": reviews.RetryAt, "attribution": "all authors; not attributed to this run", "detailTool": "read_pr_reviews"}
		if end < len(ids) {
			out["nextReceiptCursor"] = pageCursor(scope, ids[end-1])
		}
	}
	return out, nil
}
func (a *App) trackQueue(ctx context.Context, in TrackInput) (map[string]any, error) {
	entries, err := a.InboxSummaries(ctx, in.State)
	if err != nil {
		return nil, err
	}
	filtered := []domain.InboxEntry{}
	for _, e := range entries {
		if ContainsQueue(e, in.Queue) {
			filtered = append(filtered, e)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Item.ID < filtered[j].Item.ID })
	ids := []string{}
	for _, e := range filtered {
		ids = append(ids, e.Item.ID)
	}
	scope := "queue:" + in.Queue + ":" + in.State
	start, err := pageStart(in.Cursor, scope, ids)
	if err != nil {
		return nil, err
	}
	end := min(start+in.Limit, len(ids))
	rows := []map[string]any{}
	for _, e := range filtered[start:end] {
		rows = append(rows, map[string]any{"id": e.Item.ID, "title": e.Item.Title, "url": e.Item.URL, "state": e.Item.State, "head": e.Item.Head, "queues": e.Queues, "work": e.Work, "priority": e.Priority, "unread": e.Unread, "observedAt": e.Item.DetailSyncedAt})
	}
	out := map[string]any{"items": rows, "total": len(ids), "readAt": time.Now().UTC()}
	if end < len(ids) {
		out["nextCursor"] = pageCursor(scope, ids[end-1])
	}
	return out, nil
}
