package factory

import (
	"codefactory/internal/codex"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Communication is an intentional user-facing interaction, unlike native agent
// commentary. Text is plain text; the UI supplies structure and escapes content.
type Communication struct {
	Kind    string                `json:"kind"`
	Title   string                `json:"title"`
	Body    string                `json:"body"`
	Options []CommunicationOption `json:"options,omitempty"`
}

type CommunicationOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

func (s *Service) communicate(ctx context.Context, v *Session, args json.RawMessage, msg codex.Message) (any, bool, error) {
	if v.Kind != "configuration" {
		return nil, false, fmt.Errorf("communication tool is configuration-session-bound")
	}
	var p Communication
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, false, err
	}
	if (p.Kind != "message" && p.Kind != "question") || strings.TrimSpace(p.Title) == "" || len(p.Title) > 160 || strings.TrimSpace(p.Body) == "" || len(p.Body) > 4096 || len(p.Options) > 6 || (p.Kind == "message" && len(p.Options) != 0) || len(msg.ID) == 0 {
		return nil, false, fmt.Errorf("message/question, title (1–160 bytes), body (1–4096 bytes) and at most six question options required")
	}
	seen := map[string]bool{}
	for _, option := range p.Options {
		if strings.TrimSpace(option.Label) == "" || len(option.Label) > 160 || len(option.Description) > 512 || seen[option.Label] {
			return nil, false, fmt.Errorf("question options require unique nonempty labels of at most 160 bytes and descriptions of at most 512 bytes")
		}
		seen[option.Label] = true
	}
	id := digest([]string{v.ID, v.Epoch, v.TurnID, string(msg.ID)})
	if p.Kind == "message" {
		err := s.recordCommunication(ctx, v, p, msg)
		return map[string]any{"recorded": err == nil}, false, err
	}
	q := Question{ID: id, SessionID: v.ID, Epoch: v.Epoch, RequestID: msg.ID, Method: "code_factory_communicate", Params: bytes(p), Blocking: true, Status: "pending", Created: s.Now()}
	var previous Question
	if err := s.Store.Get(ctx, "factory-question", id, &previous); err == nil {
		if previous.Status == "pending" {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("request already resolved")
	} else if err != store.ErrNotFound {
		return nil, false, err
	}
	v.Execution = "waiting"
	err := s.Store.Write(ctx, func(t *store.Tx) error {
		if err := t.Put("factory-question", id, q); err != nil {
			return err
		}
		if err := t.Put("factory-session", v.ID, v); err != nil {
			return err
		}
		if err := t.Event("session.changed", v.ID, map[string]string{"id": v.ID}); err != nil {
			return err
		}
		return t.Event("attention.required", id, map[string]string{"sessionId": v.ID})
	})
	return nil, err == nil, err
}

func (s *Service) recordCommunication(ctx context.Context, v *Session, p Communication, msg codex.Message) error {
	if strings.TrimSpace(p.Body) == "" || len(p.Body) > 4096 {
		return fmt.Errorf("communication body must contain 1–4096 bytes")
	}
	id := digest([]string{v.ID, v.Epoch, v.TurnID, string(msg.ID)})
	var previous Communication
	if err := s.Store.Get(ctx, "factory-communication", id, &previous); err == nil {
		return nil
	} else if err != store.ErrNotFound {
		return err
	}
	return s.Store.Write(ctx, func(t *store.Tx) error {
		if err := t.Put("factory-communication", id, p); err != nil {
			return err
		}
		return t.Event("session.output", v.ID, map[string]any{"interaction": p})
	})
}
