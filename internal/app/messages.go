package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/store"
)

// MessageDelivery contains no message text. The durable payload stays encrypted
// in the vault; status can be returned to the model and journaled safely.
type MessageDelivery struct {
	ID           string    `json:"id"`
	RunID        string    `json:"runId"`
	State        string    `json:"state"`
	ExpectedTurn string    `json:"expectedTurn,omitempty"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
	Error        string    `json:"error,omitempty"`
}

// QueueMessage acknowledges a local commit, never a provider or GitHub call.
// Only the worker touches a session, so a blocked provider cannot delay receipt.
func (m *Manager) QueueMessage(ctx context.Context, id, text, expected string) (MessageDelivery, error) {
	var retained domain.Run
	if err := m.app.Store.Get(ctx, "run", id, &retained); err != nil {
		return MessageDelivery{}, err
	}
	if retained.ReviewWorkflow == "" && strings.TrimSpace(text) == "" {
		return MessageDelivery{}, fmt.Errorf("message is empty")
	}
	if !m.adminGate.TryRLock() {
		return MessageDelivery{}, fmt.Errorf("plugin maintenance in progress; try again when it finishes")
	}
	defer m.adminGate.RUnlock()
	if retained.ReviewWorkflow != "" {
		return m.queueReviewContinuation(ctx, retained, text, expected)
	}
	d := MessageDelivery{ID: ID(), RunID: id, State: "queued", ExpectedTurn: expected, Created: time.Now().UTC()}
	d.Updated = d.Created
	// Persist the payload first: no committed queue record can lack its text.
	if err := m.app.Vault.Set(ctx, "message:"+d.ID, text); err != nil {
		return MessageDelivery{}, err
	}
	err := m.app.Store.Write(ctx, func(t *store.Tx) error {
		var r domain.Run
		if err := t.Get("run", id, &r); err != nil {
			return err
		}
		if r.Status != "running" && r.Status != "waiting" && r.Status != "idle" && r.Status != "interrupted" && r.Status != "failed" {
			return fmt.Errorf("run cannot accept a message from %s", r.Status)
		}
		active := r.Status == "running" || r.Status == "waiting"
		if active && r.TurnID != expected || !active && expected != "" {
			return fmt.Errorf("active turn changed; refresh before sending")
		}
		if !active && r.ThreadID == "" {
			return fmt.Errorf("retained Codex session is unavailable; start a new run")
		}
		var previous MessageDelivery
		if err := t.Get("message-delivery", id, &previous); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if previous.State == "queued" || previous.State == "delivering" {
			return fmt.Errorf("a message is already awaiting delivery; check track_work before sending again")
		}
		return saveMessage(t, d)
	})
	if err != nil {
		// A rejected message has never entered the queue.
		_ = m.app.Store.Write(context.WithoutCancel(ctx), func(t *store.Tx) error { return t.Delete("secret", "message:"+d.ID) })
		return MessageDelivery{}, err
	}
	// A shutdown after the commit leaves queued work for Recover.
	m.background(func() { m.deliverMessage(d) })
	return d, nil
}

func saveMessage(t *store.Tx, d MessageDelivery) error {
	if err := t.Put("message-delivery", d.RunID, d); err != nil {
		return err
	}
	if err := t.Put("message", d.ID, d); err != nil {
		return err
	}
	return t.Event("run.output", d.RunID, map[string]any{"method": "codefactory/messageDelivery", "params": map[string]any{"itemId": d.ID, "state": d.State, "error": d.Error}})
}

func (m *Manager) deliverMessage(d MessageDelivery) {
	m.deliveryMu.Lock()
	if m.messageWorkers == nil {
		m.messageWorkers = map[string]bool{}
	}
	if m.messageWorkers[d.ID] {
		m.deliveryMu.Unlock()
		return
	}
	m.messageWorkers[d.ID] = true
	m.deliveryMu.Unlock()
	defer func() {
		m.deliveryMu.Lock()
		delete(m.messageWorkers, d.ID)
		m.deliveryMu.Unlock()
	}()
	// Claim before any provider interaction. A lost acknowledgement must never
	// cause an automatic replay after restart or concurrent recovery.
	err := m.app.Store.Write(m.ctx, func(t *store.Tx) error {
		var current MessageDelivery
		if err := t.Get("message-delivery", d.RunID, &current); err != nil {
			return err
		}
		if current.ID != d.ID || current.State != "queued" {
			return fmt.Errorf("message is already claimed")
		}
		d.State, d.Updated = "delivering", time.Now().UTC()
		return saveMessage(t, d)
	})
	if err != nil {
		return
	}
	text, err := m.app.Vault.Get(m.ctx, "message:"+d.ID)
	if err == nil {
		err = m.Message(m.ctx, d.RunID, text, d.ExpectedTurn)
	}
	d.State, d.Updated = "delivered", time.Now().UTC()
	if err != nil {
		d.State = "uncertain"
		// Provider errors can include submitted text; keep payloads out of status.
		d.Error = "Agent delivery could not be confirmed. Inspect this session before sending again."
	}
	// Finalize even if the daemon context was canceled. A crash before this
	// commit leaves 'delivering', which recovery marks uncertain without replay.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(m.ctx), 5*time.Second)
	defer cancel()
	_ = m.app.Store.Write(ctx, func(t *store.Tx) error { return saveMessage(t, d) })
	if d.State == "delivered" {
		m.queueAttentionReconciliation(d.RunID)
	}
}

func (a *App) MessageDelivery(ctx context.Context, runID string) (*MessageDelivery, error) {
	var d MessageDelivery
	err := a.Store.Get(ctx, "message-delivery", runID, &d)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return &d, err
}

func (m *Manager) recoverMessages(ctx context.Context) (map[string]bool, []MessageDelivery, error) {
	rows, err := store.List[MessageDelivery](ctx, m.app.Store, "message-delivery")
	blocked := map[string]bool{}
	queued := []MessageDelivery{}
	for _, d := range rows {
		m.deliveryMu.Lock()
		active := m.messageWorkers[d.ID]
		m.deliveryMu.Unlock()
		if active {
			blocked[d.RunID] = true
			continue
		}
		if d.State == "delivering" {
			d.State, d.Updated = "uncertain", time.Now().UTC()
			d.Error = "Daemon stopped during delivery. Inspect this session before sending again; the message was not replayed."
			if e := m.app.Store.Write(ctx, func(t *store.Tx) error { return saveMessage(t, d) }); e != nil {
				return nil, nil, e
			}
		}
		if d.State == "queued" {
			queued = append(queued, d)
		}
		blocked[d.RunID] = d.State == "queued" || d.State == "uncertain"
	}
	return blocked, queued, err
}
