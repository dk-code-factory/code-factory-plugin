package transport

import (
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScanWaitsCoalescesAndReleasesWorkers(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := newOperationQueue(db, &documentCache{})
	var clock atomic.Int64
	start := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	clock.Store(start.UnixNano())
	q.mu.Lock()
	q.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	q.mu.Unlock()
	var attempts atomic.Int64
	retry := start.Add(time.Minute)
	_, err = submitUntilAvailable(t, q, q.instance, "scan", "sync_repositories", "sync", false, 17, func(context.Context) (any, error) {
		if attempts.Add(1) == 1 {
			return nil, &providers.RateLimitError{Until: retry, Reason: "GitHub secondary limit"}
		}
		return map[string]bool{"synced": true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	op := waitOperation(t, q, q.instance, "scan", "waiting")
	if op.RetryAt == nil || !op.RetryAt.Equal(retry) || op.RetryReason != "GitHub secondary limit" || op.Error != "" {
		t.Fatal("incomplete waiting state", op)
	}
	var meta operationMetadata
	if err = db.Get(context.Background(), "plugin-api-operation", operationKey(q.instance, "scan"), &meta); err != nil || meta.State != "waiting" {
		t.Fatal("waiting not journaled", meta, err)
	}
	// An independently identified click joins the existing scan. Its identity also
	// resolves if its receipt is lost; neither closure nor input bytes are retained.
	_, err = submitUntilAvailable(t, q, q.instance, "second-click", "sync_repositories", "sync", false, 999, func(context.Context) (any, error) { t.Error("duplicate scan executed"); return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	alias := waitOperation(t, q, q.instance, "second-click", "waiting")
	if alias.RequestID != "scan" {
		t.Fatal("duplicate did not join canonical scan", alias)
	}
	entered := make(chan struct{}, ordinaryWorkers)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	for n := 0; n < ordinaryWorkers; n++ {
		id := string(rune('a' + n))
		_, err = submitUntilAvailable(t, q, q.instance, id, "fixture", id, false, 1, func(ctx context.Context) (any, error) {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < ordinaryWorkers; n++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("waiting scan occupied ordinary worker")
		}
	}
	if attempts.Load() != 1 {
		t.Fatal("scan bypassed controlled cooldown", attempts.Load())
	}
	q.mu.Lock()
	held := q.bytes
	aliasBytes := q.scanAliasBytes
	q.mu.Unlock()
	if aliasBytes == 0 || held != 17+ordinaryWorkers+aliasBytes {
		t.Fatal("duplicate or waiting payload accounting", held)
	}
	// Release workers and advance the controlled clock. Signaling uses exactly the
	// scheduler wake path of the production timer without a minute-long sleep.
	unblock()
	for n := 0; n < ordinaryWorkers; n++ {
		waitOperation(t, q, q.instance, string(rune('a'+n)), "succeeded")
	}
	clock.Store(retry.UnixNano())
	q.signal()
	waitOperation(t, q, q.instance, "second-click", "succeeded")
	if attempts.Load() != 2 {
		t.Fatal("scan was replayed more than once", attempts.Load())
	}
	_, err = submitUntilAvailable(t, q, q.instance, "second-click", "sync_repositories", "sync", false, 99, func(context.Context) (any, error) { t.Error("completed duplicate replayed"); return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	held = q.bytes
	q.mu.Unlock()
	if held != 0 {
		t.Fatal("completed scan retained submitted payload", held)
	}
}

func TestScanRetryTimerAndMutationsNeverRetry(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := newOperationQueue(db, &documentCache{})
	var attempts atomic.Int64
	resumed := make(chan struct{})
	retry := time.Now().Add(100 * time.Millisecond)
	_, err = submitUntilAvailable(t, q, q.instance, "timer", "sync_repositories", "sync", false, 1, func(context.Context) (any, error) {
		if attempts.Add(1) == 1 {
			return nil, &providers.RateLimitError{Until: retry, Reason: "GitHub primary quota reset"}
		}
		if time.Now().Before(retry) {
			t.Error("timer retried before provider deadline")
		}
		close(resumed)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-resumed:
	case <-time.After(5 * time.Second):
		t.Fatal("timer did not resume scan")
	}
	waitOperation(t, q, q.instance, "timer", "succeeded")
	if attempts.Load() != 2 {
		t.Fatal("timer did not resume exactly once", attempts.Load())
	}
	var mutations atomic.Int64
	_, err = submitUntilAvailable(t, q, q.instance, "mutation", "session_chat", "session", false, 1, func(context.Context) (any, error) {
		mutations.Add(1)
		return nil, &providers.RateLimitError{Until: retry, Reason: "GitHub secondary limit"}
	})
	if err != nil {
		t.Fatal(err)
	}
	waitOperation(t, q, q.instance, "mutation", "failed")
	q.signal()
	_, err = submitUntilAvailable(t, q, q.instance, "barrier", "fixture", "session", false, 1, func(context.Context) (any, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	waitOperation(t, q, q.instance, "barrier", "succeeded")
	if mutations.Load() != 1 {
		t.Fatal("mutation automatically replayed", mutations.Load())
	}
}

func TestWaitingScanRestartDoesNotReplayOperation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := newOperationQueue(db, &documentCache{})
	var attempts atomic.Int64
	_, err = submitUntilAvailable(t, q, q.instance, "scan", "sync_repositories", "sync", false, 1, func(context.Context) (any, error) {
		attempts.Add(1)
		return nil, &providers.RateLimitError{Until: time.Now().Add(time.Hour)}
	})
	if err != nil {
		t.Fatal(err)
	}
	waitOperation(t, q, q.instance, "scan", "waiting")
	old := q.instance
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	resumed := newOperationQueue(reopened, &documentCache{})
	op := waitOperation(t, resumed, old, "scan", "uncertain")
	if op.RetryAt != nil || !op.ResultUnavailable {
		t.Fatal("restart invented active waiting operation", op)
	}
	_, err = submitUntilAvailable(t, resumed, old, "scan", "sync_repositories", "sync", false, 1, func(context.Context) (any, error) { attempts.Add(1); return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 1 {
		t.Fatal("restart replayed accepted operation", attempts.Load())
	}
}

func TestScanRetainsAdmissionWhenItLeavesSaturatedWorkers(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Drive dispatch explicitly to fill admission between the initial attempt and
	// its throttle response, without scheduler races or hundreds of live helpers.
	q := &operationQueue{instance: "fixture", db: db, ctx: db.Context(), operations: map[string]operation{}, active: map[string]bool{}, wake: make(chan struct{}), documents: &documentCache{}, executionTimeout: time.Minute}
	defer func() {
		if q.retryTimer != nil {
			q.retryTimer.Stop()
		}
	}()
	_, err = submitUntilAvailable(t, q, q.instance, "scan", "sync_repositories", "sync", false, 1, func(context.Context) (any, error) {
		return nil, &providers.RateLimitError{Until: time.Now().Add(time.Hour)}
	})
	if err != nil {
		t.Fatal(err)
	}
	task, ok, _ := q.take(false)
	if !ok {
		t.Fatal("scan not dispatched")
	}
	for n := 0; n < pendingLimit-1; n++ {
		_, err = submitUntilAvailable(t, q, q.instance, fmt.Sprintf("pending-%d", n), "fixture", "other", false, 1, func(context.Context) (any, error) { t.Error("pending fixture unexpectedly executed"); return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	q.execute(task)
	waitOperation(t, q, q.instance, "scan", "waiting")
	if len(q.pending) != pendingLimit-1 || q.waitingScan == nil || q.bytes != pendingLimit {
		t.Fatal("scan exceeded pending capacity", len(q.pending), q.bytes)
	}
	result, err := q.submit(q.instance, "overflow", "fixture", "other", false, 1, func(context.Context) (any, error) { return nil, nil })
	if err != nil || result.(map[string]any)["state"] != "busy" {
		t.Fatal("waiting scan lost reserved pending capacity", result, err)
	}
}

func TestScanAliasAdmissionIsBoundedAndReleased(t *testing.T) {
	for _, bound := range []string{"identities", "bytes"} {
		t.Run(bound, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			clock := time.Now().UTC()
			q := &operationQueue{instance: "fixture", db: db, ctx: db.Context(), operations: map[string]operation{}, active: map[string]bool{}, wake: make(chan struct{}), documents: &documentCache{}, executionTimeout: time.Minute, now: func() time.Time { return clock }}
			defer func() {
				if q.retryTimer != nil {
					q.retryTimer.Stop()
				}
			}()
			attempts := 0
			size := 17
			if bound == "bytes" {
				size = payloadLimit - 1
			}
			_, err = q.submit(q.instance, "scan", "sync_repositories", "sync", false, size, func(context.Context) (any, error) {
				attempts++
				if attempts == 1 {
					return nil, &providers.RateLimitError{Until: clock.Add(time.Minute), Reason: "GitHub secondary limit"}
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			aliasID := func(n int) string { return fmt.Sprintf("alias-%03d-", n) + strings.Repeat("x", 118) }
			if bound == "identities" {
				for n := 0; n < pendingLimit; n++ {
					result, err := q.submit(q.instance, aliasID(n), "sync_repositories", "sync", false, payloadLimit, func(context.Context) (any, error) { t.Error("alias closure executed"); return nil, nil })
					if err != nil {
						t.Fatal(err)
					}
					if op, ok := result.(operation); !ok || op.RequestID != "scan" {
						t.Fatal("alias did not join original scan", result)
					}
				}
				if q.bytes <= size || q.bytes > payloadLimit {
					t.Fatal("alias metadata not charged to admission", q.bytes)
				}
			}
			retained, held := len(q.operations), q.bytes
			for n := 0; n < pendingLimit*2; n++ {
				result, err := q.submit(q.instance, aliasID(pendingLimit+n), "sync_repositories", "sync", false, 1, func(context.Context) (any, error) { t.Error("overflow executed"); return nil, nil })
				if busy, ok := result.(map[string]any); err != nil || !ok || busy["state"] != "busy" {
					t.Fatal("alias admission exceeded bound", result, err)
				}
			}
			if len(q.operations) != retained || q.bytes != held {
				t.Fatal("rejected aliases retained identities or bytes", len(q.operations), q.bytes)
			}
			task, ok, _ := q.take(false)
			if !ok {
				t.Fatal("canonical scan did not dispatch")
			}
			q.execute(task)
			waitOperation(t, q, q.instance, "scan", "waiting")
			duplicateID := "scan"
			if bound == "identities" {
				duplicateID = aliasID(0)
			}
			result, err := q.submit(q.instance, duplicateID, "sync_repositories", "sync", false, payloadLimit, func(context.Context) (any, error) { t.Error("accepted ID replayed"); return nil, nil })
			if op, ok := result.(operation); err != nil || !ok || op.State != "waiting" || op.RequestID != "scan" {
				t.Fatal("full alias admission rejected exact duplicate", result, err)
			}
			clock = clock.Add(time.Minute)
			task, ok, _ = q.take(false)
			if !ok {
				t.Fatal("due scan did not dispatch")
			}
			q.execute(task)
			waitOperation(t, q, q.instance, duplicateID, "succeeded")
			if attempts != 2 || q.bytes != 0 || q.scanKey != "" {
				t.Fatal("scan did not release active admission exactly once", attempts, q.bytes, q.scanKey)
			}
			result, err = q.submit(q.instance, duplicateID, "sync_repositories", "sync", false, payloadLimit, func(context.Context) (any, error) { t.Error("terminal accepted ID replayed"); return nil, nil })
			if op, ok := result.(operation); err != nil || !ok || op.State != "succeeded" || op.RequestID != "scan" {
				t.Fatal("terminal duplicate lost canonical identity", result, err)
			}
			_, err = q.submit(q.instance, "next", "sync_repositories", "sync", false, 1, func(context.Context) (any, error) { return nil, nil })
			if err != nil {
				t.Fatal(err)
			}
			result, err = q.submit(q.instance, "next-alias", "sync_repositories", "sync", false, 1, func(context.Context) (any, error) { t.Error("next alias executed"); return nil, nil })
			if op, ok := result.(operation); err != nil || !ok || op.RequestID != "next" {
				t.Fatal("completed scan did not release alias capacity", result, err)
			}
		})
	}
}
