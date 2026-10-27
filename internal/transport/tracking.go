package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"time"
)

func (s *Server) dashboard(ctx context.Context) (map[string]any, error) {
	p, err := s.publishedUI(ctx)
	if err != nil {
		return nil, err
	}
	entries, counts := p.inbox, p.counts

	var runs []domain.Run
	if err := json.Unmarshal(p.data["runs"], &runs); err != nil {
		return nil, err
	}
	states := map[string]int{}
	for _, r := range runs {
		states[r.Status]++
	}
	var attention []domain.Attention
	if err := json.Unmarshal(p.data["attention"], &attention); err != nil {
		return nil, err
	}
	hasAttention := false
	for _, at := range attention {
		if at.Status == "pending" {
			hasAttention = true
		}
	}

	return map[string]any{"itemCount": len(entries), "queues": counts, "runCount": len(runs), "runStates": states, "hasAttention": hasAttention, "readAt": time.Now().UTC(), "cursor": p.cursor, "publishedAt": p.published, "detailTool": "track_work", "appSnapshotTool": "read_ui_state"}, nil
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
