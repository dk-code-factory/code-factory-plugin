package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"errors"
)

// ItemDetail reads only the selected item and its direct neighborhood. Network
// observation is scheduled separately and never holds up saved-state delivery.
func (a *App) ItemDetail(ctx context.Context, id string) (map[string]any, error) {
	var item domain.Item
	if err := a.Store.Get(ctx, "item", id, &item); err != nil {
		return nil, err
	}
	links, err := store.Select[domain.Relationship](ctx, a.Store, "relationship", "related", id)
	if err != nil {
		return nil, err
	}
	runs, err := store.Select[domain.Run](ctx, a.Store, "run", "itemId", id, "$.skills", "$.prompt", "$.profile", "$.sourceSnapshot", "$.reviewPromptSnapshot")
	if err != nil {
		return nil, err
	}
	attention := []domain.Attention{}
	for _, run := range runs {
		ats, e := store.Select[domain.Attention](ctx, a.Store, "attention", "runId", run.ID)
		if e != nil {
			return nil, e
		}
		attention = append(attention, ats...)
	}
	neighbors := []domain.Item{item}
	tickets := []domain.Ticket{}
	outgoing := []domain.Relationship{}
	for _, link := range links {
		other := link.Source
		if other == id {
			other = link.Target
			outgoing = append(outgoing, link)
			var t domain.Ticket
			if e := a.Store.Get(ctx, "ticket", other, &t); e == nil {
				tickets = append(tickets, t)
			} else if !errors.Is(e, store.ErrNotFound) {
				return nil, e
			}
		}
		for _, kind := range []string{"item", "linked-pr"} {
			rows, e := store.Select[domain.Item](ctx, a.Store, kind, "id", other, "$.activity", "$.checks")
			if e != nil {
				return nil, e
			}
			if len(rows) > 0 {
				neighbors = append(neighbors, rows[0])
				break
			}
		}
	}
	var user domain.UserState
	if err = a.Store.Get(ctx, "user", id, &user); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	entries := []domain.InboxEntry{{Item: item, User: user}}
	if err = a.projectWorkRecords(ctx, neighbors, links, entries, runs, attention, nil); err != nil {
		return nil, err
	}
	observation, err := a.ItemObservation(ctx, item)
	if err != nil {
		return nil, err
	}
	return map[string]any{"item": item, "user": user, "relationships": outgoing, "tickets": tickets, "linkedPullRequests": entries[0].LinkedPullRequests, "work": entries[0].Work, "queues": entries[0].Queues, "observation": observation, "refreshError": observation.Error}, nil
}
