package providers

import (
	"context"
	"sync"
)

// githubGate has an unlocked zero value and admits only live callers. Waiting
// never outlives the caller's context or requires a detached waiter goroutine.
type githubGate struct {
	once sync.Once
	busy chan struct{}
}

func (g *githubGate) Lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g.once.Do(func() { g.busy = make(chan struct{}, 1) })
	select {
	case g.busy <- struct{}{}:
		// Admission and cancellation can become ready together. Do not pass an
		// expired caller on to authentication, accounting or HTTP dispatch.
		if err := ctx.Err(); err != nil {
			g.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *githubGate) Unlock() { <-g.busy }
