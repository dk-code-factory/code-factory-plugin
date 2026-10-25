package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"time"
)

func (s *Server) dashboard(ctx context.Context) (map[string]any, error) {
	entries, err := s.App.InboxSummaries(ctx, "all")
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, e := range entries {
		for _, q := range e.Queues {
			counts[q]++
		}
	}
	runs, err := store.ListWithout[domain.Run](ctx, s.App.Store, "run", "$.skills", "$.prompt", "$.profile", "$.sourceSnapshot", "$.reviewPromptSnapshot")
	if err != nil {
		return nil, err
	}
	states := map[string]int{}
	for _, r := range runs {
		states[r.Status]++
	}
	attention, err := s.App.ListAttention(ctx, app.AttentionInput{Limit: 1})
	if err != nil {
		return nil, err
	}
	return map[string]any{"itemCount": len(entries), "queues": counts, "runCount": len(runs), "runStates": states, "hasAttention": len(attention.Requests) > 0, "readAt": time.Now().UTC(), "detailTool": "track_work", "appSnapshotTool": "read_snapshot"}, nil
}

// A successful mutation response establishes local acceptance/delivery only.
// If a post-read fails, retain that result and never encourage replay.
func (s *Server) operationState(ctx context.Context, id, delivery string) map[string]any {
	out := map[string]any{"delivery": delivery, "runId": id, "taskCompletion": "not established"}
	runs, err := store.Runs(ctx, s.App.Store, "", id)
	if err != nil || len(runs) == 0 {
		out["stateUnavailable"] = true
		return out
	}
	r := runs[0]
	out["run"] = map[string]any{"id": r.ID, "itemId": r.ItemID, "status": r.Status, "error": r.Error, "threadId": r.ThreadID, "turnId": r.TurnID}
	out["turnId"] = r.TurnID
	out["threadId"] = r.ThreadID
	attention, err := s.App.ListAttention(ctx, app.AttentionInput{RunID: id, Limit: 5})
	if err != nil {
		out["attentionUnavailable"] = true
	} else {
		out["attention"] = attention
	}
	return out
}
