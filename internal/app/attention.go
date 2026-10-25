package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type AttentionInput struct {
	RunID     string `json:"runId,omitempty" jsonschema:"Optionally restrict pending requests to this run."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Page size from 1 to 100; defaults to 25."`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Use nextCursor from the previous page; omit to refresh from the start."`
	Freshness string `json:"freshness,omitempty" jsonschema:"Use a new marker on each fresh read to avoid host-cached results."`
}

type AttentionRun struct {
	ID          string `json:"id"`
	ItemID      string `json:"itemId"`
	ProfileID   string `json:"profileId"`
	ProfileName string `json:"profileName"`
	Status      string `json:"status"`
}

type AttentionEntry struct {
	Request domain.Attention `json:"request"`
	Run     AttentionRun     `json:"run"`
}

type AttentionPage struct {
	Requests   []AttentionEntry `json:"requests"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type attentionCursor struct {
	Created time.Time `json:"created"`
	ID      string    `json:"id"`
	RunID   string    `json:"runId"`
}

// ListAttention is the model-visible equivalent of Messages. It returns only
// currently actionable asks, without transcripts, skill bundles or user answers.
// Answer still revalidates the request and claims delivery exactly once.
func (a *App) ListAttention(ctx context.Context, in AttentionInput) (AttentionPage, error) {
	out := AttentionPage{Requests: []AttentionEntry{}}
	if in.Limit == 0 {
		in.Limit = 25
	}
	if in.Limit < 1 || in.Limit > 100 {
		return out, fmt.Errorf("limit must be between 1 and 100")
	}
	var after attentionCursor
	if in.Cursor != "" {
		if len(in.Cursor) > 1024 {
			return out, fmt.Errorf("invalid attention cursor")
		}
		b, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || json.Unmarshal(b, &after) != nil || after.ID == "" || after.RunID != in.RunID {
			return out, fmt.Errorf("invalid attention cursor for this run filter")
		}
	}
	requests, err := store.List[domain.Attention](ctx, a.Store, "attention")
	if err != nil {
		return out, err
	}
	runs, err := store.ListWithout[domain.Run](ctx, a.Store, "run", "$.skills", "$.prompt", "$.reviewPromptSnapshot", "$.profile.prompt")
	if err != nil {
		return out, err
	}
	byID := map[string]domain.Run{}
	for _, r := range runs {
		byID[r.ID] = r
	}
	for _, request := range requests {
		r, ok := byID[request.RunID]
		if !ok || request.Status != "pending" || in.RunID != "" && r.ID != in.RunID {
			continue
		}
		if request.Origin == "async" || request.Origin == "text" {
			if r.Status == "prepared" || r.Status == "cleaned" {
				continue
			}
		} else if r.Status != "waiting" && r.Status != "running" {
			continue
		}
		if in.Cursor != "" && (request.Created.Before(after.Created) || request.Created.Equal(after.Created) && request.ID <= after.ID) {
			continue
		}
		request.RequestID = nil // Provider RPC IDs are not answer_request IDs.
		out.Requests = append(out.Requests, AttentionEntry{Request: request, Run: AttentionRun{ID: r.ID, ItemID: r.ItemID, ProfileID: r.Profile.ID, ProfileName: r.Profile.Name, Status: r.Status}})
	}
	sort.Slice(out.Requests, func(i, j int) bool {
		x, y := out.Requests[i].Request, out.Requests[j].Request
		if x.Created.Equal(y.Created) {
			return x.ID < y.ID
		}
		return x.Created.Before(y.Created)
	})
	if len(out.Requests) > in.Limit {
		out.Requests = out.Requests[:in.Limit]
		last := out.Requests[len(out.Requests)-1].Request
		b, _ := json.Marshal(attentionCursor{Created: last.Created, ID: last.ID, RunID: in.RunID})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, nil
}
