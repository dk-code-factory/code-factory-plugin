package transport

import (
	"codefactory/internal/factory"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
)

type questionPageInput struct {
	Empty
	SessionID string `json:"sessionId,omitempty"`
	Status    string `json:"status,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

func questionPage(ctx context.Context, db *store.Store, in questionPageInput) (any, error) {
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return nil, fmt.Errorf("offset must be nonnegative and limit 1–200")
	}
	if in.Status == "" {
		in.Status = "pending"
	}
	switch in.Status {
	case "pending", "answered", "stale", "uncertain", "all":
	default:
		return nil, fmt.Errorf("unknown question status")
	}
	raw, total, err := db.QuestionPage(ctx, in.SessionID, in.Status, in.Offset, in.Limit)
	if err != nil {
		return nil, err
	}
	questions := []factory.Question{}
	for _, data := range raw {
		var q factory.Question
		if err := json.Unmarshal(data, &q); err != nil {
			return nil, err
		}
		questions = append(questions, q)
	}
	return map[string]any{"questions": questions, "total": total, "offset": in.Offset, "limit": in.Limit}, nil
}
